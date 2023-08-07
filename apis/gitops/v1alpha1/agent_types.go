/*
Copyright 2022 The Crossplane Authors.

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

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// AgentParameters are the configurable fields of a Agent.
type AgentParameters struct {
	// Account Identifier for the Entity.
	AccountIdentifier string `json:"accountIdentifier,omitempty"`
	// Project Identifier for the Entity.
	ProjectIdentifier string `json:"projectIdentifier,omitempty"`
	// Organization Identifier for the Entity.
	OrgIdentifier string `json:"orgIdentifier,omitempty"`
	// +optional
	Description string `json:"description,omitempty"`
	// +optional
	Tags       map[string]string `json:"tags,omitempty"`
	Identifier string            `json:"identifier"`
}

// AgentObservation are the observable fields of a Agent.
type AgentObservation struct {
	// Health *nextgen.V1AgentHealth `json:"health,omitempty"`
	State string `json:state`
}

// A AgentSpec defines the desired state of a Agent.
type AgentSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       AgentParameters `json:"forProvider"`
}

// A AgentStatus represents the observed state of a Agent.
type AgentStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          AgentObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Agent is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,harness}
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec"`
	Status AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

// Agent type metadata.
var (
	AgentKind             = reflect.TypeOf(Agent{}).Name()
	AgentGroupKind        = schema.GroupKind{Group: Group, Kind: AgentKind}.String()
	AgentKindAPIVersion   = AgentKind + "." + SchemeGroupVersion.String()
	AgentGroupVersionKind = SchemeGroupVersion.WithKind(AgentKind)
)

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}
