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
	"context"
	"strings"
	"testing"
)

//nolint:funlen
func TestRestore_ValidateCreate(t *testing.T) {
	skip := "skip"
	latest := "latest"
	specificBackup := "acm-backup-123"

	tests := []struct {
		name      string
		restore   *Restore
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid non-sync restore",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       false,
					VeleroManagedClustersBackupName: &specificBackup,
					VeleroCredentialsBackupName:     &specificBackup,
					VeleroResourcesBackupName:       &specificBackup,
				},
			},
			wantErr: false,
		},
		{
			name: "valid sync restore with skip",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &skip,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid sync - MC specific backup",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &specificBackup,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
			},
			wantErr:   true,
			errSubstr: "veleroManagedClustersBackupName to be 'skip' or 'latest'",
		},
		{
			name: "invalid sync - creds not latest",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &skip,
					VeleroCredentialsBackupName:     &specificBackup,
					VeleroResourcesBackupName:       &latest,
				},
			},
			wantErr:   true,
			errSubstr: "veleroCredentialsBackupName to be 'latest'",
		},
		{
			name: "invalid sync - resources not latest",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &skip,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &specificBackup,
				},
			},
			wantErr:   true,
			errSubstr: "veleroResourcesBackupName to be 'latest'",
		},
		{
			name: "invalid sync - MC latest on initial create",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &latest,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
			},
			wantErr:   true,
			errSubstr: "must initially be set to 'skip'",
		},
		{
			name: "valid sync - MC latest when Phase=Enabled (activation)",
			restore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &latest,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
				Status: RestoreStatus{
					Phase: RestorePhaseEnabled,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.restore.ValidateCreate(context.Background(), tt.restore)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateCreate() error = %v, should contain %v", err, tt.errSubstr)
				}
			}
		})
	}
}

func TestRestore_ValidateUpdate(t *testing.T) {
	skip := "skip"
	latest := "latest"

	tests := []struct {
		name       string
		oldRestore *Restore
		newRestore *Restore
		wantErr    bool
	}{
		{
			name: "valid update - activation from skip to latest",
			oldRestore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &skip,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
				Status: RestoreStatus{
					Phase: RestorePhaseEnabled,
				},
			},
			newRestore: &Restore{
				Spec: RestoreSpec{
					SyncRestoreWithNewBackups:       true,
					VeleroManagedClustersBackupName: &latest,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
				Status: RestoreStatus{
					Phase: RestorePhaseEnabled,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.newRestore.ValidateUpdate(context.Background(), tt.oldRestore, tt.newRestore)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
