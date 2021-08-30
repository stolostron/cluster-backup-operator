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

// ClusterBackupScheduleSpec defines the desired state of ClusterBackupSchedule
type ClusterBackupScheduleSpec struct {
	// Schedule is a Cron expression defining when to run
	// the Velero Backup
	// +kubebuilder:validation:Required
	VeleroSchedule string `json:"veleroSchedule"`
	// TTL is a time.Duration-parseable string describing how long
	// the Velero Backup should be retained for.
	// +kubebuilder:validation:Required
	VeleroTTL metav1.Duration `json:"veleroTtl,omitempty"`
}

// ClusterBackupScheduleStatus defines the observed state of ClusterBackupSchedule
type ClusterBackupScheduleStatus struct {
	// Phase shows the status for the backup operation
	// +kubebuilder:validation:Optional
	Phase StatusPhase `json:"phase"`
	// Velero Schedule for backing up remote clusters
	// +kubebuilder:validation:Optional
	VeleroScheduleManagedClusters *veleroapi.Schedule `json:"veleroManagedClustersSchedule,omitempty"`
	// Velero Schedule for backing up other resources
	// +kubebuilder:validation:Optional
	VeleroScheduleResources *veleroapi.Schedule `json:"veleroResourcesSchedule,omitempty"`
	// Velero Schedule for backing up credentials
	// +kubebuilder:validation:Optional
	VeleroScheduleCredentials *veleroapi.Schedule `json:"veleroCredentialsSchedule,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:validation:Optional
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// ClusterBackupSchedule is the Schema for the backup schedules API
type ClusterBackupSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterBackupScheduleSpec   `json:"spec,omitempty"`
	Status ClusterBackupScheduleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterBackupScheduleList contains a list of backup schedules
type ClusterBackupScheduleList struct {
	metav1.TypeMeta `                        json:",inline"`
	metav1.ListMeta `                        json:"metadata,omitempty"`
	Items           []ClusterBackupSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterBackupSchedule{}, &ClusterBackupScheduleList{})
}
