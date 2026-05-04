package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AutoApproverRoute declares a subnet that should be auto-approved when advertised
// by a node carrying any of the listed tags.
type AutoApproverRoute struct {
	// CIDR is the subnet route to auto-approve (IPv4 or IPv6).
	// +kubebuilder:validation:Pattern=`^(([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}|([0-9a-fA-F]{0,4}:){2,7}([0-9a-fA-F]{0,4})/[0-9]{1,3})$`
	// +required
	CIDR string `json:"cidr"`

	// Tags whose nodes will have this route auto-approved when advertised.
	// Each tag must be declared in the parent Headscale's spec.acl_policy.tag_owners.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^tag:[a-zA-Z0-9._-]+$`
	// +required
	Tags []string `json:"tags"`
}

// HeadscaleAutoApproverSpec defines the desired state of HeadscaleAutoApprover.
//
// Each HeadscaleAutoApprover contributes entries to the parent Headscale's policy
// document. The operator merges all HeadscaleAutoApprover resources that reference
// the same Headscale and pushes the result via the Headscale gRPC SetPolicy API.
// This requires the parent Headscale to be configured with `spec.config.policy.mode=database`.
type HeadscaleAutoApproverSpec struct {
	// HeadscaleRef is the name of the Headscale instance (in the same namespace)
	// whose policy these auto-approvers contribute to.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +required
	HeadscaleRef string `json:"headscaleRef"`

	// Routes is the list of subnet routes to auto-approve when advertised by a
	// node carrying one of the listed tags.
	// +optional
	Routes []AutoApproverRoute `json:"routes,omitempty"`

	// ExitNodeTags is the list of tags whose nodes will be auto-approved as
	// exit nodes when they advertise themselves as such.
	// +kubebuilder:validation:items:Pattern=`^tag:[a-zA-Z0-9._-]+$`
	// +optional
	ExitNodeTags []string `json:"exitNodeTags,omitempty"`
}

// HeadscaleAutoApproverStatus defines the observed state of HeadscaleAutoApprover.
type HeadscaleAutoApproverStatus struct {
	// conditions represent the current state of the HeadscaleAutoApprover resource.
	//
	// Standard condition types include:
	// - "Ready": the entries from this resource have been merged into the active policy
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hsaa
// +kubebuilder:printcolumn:name="Headscale",type=string,JSONPath=`.spec.headscaleRef`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="(has(self.spec.routes) && size(self.spec.routes) > 0) || (has(self.spec.exitNodeTags) && size(self.spec.exitNodeTags) > 0)",message="at least one of spec.routes or spec.exitNodeTags must be specified"

// HeadscaleAutoApprover is the Schema for the headscaleautoapprovers API.
type HeadscaleAutoApprover struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HeadscaleAutoApprover
	// +required
	Spec HeadscaleAutoApproverSpec `json:"spec"`

	// status defines the observed state of HeadscaleAutoApprover
	// +optional
	Status HeadscaleAutoApproverStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HeadscaleAutoApproverList contains a list of HeadscaleAutoApprover.
type HeadscaleAutoApproverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HeadscaleAutoApprover `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HeadscaleAutoApprover{}, &HeadscaleAutoApproverList{})
}
