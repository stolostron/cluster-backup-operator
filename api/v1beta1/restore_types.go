/*
Copyright 2021.

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
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RestoreSpec defines the desired state of Restore
type RestoreSpec struct {
	// Important: Run "make" to regenerate code after modifying this file
	BackupName   string                    `json:"backupName,omitempty"`
	VeleroConfig *VeleroConfigRestoreProxy `json:"veleroConfigRestoreProxy,omitempty"`
}

// RestoreStatus defines the observed state of Restore
type RestoreStatus struct {
	// Phase shows the status for the restore operation
	// +kubebuilder:validation:Optional
	Phase StatusPhase `json:"phase"`
	// Important: Run "make" to regenerate code after modifying this file
	RestoreProxyReference *corev1.ObjectReference `json:"restoreProxyReference,omitempty"`
	// Message on the last operation
	// +kubebuilder:validation:Optional
	LastMessage string `json:"lastMessage"`
	// Velero operation status
	// +kubebuilder:validation:Optional
	VeleroRestore *veleroapi.Restore `json:"veleroRestore,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:validation:Optional
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"crst"}
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.lastMessage`

// Restore is the Schema for the restores API
type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec,omitempty"`
	Status RestoreStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RestoreList contains a list of Restore
type RestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Restore `json:"items"`
}

// VeleroConfigRestoreProxy defines the configuration information for velero configuration to restore ACM through Velero
type VeleroConfigRestoreProxy struct {
	// Namespace defines the namespace where velero is installed
	Namespace string `json:"metadata"`
	// DetachManagedCluster will detach managedcluster  from backedHub. backedHub has to be available and BackedUpSecreName must be supplied
	DetachManagedCluster bool `json:"detachManagedClusters,omitempty"`
	// BackedUpHubSecretName contains the secret name to access the backed up HUB
	BackedUpHubSecretName string `json:"BackedUpHubSecretName,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Restore{}, &RestoreList{})
}
