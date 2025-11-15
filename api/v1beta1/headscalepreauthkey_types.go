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

// HeadscalePreAuthKeySpec defines the desired state of HeadscalePreAuthKey
type HeadscalePreAuthKeySpec struct {
	// HeadscaleRef is the name of the Headscale instance to create the preauth key in
	// +kubebuilder:validation:Required
	// +required
	HeadscaleRef string `json:"headscaleRef"`

	// HeadscaleUserRef is the name of the HeadscaleUser resource to create the preauth key for
	// Either HeadscaleUserRef or UserID must be specified, but not both
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	HeadscaleUserRef string `json:"headscaleUserRef,omitempty"`

	// UserID is the ID of the user in Headscale to create the preauth key for
	// Either HeadscaleUserRef or UserID must be specified, but not both
	// +kubebuilder:validation:Minimum=1
	// +optional
	UserID uint64 `json:"userId,omitempty"`

	// Expiration is the duration after which the preauth key expires
	// Examples: 30m, 24h, 1h30m (compound durations allowed; must be a valid Go duration string with hours and/or minutes)
	// +kubebuilder:validation:Pattern=`^([0-9]+h)?([0-9]+m)?$`
	// +kubebuilder:default="1h"
	// +optional
	Expiration string `json:"expiration,omitempty"`

	// Reusable indicates whether the preauth key can be used multiple times
	// +kubebuilder:default=false
	// +optional
	Reusable bool `json:"reusable,omitempty"`

	// Ephemeral indicates whether nodes using this key should be ephemeral
	// +kubebuilder:default=false
	// +optional
	Ephemeral bool `json:"ephemeral,omitempty"`

	// Tags to automatically assign to nodes registered with this preauth key
	// +kubebuilder:validation:items:Pattern=`^tag:[a-zA-Z0-9._-]+$`
	// +optional
	Tags []string `json:"tags,omitempty"`

	// SecretName is the name of the secret to store the preauth key in
	// If not specified, defaults to the HeadscalePreAuthKey resource name
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// HeadscalePreAuthKeyStatus defines the observed state of HeadscalePreAuthKey.
type HeadscalePreAuthKeyStatus struct {
	// KeyID is the ID of the preauth key in Headscale
	// +optional
	KeyID string `json:"keyId,omitempty"`

	// conditions represent the current state of the HeadscalePreAuthKey resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hspak;hspre
// +kubebuilder:printcolumn:name="HeadscaleUser",type=string,JSONPath=`.spec.headscaleUserRef`
// +kubebuilder:printcolumn:name="UserID",type=integer,JSONPath=`.spec.userId`
// +kubebuilder:printcolumn:name="Reusable",type=boolean,JSONPath=`.spec.reusable`
// +kubebuilder:printcolumn:name="Ephemeral",type=boolean,JSONPath=`.spec.ephemeral`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="(has(self.spec.headscaleUserRef) && self.spec.headscaleUserRef != \"\") != (has(self.spec.userId) && self.spec.userId != 0)",message="exactly one of spec.headscaleUserRef or spec.userId must be specified"

// HeadscalePreAuthKey is the Schema for the headscalepreauthkeys API
type HeadscalePreAuthKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of HeadscalePreAuthKey
	// +required
	Spec HeadscalePreAuthKeySpec `json:"spec"`

	// status defines the observed state of HeadscalePreAuthKey
	// +optional
	Status HeadscalePreAuthKeyStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// HeadscalePreAuthKeyList contains a list of HeadscalePreAuthKey
type HeadscalePreAuthKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HeadscalePreAuthKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HeadscalePreAuthKey{}, &HeadscalePreAuthKeyList{})
}
