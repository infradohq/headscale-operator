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
	"slices"
	"sort"

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
	"sigs.k8s.io/controller-runtime/pkg/event"
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

	// headscaleRefIndex names the field index on
	// HeadscaleAutoApprover.spec.headscaleRef. Registered in SetupWithManager
	// so collectApprovers and requeueApproversForHeadscale can fetch only the
	// approvers targeting a given Headscale instead of listing the namespace
	// and filtering client-side.
	headscaleRefIndex = "spec.headscaleRef"
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

	if approver.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, approver)
	}

	if err := r.ensureFinalizer(ctx, approver); err != nil {
		return ctrl.Result{}, err
	}

	headscale, err := r.getHeadscale(ctx, approver)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.setCondition(ctx, approver, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "HeadscaleNotFound",
				Message: fmt.Sprintf("Referenced Headscale %q not found", approver.Spec.HeadscaleRef),
			}); err != nil {
				return ctrl.Result{}, err
			}
			// No periodic requeue: the Watches() on Headscale fires Create
			// events for new instances, which requeues every approver targeting
			// them via requeueApproversForHeadscale.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
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
		// No periodic requeue: the GenerationChangedPredicate on the Headscale
		// watch fires when the user flips policy.mode (a spec change), which
		// requeues this approver.
		return ctrl.Result{}, nil
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

// handleDeletion attempts to re-push the merged policy without this resource's
// contributions, then removes the finalizer.
//
// Cleanup is best-effort: a failed lookup or push is logged but does not block
// finalizer removal. Blocking would risk leaving the CR stuck Terminating
// forever (e.g., Headscale is down for an extended period, or the user pushed
// an invalid base policy that makes SetPolicy persistently reject), and the
// only on-disk consequence of skipping the re-push is some stale entries in
// the active policy. Those entries are inert — no node carries the deleted
// tag anymore — and they are cleaned up the next time any sibling approver
// reconciles (collectApprovers excludes the deleted one).
func (r *HeadscaleAutoApproverReconciler) handleDeletion(
	ctx context.Context,
	approver *headscalev1beta1.HeadscaleAutoApprover,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(approver, headscaleAutoApproverFinalizer) {
		return ctrl.Result{}, nil
	}

	headscale, err := r.getHeadscale(ctx, approver)
	switch {
	case apierrors.IsNotFound(err):
		// Parent already gone — nothing to re-push.
	case err != nil:
		log.Error(err, "Failed to fetch parent Headscale during deletion; proceeding with finalizer removal")
	case headscale.Spec.Config.Policy.Mode == policyModeDatabase:
		if err := r.renderAndPush(ctx, headscale); err != nil {
			log.Error(err, "Failed to re-push policy during deletion; proceeding with finalizer removal")
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
// the Headscale that targets it by name and is not being deleted. Uses the
// spec.headscaleRef field index for server-side filtering.
func (r *HeadscaleAutoApproverReconciler) collectApprovers(
	ctx context.Context,
	headscale *headscalev1beta1.Headscale,
) ([]headscalev1beta1.HeadscaleAutoApprover, error) {
	list := &headscalev1beta1.HeadscaleAutoApproverList{}
	if err := r.List(ctx, list,
		client.InNamespace(headscale.Namespace),
		client.MatchingFields{headscaleRefIndex: headscale.Name},
	); err != nil {
		return nil, err
	}

	out := make([]headscalev1beta1.HeadscaleAutoApprover, 0, len(list.Items))
	for _, item := range list.Items {
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
	if err := r.List(ctx, list,
		client.InNamespace(headscale.Namespace),
		client.MatchingFields{headscaleRefIndex: headscale.Name},
	); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list HeadscaleAutoApprovers for Headscale watch")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, item := range list.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: item.Name, Namespace: item.Namespace},
		})
	}
	return requests
}

// reconcilableUpdates filters Update events on the For() watch so the
// controller only reconciles on changes that actually require work: spec edits
// (generation bumps) and deletion-timestamp transitions. Status-only patches
// (our own setCondition writes) and finalizer-only edits (our own
// ensureFinalizer writes) are filtered out so we don't re-push the policy in
// response to the controller's own writes — without this, a single user
// `kubectl apply` triggers 3-4 redundant SetPolicy round-trips.
var reconcilableUpdates = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectOld == nil || e.ObjectNew == nil {
			return false
		}
		if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
			return true
		}
		return !e.ObjectOld.GetDeletionTimestamp().Equal(e.ObjectNew.GetDeletionTimestamp())
	},
}

// SetupWithManager sets up the controller with the Manager.
func (r *HeadscaleAutoApproverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&headscalev1beta1.HeadscaleAutoApprover{},
		headscaleRefIndex,
		func(obj client.Object) []string {
			return []string{obj.(*headscalev1beta1.HeadscaleAutoApprover).Spec.HeadscaleRef}
		},
	); err != nil {
		return fmt.Errorf("failed to register %s field indexer: %w", headscaleRefIndex, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&headscalev1beta1.HeadscaleAutoApprover{},
			builder.WithPredicates(reconcilableUpdates),
		).
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
			// Canonicalize: sort + dedupe so user-input ordering doesn't churn
			// the rendered output and trip up SetPolicy idempotency.
			sorted := append([]string(nil), principals...)
			sort.Strings(sorted)
			sorted = slices.Compact(sorted)
			owners[tag] = sorted
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
