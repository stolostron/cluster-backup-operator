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

package controllers

import (
	"testing"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_isVeleroRestoreFinished(t *testing.T) {
	type args struct {
		restore *veleroapi.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "No velero restore",
			args: args{
				restore: nil,
			},
		},
		{
			name: "Finished",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseCompleted,
					},
				},
			},
			want: true,
		},
		{
			name: "Not Finished",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseInProgress,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVeleroRestoreFinished(tt.args.restore); got != tt.want {
				t.Errorf("isVeleroRestoreFinished() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isVeleroRestoreRunning(t *testing.T) {
	type args struct {
		restore *veleroapi.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "No velero restore",
			args: args{
				restore: nil,
			},
		},
		{
			name: "New velero restore",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseNew,
					},
				},
			},
			want: true,
		},
		{
			name: "Failed velero restore",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseFailed,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVeleroRestoreRunning(tt.args.restore); got != tt.want {
				t.Errorf("isVeleroRestoreRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isSkipAllRestores(t *testing.T) {
	skipRestore := "skip"
	latestBackup := "latest"
	type args struct {
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Skip all",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: true,
		},
		{
			name: "No backup name",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
				},
			},
			want: true,
		},
		{
			name: "Do not skip all",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSkipAllRestores(tt.args.restore); got != tt.want {
				t.Errorf("isSkipAllRestores() = %v, want %v", got, tt.want)
			}
		})
	}
}
