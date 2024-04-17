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

// SchedulePhase shows phase of the schedule
type SchedulePhase string

const (
	// SchedulePhaseNew means the schedule has been created but not
	// yet processed by the ScheduleController
	SchedulePhaseNew SchedulePhase = "New"
	// SchedulePhaseEnabled means the schedule has been validated and
	// will now be triggering backups according to the schedule spec.
	SchedulePhaseEnabled SchedulePhase = "Enabled"
	// SchedulePhaseFailedValidation means the schedule has failed
	// the controller's validations and therefore will not trigger backups.
	SchedulePhaseFailedValidation SchedulePhase = "FailedValidation"
	// SchedulePhaseFailed means the schedule has been processed by
	// the ScheduleController but there are some failures
	SchedulePhaseFailed SchedulePhase = "Failed"
	// SchedulePhaseUnknown means the schedule has been processed by
	// the ScheduleController but there are some unknown issues
	SchedulePhaseUnknown SchedulePhase = "Unknown"
	// another cluster pushes backups to the same storage location
	// resulting in a backup collision
	SchedulePhaseBackupCollision SchedulePhase = "BackupCollision"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BackupScheduleSpec defines the desired state of BackupSchedule
type BackupScheduleSpec struct {
	// Schedule is a Cron expression defining when to run
	// the Velero Backup
	// +kubebuilder:validation:Required
	VeleroSchedule string `json:"veleroSchedule"`
	// TTL is a time.Duration-parseable string describing how long
	// the Velero Backup should be retained for. If not specified
	// the maximum default value set by velero is used - 720h
	// +kubebuilder:validation:Optional
	VeleroTTL metav1.Duration `json:"veleroTtl,omitempty"`
	// +kubebuilder:validation:Optional
	// Set this to true if you want to use the ManagedServiceAccount token to auto connect imported clusters on the restored hub.
	// For this option to work, the user must first enable the managedserviceaccount component on the MultiClusterHub
	// If not defined, the value is set to false.
	// If this option is set to true, the backup controller will create ManagedServiceAccount
	// for each managed cluster connected on the primary hub using the import cluster option
	// see https://github.com/stolostron/managed-serviceaccount#what-is-managed-service-account
	// The token generated by the ManagedServiceAccount will have a TTL defaulted using the backup veleroTTL option
	// When set to false, all ManagedServiceAccounts created by the backup controller are being removed
	UseManagedServiceAccount bool `json:"useManagedServiceAccount,omitempty"`
	// +kubebuilder:validation:Optional
	// Used in combination with the UseManagedServiceAccount property
	// When UseManagedServiceAccount is set to true, defines the TTL for the generated token
	// If not defined and UseManagedServiceAccount is set to true, it defaults to a value using veleroTTL
	ManagedServiceAccountTTL metav1.Duration `json:"managedServiceAccountTTL,omitempty"`
	// +kubebuilder:validation:Optional
	// If not defined, the value is set to false.
	// Set this to true if you don't want to have backups created immediately after the velero schedules are created
	// Keep in mind that if you choose not to create ACM backups immediately after the schedules are generated
	// the validation policy will show a violation until backups are generated as defined by the cron job.
	// Also, if backups are not generated immediately, the BackupSchedule will always get into a BackupCollision state
	// during reconcile, if another hub had generated backups to the same storage location. Reconcile will find
	// the ACM backups with newer timestamp and generated by another hub so it will assume another hub currently
	// writes data to the same storage location.
	NoBackupOnStart bool `json:"noBackupOnStart,omitempty"`
	// +kubebuilder:validation:Optional
	// VolumeSnapshotLocations is a list containing names of VolumeSnapshotLocations associated with this backup.
	VolumeSnapshotLocations []string `json:"volumeSnapshotLocations,omitempty"`
}

// BackupScheduleStatus defines the observed state of BackupSchedule
type BackupScheduleStatus struct {
	// Phase is the current phase of the schedule
	// +kubebuilder:validation:Optional
	Phase SchedulePhase `json:"phase"`
	// Message on the last operation
	// +kubebuilder:validation:Optional
	LastMessage string `json:"lastMessage"`
	// Velero Schedule for backing up remote clusters
	// +kubebuilder:validation:Optional
	VeleroScheduleManagedClusters *veleroapi.Schedule `json:"veleroScheduleManagedClusters,omitempty"`
	// Velero Schedule for backing up other resources
	// +kubebuilder:validation:Optional
	VeleroScheduleResources *veleroapi.Schedule `json:"veleroScheduleResources,omitempty"`
	// Velero Schedule for backing up credentials
	// +kubebuilder:validation:Optional
	VeleroScheduleCredentials *veleroapi.Schedule `json:"veleroScheduleCredentials,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:validation:Optional
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"bsch"}
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.lastMessage`

// BackupSchedule is an ACM resource that you can use to schedule cluster backups at specified intervals.
// The backupschedule resource creates a set of schedule.velero.io resources to periodically generate backups for resources on your ACM hub cluster.
type BackupSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupScheduleSpec   `json:"spec,omitempty"`
	Status BackupScheduleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BackupScheduleList contains a list of backup schedules
type BackupScheduleList struct {
	metav1.TypeMeta `                 json:",inline"`
	metav1.ListMeta `                 json:"metadata,omitempty"`
	Items           []BackupSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackupSchedule{}, &BackupScheduleList{})
}
