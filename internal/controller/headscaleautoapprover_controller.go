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
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tailscale/hujson"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
	hsclient "github.com/infradohq/headscale-operator/pkg/headscale"
)

const (
	headscaleAutoApproverFinalizer = "headscale.infrado.cloud/autoapprover-finalizer"
	policyModeDatabase             = "database"
)

// HeadscaleAutoApproverReconciler reconciles a HeadscaleAutoApprover object
type HeadscaleAutoApproverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleautoapprovers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleautoapprovers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleautoapprovers/finalizers,verbs=update
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get

// Reconcile renders the merged ACL policy for the parent Headscale and pushes
// it via the gRPC SetPolicy API. It is triggered by changes to any
// HeadscaleAutoApprover and (via SetupWithManager) to the parent Headscale.
func (r *HeadscaleAutoApproverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	approver := &headscalev1beta1.HeadscaleAutoApprover{}
	if err := r.Get(ctx, req.NamespacedName, approver); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HeadscaleAutoApprover")
		return ctrl.Result{}, err
	}

	headscale, hsErr := r.getHeadscale(ctx, approver)

	if approver.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, approver, headscale)
	}

	if err := r.ensureFinalizer(ctx, approver); err != nil {
		return ctrl.Result{}, err
	}

	if hsErr != nil {
		if apierrors.IsNotFound(hsErr) {
			if err := r.setCondition(ctx, approver, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "HeadscaleNotFound",
				Message: fmt.Sprintf("Referenced Headscale %q not found", approver.Spec.HeadscaleRef),
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, hsErr
	}

	if headscale.Spec.Config.Policy.Mode != policyModeDatabase {
		if err := r.setCondition(ctx, approver, metav1.Condition{
			Type:   "Ready",
			Status: metav1.ConditionFalse,
			Reason: "PolicyModeUnsupported",
			Message: fmt.Sprintf(
				"Headscale %q has policy.mode=%q; HeadscaleAutoApprover requires policy.mode=%q",
				headscale.Name, headscale.Spec.Config.Policy.Mode, policyModeDatabase,
			),
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if err := r.renderAndPush(ctx, headscale); err != nil {
		if err := r.setCondition(ctx, approver, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "PolicyPushFailed",
			Message: err.Error(),
		}); err != nil {
			return ctrl.Result{}, err
		}
		// Returning the error is enough — controller-runtime ignores Result
		// when err != nil and uses the rate-limited workqueue for backoff.
		return ctrl.Result{}, fmt.Errorf("failed to push policy: %w", err)
	}

	if err := r.setCondition(ctx, approver, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "PolicyApplied",
		Message: "Auto-approver entries merged into the active Headscale policy",
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// handleDeletion removes the finalizer after re-pushing the policy without this
// resource's contributions. If the parent Headscale is gone we just drop the
// finalizer — the policy lives with Headscale and there is nothing to clean up.
func (r *HeadscaleAutoApproverReconciler) handleDeletion(
	ctx context.Context,
	approver *headscalev1beta1.HeadscaleAutoApprover,
	headscale *headscalev1beta1.Headscale,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(approver, headscaleAutoApproverFinalizer) {
		return ctrl.Result{}, nil
	}

	// Re-push policy excluding this resource. The list-and-render path naturally
	// excludes objects with a deletion timestamp (see collectApprovers).
	if headscale != nil && headscale.Spec.Config.Policy.Mode == policyModeDatabase {
		if err := r.renderAndPush(ctx, headscale); err != nil {
			log.Error(err, "Failed to re-push policy during deletion; will retry")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	controllerutil.RemoveFinalizer(approver, headscaleAutoApproverFinalizer)
	if err := r.Update(ctx, approver); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *HeadscaleAutoApproverReconciler) ensureFinalizer(
	ctx context.Context,
	approver *headscalev1beta1.HeadscaleAutoApprover,
) error {
	if controllerutil.ContainsFinalizer(approver, headscaleAutoApproverFinalizer) {
		return nil
	}
	controllerutil.AddFinalizer(approver, headscaleAutoApproverFinalizer)
	return r.Update(ctx, approver)
}

func (r *HeadscaleAutoApproverReconciler) getHeadscale(
	ctx context.Context,
	approver *headscalev1beta1.HeadscaleAutoApprover,
) (*headscalev1beta1.Headscale, error) {
	headscale := &headscalev1beta1.Headscale{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      approver.Spec.HeadscaleRef,
		Namespace: approver.Namespace,
	}, headscale)
	if err != nil {
		return nil, err
	}
	return headscale, nil
}

// renderAndPush lists every HeadscaleAutoApprover that targets this Headscale,
// merges their entries with the parent's ACLPolicy, and pushes the resulting
// JSON document via SetPolicy.
func (r *HeadscaleAutoApproverReconciler) renderAndPush(
	ctx context.Context,
	headscale *headscalev1beta1.Headscale,
) error {
	log := logf.FromContext(ctx)

	approvers, err := r.collectApprovers(ctx, headscale)
	if err != nil {
		return fmt.Errorf("failed to list auto-approvers: %w", err)
	}

	policy, err := buildPolicyDocument(&headscale.Spec.ACLPolicy, approvers)
	if err != nil {
		return fmt.Errorf("failed to build policy document: %w", err)
	}

	apiKey, err := getAPIKey(ctx, r.Client, headscale)
	if err != nil {
		return fmt.Errorf("failed to get API key: %w", err)
	}

	hsClient, err := hsclient.NewClientWithAPIKey(getGRPCServiceAddress(headscale), apiKey)
	if err != nil {
		return fmt.Errorf("failed to create Headscale client: %w", err)
	}
	defer func() {
		if cerr := hsClient.Close(); cerr != nil {
			log.Error(cerr, "failed to close Headscale client")
		}
	}()

	if err := hsClient.SetPolicy(ctx, policy); err != nil {
		return err
	}
	log.Info("Pushed merged policy to Headscale", "Headscale", headscale.Name, "Approvers", len(approvers))
	return nil
}

// collectApprovers returns every HeadscaleAutoApprover in the same namespace as
// the Headscale that references it by name and is not being deleted.
func (r *HeadscaleAutoApproverReconciler) collectApprovers(
	ctx context.Context,
	headscale *headscalev1beta1.Headscale,
) ([]headscalev1beta1.HeadscaleAutoApprover, error) {
	list := &headscalev1beta1.HeadscaleAutoApproverList{}
	if err := r.List(ctx, list, client.InNamespace(headscale.Namespace)); err != nil {
		return nil, err
	}

	out := make([]headscalev1beta1.HeadscaleAutoApprover, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Spec.HeadscaleRef != headscale.Name {
			continue
		}
		if item.GetDeletionTimestamp() != nil {
			continue
		}
		out = append(out, item)
	}
	// Deterministic order keeps SetPolicy idempotent and makes diffs reviewable.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *HeadscaleAutoApproverReconciler) setCondition(
	ctx context.Context,
	approver *headscalev1beta1.HeadscaleAutoApprover,
	condition metav1.Condition,
) error {
	patch := client.MergeFrom(approver.DeepCopy())
	condition.ObservedGeneration = approver.Generation
	if !meta.SetStatusCondition(&approver.Status.Conditions, condition) {
		return nil
	}
	return r.Status().Patch(ctx, approver, patch)
}

// requeueApproversForHeadscale returns a handler.MapFunc that, when a Headscale
// changes, enqueues every HeadscaleAutoApprover that targets it. This makes
// edits to spec.acl_policy or spec.config.policy.mode trigger a re-render.
func (r *HeadscaleAutoApproverReconciler) requeueApproversForHeadscale(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	headscale, ok := obj.(*headscalev1beta1.Headscale)
	if !ok {
		return nil
	}
	list := &headscalev1beta1.HeadscaleAutoApproverList{}
	if err := r.List(ctx, list, client.InNamespace(headscale.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list HeadscaleAutoApprovers for Headscale watch")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Spec.HeadscaleRef != headscale.Name {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: item.Name, Namespace: item.Namespace},
		})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *HeadscaleAutoApproverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&headscalev1beta1.HeadscaleAutoApprover{}).
		Watches(
			&headscalev1beta1.Headscale{},
			handler.EnqueueRequestsFromMapFunc(r.requeueApproversForHeadscale),
			// Headscale CRs receive frequent status updates from their own
			// reconciler; only rendering changes matter here, so filter to
			// spec generation bumps to avoid re-pushing on every status flip.
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Named("headscaleautoapprover").
		Complete(r)
}

// policyDocument is the subset of the Headscale policy schema the operator
// renders. Fields the user supplied via ACLPolicy.Inline (acls, groups, hosts,
// ssh, ...) are preserved as-is. The operator only owns tagOwners and
// autoApprovers: when the operator has values for either it replaces that key
// in the inline base; when it has none, any key the inline base supplied
// passes through untouched.
type policyDocument map[string]any

const (
	policyKeyTagOwners     = "tagOwners"
	policyKeyAutoApprovers = "autoApprovers"
	policyKeyRoutes        = "routes"
	policyKeyExitNode      = "exitNode"
)

// buildPolicyDocument merges the inline base, tag owners, and auto-approver
// entries into a single JSON document ready for SetPolicy.
func buildPolicyDocument(
	base *headscalev1beta1.ACLPolicyConfig,
	approvers []headscalev1beta1.HeadscaleAutoApprover,
) (string, error) {
	doc := policyDocument{}
	if base != nil && base.Inline != "" {
		// Headscale's policy file is HuJSON (JSON with comments + trailing
		// commas), so accept the same dialect on input. Standardize() rewrites
		// it to strict JSON before unmarshaling.
		standardized, err := hujson.Standardize([]byte(base.Inline))
		if err != nil {
			return "", fmt.Errorf("acl_policy.inline is not valid HuJSON: %w", err)
		}
		if err := json.Unmarshal(standardized, &doc); err != nil {
			return "", fmt.Errorf("acl_policy.inline failed to unmarshal: %w", err)
		}
	}

	if base != nil && len(base.TagOwners) > 0 {
		owners := make(map[string]any, len(base.TagOwners))
		for tag, principals := range base.TagOwners {
			owners[tag] = principals
		}
		doc[policyKeyTagOwners] = owners
	}

	routes := map[string][]string{}
	exitTagSet := map[string]struct{}{}
	for _, a := range approvers {
		for _, route := range a.Spec.Routes {
			// Union of tags per CIDR; if two CRs claim the same CIDR they merge.
			existing := routes[route.CIDR]
			seen := make(map[string]struct{}, len(existing))
			for _, t := range existing {
				seen[t] = struct{}{}
			}
			for _, t := range route.Tags {
				if _, ok := seen[t]; ok {
					continue
				}
				seen[t] = struct{}{}
				existing = append(existing, t)
			}
			routes[route.CIDR] = existing
		}
		for _, t := range a.Spec.ExitNodeTags {
			exitTagSet[t] = struct{}{}
		}
	}

	if len(routes) > 0 || len(exitTagSet) > 0 {
		section := map[string]any{}
		if len(routes) > 0 {
			// Sort tag lists per CIDR for deterministic output.
			for cidr, tags := range routes {
				sort.Strings(tags)
				routes[cidr] = tags
			}
			section[policyKeyRoutes] = routes
		}
		if len(exitTagSet) > 0 {
			exit := make([]string, 0, len(exitTagSet))
			for t := range exitTagSet {
				exit = append(exit, t)
			}
			sort.Strings(exit)
			section[policyKeyExitNode] = exit
		}
		doc[policyKeyAutoApprovers] = section
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy document: %w", err)
	}
	return string(out), nil
}
