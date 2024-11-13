// Copyright 2024
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// MultiClusterServiceFinalizer is finalizer applied to MultiClusterService objects.
	MultiClusterServiceFinalizer = "hmc.mirantis.com/multicluster-service"
	// MultiClusterServiceKind is the string representation of a MultiClusterServiceKind.
	MultiClusterServiceKind = "MultiClusterService"

	// SveltosProfileReadyCondition indicates if the Sveltos Profile is ready.
	SveltosProfileReadyCondition = "SveltosProfileReady"
	// SveltosClusterProfileReadyCondition indicates if the Sveltos ClusterProfile is ready.
	SveltosClusterProfileReadyCondition = "SveltosClusterProfileReady"
	// SveltosHelmReleaseReadyCondition indicates if the HelmRelease
	// managed by a Sveltos Profile/ClusterProfile is ready.
	SveltosHelmReleaseReadyCondition = "SveltosHelmReleaseReady"

	// FetchServicesStatusSuccessCondition indicates if status
	// for the deployed services have been fetched successfully.
	FetchServicesStatusSuccessCondition = "FetchServicesStatusSuccess"
)

// ServiceSpec represents a Service to be managed
type ServiceSpec struct {
	// Values is the helm values to be passed to the template.
	Values *apiextensionsv1.JSON `json:"values,omitempty"`

	// +kubebuilder:validation:MinLength=1

	// Template is a reference to a Template object located in the same namespace.
	Template string `json:"template"`

	// +kubebuilder:validation:MinLength=1

	// Name is the chart release.
	Name string `json:"name"`
	// Namespace is the namespace the release will be installed in.
	// It will default to Name if not provided.
	Namespace string `json:"namespace,omitempty"`
	// Disable can be set to disable handling of this service.
	Disable bool `json:"disable,omitempty"`
}

// MultiClusterServiceSpec defines the desired state of MultiClusterService
type MultiClusterServiceSpec struct {
	// ClusterSelector identifies target clusters to manage services on.
	ClusterSelector metav1.LabelSelector `json:"clusterSelector,omitempty"`
	ServicesType    `json:",inline"`
}

// ServiceStatus contains details for the state of services.
type ServiceStatus struct {
	// ClusterName is the name of the associated cluster.
	ClusterName string `json:"clusterName"`
	// ClusterNamespace is the namespace of the associated cluster.
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
	// Conditions contains details for the current state of managed services.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MultiClusterServiceStatus defines the observed state of MultiClusterService.
type MultiClusterServiceStatus struct {
	// Services contains details for the state of services.
	Services []ServiceStatus `json:"services,omitempty"`
	// Conditions contains details for the current state of the MultiClusterService.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// MultiClusterService is the Schema for the multiclusterservices API
type MultiClusterService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MultiClusterServiceSpec   `json:"spec,omitempty"`
	Status MultiClusterServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MultiClusterServiceList contains a list of MultiClusterService
type MultiClusterServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiClusterService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MultiClusterService{}, &MultiClusterServiceList{})
}
