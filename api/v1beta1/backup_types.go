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

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type StatusPhase string

const (
	InProgressStatusPhase StatusPhase = "INPROGRESS"
	RunningStatusPhase    StatusPhase = "RUNNING"
	PendingStatusPhase    StatusPhase = "PENDING"
	ErrorStatusPhase      StatusPhase = "ERROR"
)

type StatusExecution string

const (
	SuccessExecution  StatusExecution = "SUCCESS"
	ErrorExecution    StatusExecution = "ERROR"
	CanceledExecution StatusExecution = "CANCELED"
)

// VeleroConfigBackupProxy defines the configuration information for velero configuration to  backup ACM through Velero
type VeleroConfigBackupProxy struct {
	// Namespace defines velero namespace
	Namespace string `json:"metadata"`
}

// BackupSpec defines the desired state of Backup
type BackupSpec struct {
	//Velero namespace and any other configuration option
	// +kubebuilder:validation:Required
	VeleroConfig *VeleroConfigBackupProxy `json:"veleroConfigBackupProxy,omitempty"`
	// Interval, in minutes, between backups
	// +kubebuilder:validation:Required
	Interval int `json:"interval"`
	// Maximum number of backups after which the old backups are being removed
	// +kubebuilder:validation:Required
	MaxBackups int `json:"maxBackups"`
}

// BackupStatus defines the observed state of Backup
type BackupStatus struct {
	// Phase shows the status for the backup operation
	// +kubebuilder:validation:Optional
	Phase StatusPhase `json:"phase"`
	// Then name of the last velero.io Backup CR
	// +kubebuilder:validation:Optional
	LastBackup string `json:"lastBackup"`
	// The completion time for the last backup
	// +kubebuilder:validation:Optional
	CompletionTimestamp *metav1.Time `json:"completionTimestamp,omitempty"`
	// The duration in minutes for the last backup operation
	// +kubebuilder:validation:Optional
	LastBackupDuration string `json:"backupDuration"`
	// Then name of the last velero.io Backup CR
	// +kubebuilder:validation:Optional
	CurrentBackup string `json:"currentBackup"`
	// Message on the last operation
	// +kubebuilder:validation:Optional
	LastMessage string `json:"lastMessage"`
	// Velero Backups operation status
	// +kubebuilder:validation:Optional
	VeleroBackups []*veleroapi.Backup `json:"veleroBackup,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:validation:Optional
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"cbkp"}
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Backup",type=string,JSONPath=`.status.currentBackup`
// +kubebuilder:printcolumn:name="LastBackup",type=string,JSONPath=`.status.lastBackup`
// +kubebuilder:printcolumn:name="LastBackupTime",type=string,JSONPath=`.status.completionTimestamp`
// +kubebuilder:printcolumn:name="Duration",type=string,JSONPath=`.status.backupDuration`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.lastMessage`

// Backup is the Schema for the backups API
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec,omitempty"`
	Status BackupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BackupList contains a list of Backup
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Backup{}, &BackupList{})
}
