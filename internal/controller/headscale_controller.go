package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

// HeadscaleReconciler reconciles a Headscale object
type HeadscaleReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// notReadyRequeue is how long to wait before re-checking workload readiness when
// the Headscale StatefulSet has not yet reported its desired ready replicas.
// Pods are not directly owned by the CR, so this requeue is what lets the Ready
// condition self-heal once the workload settles (or surfaces a crash reason).
const notReadyRequeue = 30 * time.Second

const (
	headscaleFinalizer = "headscale.infrado.cloud/finalizer"

	// fieldOwner identifies this operator as the Server-Side Apply field
	// manager. Child resources are reconciled via SSA so the operator owns only
	// the fields it actually sets; API-server defaulting and external mutations
	// (e.g. GKE Autopilot injecting resource requests) are owned by other
	// managers and therefore never register as a diff or cause update
	// conflicts.
	fieldOwner client.FieldOwner = "headscale-operator"
)

// Child resource names are derived from the Headscale CR's metadata.name so
// that multiple Headscale instances can coexist in the same namespace. The
// StatefulSet, Service, ServiceAccount, Role, and RoleBinding all share
// h.Name directly; only these suffixed names need a helper.
func configMapNameFor(h *headscalev1beta1.Headscale) string      { return h.Name + "-config" }
func metricsServiceNameFor(h *headscalev1beta1.Headscale) string { return h.Name + "-metrics" }

// apiKeySecretNameFor returns the configured secret name, falling back to
// "<name>-api-key" so multiple instances in one namespace don't collide.
func apiKeySecretNameFor(h *headscalev1beta1.Headscale) string {
	if h.Spec.APIKey.SecretName != "" {
		return h.Spec.APIKey.SecretName
	}
	return h.Name + "-api-key"
}

// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=headscale.infrado.cloud,resources=headscales/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
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

	// spec.config is an opaque passthrough: the operator renders it to
	// config.yaml verbatim (injecting only the wiring keys it must keep
	// consistent) and lets headscale validate the rest at startup. parseConfigView
	// extracts the few keys the operator itself reads.
	view := parseConfigView(headscale.Spec.Config)
	rendered, renderErr := renderConfigYAML(headscale.Spec.Config, view)
	configCond := configValidCondition(headscale, view, renderErr)

	// Validate the inline ACL policy up-front so a malformed document surfaces
	// on the Headscale CRD's status (and operator logs) rather than failing
	// silently. Reconciliation continues either way: Headscale itself doesn't
	// load Inline (the AutoApprover controller pushes it via gRPC), so a bad
	// policy must not block the StatefulSet from coming up.
	policyCond := validateACLPolicy(headscale)
	if policyCond.Status == metav1.ConditionFalse {
		log.Error(fmt.Errorf("%s", policyCond.Message), "Invalid acl_policy.inline")
	}

	// If spec.config can't be rendered at all (malformed beyond what the API
	// server caught) there's no usable ConfigMap to build. Report it and stop;
	// the next spec change requeues us.
	if renderErr != nil {
		log.Error(renderErr, "Failed to render headscale config")
		readyCond := newCondition(headscale, readyConditionType, metav1.ConditionFalse, "ConfigInvalid", renderErr.Error())
		if err := r.updateStatus(ctx, headscale, configCond, policyCond, readyCond); err != nil {
			log.Error(err, "Failed to update Headscale status")
		}
		return ctrl.Result{}, nil
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
	if err := r.reconcileConfigMap(ctx, headscale, rendered); err != nil {
		log.Error(err, "Failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile StatefulSet
	configHash := computeConfigHash(rendered)
	if err := r.reconcileStatefulSet(ctx, headscale, view, configHash); err != nil {
		log.Error(err, "Failed to reconcile StatefulSet")
		return ctrl.Result{}, err
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, headscale, view); err != nil {
		log.Error(err, "Failed to reconcile Service")
		return ctrl.Result{}, err
	}

	// Reconcile Metrics Service
	if err := r.reconcileMetricsService(ctx, headscale, view); err != nil {
		log.Error(err, "Failed to reconcile Metrics Service")
		return ctrl.Result{}, err
	}

	// Derive the Ready condition from the actual workload state so that a config
	// headscale rejects at startup surfaces here instead of silently looking
	// healthy.
	readyCond, requeue := r.computeReadyCondition(ctx, headscale)

	// Update status
	if err := r.updateStatus(ctx, headscale, configCond, policyCond, readyCond); err != nil {
		log.Error(err, "Failed to update Headscale status")
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: notReadyRequeue}, nil
	}
	return ctrl.Result{}, nil
}

// validateACLPolicy parses the inline ACL policy and returns a PolicyValid
// status condition reflecting the result. An empty inline policy is treated as
// valid so users always get explicit confirmation that the operator inspected
// their config.
func validateACLPolicy(headscale *headscalev1beta1.Headscale) metav1.Condition {
	cond := metav1.Condition{
		Type:               "PolicyValid",
		ObservedGeneration: headscale.Generation,
	}
	if _, err := parseInlinePolicy(headscale.Spec.ACLPolicy.Inline); err != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "InvalidPolicy"
		cond.Message = err.Error()
		return cond
	}
	cond.Status = metav1.ConditionTrue
	cond.Reason = "Valid"
	cond.Message = "acl_policy.inline parsed successfully"
	return cond
}

// applyResource reconciles a desired child object onto the cluster using
// Server-Side Apply. Compared to a get/diff/update cycle, SSA lets the operator
// declare ownership of only the fields it sets: API-server defaulting and
// external mutations (for example GKE Autopilot injecting resource requests)
// are tracked under other field managers, so they no longer show up as a
// perpetual diff or trigger optimistic-lock ("the object has been modified")
// conflicts on every reconcile.
func (r *HeadscaleReconciler) applyResource(ctx context.Context, owner *headscalev1beta1.Headscale, obj client.Object) error {
	if err := controllerutil.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return err
	}

	gvk, err := r.GroupVersionKindFor(obj)
	if err != nil {
		return err
	}

	// Apply works on an apply configuration. Convert the typed object to
	// unstructured (json semantics, so unset omitempty fields stay absent) and
	// carry the GVK so the payload includes apiVersion/kind.
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return err
	}
	u := &unstructured.Unstructured{Object: raw}
	u.SetGroupVersionKind(gvk)

	return r.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), fieldOwner, client.ForceOwnership)
}

// handleDeletion handles the deletion of a Headscale instance
func (r *HeadscaleReconciler) handleDeletion(ctx context.Context, headscale *headscalev1beta1.Headscale) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if controllerutil.ContainsFinalizer(headscale, headscaleFinalizer) {
		log.Info("Performing cleanup for Headscale", "Name", headscale.Name)

		// Delete the auto-managed API key secret. The apikey-manager sidecar
		// creates this secret without an OwnerReference, so it would otherwise
		// outlive the CR. Only the auto-managed case is cleaned up here; when
		// AutoManage is disabled the secret is owned by the user.
		autoManage := headscale.Spec.APIKey.AutoManage == nil || *headscale.Spec.APIKey.AutoManage
		if autoManage {
			secretName := apiKeySecretNameFor(headscale)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: headscale.Namespace,
				},
			}
			if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete API key secret", "Name", secretName)
				return ctrl.Result{}, err
			}
			log.Info("Deleted API key secret", "Namespace", headscale.Namespace, "Name", secretName)
		}

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
func (r *HeadscaleReconciler) reconcileConfigMap(ctx context.Context, headscale *headscalev1beta1.Headscale, rendered []byte) error {
	configMap := r.configMapForHeadscale(headscale, rendered)
	return r.applyResource(ctx, headscale, configMap)
}

// reconcileStatefulSet reconciles the StatefulSet for Headscale
func (r *HeadscaleReconciler) reconcileStatefulSet(ctx context.Context, headscale *headscalev1beta1.Headscale, view headscaleConfigView, configHash string) error {
	statefulSet := r.statefulSetForHeadscale(headscale, view, configHash)
	return r.applyResource(ctx, headscale, statefulSet)
}

// reconcileService reconciles the Service for Headscale
func (r *HeadscaleReconciler) reconcileService(ctx context.Context, headscale *headscalev1beta1.Headscale, view headscaleConfigView) error {
	service := r.serviceForHeadscale(headscale, view)
	return r.applyResource(ctx, headscale, service)
}

// reconcileMetricsService reconciles the Metrics Service for Headscale
func (r *HeadscaleReconciler) reconcileMetricsService(ctx context.Context, headscale *headscalev1beta1.Headscale, view headscaleConfigView) error {
	metricsService := r.metricsServiceForHeadscale(headscale, view)
	return r.applyResource(ctx, headscale, metricsService)
}

// reconcileServiceAccount reconciles the ServiceAccount for Headscale pods
func (r *HeadscaleReconciler) reconcileServiceAccount(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	sa := r.serviceAccountForHeadscale(headscale)
	return r.applyResource(ctx, headscale, sa)
}

// reconcileRole reconciles the Role for Headscale pods
func (r *HeadscaleReconciler) reconcileRole(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	role := r.roleForHeadscale(headscale)
	return r.applyResource(ctx, headscale, role)
}

// reconcileRoleBinding reconciles the RoleBinding for Headscale pods
func (r *HeadscaleReconciler) reconcileRoleBinding(ctx context.Context, headscale *headscalev1beta1.Headscale) error {
	rb := r.roleBindingForHeadscale(headscale)
	return r.applyResource(ctx, headscale, rb)
}

// updateStatus sets the supplied conditions on the Headscale instance and
// patches the status subresource only when something actually changed. Callers
// pass the already-computed conditions (ConfigValid, PolicyValid, Ready) so this
// helper does no re-derivation.
func (r *HeadscaleReconciler) updateStatus(
	ctx context.Context,
	headscale *headscalev1beta1.Headscale,
	conds ...metav1.Condition,
) error {
	patch := client.MergeFrom(headscale.DeepCopy())

	changed := false
	for _, cond := range conds {
		if meta.SetStatusCondition(&headscale.Status.Conditions, cond) {
			changed = true
		}
	}
	if !changed {
		return nil
	}

	return r.Status().Patch(ctx, headscale, patch)
}

// configMapForHeadscale returns a ConfigMap holding the rendered headscale
// config.yaml. Rendering happens in renderConfigYAML; this only wraps the bytes.
func (r *HeadscaleReconciler) configMapForHeadscale(h *headscalev1beta1.Headscale, rendered []byte) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapNameFor(h),
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
		Data: map[string]string{
			"config.yaml": string(rendered),
		},
	}
}

// computeConfigHash computes a short SHA256 hash of the rendered config.yaml. It
// is stamped onto the pod template so any change to the effective config rolls
// the StatefulSet.
func computeConfigHash(rendered []byte) string {
	hash := sha256.New()
	hash.Write(rendered)
	return fmt.Sprintf("%x", hash.Sum(nil))[:16]
}

// statefulSetForHeadscale returns a StatefulSet object for Headscale
func (r *HeadscaleReconciler) statefulSetForHeadscale(h *headscalev1beta1.Headscale, view headscaleConfigView, configHash string) *appsv1.StatefulSet {
	labels := labelsForHeadscale(h.Name)
	replicas := h.Spec.Replicas

	// Determine the image to use
	image := fmt.Sprintf("%s:%s", h.Spec.Image, h.Spec.Version)

	// Extract ports from the effective config view (defaults already applied)
	httpPort := extractPort(view.ListenAddr, 8080)
	metricsPort := extractPort(view.MetricsListenAddr, 9090)
	grpcPort := extractPort(view.GRPCListenAddr, 50443)

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
			Name:            headscaleAppName,
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			// Fall back to the container logs for the termination message so a
			// fatal config error (headscale validates its config at startup)
			// surfaces on the Headscale status instead of being lost.
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			Command: []string{
				headscaleAppName,
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
			Env: h.Spec.ExtraEnv,
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
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
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
						Name: configMapNameFor(h),
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
			Name: socketVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})

		// Update Headscale container to mount the socket volume
		containers[0].VolumeMounts = append(containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      socketVolumeName,
			MountPath: "/var/run/headscale",
		})

		// Determine the API key manager image to use
		managerImage := "ghcr.io/infradohq/headscale-operator/apikey-manager:latest"
		if h.Spec.APIKey.ManagerImage != "" {
			managerImage = h.Spec.APIKey.ManagerImage
		}

		// Add API key manager sidecar
		containers = append(containers, corev1.Container{
			Name:            apiKeyManagerContainerName,
			Image:           managerImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Args: []string{
				"--socket-path=" + view.UnixSocket,
				"--secret-name=" + apiKeySecretNameFor(h),
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
					Name:      socketVolumeName,
					MountPath: "/var/run/headscale",
				},
			},
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
		})
	}

	// Append extra volumes and volume mounts from spec
	if len(h.Spec.ExtraVolumes) > 0 {
		volumes = append(volumes, h.Spec.ExtraVolumes...)
	}
	if len(h.Spec.ExtraVolumeMounts) > 0 {
		containers[0].VolumeMounts = append(containers[0].VolumeMounts, h.Spec.ExtraVolumeMounts...)
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			ServiceName: h.Name,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"headscale.infrado.cloud/config-hash": configHash,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: h.Name,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(65532)),
						RunAsGroup:   ptr.To(int64(65532)),
						FSGroup:      ptr.To(int64(65532)),
						RunAsNonRoot: ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers:       containers,
					Volumes:          volumes,
					ImagePullSecrets: buildImagePullSecrets(h.Spec.ImagePullSecrets),
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
func (r *HeadscaleReconciler) serviceForHeadscale(h *headscalev1beta1.Headscale, view headscaleConfigView) *corev1.Service {
	labels := labelsForHeadscale(h.Name)

	// Extract ports from the effective config view
	httpPort := extractPort(view.ListenAddr, 8080)
	grpcPort := extractPort(view.GRPCListenAddr, 50443)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
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
func (r *HeadscaleReconciler) metricsServiceForHeadscale(h *headscalev1beta1.Headscale, view headscaleConfigView) *corev1.Service {
	labels := labelsForHeadscale(h.Name)

	// Extract metrics port from the effective config view
	metricsPort := extractPort(view.MetricsListenAddr, 9090)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricsServiceNameFor(h),
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
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
	}
}

// roleForHeadscale returns a Role object for Headscale pods with permissions to manage Secrets
func (r *HeadscaleReconciler) roleForHeadscale(h *headscalev1beta1.Headscale) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{apiKeySecretNameFor(h)},
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
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    labelsForHeadscale(h.Name),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     h.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      h.Name,
				Namespace: h.Namespace,
			},
		},
	}
}

// buildImagePullSecrets converts a list of secret names to LocalObjectReferences
func buildImagePullSecrets(secretNames []string) []corev1.LocalObjectReference {
	if len(secretNames) == 0 {
		return nil
	}

	secrets := make([]corev1.LocalObjectReference, len(secretNames))
	for i, name := range secretNames {
		secrets[i] = corev1.LocalObjectReference{
			Name: name,
		}
	}
	return secrets
}

// labelsForHeadscale returns the labels for selecting the resources
func labelsForHeadscale(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       headscaleAppName,
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
		Named(headscaleAppName).
		Complete(r)
}
