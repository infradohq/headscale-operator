package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PersistentVolumeClaimConfig represents the PVC configuration for Headscale data storage
type PersistentVolumeClaimConfig struct {
	// Size is the storage size for the PVC
	// +kubebuilder:default="128Mi"
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// StorageClassName is the storage class name for the PVC
	// +optional
	StorageClassName *string `json:"storage_class_name,omitempty"`
}

// APIKeyConfig represents API key management configuration
type APIKeyConfig struct {
	// AutoManage enables automatic API key creation and rotation
	// +kubebuilder:default=true
	// +optional
	AutoManage *bool `json:"auto_manage,omitempty"`

	// SecretName is the name of the Kubernetes secret to store the API key.
	// When empty, the operator defaults to "<headscale-name>-api-key" so that
	// multiple Headscale instances in the same namespace do not collide.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	SecretName string `json:"secret_name,omitempty"`

	// Expiration is the API key expiration duration in Go duration format (e.g., "2160h", "90d" is not valid, use "2160h" for 90 days)
	// The API key will be rotated before it expires
	// Examples: "720h" (30 days), "2160h" (90 days), "8760h" (365 days)
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default="2160h"
	// +optional
	Expiration string `json:"expiration,omitempty"`

	// RotationBuffer is the time before expiration to rotate the key in Go duration format (e.g., "168h" for 7 days)
	// Key will be rotated when it has less than this time remaining
	// Examples: "168h" (7 days), "1920h" (80 days)
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default="1920h"
	// +optional
	RotationBuffer string `json:"rotation_buffer,omitempty"`

	// ManagerImage is the container image to use for the API key manager sidecar
	// +kubebuilder:default="ghcr.io/infradohq/headscale-operator/apikey-manager:latest"
	// +optional
	ManagerImage string `json:"manager_image,omitempty"`
}

// ACLPolicyConfig defines the ACL policy that the operator pushes to Headscale
// via the gRPC SetPolicy API. The operator merges TagOwners and any
// HeadscaleAutoApprover entries on top of the Inline base before pushing.
//
// Using this requires `spec.config.policy.mode=database` because Headscale's
// SetPolicy API is only available in database-mode. The HeadscaleAutoApprover
// reconciler will surface a status condition on each auto-approver if the mode
// is not set correctly.
type ACLPolicyConfig struct {
	// TagOwners maps tag names to the list of users or groups that may apply
	// that tag (e.g. {"tag:router": ["group:admin"]}). Tags referenced by
	// HeadscaleAutoApprover resources must be declared here.
	// +optional
	TagOwners map[string][]string `json:"tag_owners,omitempty"`

	// Inline is a JSON or HuJSON Headscale policy document that serves as the
	// base policy. The operator parses it, merges TagOwners and any
	// HeadscaleAutoApprover entries on top, then pushes the result via
	// the gRPC API. Use this for `acls`, `groups`, `hosts`, and `ssh` rules.
	// +optional
	Inline string `json:"inline,omitempty"`
}

// HeadscaleSpec defines the desired state of Headscale
type HeadscaleSpec struct {
	// Version indicates the version of Headscale to deploy.
	// +kubebuilder:validation:Pattern=`^v?(\d+\.)?(\d+\.)?(\*|\d+)(-.+)?$`
	// +required
	Version string `json:"version"`

	// Image is the container image to use for Headscale.
	// +kubebuilder:default="headscale/headscale"
	// +kubebuilder:validation:MinLength=1
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas indicates the number of Headscale instances to deploy.
	// +kubebuilder:validation:Minimum=0
	// +required
	Replicas int32 `json:"replicas"`

	// Config is headscale's configuration, passed through to config.yaml
	// verbatim. The schema is intentionally open: any key supported by the
	// deployed headscale version is accepted, and headscale validates it itself
	// at startup (errors surface on the Headscale status and as events). The
	// operator only fills in the few keys it consumes when they are unset:
	// listen_addr (default 0.0.0.0:8080), metrics_listen_addr (0.0.0.0:9090),
	// grpc_listen_addr (0.0.0.0:50443), and unix_socket
	// (/var/run/headscale/headscale.sock). server_url is required by headscale.
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Config runtime.RawExtension `json:"config,omitempty"`

	// PersistentVolumeClaim configuration for data storage
	// +optional
	PersistentVolumeClaim PersistentVolumeClaimConfig `json:"persistent_volume_claim"`

	// APIKey configuration for automatic API key management
	// +optional
	APIKey APIKeyConfig `json:"api_key"`

	// ImagePullSecrets is a list of references to secrets for pulling images from private registries
	// +optional
	ImagePullSecrets []string `json:"image_pull_secrets,omitempty"`

	// ExtraEnv allows injecting additional environment variables into the Headscale container
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extra_env,omitempty"`

	// ExtraVolumes allows adding additional volumes to the Headscale pod
	// +optional
	ExtraVolumes []corev1.Volume `json:"extra_volumes,omitempty"`

	// ExtraVolumeMounts allows adding additional volume mounts to the Headscale container
	// +optional
	ExtraVolumeMounts []corev1.VolumeMount `json:"extra_volume_mounts,omitempty"`

	// ACLPolicy is the base ACL policy and tag ownership map. The operator
	// merges this with any HeadscaleAutoApprover resources that reference this
	// Headscale and pushes the result via the gRPC SetPolicy API.
	// Requires `spec.config.policy.mode=database`.
	// +optional
	ACLPolicy ACLPolicyConfig `json:"acl_policy"`
}

// HeadscaleStatus defines the observed state of Headscale.
type HeadscaleStatus struct {
	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the Headscale resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hs
// +kubebuilder:validation:XValidation:rule="!has(self.spec.api_key.rotation_buffer) || !has(self.spec.api_key.expiration) || duration(self.spec.api_key.rotation_buffer) < duration(self.spec.api_key.expiration)",message="api_key.rotation_buffer must be less than api_key.expiration"

// Headscale is the Schema for the headscales API
type Headscale struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Headscale
	// +required
	Spec HeadscaleSpec `json:"spec"`

	// status defines the observed state of Headscale
	// +optional
	Status HeadscaleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HeadscaleList contains a list of Headscale
type HeadscaleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Headscale `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Headscale{}, &HeadscaleList{})
		return nil
	})
}
