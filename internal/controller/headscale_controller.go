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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	headscaleFinalizer = "headscale.infrado.cloud/finalizer"
	configMapName      = "headscale-config"
	statefulSetName    = "headscale"
	serviceName        = "headscale"
	metricsServiceName = "headscale-metrics"
)

// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch

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

	// Update the ConfigMap if it exists
	foundConfigMap.Data = configMap.Data
	log.Info("Updating ConfigMap", "Namespace", configMap.Namespace, "Name", configMap.Name)
	return r.Update(ctx, foundConfigMap)
}

// reconcileStatefulSet reconciles the StatefulSet for Headscale
func (r *HeadscaleReconciler) reconcileStatefulSet(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	log := logf.FromContext(ctx)

	statefulSet := r.statefulSetForHeadscale(headscale)
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

	// Update the StatefulSet if needed
	if foundStatefulSet.Spec.Replicas == nil || *foundStatefulSet.Spec.Replicas != headscale.Spec.Replicas {
		foundStatefulSet.Spec.Replicas = &headscale.Spec.Replicas
		foundStatefulSet.Spec.Template = statefulSet.Spec.Template
		log.Info("Updating StatefulSet", "Namespace", statefulSet.Namespace, "Name", statefulSet.Name)
		return r.Update(ctx, foundStatefulSet)
	}
	return nil
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

// updateStatus updates the status of the Headscale instance
func (r *HeadscaleReconciler) updateStatus(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	headscale.Status.Conditions = []metav1.Condition{
		{
			Type:               "Available",
			Status:             metav1.ConditionTrue,
			Reason:             "Reconciled",
			Message:            "Headscale is running",
			LastTransitionTime: metav1.Now(),
		},
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

// extractPort extracts the port number from an address string like "127.0.0.1:8080" or ":8080"
func extractPort(addr string, defaultPort int32) int32 {
	if addr == "" {
		return defaultPort
	}

	// Split by colon to get the port part
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return defaultPort
	}

	portStr := parts[len(parts)-1]
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return defaultPort
	}

	return int32(port)
}

// statefulSetForHeadscale returns a StatefulSet object for Headscale
func (r *HeadscaleReconciler) statefulSetForHeadscale(h *headscalev1beta1.Headscale) *appsv1.StatefulSet {
	labels := labelsForHeadscale(h.Name)
	replicas := h.Spec.Replicas

	// Determine the image to use
	image := fmt.Sprintf("headscale/headscale:%s", h.Spec.Version)

	// Extract ports from configuration
	httpPort := extractPort(h.Spec.Config.ListenAddr, 8080)
	metricsPort := extractPort(h.Spec.Config.MetricsListenAddr, 9090)
	grpcPort := extractPort(h.Spec.Config.GRPCListenAddr, 50443)

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
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "headscale",
							Image: image,
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
					},
					Volumes: []corev1.Volume{
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
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("128Mi"),
							},
						},
					},
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
		Named("headscale").
		Complete(r)
}
