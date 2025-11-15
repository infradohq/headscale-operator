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
	"errors"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
	hsclient "github.com/infradohq/headscale-operator/pkg/headscale"
)

const (
	headscaleUserFinalizer = "headscale.infrado.cloud/user-finalizer"
)

// HeadscaleUserReconciler reconciles a HeadscaleUser object
type HeadscaleUserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleusers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscaleusers/finalizers,verbs=update
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales,verbs=get
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HeadscaleUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HeadscaleUser instance
	headscaleUser := &headscalev1beta1.HeadscaleUser{}
	err := r.Get(ctx, req.NamespacedName, headscaleUser)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("HeadscaleUser resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HeadscaleUser")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if headscaleUser.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, headscaleUser)
	}

	// Handle finalizer
	if err := r.ensureFinalizer(ctx, headscaleUser); err != nil {
		return ctrl.Result{}, err
	}

	// Get the referenced Headscale instance
	headscale := &headscalev1beta1.Headscale{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      headscaleUser.Spec.HeadscaleRef,
		Namespace: headscaleUser.Namespace,
	}, headscale)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Error(err, "Referenced Headscale instance not found", "HeadscaleRef", headscaleUser.Spec.HeadscaleRef)
			if err := r.updateStatusCondition(ctx, headscaleUser, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "HeadscaleNotFound",
				Message: fmt.Sprintf("Referenced Headscale instance %s not found", headscaleUser.Spec.HeadscaleRef),
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		log.Error(err, "Failed to get referenced Headscale instance")
		return ctrl.Result{}, err
	}

	// Check if the user already exists in Headscale
	if headscaleUser.Status.UserID != "" {
		// User already created, verify it still exists
		if err := r.verifyUser(ctx, headscale, headscaleUser); err != nil {
			// Only recreate if the user was not found
			if errors.Is(err, hsclient.ErrUserNotFound) {
				log.Info("User not found in Headscale, will recreate", "Username", headscaleUser.Spec.Username)
				// Clear the UserID so we recreate the user
				headscaleUser.Status.UserID = ""
				headscaleUser.Status.CreatedAt = ""
				if err := r.Status().Update(ctx, headscaleUser); err != nil {
					return ctrl.Result{}, err
				}
				// Requeue immediately to recreate the user
				return ctrl.Result{}, nil
			}
			// For other errors (network, API, etc), log and retry later
			log.Error(err, "Failed to verify user in Headscale")
			if err := r.updateStatusCondition(ctx, headscaleUser, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "UserVerificationFailed",
				Message: fmt.Sprintf("Failed to verify user: %v", err),
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}

		// User exists and is verified
		if err := r.updateStatusCondition(ctx, headscaleUser, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "UserReady",
			Message: "User is ready in Headscale",
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Create the user in Headscale
	if err := r.createUser(ctx, headscale, headscaleUser); err != nil {
		log.Error(err, "Failed to create user in Headscale")
		if err := r.updateStatusCondition(ctx, headscaleUser, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "UserCreationFailed",
			Message: fmt.Sprintf("Failed to create user: %v", err),
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Update status to indicate user is ready
	if err := r.updateStatusCondition(ctx, headscaleUser, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "UserCreated",
		Message: "User successfully created in Headscale",
	}); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Successfully created user in Headscale", "Username", headscaleUser.Spec.Username)
	return ctrl.Result{}, nil
}

// handleDeletion handles the deletion of a HeadscaleUser instance
func (r *HeadscaleUserReconciler) handleDeletion(ctx context.Context, headscaleUser *headscalev1beta1.HeadscaleUser) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if controllerutil.ContainsFinalizer(headscaleUser, headscaleUserFinalizer) {
		// Get the referenced Headscale instance
		headscale := &headscalev1beta1.Headscale{}
		err := r.Get(ctx, types.NamespacedName{
			Name:      headscaleUser.Spec.HeadscaleRef,
			Namespace: headscaleUser.Namespace,
		}, headscale)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("Referenced Headscale instance not found during deletion, removing finalizer", "HeadscaleRef", headscaleUser.Spec.HeadscaleRef)
				controllerutil.RemoveFinalizer(headscaleUser, headscaleUserFinalizer)
				if err := r.Update(ctx, headscaleUser); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			log.Error(err, "Failed to get referenced Headscale instance during deletion")
			return ctrl.Result{}, err
		}

		// Delete the user from Headscale if UserID is set
		if headscaleUser.Status.UserID != "" {
			if err := r.deleteUser(ctx, headscale, headscaleUser); err != nil {
				log.Error(err, "Failed to delete user from Headscale")
				// Continue with finalizer removal even if deletion fails
			} else {
				log.Info("Successfully deleted user from Headscale", "Username", headscaleUser.Spec.Username)
			}
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(headscaleUser, headscaleUserFinalizer)
		if err := r.Update(ctx, headscaleUser); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// ensureFinalizer ensures the finalizer is present on the HeadscaleUser instance
func (r *HeadscaleUserReconciler) ensureFinalizer(ctx context.Context, headscaleUser *headscalev1beta1.HeadscaleUser) error {
	if !controllerutil.ContainsFinalizer(headscaleUser, headscaleUserFinalizer) {
		controllerutil.AddFinalizer(headscaleUser, headscaleUserFinalizer)
		return r.Update(ctx, headscaleUser)
	}
	return nil
}

// createUser creates a user in the Headscale instance
func (r *HeadscaleUserReconciler) createUser(ctx context.Context, headscale *headscalev1beta1.Headscale, headscaleUser *headscalev1beta1.HeadscaleUser) error {
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

	// Create the user
	user, err := hsClient.CreateUser(ctx, headscaleUser.Spec.Username, headscaleUser.Spec.DisplayName, headscaleUser.Spec.Email, headscaleUser.Spec.PictureURL)
	if err != nil {
		return fmt.Errorf("failed to create user via Headscale API: %w", err)
	}

	log.Info("Created user in Headscale", "Username", headscaleUser.Spec.Username, "UserID", user.GetId())

	// Update status with user information
	headscaleUser.Status.UserID = strconv.FormatUint(user.GetId(), 10)
	if user.GetCreatedAt() != nil {
		headscaleUser.Status.CreatedAt = user.GetCreatedAt().AsTime().Format(time.RFC3339)
	}

	if err := r.Status().Update(ctx, headscaleUser); err != nil {
		return fmt.Errorf("failed to update HeadscaleUser status: %w", err)
	}

	return nil
}

// deleteUser deletes a user from the Headscale instance
func (r *HeadscaleUserReconciler) deleteUser(ctx context.Context, headscale *headscalev1beta1.Headscale, headscaleUser *headscalev1beta1.HeadscaleUser) error {
	log := logf.FromContext(ctx)

	// Parse UserID
	userID, err := strconv.ParseUint(headscaleUser.Status.UserID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse user ID: %w", err)
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

	// Delete the user
	if err := hsClient.DeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("failed to delete user via Headscale API: %w", err)
	}

	log.Info("Deleted user from Headscale", "Username", headscaleUser.Spec.Username, "UserID", userID)
	return nil
}

// verifyUser verifies that the user still exists in Headscale
func (r *HeadscaleUserReconciler) verifyUser(ctx context.Context, headscale *headscalev1beta1.Headscale, headscaleUser *headscalev1beta1.HeadscaleUser) error {
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

	// Try to get the user
	_, err = hsClient.GetUserByName(ctx, headscaleUser.Spec.Username)
	if err != nil {
		return fmt.Errorf("user not found in Headscale: %w", err)
	}

	return nil
}

// updateStatusCondition updates the status condition of the HeadscaleUser
func (r *HeadscaleUserReconciler) updateStatusCondition(ctx context.Context, headscaleUser *headscalev1beta1.HeadscaleUser, condition metav1.Condition) error {
	existing := meta.FindStatusCondition(headscaleUser.Status.Conditions, condition.Type)
	if existing != nil && existing.Status == condition.Status && existing.Reason == condition.Reason && existing.Message == condition.Message {
		// No change needed
		return nil
	}

	condition.LastTransitionTime = metav1.Now()
	if existing == nil {
		headscaleUser.Status.Conditions = append(headscaleUser.Status.Conditions, condition)
	} else {
		for i := range headscaleUser.Status.Conditions {
			if headscaleUser.Status.Conditions[i].Type == condition.Type {
				headscaleUser.Status.Conditions[i] = condition
				break
			}
		}
	}

	return r.Status().Update(ctx, headscaleUser)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HeadscaleUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&headscalev1beta1.HeadscaleUser{}).
		Named("headscaleuser").
		Complete(r)
}
