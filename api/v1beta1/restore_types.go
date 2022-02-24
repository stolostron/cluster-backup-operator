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

// RestorePhase contains the phase of the restore
type RestorePhase string

const (
	// RestorePhaseStarted means the restore has been initialized and started
	RestorePhaseStarted = "Started"
	// RestorePhaseRunning means the restore is running and not yet finished
	RestorePhaseRunning = "Running"
	// RestorePhaseFinished means the restore finsihed
	RestorePhaseFinished = "Finished"
	// RestorePhaseFinishedWithErrors means the restore finsihed with 1+ errors restoring individual items
	RestorePhaseFinishedWithErrors = "FinishedWithErrors"
	// RestorePhaseError means the restore is in error phase and was unable to execute
	RestorePhaseError = "Error"
	// RestorePhaseUnknown means the restore is in unknown phase
	RestorePhaseUnknown = "Unknown"
	// RestorePhaseEnabled means the restore is enabled and will continue syncing with new backups
	RestorePhaseEnabled = "Enabled"
)

type CleanupType string

const (
	// clean up all resources created by CRD in the acm backup criteria
	CleanupTypeAll = "CleanupAll"
	// clean up only resources created as a result of a restore operation
	CleanupTypeRestored = "CleanupRestored"
	// don't clean up any resources
	CleanupTypeNone = "None"
)

// RestoreSpec defines the desired state of Restore
type RestoreSpec struct {
	// VeleroManagedClustersBackupName is the name of the velero back-up used to restore managed clusters.
	// Is required, valid values are latest, skip or backup_name
	// If value is set to latest, the latest backup is used, skip will not restore this type of backup
	// backup_name points to the name of the backup to be restored
	// +kubebuilder:validation:Required
	VeleroManagedClustersBackupName *string `json:"veleroManagedClustersBackupName"`
	// VeleroResourcesBackupName is the name of the velero back-up used to restore resources.
	// Is required, valid values are latest, skip or backup_name
	// If value is set to latest, the latest backup is used, skip will not restore this type of backup
	// backup_name points to the name of the backup to be restored
	// +kubebuilder:validation:Required
	VeleroResourcesBackupName *string `json:"veleroResourcesBackupName"`
	// VeleroCredentialsBackupName is the name of the velero back-up used to restore credentials.
	// Is required, valid values are latest, skip or backup_name
	// If value is set to latest, the latest backup is used, skip will not restore this type of backup
	// backup_name points to the name of the backup to be restored
	// +kubebuilder:validation:Required
	VeleroCredentialsBackupName *string `json:"veleroCredentialsBackupName"`
	// +kubebuilder:validation:Optional
	// set this to CleanupTypeAll if you want the restore to attempt to first delete all
	// resources with a CRD in the acm backup criteria
	//
	// set this to CleanupTypeRestored if you want the restore to attempt to delete all
	// resources created by a previous restore operation
	// This will allow updating resources that are already on the hub and also part of the new backup
	// And it will also delete resources previously restored but no longer in the current backup
	//
	// if not defined, the value is assumed to be CleanupTypeNone - no clean up called
	CleanupBeforeRestore CleanupType `json:"cleanupBeforeRestore,omitempty"`
	// +kubebuilder:validation:Optional
	// set this to true if you want to keep checking for new backups and restore if updates are available.
	// If not defined, the value is set to false
	SyncRestoreWithNewBackups bool `json:"syncRestoreWithNewBackups,omitempty"`
}

// RestoreStatus defines the observed state of Restore
type RestoreStatus struct {
	// +kubebuilder:validation:Optional
	VeleroManagedClustersRestoreName string `json:"veleroManagedClustersRestoreName,omitempty"`
	// +kubebuilder:validation:Optional
	VeleroResourcesRestoreName string `json:"veleroResourcesRestoreName,omitempty"`
	// +kubebuilder:validation:Optional
	VeleroCredentialsRestoreName string `json:"veleroCredentialsRestoreName,omitempty"`
	// Phase is the current phase of the restore
	// +kubebuilder:validation:Optional
	Phase RestorePhase `json:"phase"`
	// Message on the last operation
	// +kubebuilder:validation:Optional
	LastMessage string `json:"lastMessage"`
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

// Restore condition type
const (
	// RestoreComplete means the restore runs to completion
	RestoreComplete = "Complete"
)

// Valid Restore Reason
const (
	RestoreReasonNotStarted = "RestoreNotStarted"
	RestoreReasonStarted    = "RestoreStarted"
	RestoreReasonRunning    = "RestoreRunning"
	RestoreReasonFinished   = "RestoreFinished"
)

//+kubebuilder:object:root=true

// RestoreList contains a list of Restore
type RestoreList struct {
	metav1.TypeMeta `          json:",inline"`
	metav1.ListMeta `          json:"metadata,omitempty"`
	Items           []Restore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Restore{}, &RestoreList{})
}
