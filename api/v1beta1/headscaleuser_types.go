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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HeadscaleUserSpec defines the desired state of HeadscaleUser
type HeadscaleUserSpec struct {
	// HeadscaleRef references the Headscale instance to create the user in
	// +required
	HeadscaleRef string `json:"headscaleRef"`

	// Username is the unique username for the Headscale user
	// This field is immutable after creation
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="username is immutable"
	// +required
	Username string `json:"username"`

	// DisplayName is the display name for the user
	// This field is immutable after creation
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="displayName is immutable"
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Email is the email address of the user
	// This field is immutable after creation
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	// +kubebuilder:validation:MaxLength=320
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="email is immutable"
	// +optional
	Email string `json:"email,omitempty"`

	// PictureURL is the URL to the user's profile picture
	// This field is immutable after creation
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="pictureURL is immutable"
	// +optional
	PictureURL string `json:"pictureURL,omitempty"`
}

// HeadscaleUserStatus defines the observed state of HeadscaleUser.
type HeadscaleUserStatus struct {
	// UserID is the unique identifier assigned by Headscale
	// +optional
	UserID string `json:"userId,omitempty"`

	// CreatedAt is the timestamp when the user was created in Headscale
	// +optional
	CreatedAt string `json:"createdAt,omitempty"`

	// conditions represent the current state of the HeadscaleUser resource.
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
// +kubebuilder:resource:shortName=hsuser

// HeadscaleUser is the Schema for the headscaleusers API
type HeadscaleUser struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HeadscaleUser
	// +required
	Spec HeadscaleUserSpec `json:"spec"`

	// status defines the observed state of HeadscaleUser
	// +optional
	Status HeadscaleUserStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HeadscaleUserList contains a list of HeadscaleUser
type HeadscaleUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HeadscaleUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HeadscaleUser{}, &HeadscaleUserList{})
}
