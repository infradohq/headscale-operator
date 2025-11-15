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
	"crypto/sha256"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

// HeadscaleReconciler reconciles a Headscale object
type HeadscaleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	headscaleFinalizer      = "headscale.infrado.cloud/finalizer"
	configMapName           = "headscale-config"
	statefulSetName         = "headscale"
	serviceName             = "headscale"
	metricsServiceName      = "headscale-metrics"
	serviceAccountName      = "headscale"
	roleName                = "headscale"
	roleBindingName         = "headscale"
	defaultAPIKeySecretName = "headscale-api-key"
)

// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HeadscaleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Headscale instance
	headscale := &headscalev1beta1.Headscale{}
	err := r.Get(ctx, req.NamespacedName, headscale)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Headscale resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Headscale")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if headscale.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, headscale)
	}

	// Handle finalizer
	if err := r.ensureFinalizer(ctx, headscale); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile RBAC resources only if APIKey.AutoManage is true or nil (default true)
	if headscale.Spec.APIKey.AutoManage == nil || *headscale.Spec.APIKey.AutoManage {
		// Reconcile ServiceAccount
		if err := r.reconcileServiceAccount(ctx, headscale); err != nil {
			log.Error(err, "Failed to reconcile ServiceAccount")
			return ctrl.Result{}, err
		}

		// Reconcile Role
		if err := r.reconcileRole(ctx, headscale); err != nil {
			log.Error(err, "Failed to reconcile Role")
			return ctrl.Result{}, err
		}

		// Reconcile RoleBinding
		if err := r.reconcileRoleBinding(ctx, headscale); err != nil {
			log.Error(err, "Failed to reconcile RoleBinding")
			return ctrl.Result{}, err
		}
	}

	// Reconcile ConfigMap
	if err := r.reconcileConfigMap(ctx, headscale); err != nil {
		log.Error(err, "Failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile StatefulSet
	if err := r.reconcileStatefulSet(ctx, headscale); err != nil {
		log.Error(err, "Failed to reconcile StatefulSet")
		return ctrl.Result{}, err
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, headscale); err != nil {
		log.Error(err, "Failed to reconcile Service")
		return ctrl.Result{}, err
	}

	// Reconcile Metrics Service
	if err := r.reconcileMetricsService(ctx, headscale); err != nil {
		log.Error(err, "Failed to reconcile Metrics Service")
		return ctrl.Result{}, err
	}

	// Update status
	if err := r.updateStatus(ctx, headscale); err != nil {
		log.Error(err, "Failed to update Headscale status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleDeletion handles the deletion of a Headscale instance
func (r *HeadscaleReconciler) handleDeletion(ctx context.Context, headscale *headscalev1beta1.Headscale) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if controllerutil.ContainsFinalizer(headscale, headscaleFinalizer) {
		// Perform cleanup logic here if needed
		log.Info("Performing cleanup for Headscale", "Name", headscale.Name)

		// Remove finalizer
		controllerutil.RemoveFinalizer(headscale, headscaleFinalizer)
		if err := r.Update(ctx, headscale); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// ensureFinalizer ensures the finalizer is present on the Headscale instance
func (r *HeadscaleReconciler) ensureFinalizer(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	if !controllerutil.ContainsFinalizer(headscale, headscaleFinalizer) {
		controllerutil.AddFinalizer(headscale, headscaleFinalizer)
		return r.Update(ctx, headscale)
	}
	return nil
}

// reconcileConfigMap reconciles the ConfigMap for Headscale
func (r *HeadscaleReconciler) reconcileConfigMap(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	configMap, err := r.configMapForHeadscale(headscale)
	if err != nil {
		return fmt.Errorf("failed to generate ConfigMap: %w", err)
	}
	if err := controllerutil.SetControllerReference(headscale, configMap, r.Scheme); err != nil {
		return err
	}

	foundConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, foundConfigMap)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating ConfigMap", "Namespace", configMap.Namespace, "Name", configMap.Name)
		return r.Create(ctx, configMap)
	} else if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Update the ConfigMap only if data changed
	if !equality.Semantic.DeepEqual(foundConfigMap.Data, configMap.Data) {
		foundConfigMap.Data = configMap.Data
		log.Info("Updating ConfigMap", "Namespace", configMap.Namespace, "Name", configMap.Name)
		return r.Update(ctx, foundConfigMap)
	}
	return nil
}

// reconcileStatefulSet reconciles the StatefulSet for Headscale
func (r *HeadscaleReconciler) reconcileStatefulSet(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	// Compute hash of the config spec directly from the Headscale CR
	configHash := computeConfigHashFromSpec(&headscale.Spec.Config)

	statefulSet := r.statefulSetForHeadscale(headscale, configHash)
	if err := controllerutil.SetControllerReference(headscale, statefulSet, r.Scheme); err != nil {
		return err
	}

	foundStatefulSet := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: statefulSet.Name, Namespace: statefulSet.Namespace}, foundStatefulSet)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating StatefulSet", "Namespace", statefulSet.Namespace, "Name", statefulSet.Name)
		return r.Create(ctx, statefulSet)
	} else if err != nil {
		return fmt.Errorf("failed to get StatefulSet: %w", err)
	}

	// NOTE: for now we check the whole spec, including the immutable fields
	// if we decided to change those fields later, we would need to change
	// the logic here to avoid trying doing that.
	if equality.Semantic.DeepEqual(foundStatefulSet.Spec, statefulSet.Spec) {
		return nil
	}

	foundStatefulSet.Spec = statefulSet.Spec

	log.Info("Updating StatefulSet", "Namespace", statefulSet.Namespace, "Name", statefulSet.Name)
	return r.Update(ctx, foundStatefulSet)
}

// reconcileService reconciles the Service for Headscale
func (r *HeadscaleReconciler) reconcileService(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	service := r.serviceForHeadscale(headscale)
	if err := controllerutil.SetControllerReference(headscale, service, r.Scheme); err != nil {
		return err
	}

	foundService := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Service", "Namespace", service.Namespace, "Name", service.Name)
		return r.Create(ctx, service)
	} else if err != nil {
		return fmt.Errorf("failed to get Service: %w", err)
	}
	return nil
}

// reconcileMetricsService reconciles the Metrics Service for Headscale
func (r *HeadscaleReconciler) reconcileMetricsService(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	metricsService := r.metricsServiceForHeadscale(headscale)
	if err := controllerutil.SetControllerReference(headscale, metricsService, r.Scheme); err != nil {
		return err
	}

	foundMetricsService := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: metricsService.Name, Namespace: metricsService.Namespace}, foundMetricsService)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Metrics Service", "Namespace", metricsService.Namespace, "Name", metricsService.Name)
		return r.Create(ctx, metricsService)
	} else if err != nil {
		return fmt.Errorf("failed to get Metrics Service: %w", err)
	}
	return nil
}

// reconcileServiceAccount reconciles the ServiceAccount for Headscale pods
func (r *HeadscaleReconciler) reconcileServiceAccount(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	sa := r.serviceAccountForHeadscale(headscale)
	if err := controllerutil.SetControllerReference(headscale, sa, r.Scheme); err != nil {
		return err
	}

	foundSA := &corev1.ServiceAccount{}
	err := r.Get(ctx, types.NamespacedName{Name: sa.Name, Namespace: sa.Namespace}, foundSA)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating ServiceAccount", "Namespace", sa.Namespace, "Name", sa.Name)
		return r.Create(ctx, sa)
	} else if err != nil {
		return fmt.Errorf("failed to get ServiceAccount: %w", err)
	}

	// Update the ServiceAccount only if labels changed
	if !equality.Semantic.DeepEqual(foundSA.Labels, sa.Labels) {
		foundSA.Labels = sa.Labels
		log.Info("Updating ServiceAccount", "Namespace", sa.Namespace, "Name", sa.Name)
		return r.Update(ctx, foundSA)
	}
	return nil
}

// reconcileRole reconciles the Role for Headscale pods
func (r *HeadscaleReconciler) reconcileRole(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	role := r.roleForHeadscale(headscale)
	if err := controllerutil.SetControllerReference(headscale, role, r.Scheme); err != nil {
		return err
	}

	foundRole := &rbacv1.Role{}
	err := r.Get(ctx, types.NamespacedName{Name: role.Name, Namespace: role.Namespace}, foundRole)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Role", "Namespace", role.Namespace, "Name", role.Name)
		return r.Create(ctx, role)
	} else if err != nil {
		return fmt.Errorf("failed to get Role: %w", err)
	}

	// Update the Role only if rules changed
	if !equality.Semantic.DeepEqual(foundRole.Rules, role.Rules) {
		foundRole.Rules = role.Rules
		log.Info("Updating Role", "Namespace", role.Namespace, "Name", role.Name)
		return r.Update(ctx, foundRole)
	}
	return nil
}

// reconcileRoleBinding reconciles the RoleBinding for Headscale pods
func (r *HeadscaleReconciler) reconcileRoleBinding(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	rb := r.roleBindingForHeadscale(headscale)
	if err := controllerutil.SetControllerReference(headscale, rb, r.Scheme); err != nil {
		return err
	}

	foundRB := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{Name: rb.Name, Namespace: rb.Namespace}, foundRB)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating RoleBinding", "Namespace", rb.Namespace, "Name", rb.Name)
		return r.Create(ctx, rb)
	} else if err != nil {
		return fmt.Errorf("failed to get RoleBinding: %w", err)
	}

	// Update the RoleBinding only if subjects or roleRef changed
	if !equality.Semantic.DeepEqual(foundRB.Subjects, rb.Subjects) || !equality.Semantic.DeepEqual(foundRB.RoleRef, rb.RoleRef) {
		foundRB.Subjects = rb.Subjects
		foundRB.RoleRef = rb.RoleRef
		log.Info("Updating RoleBinding", "Namespace", rb.Namespace, "Name", rb.Name)
		return r.Update(ctx, foundRB)
	}
	return nil
}

// updateStatus updates the status of the Headscale instance
func (r *HeadscaleReconciler) updateStatus(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	// Desired condition
	desired := metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciled",
		Message: "Headscale is running",
	}

	existing := meta.FindStatusCondition(headscale.Status.Conditions, desired.Type)
	if existing != nil && existing.Status == desired.Status && existing.Reason == desired.Reason && existing.Message == desired.Message {
		// No change needed; keep original LastTransitionTime
		return nil
	}

	desired.LastTransitionTime = metav1.Now()
	// Replace or append condition
	if existing == nil {
		headscale.Status.Conditions = append(headscale.Status.Conditions, desired)
	} else {
		for i := range headscale.Status.Conditions {
			if headscale.Status.Conditions[i].Type == desired.Type {
				headscale.Status.Conditions[i] = desired
				break
			}
		}
	}
	return r.Status().Update(ctx, headscale)
}

// configMapForHeadscale returns a ConfigMap object for Headscale configuration
func (r *HeadscaleReconciler) configMapForHeadscale(h *headscalev1beta1.Headscale) (*corev1.ConfigMap, error) {
	configData, err := yaml.Marshal(h.Spec.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal headscale config: %w", err)
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
		Data: map[string]string{
			"config.yaml": string(configData),
		},
	}, nil
}

// computeConfigHashFromSpec computes a SHA256 hash of the Headscale config spec
func computeConfigHashFromSpec(config *headscalev1beta1.HeadscaleConfig) string {
	// Marshal the config to YAML to get a consistent representation
	configData, err := yaml.Marshal(config)
	if err != nil {
		// If marshaling fails, return empty string - the StatefulSet will still work
		// but won't get automatic restarts on config changes
		return ""
	}

	hash := sha256.New()
	hash.Write(configData)
	return fmt.Sprintf("%x", hash.Sum(nil))[:16]
}

// statefulSetForHeadscale returns a StatefulSet object for Headscale
func (r *HeadscaleReconciler) statefulSetForHeadscale(h *headscalev1beta1.Headscale, configHash string) *appsv1.StatefulSet {
	labels := labelsForHeadscale(h.Name)
	replicas := h.Spec.Replicas

	// Determine the image to use
	image := fmt.Sprintf("%s:%s", h.Spec.Image, h.Spec.Version)

	// Extract ports from configuration
	httpPort := extractPort(h.Spec.Config.ListenAddr, 8080)
	metricsPort := extractPort(h.Spec.Config.MetricsListenAddr, 9090)
	grpcPort := extractPort(h.Spec.Config.GRPCListenAddr, 50443)

	// Get PVC configuration with defaults
	pvcSize := resource.NewQuantity(128*1024*1024, resource.BinarySI) // 128Mi default
	if h.Spec.PersistentVolumeClaim.Size != nil {
		pvcSize = h.Spec.PersistentVolumeClaim.Size
	}

	// Build PVC spec
	pvcSpec := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		},
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: *pvcSize,
			},
		},
		StorageClassName: h.Spec.PersistentVolumeClaim.StorageClassName,
	}

	// Build container list starting with Headscale
	containers := []corev1.Container{
		{
			Name:            "headscale",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command: []string{
				"headscale",
				"serve",
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "http",
					ContainerPort: httpPort,
					Protocol:      corev1.ProtocolTCP,
				},
				{
					Name:          "metrics",
					ContainerPort: metricsPort,
					Protocol:      corev1.ProtocolTCP,
				},
				{
					Name:          "grpc",
					ContainerPort: grpcPort,
					Protocol:      corev1.ProtocolTCP,
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "config",
					MountPath: "/etc/headscale",
					ReadOnly:  true,
				},
				{
					Name:      "data",
					MountPath: "/var/lib/headscale",
				},
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/health",
						Port: intstr.FromInt32(httpPort),
					},
				},
				InitialDelaySeconds: 30,
				PeriodSeconds:       10,
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/health",
						Port: intstr.FromInt32(httpPort),
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       5,
			},
		},
	}

	// Build volumes list
	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		},
	}

	// Add API key manager sidecar if auto_manage is enabled
	autoManage := true // default value
	if h.Spec.APIKey.AutoManage != nil {
		autoManage = *h.Spec.APIKey.AutoManage
	}

	if autoManage {
		// Add socket volume for communication between Headscale and API key manager
		volumes = append(volumes, corev1.Volume{
			Name: "socket",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})

		// Update Headscale container to mount the socket volume
		containers[0].VolumeMounts = append(containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      "socket",
			MountPath: "/var/run/headscale",
		})

		// Determine the API key manager image to use
		managerImage := "ghcr.io/infradohq/headscale-operator/apikey-manager:latest"
		if h.Spec.APIKey.ManagerImage != "" {
			managerImage = h.Spec.APIKey.ManagerImage
		}

		// Add API key manager sidecar
		containers = append(containers, corev1.Container{
			Name:            "apikey-manager",
			Image:           managerImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Args: []string{
				"--socket-path=" + h.Spec.Config.UnixSocket,
				"--secret-name=" + h.Spec.APIKey.SecretName,
				"--expiration=" + h.Spec.APIKey.Expiration,
				"--rotation-buffer=" + h.Spec.APIKey.RotationBuffer,
			},
			Env: []corev1.EnvVar{
				{
					Name: "POD_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "socket",
					MountPath: "/var/run/headscale",
				},
			},
		})
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName,
			Namespace: h.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			ServiceName: serviceName,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"headscale.infrado.cloud/config-hash": configHash,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(65532)),
						RunAsGroup:   ptr.To(int64(65532)),
						FSGroup:      ptr.To(int64(65532)),
						RunAsNonRoot: ptr.To(true),
					},
					Containers: containers,
					Volumes:    volumes,
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: pvcSpec,
				},
			},
		},
	}
}

// serviceForHeadscale returns a Service object for Headscale
func (r *HeadscaleReconciler) serviceForHeadscale(h *headscalev1beta1.Headscale) *corev1.Service {
	labels := labelsForHeadscale(h.Name)

	// Extract ports from configuration
	httpPort := extractPort(h.Spec.Config.ListenAddr, 8080)
	grpcPort := extractPort(h.Spec.Config.GRPCListenAddr, 50443)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: h.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       httpPort,
					TargetPort: intstr.FromInt32(httpPort),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "grpc",
					Port:       grpcPort,
					TargetPort: intstr.FromInt32(grpcPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// metricsServiceForHeadscale returns a Service object for Headscale metrics
func (r *HeadscaleReconciler) metricsServiceForHeadscale(h *headscalev1beta1.Headscale) *corev1.Service {
	labels := labelsForHeadscale(h.Name)

	// Extract metrics port from configuration
	metricsPort := extractPort(h.Spec.Config.MetricsListenAddr, 9090)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricsServiceName,
			Namespace: h.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       metricsPort,
					TargetPort: intstr.FromInt32(metricsPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// serviceAccountForHeadscale returns a ServiceAccount object for Headscale pods
func (r *HeadscaleReconciler) serviceAccountForHeadscale(h *headscalev1beta1.Headscale) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
	}
}

// roleForHeadscale returns a Role object for Headscale pods with permissions to manage Secrets
func (r *HeadscaleReconciler) roleForHeadscale(h *headscalev1beta1.Headscale) *rbacv1.Role {
	// Determine the API key secret name from the spec, with fallback to default
	secretName := h.Spec.APIKey.SecretName
	if secretName == "" {
		secretName = defaultAPIKeySecretName
	}

	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secretName},
				Verbs:         []string{"get", "list", "watch", "update", "patch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"create"},
			},
		},
	}
}

// roleBindingForHeadscale returns a RoleBinding object for Headscale pods
func (r *HeadscaleReconciler) roleBindingForHeadscale(h *headscalev1beta1.Headscale) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: h.Namespace,
			},
		},
	}
}

// labelsForHeadscale returns the labels for selecting the resources
func labelsForHeadscale(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "headscale",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "headscale-operator",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HeadscaleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&headscalev1beta1.Headscale{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Named("headscale").
		Complete(r)
}
