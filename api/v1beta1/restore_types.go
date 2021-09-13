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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestoreSpec defines the desired state of Restore
type RestoreSpec struct {
	// VeleroBackupName is the name of the velero back-up used to restore ACM Hub.
	// Is optional, in case no VeleroBackupName is supplied the backup name will be selected
	// from the available backups in the current namespace
	// +kubebuilder:validation:Optional
	VeleroBackupName *string `json:"veleroBackupName,omitempty"`
}

// RestoreStatus defines the observed state of Restore
type RestoreStatus struct {
	// TODO: replace with core/v1 TypedLocalObjectReference
	// +kubebuilder:validation:Optional
	VeleroRestoreName string `json:"veleroRestoreName,omitempty"`
	// RestoreCondition
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:validation:Optional
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"crst"}

// Restore is the Schema for the restores API
type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec,omitempty"`
	Status RestoreStatus `json:"status,omitempty"`
}

// Valid conditions of a restore
const (
	// RestoreStarted means the Restore is running.
	RestoreStarted = "Started"
	// RestoreAttaching means the Restore is attaching the managed clusters
	RestoreAttaching = "Attaching"
	// RestoreComplete means the Restore has completed its execution.
	RestoreComplete = "Complete"
	// RestoreFailed means the Restore has failed its execution.
	RestoreFailed = "Failed"
)

// Valid Restore Reason
const (
	RestoreReasonStarted  = "RestoreStarted"
	RestoreReasonRunning  = "RestoreRunning"
	RestoreReasonFinished = "RestoreFinished"
)

const (
	CSRReasonApprovedReason = "AutoApprovedByClusterBackupController"
)

//+kubebuilder:object:root=true

// RestoreList contains a list of Restore
type RestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Restore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Restore{}, &RestoreList{})
}
