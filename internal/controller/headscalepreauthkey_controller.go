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
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
	hsclient "github.com/infradohq/headscale-operator/pkg/headscale"
)

const (
	headscalePreAuthKeyFinalizer = "headscale.infrado.cloud/preauthkey-finalizer"
	preAuthKeySecretKey          = "key"
)

// HeadscalePreAuthKeyReconciler reconciles a HeadscalePreAuthKey object
type HeadscalePreAuthKeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscalepreauthkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscalepreauthkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscalepreauthkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales,verbs=get
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleusers,verbs=get
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HeadscalePreAuthKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HeadscalePreAuthKey instance
	preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{}
	err := r.Get(ctx, req.NamespacedName, preAuthKey)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("HeadscalePreAuthKey resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HeadscalePreAuthKey")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if preAuthKey.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, preAuthKey)
	}

	// Handle finalizer
	if err := r.ensureFinalizer(ctx, preAuthKey); err != nil {
		return ctrl.Result{}, err
	}

	// Get the referenced Headscale instance
	headscale := &headscalev1beta1.Headscale{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      preAuthKey.Spec.HeadscaleRef,
		Namespace: preAuthKey.Namespace,
	}, headscale)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Error(err, "Referenced Headscale instance not found", "HeadscaleRef", preAuthKey.Spec.HeadscaleRef)
			if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "HeadscaleNotFound",
				Message: fmt.Sprintf("Referenced Headscale instance %s not found", preAuthKey.Spec.HeadscaleRef),
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		log.Error(err, "Failed to get referenced Headscale instance")
		return ctrl.Result{}, err
	}

	// Validate that either HeadscaleUserRef or UserID is provided
	if preAuthKey.Spec.HeadscaleUserRef == "" && preAuthKey.Spec.UserID == 0 {
		log.Error(nil, "Either HeadscaleUserRef or UserID must be specified")
		if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSpec",
			Message: "Either HeadscaleUserRef or UserID must be specified",
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Validate that both are not provided
	if preAuthKey.Spec.HeadscaleUserRef != "" && preAuthKey.Spec.UserID != 0 {
		log.Error(nil, "Only one of HeadscaleUserRef or UserID should be specified")
		if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSpec",
			Message: "Only one of HeadscaleUserRef or UserID should be specified, not both",
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Determine the user ID to use
	var userID uint64
	if preAuthKey.Spec.HeadscaleUserRef != "" {
		// Get the referenced HeadscaleUser instance
		headscaleUser := &headscalev1beta1.HeadscaleUser{}
		err = r.Get(ctx, types.NamespacedName{
			Name:      preAuthKey.Spec.HeadscaleUserRef,
			Namespace: preAuthKey.Namespace,
		}, headscaleUser)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Error(err, "Referenced HeadscaleUser not found", "HeadscaleUserRef", preAuthKey.Spec.HeadscaleUserRef)
				if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
					Type:    "Ready",
					Status:  metav1.ConditionFalse,
					Reason:  "UserNotFound",
					Message: fmt.Sprintf("Referenced HeadscaleUser %s not found", preAuthKey.Spec.HeadscaleUserRef),
				}); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			log.Error(err, "Failed to get referenced HeadscaleUser")
			return ctrl.Result{}, err
		}

		// Check if user has a UserID (is created in Headscale)
		if headscaleUser.Status.UserID == "" {
			log.Info("User not yet created in Headscale, waiting", "HeadscaleUserRef", preAuthKey.Spec.HeadscaleUserRef)
			if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "UserNotReady",
				Message: fmt.Sprintf("User %s not yet created in Headscale", preAuthKey.Spec.HeadscaleUserRef),
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// Parse the user ID from HeadscaleUser status
		parsedUserID, err := strconv.ParseUint(headscaleUser.Status.UserID, 10, 64)
		if err != nil {
			log.Error(err, "Failed to parse user ID from HeadscaleUser")
			if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "InvalidUserID",
				Message: fmt.Sprintf("Failed to parse user ID: %v", err),
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, fmt.Errorf("failed to parse user ID: %w", err)
		}
		userID = parsedUserID

		// Set owner reference to HeadscaleUser for automatic cleanup
		// This ensures when HeadscaleUser is deleted, the PreAuthKey is also deleted
		if err := r.ensureHeadscaleUserOwnerReference(ctx, preAuthKey, headscaleUser); err != nil {
			log.Error(err, "Failed to set owner reference to HeadscaleUser")
			return ctrl.Result{}, err
		}
	} else {
		// Use the directly provided UserID
		userID = preAuthKey.Spec.UserID
	}

	// Check if the preauth key already exists
	if preAuthKey.Status.KeyID != "" {
		// PreAuth key already created
		if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "PreAuthKeyReady",
			Message: "PreAuth key is ready",
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Create the preauth key in Headscale
	if err := r.createPreAuthKey(ctx, headscale, userID, preAuthKey); err != nil {
		log.Error(err, "Failed to create preauth key in Headscale")
		if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "PreAuthKeyCreationFailed",
			Message: fmt.Sprintf("Failed to create preauth key: %v", err),
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Update status to indicate preauth key is ready
	if err := r.updateStatusCondition(ctx, preAuthKey, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "PreAuthKeyCreated",
		Message: "PreAuth key successfully created",
	}); err != nil {
		return ctrl.Result{}, err
	}

	userRefInfo := fmt.Sprintf("UserID: %d", userID)
	if preAuthKey.Spec.HeadscaleUserRef != "" {
		userRefInfo = fmt.Sprintf("HeadscaleUserRef: %s", preAuthKey.Spec.HeadscaleUserRef)
	}
	log.Info("Successfully created preauth key", "UserInfo", userRefInfo)
	return ctrl.Result{}, nil
}

// handleDeletion handles the deletion of a HeadscalePreAuthKey instance
func (r *HeadscalePreAuthKeyReconciler) handleDeletion(
	ctx context.Context,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if controllerutil.ContainsFinalizer(preAuthKey, headscalePreAuthKeyFinalizer) {
		// Get the referenced Headscale instance
		headscale := &headscalev1beta1.Headscale{}
		err := r.Get(ctx, types.NamespacedName{
			Name:      preAuthKey.Spec.HeadscaleRef,
			Namespace: preAuthKey.Namespace,
		}, headscale)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("Referenced Headscale instance not found during deletion, removing finalizer",
					"HeadscaleRef", preAuthKey.Spec.HeadscaleRef)
				controllerutil.RemoveFinalizer(preAuthKey, headscalePreAuthKeyFinalizer)
				if err := r.Update(ctx, preAuthKey); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			log.Error(err, "Failed to get referenced Headscale instance during deletion")
			return ctrl.Result{}, err
		}

		// Determine the user ID if we need to expire the key
		var userID uint64
		if preAuthKey.Status.KeyID != "" {
			if preAuthKey.Spec.HeadscaleUserRef != "" {
				// Get the referenced HeadscaleUser instance
				headscaleUser := &headscalev1beta1.HeadscaleUser{}
				err = r.Get(ctx, types.NamespacedName{
					Name:      preAuthKey.Spec.HeadscaleUserRef,
					Namespace: preAuthKey.Namespace,
				}, headscaleUser)
				if err != nil {
					if apierrors.IsNotFound(err) {
						log.Info("Referenced HeadscaleUser not found during deletion, skipping expiration",
							"HeadscaleUserRef", preAuthKey.Spec.HeadscaleUserRef)
					} else {
						log.Error(err, "Failed to get referenced HeadscaleUser during deletion")
						return ctrl.Result{}, err
					}
				} else if headscaleUser.Status.UserID != "" {
					parsedUserID, err := strconv.ParseUint(headscaleUser.Status.UserID, 10, 64)
					if err == nil {
						userID = parsedUserID
					} else {
						log.Error(err, "Failed to parse user ID during deletion")
					}
				}
			} else if preAuthKey.Spec.UserID != 0 {
				userID = preAuthKey.Spec.UserID
			}
		}

		// Expire the preauth key in Headscale if we have a valid userID
		if userID != 0 {
			// Get the preauth key string from the secret
			keyString, err := r.getPreAuthKeyFromSecret(ctx, preAuthKey)
			if err != nil {
				if apierrors.IsNotFound(err) {
					log.Info("Secret not found during deletion, skipping expiration")
				} else {
					log.Error(err, "Failed to get preauth key from secret")
					return ctrl.Result{}, err
				}
			} else {
				// We have the key, try to expire it
				if err := r.expirePreAuthKey(ctx, headscale, userID, keyString); err != nil {
					// Check if the error is because the key was already expired or not found
					// This can happen if the key expired naturally or was manually expired
					if isPreAuthKeyNotFoundError(err) {
						log.Info("PreAuth key already expired or not found in Headscale, skipping")
					} else {
						log.Error(err, "Failed to expire preauth key in Headscale")
						return ctrl.Result{}, err
					}
				} else {
					log.Info("Successfully expired preauth key in Headscale", "UserID", userID)
				}
			}
		}

		// Delete the secret
		if err := r.deleteSecret(ctx, preAuthKey); err != nil {
			log.Error(err, "Failed to delete secret")
			// Continue with finalizer removal even if secret deletion fails
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(preAuthKey, headscalePreAuthKeyFinalizer)
		if err := r.Update(ctx, preAuthKey); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// ensureFinalizer ensures the finalizer is present on the HeadscalePreAuthKey instance
func (r *HeadscalePreAuthKeyReconciler) ensureFinalizer(
	ctx context.Context,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
) error {
	if !controllerutil.ContainsFinalizer(preAuthKey, headscalePreAuthKeyFinalizer) {
		controllerutil.AddFinalizer(preAuthKey, headscalePreAuthKeyFinalizer)
		return r.Update(ctx, preAuthKey)
	}
	return nil
}

// ensureHeadscaleUserOwnerReference ensures the HeadscaleUser is set as an owner reference
// This enables automatic garbage collection - when HeadscaleUser is deleted, PreAuthKey is also deleted
// Uses blockOwnerDeletion to ensure PreAuthKey finalizer runs before HeadscaleUser is fully deleted
func (r *HeadscalePreAuthKeyReconciler) ensureHeadscaleUserOwnerReference(
	ctx context.Context,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
	headscaleUser *headscalev1beta1.HeadscaleUser,
) error {
	// Check if owner reference already exists
	for _, ref := range preAuthKey.GetOwnerReferences() {
		if ref.UID == headscaleUser.GetUID() {
			return nil // Already set
		}
	}

	// Create owner reference with blockOwnerDeletion=true
	// This ensures PreAuthKey is deleted (and key expired) before HeadscaleUser is fully removed
	blockOwnerDeletion := true
	gvk, err := apiutil.GVKForObject(headscaleUser, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK for HeadscaleUser: %w", err)
	}

	ownerRef := metav1.OwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Name:               headscaleUser.Name,
		UID:                headscaleUser.UID,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}

	preAuthKey.SetOwnerReferences(append(preAuthKey.GetOwnerReferences(), ownerRef))

	return r.Update(ctx, preAuthKey)
}

// createPreAuthKey creates a preauth key in the Headscale instance and stores it in a secret
func (r *HeadscalePreAuthKeyReconciler) createPreAuthKey(
	ctx context.Context,
	headscale *headscalev1beta1.Headscale,
	userID uint64,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
) error {
	log := logf.FromContext(ctx)

	// Parse expiration duration
	expiration, err := time.ParseDuration(preAuthKey.Spec.Expiration)
	if err != nil {
		return fmt.Errorf("failed to parse expiration: %w", err)
	}

	// Get the API key from the secret
	apiKey, err := getAPIKey(ctx, r.Client, headscale)
	if err != nil {
		return fmt.Errorf("failed to get API key: %w", err)
	}

	// Get the gRPC service address
	serviceAddr := getGRPCServiceAddress(headscale)

	// Create Headscale client with API key
	hsClient, err := hsclient.NewClientWithAPIKey(serviceAddr, apiKey)
	if err != nil {
		return fmt.Errorf("failed to create Headscale client: %w", err)
	}
	defer func() {
		if err := hsClient.Close(); err != nil {
			log.Error(err, "failed to close Headscale client")
		}
	}()

	// Create the preauth key
	key, err := hsClient.CreatePreAuthKey(
		ctx,
		userID,
		preAuthKey.Spec.Reusable,
		preAuthKey.Spec.Ephemeral,
		expiration,
		preAuthKey.Spec.Tags,
	)
	if err != nil {
		return fmt.Errorf("failed to create preauth key via Headscale API: %w", err)
	}

	log.Info("Created preauth key in Headscale", "UserID", userID, "KeyID", key.GetId())

	// Create secret with the preauth key
	secretName := preAuthKey.Spec.SecretName
	if secretName == "" {
		secretName = preAuthKey.Name
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: preAuthKey.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "headscale-operator",
				"headscale.infrado.cloud/type": "preauth-key",
			},
		},
		StringData: map[string]string{
			preAuthKeySecretKey: key.GetKey(),
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(preAuthKey, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	if err := r.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create secret: %w", err)
		}
		// Get the existing secret first
		existingSecret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: preAuthKey.Namespace}, existingSecret); err != nil {
			return fmt.Errorf("failed to get existing secret: %w", err)
		}
		// Update the data (use Data field with byte values for updates, not StringData)
		if existingSecret.Data == nil {
			existingSecret.Data = make(map[string][]byte)
		}
		existingSecret.Data[preAuthKeySecretKey] = []byte(key.GetKey())
		if err := r.Update(ctx, existingSecret); err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
	}

	// Update status with preauth key information
	preAuthKey.Status.KeyID = strconv.FormatUint(key.GetId(), 10)

	if err := r.Status().Update(ctx, preAuthKey); err != nil {
		return fmt.Errorf("failed to update HeadscalePreAuthKey status: %w", err)
	}

	return nil
}

// expirePreAuthKey expires a preauth key in the Headscale instance
func (r *HeadscalePreAuthKeyReconciler) expirePreAuthKey(
	ctx context.Context,
	headscale *headscalev1beta1.Headscale,
	userID uint64,
	keyString string,
) error {
	log := logf.FromContext(ctx)

	// Get the API key from the secret
	apiKey, err := getAPIKey(ctx, r.Client, headscale)
	if err != nil {
		return fmt.Errorf("failed to get API key: %w", err)
	}

	// Get the gRPC service address
	serviceAddr := getGRPCServiceAddress(headscale)

	// Create Headscale client with API key
	hsClient, err := hsclient.NewClientWithAPIKey(serviceAddr, apiKey)
	if err != nil {
		return fmt.Errorf("failed to create Headscale client: %w", err)
	}
	defer func() {
		if err := hsClient.Close(); err != nil {
			log.Error(err, "failed to close Headscale client")
		}
	}()

	// Expire the preauth key
	if err := hsClient.ExpirePreAuthKey(ctx, userID, keyString); err != nil {
		return fmt.Errorf("failed to expire preauth key via Headscale API: %w", err)
	}

	log.Info("Expired preauth key in Headscale", "UserID", userID)
	return nil
}

// isPreAuthKeyNotFoundError checks if the error indicates the preauth key
// doesn't exist or is already expired by checking for "AuthKey not found" message
func isPreAuthKeyNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	// Check for "AuthKey not found" in the error message using strings.Contains
	// to be more resilient to minor error message changes.
	// Headscale does not return proper gRPC status codes, so we rely on message matching.
	return strings.Contains(err.Error(), "AuthKey not found")
}

// getPreAuthKeyFromSecret retrieves the preauth key string from the secret
func (r *HeadscalePreAuthKeyReconciler) getPreAuthKeyFromSecret(
	ctx context.Context,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
) (string, error) {
	// Determine the secret name
	secretName := preAuthKey.Spec.SecretName
	if secretName == "" {
		secretName = preAuthKey.Name
	}

	// Get the secret
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: preAuthKey.Namespace,
	}, secret)
	if err != nil {
		return "", fmt.Errorf("failed to get preauth key secret: %w", err)
	}

	// Get the preauth key from the secret data
	keyBytes, ok := secret.Data[preAuthKeySecretKey]
	if !ok {
		return "", fmt.Errorf("preauth key not found in secret %s", secretName)
	}

	return string(keyBytes), nil
}

// deleteSecret deletes the secret containing the preauth key
func (r *HeadscalePreAuthKeyReconciler) deleteSecret(
	ctx context.Context,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
) error {
	// Determine the secret name
	secretName := preAuthKey.Spec.SecretName
	if secretName == "" {
		secretName = preAuthKey.Name
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: preAuthKey.Namespace,
		},
	}

	err := r.Delete(ctx, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	return nil
}

// updateStatusCondition updates the status condition of the HeadscalePreAuthKey
func (r *HeadscalePreAuthKeyReconciler) updateStatusCondition(
	ctx context.Context,
	preAuthKey *headscalev1beta1.HeadscalePreAuthKey,
	condition metav1.Condition,
) error {
	existing := meta.FindStatusCondition(preAuthKey.Status.Conditions, condition.Type)
	if existing != nil && existing.Status == condition.Status &&
		existing.Reason == condition.Reason && existing.Message == condition.Message {
		// No change needed
		return nil
	}

	condition.LastTransitionTime = metav1.Now()
	if existing == nil {
		preAuthKey.Status.Conditions = append(preAuthKey.Status.Conditions, condition)
	} else {
		for i := range preAuthKey.Status.Conditions {
			if preAuthKey.Status.Conditions[i].Type == condition.Type {
				preAuthKey.Status.Conditions[i] = condition
				break
			}
		}
	}

	return r.Status().Update(ctx, preAuthKey)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HeadscalePreAuthKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&headscalev1beta1.HeadscalePreAuthKey{}).
		Named("headscalepreauthkey").
		Complete(r)
}
