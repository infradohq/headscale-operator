/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

// maxConditionMessage caps how much of a pod failure message we copy into a
// status condition so a noisy crash log can't bloat the object.
const maxConditionMessage = 1024

// newCondition is a small constructor for a status condition stamped with the
// object's current generation.
func newCondition(h *headscalev1beta1.Headscale, condType string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: h.Generation,
	}
}

// configValidCondition reports whether the operator could turn spec.config into a
// usable config.yaml. It only checks what the operator can cheaply verify
// (renderability and the server_url headscale always requires); everything else
// is left to headscale, whose verdict surfaces through the Ready condition.
func configValidCondition(h *headscalev1beta1.Headscale, view headscaleConfigView, renderErr error) metav1.Condition {
	switch {
	case renderErr != nil:
		return newCondition(h, "ConfigValid", metav1.ConditionFalse, "InvalidConfig", renderErr.Error())
	case view.ServerURL == "":
		return newCondition(h, "ConfigValid", metav1.ConditionFalse, "MissingServerURL",
			"spec.config.server_url is required by headscale")
	default:
		return newCondition(h, "ConfigValid", metav1.ConditionTrue, "Valid",
			"Configuration accepted; headscale validates it at startup")
	}
}

// computeReadyCondition derives the Ready condition from the actual workload
// state and reports whether the caller should requeue (because the workload has
// not yet reached its desired ready replicas). When the workload is unhealthy it
// tries to surface the headscale container's failure reason — which, thanks to
// the FallbackToLogsOnError termination policy, often carries headscale's fatal
// config error.
func (r *HeadscaleReconciler) computeReadyCondition(ctx context.Context, h *headscalev1beta1.Headscale) (metav1.Condition, bool) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: h.Name, Namespace: h.Namespace}, sts); err != nil {
		return newCondition(h, "Ready", metav1.ConditionFalse, "StatefulSetMissing",
			"Waiting for the Headscale StatefulSet to be created"), true
	}

	var desired int32
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}

	if sts.Status.ReadyReplicas >= desired {
		return newCondition(h, "Ready", metav1.ConditionTrue, "Reconciled",
			fmt.Sprintf("Headscale is running (%d/%d replicas ready)", sts.Status.ReadyReplicas, desired)), false
	}

	reason := "NotReady"
	message := fmt.Sprintf("Waiting for Headscale to become ready (%d/%d replicas ready)",
		sts.Status.ReadyReplicas, desired)
	if failReason, failMsg, ok := r.headscalePodFailure(ctx, h); ok {
		reason, message = failReason, failMsg
		// Only emit an event when the failure is new or has changed, so a
		// persistently crashing pod doesn't spam the event stream.
		if r.Recorder != nil {
			if prev := meta.FindStatusCondition(h.Status.Conditions, "Ready"); prev == nil ||
				prev.Reason != reason || prev.Message != message {
				r.Recorder.Event(h, corev1.EventTypeWarning, reason, message)
			}
		}
	}

	return newCondition(h, "Ready", metav1.ConditionFalse, reason, message), true
}

// headscalePodFailure looks for a failing headscale container among the pods of
// this Headscale instance and returns a reason/message describing it. ok is false
// when no clear failure is found (e.g. pods are merely still starting).
func (r *HeadscaleReconciler) headscalePodFailure(ctx context.Context, h *headscalev1beta1.Headscale) (string, string, bool) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(h.Namespace), client.MatchingLabels(labelsForHeadscale(h.Name))); err != nil {
		return "", "", false
	}

	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.Name != "headscale" {
				continue
			}

			// A container that exited non-zero on its last run carries the most
			// useful message (the startup error), even while it's now waiting to
			// be restarted.
			if t := cs.LastTerminationState.Terminated; t != nil && t.ExitCode != 0 {
				return crashReason(cs), truncate(terminationMessage(t), maxConditionMessage), true
			}
			if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
				return crashReason(cs), truncate(terminationMessage(t), maxConditionMessage), true
			}
			if w := cs.State.Waiting; w != nil && isFailureWaitReason(w.Reason) {
				msg := w.Reason
				if w.Message != "" {
					msg = fmt.Sprintf("%s: %s", w.Reason, w.Message)
				}
				return w.Reason, truncate(msg, maxConditionMessage), true
			}
		}
	}

	return "", "", false
}

// crashReason prefers the container's waiting reason (e.g. CrashLoopBackOff) when
// present, falling back to a generic crash reason.
func crashReason(cs corev1.ContainerStatus) string {
	if w := cs.State.Waiting; w != nil && w.Reason != "" {
		return w.Reason
	}
	return "ContainerCrashed"
}

// isFailureWaitReason filters out the benign "still coming up" waiting reasons so
// we don't report a brand-new pod as failed.
func isFailureWaitReason(reason string) bool {
	switch reason {
	case "", "ContainerCreating", "PodInitializing":
		return false
	default:
		return true
	}
}

// terminationMessage builds a human-readable description of a terminated
// container state, preferring the captured message (the headscale log tail under
// FallbackToLogsOnError) over the bare reason/exit code.
func terminationMessage(t *corev1.ContainerStateTerminated) string {
	switch {
	case strings.TrimSpace(t.Message) != "":
		return strings.TrimSpace(t.Message)
	case t.Reason != "":
		return fmt.Sprintf("%s (exit code %d)", t.Reason, t.ExitCode)
	default:
		return fmt.Sprintf("container exited with code %d", t.ExitCode)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
