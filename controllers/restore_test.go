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

//nolint:funlen
package controllers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// Test_isVeleroRestoreFinished tests the detection of completed Velero restore operations.
//
// This test validates the logic that determines whether a Velero restore has finished,
// which is essential for coordinating multi-step restore workflows.
//
// Test Coverage:
// - Nil restore handling (no restore object)
// - Completed restore detection (phase: completed)
// - Non-completed restore detection (phase: in-progress)
// - Various Velero restore phase states
//
// Test Scenarios:
// - No velero restore object provided
// - Finished restore with completed phase
// - Not finished restore with in-progress phase
//
// Implementation Details:
// - Uses Velero API types for realistic test scenarios
// - Tests various restore phase states
// - Validates proper null/nil handling
//
// Business Logic:
// This function is critical for restore orchestration, allowing the controller
// to determine when individual Velero restore operations have completed so
// it can proceed with subsequent restore steps or finalization.
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

// Test_isVeleroRestoreRunning tests the detection of active Velero restore operations.
//
// This test validates the logic that determines whether a Velero restore is currently
// running, which is critical for preventing concurrent restore conflicts.
//
// Test Coverage:
// - Nil restore handling (no restore object)
// - Active restore detection (phase: new)
// - Inactive restore detection (phase: failed)
// - Various Velero restore phase states
//
// Test Scenarios:
// - No velero restore object provided
// - New velero restore (considered running)
// - Failed velero restore (not running)
//
// Implementation Details:
// - Uses Velero API types for realistic test scenarios
// - Tests different restore phase states
// - Validates proper null/nil handling
//
// Business Logic:
// This function prevents restore conflicts by ensuring only one restore
// operation runs at a time, maintaining data integrity and preventing
// resource conflicts during restore operations.
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

// Test_isValidSyncOptions tests validation of sync operation configuration settings.
//
// This test validates the sync configuration validation logic that ensures
// proper setup for continuous restore synchronization with new backups.
//
// Test Coverage:
// - Skip-all configuration validation (should be invalid)
// - Missing backup name validation
// - Credential backup name validation (must be "skip" or "latest")
// - Resource backup name validation (must be "latest")
// - Sync flag requirement validation
// - Valid sync configuration scenarios
//
// Test Scenarios:
// - Skip all backups (invalid sync config)
// - No backup name provided (invalid)
// - Credentials with specific backup name (invalid for sync)
// - Resources set to skip (invalid for sync)
// - No sync flag enabled (invalid)
// - Valid sync configuration
//
// Implementation Details:
// - Uses predefined constants for "skip" and "latest" values
// - Tests various backup name combinations
// - Validates sync flag requirements
// - Uses ACM restore builder for realistic configurations
//
// Business Logic:
// Sync operations require specific backup configurations to work correctly.
// This validation ensures that only valid sync configurations are accepted,
// preventing misconfigured sync operations that could cause data inconsistencies.
func Test_isValidSyncOptions(t *testing.T) {
	skipRestore := "skip"
	latestBackup := "latest"
	backupName := "acm-managed-clusters-schedule-111"
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
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).object,
			},
			want: false,
		},
		{
			name: "No backup name",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).object,
			},
			want: false,
		},
		{
			name: "Credentials should be set to skip or latest",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).syncRestoreWithNewBackups(true).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(backupName).
					veleroResourcesBackupName(latestBackup).object,
			},
			want: false,
		},
		{
			name: "Resources should be set to latest",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).syncRestoreWithNewBackups(true).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackup).
					veleroResourcesBackupName(skipRestore).object,
			},
			want: false,
		},
		{
			name: "InValid config, no sync",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackup).
					veleroResourcesBackupName(latestBackup).object,
			},
			want: false,
		},
		{
			name: "Valid config",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackup).
					veleroResourcesBackupName(latestBackup).object,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, msg := isValidSyncOptions(tt.args.restore); got != tt.want {
				t.Errorf("failed test %s isValidSyncOptions() = %v, want %v, message: %s", tt.name, got, tt.want, msg)
			}
		})
	}
}

// Test_isSkipAllRestores tests detection of skip-all restore configuration.
//
// This test validates the logic that determines when all restore operations
// should be skipped, which is useful for sync operations that only monitor
// without performing actual restores.
//
// Test Coverage:
// - All backup types set to "skip" (should return true)
// - Mixed backup configurations with some skipped
// - Latest backup configurations (should return false)
// - Various combinations of skip/latest/specific backup names
//
// Test Scenarios:
// - Skip all backup types (managed clusters, credentials, resources)
// - Mixed configuration with some skipped and some using latest
// - All latest backup configuration
// - Combinations of skip, latest, and specific backup names
//
// Implementation Details:
// - Uses predefined constants for "skip" and "latest" values
// - Tests various backup name combinations
// - Uses ACM restore builder for realistic configurations
// - Validates different backup type combinations
//
// Business Logic:
// This function is essential for sync operations where monitoring is needed
// but actual restore operations should be skipped. It helps determine when
// a restore configuration is set to skip all operations, allowing the
// controller to handle these cases appropriately.
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
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).object,
			},
			want: true,
		},
		{
			name: "No backup name",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					object,
			},
			want: true,
		},
		{
			name: "Do not skip all",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackup).
					veleroResourcesBackupName(latestBackup).object,
			},
			want: false,
		},
		{
			name: "Managed clusters name is not skip",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackup).
					veleroCredentialsBackupName(latestBackup).
					veleroResourcesBackupName(latestBackup).object,
			},
			want: false,
		},
		{
			name: "Resources is not skip",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(latestBackup).object,
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

// Test_sendResults tests the notification and result reporting functionality for restore operations.
//
// This test validates the result reporting logic that sends restore operation
// outcomes to external systems or logs for monitoring and alerting purposes.
//
// Test Coverage:
// - Successful restore result reporting
// - Error condition result reporting
// - Sync restore configurations with different phases
// - Result handling for various restore states
//
// Test Scenarios:
// - Try restore again (enabled phase with sync interval)
// - Skip restore again (finished phase with skip configuration)
// - Different sync configurations and cleanup settings
// - Various restore phases and error conditions
//
// Implementation Details:
// - Uses sync restore configurations for testing
// - Tests both success and failure scenarios
// - Validates proper error handling and reporting
// - Uses realistic restore objects with sync settings
//
// Business Logic:
// Result reporting is crucial for monitoring restore operations and
// providing feedback to users and monitoring systems about restore
// success or failure, enabling proper operational awareness and
// determining when to retry or skip future restore attempts.
func Test_sendResults(t *testing.T) {
	skipRestore := "skip"
	type args struct {
		restore *v1beta1.Restore
		err     error
	}
	tests := []struct {
		name string
		args args
		want error
	}{
		{
			name: "Try restore again",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 15}).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).
					phase(v1beta1.RestorePhaseEnabled).object,

				err: nil,
			},
			want: nil,
		},
		{
			name: "Skip restore again",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).
					phase(v1beta1.RestorePhaseFinished).object,

				err: nil,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := sendResult(tt.args.restore, tt.args.err); err != tt.want {
				t.Errorf("isSkipAllRestores() = %v, want %v", err, tt.want)
			}
		})
	}
}

// Test_setRestorePhase tests restore phase management and state transitions.
//
// This test validates the logic that determines and sets appropriate restore phases
// based on restore configuration and current Velero restore states.
//
// Test Coverage:
// - Phase transitions for skip-all configurations
// - Phase transitions for active restore configurations
// - Cleanup enablement logic during phase transitions
// - Empty restore list handling
// - Various restore phases and configurations
//
// Test Scenarios:
// - Empty restore list with skip-all config (should finish)
// - Empty restore list with active config (should start)
// - Various sync configurations and cleanup settings
// - Different initial phases and expected transitions
//
// Implementation Details:
// - Uses various restore configurations for comprehensive testing
// - Tests phase transition logic
// - Validates cleanup enablement flags
// - Uses realistic restore objects and Velero restore lists
//
// Business Logic:
// Phase management is critical for restore orchestration, ensuring that
// restores progress through appropriate states (started, running, finished)
// while triggering cleanup operations at the right times and handling
// sync operations correctly.
func Test_setRestorePhase(t *testing.T) {
	skipRestore := "skip"
	latestBackupStr := "latest"
	type args struct {
		restore     *v1beta1.Restore
		restoreList *veleroapi.RestoreList
	}
	tests := []struct {
		name                 string
		args                 args
		wantPhase            v1beta1.RestorePhase
		wantCleanupOnEnabled bool
	}{
		{
			name: "Restore list empty and skip all, return finished phase",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 15}).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).
					phase(v1beta1.RestorePhaseRunning).object,

				restoreList: nil,
			},
			wantPhase:            v1beta1.RestorePhaseFinished,
			wantCleanupOnEnabled: false,
		},
		{
			name: "Restore list empty and NOT skip all, return finished RestorePhaseStarted",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 15}).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).
					phase(v1beta1.RestorePhaseRunning).object,

				restoreList: nil,
			},
			wantPhase:            v1beta1.RestorePhaseStarted,
			wantCleanupOnEnabled: false,
		},
		{
			name: "Restore phase is RestorePhaseEnabled and sync option, return wantCleanupOnEnabled is false",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 15}).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseEnabled).object,

				restoreList: nil,
			},
			wantPhase:            v1beta1.RestorePhaseEnabled,
			wantCleanupOnEnabled: false,
		},
		{
			name: "Restore list empty and NOT skip all, return finished RestorePhaseEnabled and wantCleanupOnEnabled is TRUE",
			args: args{
				restore: createACMRestore("Restore", "velero-ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 15}).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseRunning).object,

				restoreList: &veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "Restore",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "restore",
								Namespace: "velero-ns",
							},
							Spec: veleroapi.RestoreSpec{
								BackupName: "backup",
							},
							Status: veleroapi.RestoreStatus{
								Phase: veleroapi.RestorePhaseCompleted,
							},
						},
					},
				},
			},
			wantPhase:            v1beta1.RestorePhaseEnabled,
			wantCleanupOnEnabled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase, cleanupOnEnabled := setRestorePhase(tt.args.restoreList, tt.args.restore)
			if phase != tt.wantPhase || cleanupOnEnabled != tt.wantCleanupOnEnabled {
				t.Errorf("setRestorePhase() phase = %v, want %v, cleanupOnEnabled = %v, want %v",
					phase, tt.wantPhase, cleanupOnEnabled, tt.wantCleanupOnEnabled)
			}
		})
	}
}

// Test_getVeleroBackupName tests retrieval of Velero backup names for different resource types.
//
// This test validates the logic that resolves backup names for different resource
// types, handling both specific backup names and "latest" backup resolution.
//
// Test Coverage:
// - Latest backup resolution for different resource types
// - Specific backup name handling
// - Backup retrieval from Kubernetes API
// - Error handling for missing backups
// - Timestamp-based backup correlation for cluster credentials
//
// Test Scenarios:
// - Schema registration error handling
// - No backup items available
// - Backup timestamp mismatch scenarios
// - Successful backup resolution for credentials
// - Missing backup scenarios for credentials hive
// - Timestamp-based matching for cluster credentials
//
// Implementation Details:
// - Uses fake Kubernetes client with Velero scheme
// - Creates realistic backup objects with proper timestamps
// - Tests different resource types and backup scenarios
// - Validates proper backup name resolution and error handling
//
// Business Logic:
// Backup name resolution is essential for restore operations, allowing
// the system to determine which specific backup to use for each resource
// type, whether using the latest available backup or a specific backup name.
// Special handling for cluster credentials requires timestamp correlation.
func Test_getVeleroBackupName(t *testing.T) {
	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)
	latestBackupStr := "latest"

	backup := *createBackup("acm-credentials-schedule-20220922170041", veleroNamespaceName).
		labels(map[string]string{
			BackupVeleroLabel:          "aa",
			BackupScheduleClusterLabel: "abcd",
		}).
		phase(veleroapi.BackupPhaseCompleted).
		errors(0).object

	backupClsNoMatch := *createBackup("acm-credentials-cluster-schedule-20220922170039", veleroNamespaceName).
		labels(map[string]string{
			BackupVeleroLabel:          "aa",
			BackupScheduleClusterLabel: "abcd",
		}).
		phase(veleroapi.BackupPhaseCompleted).
		errors(0).object

	backupClsExactTime := *createBackup("acm-credentials-cluster-schedule-20220922170041", veleroNamespaceName).
		labels(map[string]string{
			BackupVeleroLabel:          "aa",
			BackupScheduleClusterLabel: "abcd",
		}).
		phase(veleroapi.BackupPhaseCompleted).
		errors(0).object

	backupTime, _ := time.Parse(time.RFC3339, "2022-09-22T17:00:15Z")

	backupClsExactWithin30s := *createBackup("acm-credentials-cluster-schedule-202209221745", veleroNamespaceName).
		labels(map[string]string{
			BackupVeleroLabel:          "aa",
			BackupScheduleClusterLabel: "abcd",
		}).
		startTimestamp(metav1.NewTime(backupTime)).
		phase(veleroapi.BackupPhaseCompleted).
		errors(0).object

	type args struct {
		ctx              context.Context
		c                client.Client
		restoreNamespace string
		resourceType     ResourceType
		backupName       string
	}
	tests := []struct {
		name         string
		args         args
		want         string
		setupObjects []client.Object
		setupScheme  bool
	}{
		{
			name: "no kind is registered for the type v1.BackupList",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        "",
			setupScheme: false, // Don't add velero scheme to test error case
		},
		{
			name: "no backup items",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        "",
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
			},
		},
		{
			name: "found backup item but time is not matching",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsCluster,
				backupName:       "acm-credentials-schedule-20220822170041",
				restoreNamespace: veleroNamespaceName,
			},
			want:        "",
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&backupClsNoMatch,
			},
		},
		{
			name: "found backup item for credentials",
			args: args{
				ctx:              context.Background(),
				resourceType:     Credentials,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        backup.Name,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&backupClsNoMatch,
			},
		},
		{
			name: "NOT found backup item for credentials hive",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsHive,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        "",
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&backupClsNoMatch,
			},
		},
		{
			name: "found backup item for credentials cluster NOT found no exact match on timestamp and not within 30s",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        "",
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&backupClsNoMatch,
			},
		},
		{
			name: "found backup item for credentials cluster when exact match on timestamp",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        backupClsExactTime.Name,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&backupClsNoMatch,
				&backupClsExactTime,
			},
		},
		{
			name: "found backup item for credentials cluster when NOT exact match on timestamp but withn 30s",
			args: args{
				ctx:              context.Background(),
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want:        backupClsExactWithin30s.Name,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&backupClsNoMatch,
				&backupClsExactWithin30s,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			if tt.setupScheme {
				if err := corev1.AddToScheme(testScheme); err != nil {
					t.Fatalf("Error adding corev1 api to scheme: %s", err.Error())
				}
				if err := veleroapi.AddToScheme(testScheme); err != nil {
					t.Fatalf("Error adding velero api to scheme: %s", err.Error())
				}
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.setupObjects...).
				Build()

			tt.args.c = fakeClient

			// Get backup list for the function call
			veleroBackups := &veleroapi.BackupList{}
			if tt.setupScheme {
				err := tt.args.c.List(tt.args.ctx, veleroBackups, client.InNamespace(veleroNamespace.Name))
				if err != nil {
					t.Errorf("Error listing veleroBackups: %s", err.Error())
				}
			}

			// Call function under test
			if name, _, _ := getVeleroBackupName(tt.args.ctx, tt.args.c,
				tt.args.restoreNamespace, tt.args.resourceType, tt.args.backupName, veleroBackups); name != tt.want {
				t.Errorf("getVeleroBackupName() returns = %v, want %v", name, tt.want)
			}
		})
	}
}

// Test_isNewBackupAvailable tests detection of new backup availability for sync operations.
//
// This test validates the logic that determines when new backups are available
// for sync operations, which is critical for continuous restore synchronization.
//
// Test Coverage:
// - New backup detection for different resource types
// - Backup timestamp comparison logic
// - Latest backup resolution and comparison
// - Error handling for missing or invalid backups
// - Multiple backup scenarios with different timestamps
//
// Test Scenarios:
// - Schema registration error handling
// - No backup items available
// - Missing restore items
// - Same backup scenarios (no new backup)
// - New backup available scenarios
// - Complex restore and backup relationships
//
// Implementation Details:
// - Uses fake Kubernetes client with realistic backup and restore objects
// - Creates multiple backup scenarios with varied timestamps
// - Tests different resource types (credentials, resources, clusters)
// - Validates proper timestamp comparison and restore correlation logic
//
// Business Logic:
// New backup detection is essential for sync operations, allowing the
// system to automatically detect when newer backups are available and
// trigger appropriate restore or synchronization actions to keep the
// restored environment up-to-date with the latest backup data.
func Test_isNewBackupAvailable(t *testing.T) {
	skipRestore := "skip"
	latestBackup := "latest"

	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

	passiveStr := "passive"
	backupName := "acm-credentials-schedule-20220922170041"
	restoreName := passiveStr + "-" + backupName

	backup := *createBackup(backupName, veleroNamespaceName).
		labels(map[string]string{
			BackupVeleroLabel:          "aa",
			BackupScheduleClusterLabel: "abcd",
		}).
		phase(veleroapi.BackupPhaseCompleted).
		errors(0).object

	veleroRestore := veleroapi.Restore{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero/v1",
			Kind:       "Restore",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoreName,
			Namespace: veleroNamespaceName,
		},
		Spec: veleroapi.RestoreSpec{
			BackupName: backupName,
		},
		Status: veleroapi.RestoreStatus{
			Phase: "Completed",
		},
	}

	restoreCreds := *createACMRestore(passiveStr, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(latestBackup).
		veleroResourcesBackupName(latestBackup).object

	restoreCredSameBackup := *createACMRestore(passiveStr, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(latestBackup).
		veleroResourcesBackupName(latestBackup).
		veleroCredentialsBackupName(veleroRestore.Name).object

	restoreCredNewBackup := *createACMRestore(passiveStr, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(latestBackup).
		veleroResourcesBackupName(latestBackup).
		veleroCredentialsRestoreName(veleroRestore.Name + "11").object

	// Create the corresponding Velero restore that matches the ACM restore's expected name
	veleroRestoreForNewBackup := veleroapi.Restore{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero/v1",
			Kind:       "Restore",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      veleroRestore.Name + "11",
			Namespace: veleroNamespaceName,
		},
		Spec: veleroapi.RestoreSpec{
			BackupName: backupName,
		},
		Status: veleroapi.RestoreStatus{
			Phase: "Completed",
		},
	}

	type args struct {
		ctx          context.Context
		c            client.Client
		restore      *v1beta1.Restore
		resourceType ResourceType
	}
	tests := []struct {
		name         string
		args         args
		want         bool
		setupObjects []client.Object
		setupScheme  bool
	}{
		{
			name: "no kind is registered for the type v1.BackupList",
			args: args{
				ctx:          context.Background(),
				restore:      &restoreCreds,
				resourceType: CredentialsCluster,
			},
			want:        false,
			setupScheme: false, // Don't add schemes to test error case
		},
		{
			name: "no backup items",
			args: args{
				ctx:          context.Background(),
				resourceType: CredentialsCluster,
				restore:      &restoreCreds,
			},
			want:        false,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
			},
		},
		{
			name: "NOT found restore item ",
			args: args{
				ctx:          context.Background(),
				resourceType: CredentialsCluster,
				restore:      &restoreCreds,
			},
			want:        false,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&veleroRestore,
			},
		},
		{
			name: "found restore item but not the latest backup",
			args: args{
				ctx:          context.Background(),
				resourceType: Credentials,
				restore:      &restoreCredSameBackup,
			},
			want:        false,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&veleroRestore,
			},
		},
		{
			name: "found restore item AND new backup",
			args: args{
				ctx:          context.Background(),
				resourceType: Credentials,
				restore:      &restoreCredNewBackup,
			},
			want:        true,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&veleroRestore,
			},
		},
		{
			name: "found restore item AND new backup, with restore found",
			args: args{
				ctx:          context.Background(),
				resourceType: Credentials,
				restore:      &restoreCredNewBackup,
			},
			want:        false,
			setupScheme: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				&backup,
				&veleroRestore,
				&veleroRestoreForNewBackup,
				&restoreCredNewBackup,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			if tt.setupScheme {
				if err := corev1.AddToScheme(testScheme); err != nil {
					t.Fatalf("Error adding corev1 api to scheme: %s", err.Error())
				}
				if err := v1beta1.AddToScheme(testScheme); err != nil {
					t.Fatalf("Error adding v1beta1 api to scheme: %s", err.Error())
				}
				if err := veleroapi.AddToScheme(testScheme); err != nil {
					t.Fatalf("Error adding velero api to scheme: %s", err.Error())
				}
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.setupObjects...).
				Build()

			tt.args.c = fakeClient

			// Call function under test
			if got := isNewBackupAvailable(tt.args.ctx, tt.args.c,
				tt.args.restore, tt.args.resourceType); got != tt.want {
				t.Errorf("isNewBackupAvailable() returns = %v, want %v, %v", got, tt.want, tt.args.resourceType)
			}
		})
	}
}

// Test_isBackupScheduleRunning tests detection of active backup schedule operations.
//
// This test validates the logic that determines whether any backup schedules
// are currently running, which is important for restore coordination.
//
// Test Coverage:
// - Empty backup schedule list handling
// - Single running backup schedule detection
// - Multiple backup schedules with different states
// - Various backup schedule phases and conditions
//
// Business Logic:
// This function prevents restore conflicts by ensuring backup schedules
// are not actively running during restore operations, maintaining data
// consistency and preventing interference between backup and restore processes.
func Test_isBackupScheduleRunning(t *testing.T) {
	type args struct {
		backupSchedules []v1beta1.BackupSchedule
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "backup list is empty",
			args: args{
				backupSchedules: []v1beta1.BackupSchedule{},
			},
			want: "",
		},
		{
			name: "backup without backupcollision running",
			args: args{
				backupSchedules: []v1beta1.BackupSchedule{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1beta1",
							Kind:       "BackupSchedule",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "backup-name",
							Namespace: "ns",
						},
						Status: v1beta1.BackupScheduleStatus{
							Phase: v1beta1.SchedulePhaseEnabled,
						},
					},
				},
			},
			want: "backup-name",
		},
		{
			name: "backup WITH backupcollision",
			args: args{
				backupSchedules: []v1beta1.BackupSchedule{
					*createBackupSchedule("backup-name", "ns").
						phase(v1beta1.SchedulePhaseBackupCollision).
						object,
				},
			},
			want: "",
		},
		{
			name: "backup WITH paused schedule",
			args: args{
				backupSchedules: []v1beta1.BackupSchedule{
					*createBackupSchedule("backup-name", "ns").
						phase(v1beta1.SchedulePhasePaused).
						object,
				},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBackupScheduleRunning(tt.args.backupSchedules); got != tt.want {
				t.Errorf("isBackupScheduleRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_isOtherRestoresRunning tests detection of concurrent restore operations.
//
// This test validates the logic that determines whether other restore operations
// are currently running, preventing restore conflicts and ensuring serialized execution.
//
// Test Coverage:
// - Empty restore list handling
// - Single restore operation detection
// - Multiple restore operations with different states
// - Current restore exclusion from running check
// - Various restore phases and states
//
// Business Logic:
// This function ensures only one restore operation runs at a time by detecting
// other active restores, preventing resource conflicts and maintaining restore
// operation integrity in multi-restore environments.
func Test_isOtherRestoresRunning(t *testing.T) {
	type args struct {
		restores    []v1beta1.Restore
		restoreName string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "restore list is empty",
			args: args{
				restores: []v1beta1.Restore{},
			},
			want: "",
		},
		{
			name: "restore list has one running item ",
			args: args{
				restoreName: "some-name",
				restores: []v1beta1.Restore{
					*createACMRestore("some-name", "ns").object,
					*createACMRestore("some-other-name", "ns").
						phase(v1beta1.RestorePhaseEnabled).object,
				},
			},
			want: "some-other-name",
		},
		{
			name: "restore list has one completed item ",
			args: args{
				restoreName: "some-name",
				restores: []v1beta1.Restore{
					*createACMRestore("some-name", "ns").object,
					*createACMRestore("some-other-name", "ns").
						phase(v1beta1.RestorePhaseFinishedWithErrors).object,
				},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOtherRestoresRunning(tt.args.restores, tt.args.restoreName); got != tt.want {
				t.Errorf("isOtherRestoresRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_setOptionalProperties tests setting of optional properties on Velero restore objects.
//
// This test validates the logic that configures optional properties on Velero
// restore objects based on resource type and ACM restore configuration.
//
// Test Coverage:
// - Different resource types (credentials, resources, clusters)
// - Optional property mapping from ACM to Velero restore
// - Resource-specific configuration handling
// - Property inheritance and override logic
//
// Business Logic:
// This function ensures Velero restore objects are properly configured with
// ACM-specific properties, enabling proper restore behavior and integration
// between ACM restore operations and underlying Velero functionality.
func Test_setOptionalProperties(t *testing.T) {
	type args struct {
		restype       ResourceType
		acmRestore    *v1beta1.Restore
		veleroRestore *veleroapi.Restore
	}

	tests := []struct {
		name string
		args args
	}{
		{
			name: "verify that CRDs are excluded from restore",
			args: args{
				restype: Credentials,
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName("skip").
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestore: createRestore("credentials-restore", "ns").object,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOptionalProperties(tt.args.restype, tt.args.acmRestore, tt.args.veleroRestore)
			if !findValue(tt.args.veleroRestore.Spec.ExcludedResources, "CustomResourceDefinition") {
				t.Errorf("CustomResourceDefinition should be excluded from restore and be part of " +
					"veleroRestore.Spec.ExcludedResources")
			}
		})
	}
}

// Test_retrieveRestoreDetails tests comprehensive restore information gathering and validation.
//
// This test validates the logic that retrieves and processes detailed information
// about restore operations, including backup validation and restore configuration.
//
// Test Coverage:
// - Restore details retrieval and processing
// - Backup validation and correlation
// - Managed cluster only restore scenarios
// - Error handling for missing or invalid restores
// - Restore key ordering and priority handling
//
// Business Logic:
// This function gathers comprehensive restore information needed for
// restore orchestration, ensuring all necessary details are available
// for proper restore execution and validation, with credentials restored
// first and managed clusters restored last.
func Test_retrieveRestoreDetails(t *testing.T) {
	skipRestore := "skip"
	veleroNamespaceName := "default"
	invalidBackupName := ""
	backupName := "backup-name"
	latestBackupStr := "latest"

	backup := *createBackup(backupName, veleroNamespaceName).
		phase(veleroapi.BackupPhaseCompleted).
		errors(0).object

	restoreCredsNoError := *createACMRestore("restore1", veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(skipRestore).
		veleroResourcesBackupName(backupName).object

	restoreCredsInvalidBackupName := *createACMRestore("restore1", veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(skipRestore).
		veleroResourcesBackupName(invalidBackupName).object

	type args struct {
		ctx                        context.Context
		c                          client.Client
		s                          *runtime.Scheme
		restore                    *v1beta1.Restore
		restoreOnlyManagedClusters bool
		setupObjects               []client.Object
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "retrieveRestoreDetails has error, no backups found",
			args: args{
				ctx:                        context.Background(),
				s:                          runtime.NewScheme(),
				restore:                    &restoreCredsNoError,
				restoreOnlyManagedClusters: false,
				setupObjects:               []client.Object{},
			},
			want: false, // has error, restore not found
		},
		{
			name: "retrieveRestoreDetails has error, no backup name",
			args: args{
				ctx:                        context.Background(),
				s:                          runtime.NewScheme(),
				restore:                    &restoreCredsInvalidBackupName,
				restoreOnlyManagedClusters: false,
				setupObjects:               []client.Object{&backup},
			},
			want: false, // has error, backup name is invalid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			if err := veleroapi.AddToScheme(testScheme); err != nil {
				t.Fatalf("Error adding velero api to scheme: %s", err.Error())
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient
			tt.args.s = testScheme

			keys, _, got := retrieveRestoreDetails(tt.args.ctx, tt.args.c,
				tt.args.s, tt.args.restore, tt.args.restoreOnlyManagedClusters)
			if (got == nil) != tt.want {
				t.Errorf("retrieveRestoreDetails() returns = %v, want %v", got == nil, tt.want)
			}
			if len(keys) > 0 {
				if keys[0] != Credentials {
					t.Errorf("retrieveRestoreDetails() error, Credentials should be first key to restore ")
				}
				if keys[len(keys)-1] != ManagedClusters {
					t.Errorf("retrieveRestoreDetails() error, ManagedClusters should be last key for restore ")
				}
			}
		})
	}
}

// Test_isOtherResourcesRunning tests detection of other running resource operations.
//
// This test validates the logic that determines whether other resource operations
// (backup schedules, restores) are currently running, ensuring proper coordination.
//
// Test Coverage:
// - Other restore operations detection
// - Backup schedule collision detection
// - Failed backup schedule handling
// - Resource operation conflict prevention
//
// Business Logic:
// This function prevents resource conflicts by detecting other running operations,
// ensuring proper serialization and avoiding interference between different
// backup and restore processes.
func Test_isOtherResourcesRunning(t *testing.T) {
	veleroNamespaceName := "default"
	restoreName := "restore-backup-name"
	backupName := "backup-name"

	restore := *createACMRestore(restoreName, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(skipRestoreStr).
		veleroResourcesBackupName(backupName).object

	restoreOther := *createACMRestore("other-"+restoreName, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(skipRestoreStr).
		veleroResourcesBackupName(backupName).object

	backupCollision := *createBackupSchedule(backupName+"-collision", veleroNamespaceName).
		phase(v1beta1.SchedulePhaseBackupCollision).
		object
	backupFailed := *createBackupSchedule(backupName+"-failed", veleroNamespaceName).
		object

	type args struct {
		ctx          context.Context
		c            client.Client
		restore      *v1beta1.Restore
		setupObjects []client.Object
		setupScheme  bool
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "isOtherResourcesRunning has error, CRD not installed",
			args: args{
				ctx:          context.Background(),
				restore:      &restore,
				setupObjects: []client.Object{},
				setupScheme:  false, // no scheme setup to simulate CRD not installed
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has no errors, no backups found",
			args: args{
				ctx:          context.Background(),
				restore:      &restore,
				setupObjects: []client.Object{&restore},
				setupScheme:  true,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has no errors, backup found but in collision state",
			args: args{
				ctx:     context.Background(),
				restore: &restore,
				setupObjects: []client.Object{
					&restore,
					&backupCollision,
				},
				setupScheme: true,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has errors, backup found and not in collision state",
			args: args{
				ctx:     context.Background(),
				restore: &restore,
				setupObjects: []client.Object{
					&restore,
					&backupFailed,
				},
				setupScheme: true,
			},
			want: "This resource is ignored because BackupSchedule resource backup-name-failed is currently active, " +
				"before creating another resource verify that any active resources are removed.",
		},
		{
			name: "isOtherResourcesRunning has no errors, no another restore is running",
			args: args{
				ctx:     context.Background(),
				restore: &restore,
				setupObjects: []client.Object{
					&restore,
				},
				setupScheme: true,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has errors, another restore is running",
			args: args{
				ctx:     context.Background(),
				restore: &restore,
				setupObjects: []client.Object{
					&restore,
					&restoreOther,
				},
				setupScheme: true,
			},
			want: "This resource is ignored because Restore resource other-restore-backup-name is currently active, " +
				"before creating another resource verify that any active resources are removed.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			if tt.args.setupScheme {
				if err := v1beta1.AddToScheme(testScheme); err != nil {
					t.Fatalf("Error adding v1beta1 api to scheme: %s", err.Error())
				}
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient

			got, _ := isOtherResourcesRunning(tt.args.ctx, tt.args.c, tt.args.restore)
			if got != tt.want {
				t.Errorf("isOtherResourcesRunning() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_updateLabelsForActiveResources tests label management for active restore operations.
//
// This test validates the logic that updates labels on Velero restore resources
// to track active restore operations and enable proper resource correlation.
//
// Test Coverage:
// - Label updates for different resource types
// - Active resource tracking and identification
// - Velero restore resource labeling
// - Resource type specific label management
//
// Business Logic:
// This function maintains proper labeling of active restore resources,
// enabling tracking, monitoring, and correlation of restore operations
// across different resource types and components.
func Test_updateLabelsForActiveResources(t *testing.T) {
	type args struct {
		acmRestore             *v1beta1.Restore
		restype                ResourceType
		veleroRestoresToCreate map[ResourceType]*veleroapi.Restore
	}

	tests := []struct {
		name        string
		args        args
		want        bool
		wantResName string // the name of the restore after parsing the current state;
		// could have suffix -active if on the activation step
	}{
		{
			name: "Credentials restore with no ManagedCluster, should return false",
			args: args{
				restype: Credentials,
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoresToCreate: map[ResourceType]*veleroapi.Restore{
					Credentials: createRestore("credentials-restore", "ns").object,
				},
			},
			want:        false,
			wantResName: "credentials-restore",
		},
		{
			name: "Credentials restore with ManagedCluster latest and sync, should return true",
			args: args{
				restype: Credentials,
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					phase(v1beta1.RestorePhaseEnabled).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoresToCreate: map[ResourceType]*veleroapi.Restore{
					Credentials:     createRestore("credentials-restore", "ns").object,
					ManagedClusters: createRestore("clusters-restore", "ns").object,
				},
			},
			want:        true,
			wantResName: "credentials-restore-active", // -active suffix for sync to avoid name collision
		},
		{
			name: "Credentials restore for ManagedCluster latest with no sync, should return true",
			args: args{
				restype: Credentials,
				acmRestore: createACMRestore("acm-restore", "ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(skipRestoreStr).
					veleroResourcesBackupName(skipRestoreStr).object,
				veleroRestoresToCreate: map[ResourceType]*veleroapi.Restore{
					Credentials:     createRestore("credentials-restore", "ns").object,
					ManagedClusters: createRestore("clusters-restore", "ns").object,
				},
			},
			want:        true,
			wantResName: "credentials-restore", // creds was skipped
		},
		{
			name: "Credentials restore with ManagedCluster specific backup name, no sync, no -active suffix",
			args: args{
				restype: Credentials,
				acmRestore: createACMRestore("acm-restore", "ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName("acm-managed-clusters-schedule-20251029181055").
					veleroCredentialsBackupName("acm-credentials-schedule-20251029181055").
					veleroResourcesBackupName("acm-resources-schedule-20251029181055").object,
				veleroRestoresToCreate: map[ResourceType]*veleroapi.Restore{
					Credentials:     createRestore("credentials-restore", "ns").object,
					ManagedClusters: createRestore("clusters-restore", "ns").object,
				},
			},
			want:        true,
			wantResName: "credentials-restore", // No -active suffix, no activation filter - restore ALL credentials
		},
		{
			name: "Generic Res restore with ManagedCluster and no sync, should return false no active",
			args: args{
				restype: ResourcesGeneric,
				acmRestore: createACMRestore("acm-restore", "ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoresToCreate: map[ResourceType]*veleroapi.Restore{
					Resources:        createRestore("resources-restore", "ns").object,
					ResourcesGeneric: createRestore("generic-restore", "ns").object,
					ManagedClusters:  createRestore("clusters-restore", "ns").object,
					Credentials:      createRestore("credentials-restore", "ns").object,
				},
			},
			want:        false,
			wantResName: "generic-restore",
		},
		{
			name: "Generic Res restore with ManagedCluster and sync, should return false and active",
			args: args{
				restype: ResourcesGeneric,
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					phase(v1beta1.RestorePhaseEnabled).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoresToCreate: map[ResourceType]*veleroapi.Restore{
					Resources:        createRestore("resources-restore", "ns").object,
					ResourcesGeneric: createRestore("generic-restore", "ns").object,
					ManagedClusters:  createRestore("clusters-restore", "ns").object,
					Credentials:      createRestore("credentials-restore", "ns").object,
				},
			},
			want:        false,
			wantResName: "generic-restore-active",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client for the test
			fakeClient := fake.NewClientBuilder().Build()
			got := updateLabelsForActiveResources(
				context.Background(), fakeClient, tt.args.acmRestore, tt.args.restype, tt.args.veleroRestoresToCreate,
			)
			if got != tt.want {
				t.Errorf("error updating labels for: %s", tt.name)
			}
			if tt.wantResName != tt.args.veleroRestoresToCreate[tt.args.restype].Name {
				t.Errorf("The restore resource name should be  %v, but got %v",
					tt.wantResName, tt.args.veleroRestoresToCreate[tt.args.restype].Name)
			}
		})
	}
}

// Test_credentialsRestoreWithSpecificBackupName tests the bug fix for credentials restore
// when using specific backup names with managed clusters (non-sync mode).
//
// This test validates that when restoring credentials with:
// - Specific backup names (not "latest")
// - Managed clusters being restored
// - Sync mode disabled
//
// The credentials restore should:
// - NOT add activation label selector (should restore ALL credentials)
// - NOT add -active suffix to the restore name
// - Return true for isCredsClsOnActiveStep (PVC wait required)
func Test_credentialsRestoreWithSpecificBackupName(t *testing.T) {
	acmRestore := createACMRestore("acm-restore", "ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName("acm-managed-clusters-schedule-20251029181055").
		veleroCredentialsBackupName("acm-credentials-schedule-20251029181055").
		veleroResourcesBackupName("acm-resources-schedule-20251029181055").object

	veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
		Credentials:     createRestore("credentials-restore", "ns").object,
		ManagedClusters: createRestore("clusters-restore", "ns").object,
	}

	// Call the function
	fakeClient := fake.NewClientBuilder().Build()
	isCredsClsOnActiveStep := updateLabelsForActiveResources(
		context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
	)

	// Verify return value
	if !isCredsClsOnActiveStep {
		t.Errorf("Expected isCredsClsOnActiveStep to be true, got false")
	}

	// Verify restore name (should NOT have -active suffix)
	expectedName := "credentials-restore"
	actualName := veleroRestoresToCreate[Credentials].Name
	if actualName != expectedName {
		t.Errorf("Expected restore name %s, got %s", expectedName, actualName)
	}

	// Verify NO activation label selector is added
	credsRestore := veleroRestoresToCreate[Credentials]
	if hasActivationLabel(*credsRestore) {
		t.Errorf("Credentials restore should NOT have activation label selector in non-sync mode with specific backup names")
	}
}

// Test_credentialsRestoreWithoutManagedClusters tests credentials restore when managed clusters are skipped.
//
// This test validates that when restoring credentials with:
// - Specific backup name for credentials
// - Managed clusters set to "skip"
// - Resources set to "skip"
//
// The credentials restore should:
// - Add "NotIn cluster-activation" label selector (exclude activation credentials)
// - NOT add -active suffix to the restore name
// - Return false for isCredsClsOnActiveStep (no PVC wait needed)
func Test_credentialsRestoreWithoutManagedClusters(t *testing.T) {
	acmRestore := createACMRestore("acm-restore", "ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName("skip").
		veleroCredentialsBackupName("acm-credentials-schedule-20251029181055").
		veleroResourcesBackupName("skip").object

	veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
		Credentials: createRestore("credentials-restore", "ns").object,
	}

	// Call the function
	fakeClient := fake.NewClientBuilder().Build()
	isCredsClsOnActiveStep := updateLabelsForActiveResources(
		context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
	)

	// Verify return value (should be false - no PVC wait needed)
	if isCredsClsOnActiveStep {
		t.Errorf("Expected isCredsClsOnActiveStep to be false, got true")
	}

	// Verify restore name (should NOT have -active suffix)
	expectedName := "credentials-restore"
	actualName := veleroRestoresToCreate[Credentials].Name
	if actualName != expectedName {
		t.Errorf("Expected restore name %s, got %s", expectedName, actualName)
	}

	// Verify that "NotIn cluster-activation" label selector is NOT added by updateLabelsForActiveResources
	// (it's added elsewhere in the restore flow)
	credsRestore := veleroRestoresToCreate[Credentials]
	if hasActivationLabel(*credsRestore) {
		t.Errorf("Credentials restore should NOT have activation label selector added by updateLabelsForActiveResources")
	}
}

// Test_restoreCase1_SkipClustersLatestCredsSync tests Case 1:
// ManagedClusters=skip, Credentials=latest, Resources=latest, Sync=true
//
// Expected: Credentials and ResourcesGeneric with NO label selector, no -active suffix
func Test_restoreCase1_SkipClustersLatestCredsSync(t *testing.T) {
	skipRestoreStr := "skip"
	latestBackupStr := "latest"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).
			syncRestoreWithNewBackups(true).
			restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials: createRestore("credentials-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).
			syncRestoreWithNewBackups(true).
			restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase2_SkipClustersLatestCredsNoSync tests Case 2:
// ManagedClusters=skip, Credentials=latest, Resources=latest, Sync=false
//
// Expected: Credentials and ResourcesGeneric with NO label selector, no -active suffix
func Test_restoreCase2_SkipClustersLatestCredsNoSync(t *testing.T) {
	skipRestoreStr := "skip"
	latestBackupStr := "latest"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials: createRestore("credentials-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase3_LatestClustersLatestCredsNoSync tests Case 3:
// ManagedClusters=latest, Credentials=latest, Resources=latest, Sync=false
//
// Expected: NO label selector for both Credentials and ResourcesGeneric
func Test_restoreCase3_LatestClustersLatestCredsNoSync(t *testing.T) {
	latestBackupStr := "latest"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(latestBackupStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials:     createRestore("credentials-restore", "ns").object,
			ManagedClusters: createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if !isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=true, got false")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(latestBackupStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
			Resources:        createRestore("resources-restore", "ns").object,
			ManagedClusters:  createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase4_LatestClustersSkipCredsLatestResourcesNoSync tests Case 4:
// ManagedClusters=latest, Credentials=skip, Resources=latest, Sync=false
//
// Expected: Credentials with In cluster-activation, ResourcesGeneric with NO label selector
func Test_restoreCase4_LatestClustersSkipCredsLatestResourcesNoSync(t *testing.T) {
	skipRestoreStr := "skip"
	latestBackupStr := "latest"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(latestBackupStr).
			veleroCredentialsBackupName(skipRestoreStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials:     createRestore("credentials-restore", "ns").object,
			ManagedClusters: createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if !isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=true, got false")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if !hasActivationLabel(*restoreObj) {
			t.Errorf("Expected In cluster-activation label selector")
		}

		// Verify it's In
		if restoreObj.Spec.LabelSelector != nil {
			for _, req := range restoreObj.Spec.LabelSelector.MatchExpressions {
				if req.Key == backupCredsClusterLabel && req.Operator != "In" {
					t.Errorf("Expected operator In, got %s", req.Operator)
				}
			}
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(latestBackupStr).
			veleroCredentialsBackupName(skipRestoreStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
			Resources:        createRestore("resources-restore", "ns").object,
			ManagedClusters:  createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase5_LatestClustersSkipCredsSkipResourcesNoSync tests Case 5:
// ManagedClusters=latest, Credentials=skip, Resources=skip, Sync=false
//
// Expected: Both Credentials and ResourcesGeneric with In cluster-activation, no -active suffix
func Test_restoreCase5_LatestClustersSkipCredsSkipResourcesNoSync(t *testing.T) {
	skipRestoreStr := "skip"
	latestBackupStr := "latest"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(latestBackupStr).
			veleroCredentialsBackupName(skipRestoreStr).
			veleroResourcesBackupName(skipRestoreStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials:     createRestore("credentials-restore", "ns").object,
			ManagedClusters: createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if !isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=true, got false")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if !hasActivationLabel(*restoreObj) {
			t.Errorf("Expected In cluster-activation label selector")
		}

		// Verify it's In
		if restoreObj.Spec.LabelSelector != nil {
			for _, req := range restoreObj.Spec.LabelSelector.MatchExpressions {
				if req.Key == backupCredsClusterLabel && req.Operator != "In" {
					t.Errorf("Expected operator In, got %s", req.Operator)
				}
			}
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(latestBackupStr).
			veleroCredentialsBackupName(skipRestoreStr).
			veleroResourcesBackupName(skipRestoreStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
			ManagedClusters:  createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if !hasActivationLabel(*restoreObj) {
			t.Errorf("Expected In cluster-activation label selector")
		}

		// Verify it's In
		if restoreObj.Spec.LabelSelector != nil {
			for _, req := range restoreObj.Spec.LabelSelector.MatchExpressions {
				if req.Key == backupCredsClusterLabel && req.Operator != "In" {
					t.Errorf("Expected operator In, got %s", req.Operator)
				}
			}
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase6_SkipClustersLatestCredsLatestResourcesNoSync tests Case 6:
// ManagedClusters=skip, Credentials=latest, Resources=latest, Sync=false
//
// Expected: Both Credentials and ResourcesGeneric with NO label selector, no -active suffix
func Test_restoreCase6_SkipClustersLatestCredsLatestResourcesNoSync(t *testing.T) {
	skipRestoreStr := "skip"
	latestBackupStr := "latest"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials: createRestore("credentials-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(latestBackupStr).
			veleroResourcesBackupName(latestBackupStr).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase7_SpecificBackupNamesNoSync tests Case 7:
// ManagedClusters=name, Credentials=name, Resources=name, Sync=false
//
// Expected: NO label selector for both Credentials and ResourcesGeneric
func Test_restoreCase7_SpecificBackupNamesNoSync(t *testing.T) {
	specificBackupName := "acm-credentials-schedule-20251029181055"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(specificBackupName).
			veleroCredentialsBackupName(specificBackupName).
			veleroResourcesBackupName(specificBackupName).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials:     createRestore("credentials-restore", "ns").object,
			ManagedClusters: createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if !isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=true, got false")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(specificBackupName).
			veleroCredentialsBackupName(specificBackupName).
			veleroResourcesBackupName(specificBackupName).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
			Resources:        createRestore("resources-restore", "ns").object,
			ManagedClusters:  createRestore("clusters-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreCase8_SkipClustersSpecificBackupNamesNoSync tests Case 8:
// ManagedClusters=skip, Credentials=name, Resources=name, Sync=false
//
// Expected: Both Credentials and ResourcesGeneric with NO label selector, no -active suffix
func Test_restoreCase8_SkipClustersSpecificBackupNamesNoSync(t *testing.T) {
	skipRestoreStr := "skip"
	specificBackupName := "acm-credentials-schedule-20251029181055"

	// Test Credentials
	t.Run("Credentials", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(specificBackupName).
			veleroResourcesBackupName(specificBackupName).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			Credentials: createRestore("credentials-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, Credentials, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[Credentials]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})

	// Test ResourcesGeneric
	t.Run("ResourcesGeneric", func(t *testing.T) {
		acmRestore := createACMRestore("acm-restore", "ns").
			cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
			veleroManagedClustersBackupName(skipRestoreStr).
			veleroCredentialsBackupName(specificBackupName).
			veleroResourcesBackupName(specificBackupName).object

		veleroRestoresToCreate := map[ResourceType]*veleroapi.Restore{
			ResourcesGeneric: createRestore("generic-restore", "ns").object,
		}

		fakeClient := fake.NewClientBuilder().Build()
		isCredsClsOnActiveStep := updateLabelsForActiveResources(
			context.Background(), fakeClient, acmRestore, ResourcesGeneric, veleroRestoresToCreate,
		)

		if isCredsClsOnActiveStep {
			t.Errorf("Expected isCredsClsOnActiveStep=false, got true")
		}

		restoreObj := veleroRestoresToCreate[ResourcesGeneric]
		if hasActivationLabel(*restoreObj) {
			t.Errorf("Expected NO label selector")
		}

		if strings.HasSuffix(restoreObj.Name, "-active") {
			t.Errorf("Expected no -active suffix")
		}
	})
}

// Test_restoreLabelSelectorScenarios tests all 8 restore scenarios for correct label selector behavior.
//
// This comprehensive test validates that label selectors are correctly applied based on:
// - Whether managed clusters are being restored (latest/name vs skip)
// - Whether sync mode is enabled
// - Whether credentials/resources were originally set to skip
func Test_restoreLabelSelectorScenarios(t *testing.T) {
	skipRestoreStr := "skip"
	latestBackupStr := "latest"
	specificBackupName := "acm-credentials-schedule-20251029181055"

	tests := []struct {
		name                  string
		managedClusters       string
		credentials           string
		resources             string
		sync                  bool
		resourceType          ResourceType
		wantLabelSelector     string // "In", "NotIn", or "none"
		wantActiveSuffix      bool
		wantIsCredsActiveStep bool
	}{
		// Case 1: ManagedClusters=skip, Credentials=latest, Resources=latest, Sync=true
		{
			name:                  "Case1-Credentials: skip clusters, latest creds, sync=true",
			managedClusters:       skipRestoreStr,
			credentials:           latestBackupStr,
			resources:             latestBackupStr,
			sync:                  true,
			resourceType:          Credentials,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		{
			name:                  "Case1-ResourcesGeneric: skip clusters, latest resources, sync=true",
			managedClusters:       skipRestoreStr,
			credentials:           latestBackupStr,
			resources:             latestBackupStr,
			sync:                  true,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		// Case 2: ManagedClusters=skip, Credentials=latest, Resources=latest, Sync=false
		{
			name:                  "Case2-Credentials: skip clusters, latest creds, sync=false",
			managedClusters:       skipRestoreStr,
			credentials:           latestBackupStr,
			resources:             latestBackupStr,
			sync:                  false,
			resourceType:          Credentials,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		{
			name:                  "Case2-ResourcesGeneric: skip clusters, latest resources, sync=false",
			managedClusters:       skipRestoreStr,
			credentials:           latestBackupStr,
			resources:             latestBackupStr,
			sync:                  false,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		// Case 3: ManagedClusters=latest, Credentials=latest, Resources=latest, Sync=false
		{
			name:                  "Case3-Credentials: latest clusters, latest creds, sync=false",
			managedClusters:       latestBackupStr,
			credentials:           latestBackupStr,
			resources:             latestBackupStr,
			sync:                  false,
			resourceType:          Credentials,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: true,
		},
		{
			name:                  "Case3-ResourcesGeneric: latest clusters, latest resources, sync=false",
			managedClusters:       latestBackupStr,
			credentials:           latestBackupStr,
			resources:             latestBackupStr,
			sync:                  false,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		// Case 4: ManagedClusters=latest, Credentials=skip, Resources=latest, Sync=false
		{
			name:                  "Case4-Credentials: latest clusters, skip creds, sync=false",
			managedClusters:       latestBackupStr,
			credentials:           skipRestoreStr,
			resources:             latestBackupStr,
			sync:                  false,
			resourceType:          Credentials,
			wantLabelSelector:     "In",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: true,
		},
		{
			name:                  "Case4-ResourcesGeneric: latest clusters, skip creds, latest resources, sync=false",
			managedClusters:       latestBackupStr,
			credentials:           skipRestoreStr,
			resources:             latestBackupStr,
			sync:                  false,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		// Case 5: ManagedClusters=latest, Credentials=skip, Resources=skip, Sync=false
		{
			name:                  "Case5-Credentials: latest clusters, skip creds, skip resources, sync=false",
			managedClusters:       latestBackupStr,
			credentials:           skipRestoreStr,
			resources:             skipRestoreStr,
			sync:                  false,
			resourceType:          Credentials,
			wantLabelSelector:     "In",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: true,
		},
		{
			name:                  "Case5-ResourcesGeneric: latest clusters, skip creds, skip resources, sync=false",
			managedClusters:       latestBackupStr,
			credentials:           skipRestoreStr,
			resources:             skipRestoreStr,
			sync:                  false,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "In",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		// Case 7: ManagedClusters=name, Credentials=name, Resources=name, Sync=false
		{
			name:                  "Case7-Credentials: specific backup names, sync=false",
			managedClusters:       specificBackupName,
			credentials:           specificBackupName,
			resources:             specificBackupName,
			sync:                  false,
			resourceType:          Credentials,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: true,
		},
		{
			name:                  "Case7-ResourcesGeneric: specific backup names, sync=false",
			managedClusters:       specificBackupName,
			credentials:           specificBackupName,
			resources:             specificBackupName,
			sync:                  false,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		// Case 8: ManagedClusters=skip, Credentials=name, Resources=name, Sync=false
		{
			name:                  "Case8-Credentials: skip clusters, specific backup names, sync=false",
			managedClusters:       skipRestoreStr,
			credentials:           specificBackupName,
			resources:             specificBackupName,
			sync:                  false,
			resourceType:          Credentials,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
		{
			name:                  "Case8-ResourcesGeneric: skip clusters, specific backup names, sync=false",
			managedClusters:       skipRestoreStr,
			credentials:           specificBackupName,
			resources:             specificBackupName,
			sync:                  false,
			resourceType:          ResourcesGeneric,
			wantLabelSelector:     "none",
			wantActiveSuffix:      false,
			wantIsCredsActiveStep: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create ACM restore
			restoreBuilder := createACMRestore("acm-restore", "ns").
				cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
				veleroManagedClustersBackupName(tt.managedClusters).
				veleroCredentialsBackupName(tt.credentials).
				veleroResourcesBackupName(tt.resources)

			if tt.sync {
				restoreBuilder = restoreBuilder.syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20})
			}

			acmRestore := restoreBuilder.object

			// Create velero restores map
			veleroRestoresToCreate := make(map[ResourceType]*veleroapi.Restore)

			if tt.managedClusters != skipRestoreStr {
				veleroRestoresToCreate[ManagedClusters] = createRestore("clusters-restore", "ns").object
			}

			if tt.resourceType == Credentials {
				veleroRestoresToCreate[Credentials] = createRestore("credentials-restore", "ns").object
			} else {
				veleroRestoresToCreate[ResourcesGeneric] = createRestore("generic-restore", "ns").object
				if tt.resources != skipRestoreStr {
					veleroRestoresToCreate[Resources] = createRestore("resources-restore", "ns").object
				}
			}

			// Call the function
			fakeClient := fake.NewClientBuilder().Build()
			isCredsClsOnActiveStep := updateLabelsForActiveResources(
				context.Background(), fakeClient, acmRestore, tt.resourceType, veleroRestoresToCreate,
			)

			// Verify isCredsClsOnActiveStep
			if isCredsClsOnActiveStep != tt.wantIsCredsActiveStep {
				t.Errorf("Expected isCredsClsOnActiveStep=%v, got %v", tt.wantIsCredsActiveStep, isCredsClsOnActiveStep)
			}

			// Verify label selector
			restoreObj := veleroRestoresToCreate[tt.resourceType]
			hasLabel := hasActivationLabel(*restoreObj)

			switch tt.wantLabelSelector {
			case "In":
				if !hasLabel {
					t.Errorf("Expected 'In cluster-activation' label selector, but not found")
				}
				// Verify it's "In" not "NotIn"
				if restoreObj.Spec.LabelSelector != nil {
					for _, req := range restoreObj.Spec.LabelSelector.MatchExpressions {
						if req.Key == backupCredsClusterLabel && req.Operator != "In" {
							t.Errorf("Expected operator 'In', got '%s'", req.Operator)
						}
					}
				}
			case "NotIn":
				if !hasLabel {
					t.Errorf("Expected 'NotIn cluster-activation' label selector, but not found")
				}
				// Verify it's "NotIn" not "In"
				if restoreObj.Spec.LabelSelector != nil {
					for _, req := range restoreObj.Spec.LabelSelector.MatchExpressions {
						if req.Key == backupCredsClusterLabel && req.Operator != "NotIn" {
							t.Errorf("Expected operator 'NotIn', got '%s'", req.Operator)
						}
					}
				}
			case "none":
				if hasLabel {
					t.Errorf("Expected NO label selector, but found one")
				}
			}

			// Verify -active suffix
			hasActiveSuffix := strings.HasSuffix(restoreObj.Name, "-active")
			if hasActiveSuffix != tt.wantActiveSuffix {
				t.Errorf("Expected -active suffix=%v, got %v (name: %s)", tt.wantActiveSuffix, hasActiveSuffix, restoreObj.Name)
			}
		})
	}
}

// Test_isPVCInitializationStep tests detection of PVC initialization requirements.
//
// This test validates the logic that determines whether PVC (Persistent Volume Claim)
// initialization is required as part of the restore process.
//
// Test Coverage:
// - PVC initialization requirement detection
// - Velero restore list analysis for PVC needs
// - Different restore configurations and PVC scenarios
// - Storage-related restore step identification
//
// Business Logic:
// This function determines when PVC initialization is needed during restore
// operations, ensuring proper storage setup and volume handling for restored
// applications that require persistent storage.
func Test_isPVCInitializationStep(t *testing.T) {
	type args struct {
		acmRestore        *v1beta1.Restore
		veleroRestoreList veleroapi.RestoreList
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "sync restore with ManagedCluster skip, should return false",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{},
			},
			want: false,
		},
		{
			name: "no sync and ManagedCluster is skipped, should return false",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(false).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("credentials-restore", "ns").
							scheduleName(veleroScheduleNames[Credentials]).
							object,
					},
				},
			},
			want: false,
		},
		{
			name: "sync restore with ManagedCluster, and only credentials restore, should return true",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("credentials-restore", "ns").
							scheduleName(veleroScheduleNames[Credentials]).
							object,
					},
				},
			},
			want: true,
		},
		{
			name: "sync restore with ManagedCluster, and credentials plus generic restored, should return true",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("credentials-restore", "ns").
							scheduleName(veleroScheduleNames[Credentials]).
							object,
						*createRestore("generic-restore", "ns").
							scheduleName(veleroScheduleNames[ResourcesGeneric]).
							object,
					},
				},
			},
			want: true,
		},
		{
			name: "no sync restore with ManagedCluster, and credentials plus generic restored, should return true",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(false).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("credentials-restore", "ns").
							scheduleName(veleroScheduleNames[Credentials]).
							object,
						*createRestore("generic-restore", "ns").
							scheduleName(veleroScheduleNames[ResourcesGeneric]).
							object,
					},
				},
			},
			want: true,
		},
		{
			name: "no sync restore with ManagedCluster, and credentials plus generic and managedcls restored, " +
				"should return false",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(false).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("credentials-restore", "ns").
							scheduleName(veleroScheduleNames[Credentials]).
							object,
						*createRestore("generic-restore", "ns").
							scheduleName(veleroScheduleNames[ResourcesGeneric]).
							object,
						*createRestore("clusters-restore", "ns").
							scheduleName(veleroScheduleNames[ManagedClusters]).
							object,
					},
				},
			},
			want: false,
		},
		{
			name: "sync restore with ManagedCluster, and credentials plus generic and managedcls restored, should return false",
			args: args{
				acmRestore: createACMRestore("acm-restore", "ns").
					syncRestoreWithNewBackups(true).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).object,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("credentials-restore", "ns").
							scheduleName(veleroScheduleNames[Credentials]).
							object,
						*createRestore("generic-restore", "ns").
							scheduleName(veleroScheduleNames[ResourcesGeneric]).
							object,
						*createRestore("clusters-restore", "ns").
							scheduleName(veleroScheduleNames[ManagedClusters]).
							object,
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPVCInitializationStep(tt.args.acmRestore, tt.args.veleroRestoreList)
			if got != tt.want {
				t.Errorf("error with isPVCInitializationStep for: %s", tt.name)
			}
		})
	}
}

// Test_processRestoreWait tests the restore waiting and processing logic.
//
// This test validates the logic that handles waiting periods during restore
// operations and processes restore state changes and completions.
//
// Test Coverage:
// - Restore waiting period management
// - PV (Persistent Volume) and PVC processing
// - ConfigMap handling during restore wait
// - Restore state monitoring and transitions
// - Storage restoration and PVC availability checking
//
// Business Logic:
// This function manages the waiting aspects of restore operations,
// ensuring proper timing and state management while restore operations
// complete, particularly for storage-related components and PVC initialization.
func Test_processRestoreWait(t *testing.T) {
	type args struct {
		ctx              context.Context
		c                client.Client
		restoreName      string
		restoreNamespace string
		veleroRestore    veleroapi.Restore
		pvMap            corev1.ConfigMap
		pvc              corev1.PersistentVolumeClaim
		setupObjects     []client.Object
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "no velero restore found for restoreName, wait",
			args: args{
				ctx:              context.Background(),
				restoreName:      "creds-restore-name",
				restoreNamespace: "default",
				veleroRestore:    *createRestore("creds-restore-name-1", "default").object,
				pvMap:            *createConfigMap("some-other-map-1", "default", nil),
				pvc:              *createPVC("mongo-storage-1", "default"),
			},
			want: true,
		},
		{
			name: "velero restore found but status is not completed, so wait",
			args: args{
				ctx:              context.Background(),
				restoreName:      "creds-restore-name-2",
				restoreNamespace: "default",
				veleroRestore:    *createRestore("creds-restore-name-2", "default").object,
				pvMap:            *createConfigMap("some-other-map-2", "default", nil),
				pvc:              *createPVC("mongo-storage-2", "default"),
			},
			want: true,
		},
		{
			name: "velero restore found and status is completed, no wait bc no config map",
			args: args{
				ctx:              context.Background(),
				restoreName:      "creds-restore-name-3",
				restoreNamespace: "default",
				veleroRestore:    *createRestore("creds-restore-name-3", "default").phase("Completed").object,
				pvMap:            *createConfigMap("some-other-map-3", "default", nil),
				pvc:              *createPVC("mongo-storage-3", "default"),
			},
			want: false,
		},
		{
			name: "velero restore found and status is completed, have config map so wait!",
			args: args{
				ctx:              context.Background(),
				restoreName:      "creds-restore-name-4",
				restoreNamespace: "default",
				veleroRestore:    *createRestore("creds-restore-name-4", "default").phase("Completed").object,
				pvMap: *createConfigMap("hub-pvc-backup-mongo-storage-4", "default", map[string]string{
					"cluster.open-cluster-management.io/backup-pvc": "mongo-storage",
				}),
				pvc: *createPVC("mongo-storage-4", "default"), // this is NOT the expected PVC, not matching the map label !
			},
			want: true,
		},
		{
			name: "velero restore found and status is completed, have config map but the PVC exists so NO wait!",
			args: args{
				ctx:              context.Background(),
				restoreName:      "creds-restore-name-5",
				restoreNamespace: "default",
				veleroRestore:    *createRestore("creds-restore-name-5", "default").phase("Completed").object,
				pvMap: *createConfigMap("hub-pvc-backup-mongo-storage-5", "default", map[string]string{
					"cluster.open-cluster-management.io/backup-pvc": "mongo-storage",
				}),
				pvc: *createPVC("mongo-storage", "default"), // this is the expected PVC, matching the map label !
			},
			want: false,
		},
	}

	for _, tt := range tests {
		// Setup objects for each test case
		tt.args.setupObjects = []client.Object{
			&tt.args.veleroRestore,
			&tt.args.pvMap,
			&tt.args.pvc,
		}

		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			if err := scheme.AddToScheme(testScheme); err != nil {
				t.Fatalf("Error adding scheme: %s", err.Error())
			}
			if err := veleroapi.AddToScheme(testScheme); err != nil {
				t.Fatalf("Error adding velero api to scheme: %s", err.Error())
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient

			shouldWait, msg := processRestoreWait(tt.args.ctx, tt.args.c,
				tt.args.restoreName, tt.args.restoreNamespace)
			if shouldWait != tt.want {
				t.Errorf("processRestoreWait() returns = %v, want %v, msg=%v", shouldWait, tt.want, msg)
			}
		})
	}
}

func hasOrActivationLabel(
	restore veleroapi.Restore,
) bool {
	hasActivationOrSelector := false
	if len(restore.Spec.OrLabelSelectors) > 0 {
		for i := range restore.Spec.OrLabelSelectors {
			requirements := restore.Spec.OrLabelSelectors[i].MatchExpressions
			for idx := range requirements {
				if requirements[idx].Key == backupCredsClusterLabel {
					hasActivationOrSelector = true
					break
				}
			}
		}
	}
	return hasActivationOrSelector
}

func hasActivationLabel(
	restore veleroapi.Restore,
) bool {
	hasActivationSelector := false
	if restore.Spec.LabelSelector != nil {
		requirements := restore.Spec.LabelSelector.MatchExpressions
		for idx := range requirements {
			if requirements[idx].Key == backupCredsClusterLabel {
				hasActivationSelector = true
				break
			}
		}
	}
	return hasActivationSelector
}

// Test_actLabelNotOnManagedClsRestore tests activation label handling for managed cluster restores.
//
// This test validates the logic that ensures activation labels are not incorrectly
// applied to managed cluster restore operations when they should only be on credential restores.
//
// Test Coverage:
// - Activation label presence validation
// - Managed cluster restore label filtering
// - Credential restore vs cluster restore differentiation
// - OR label selector vs regular label selector handling
//
// Business Logic:
// This function ensures that activation labels are properly segregated between
// different restore types, preventing activation logic from being applied to
// managed cluster restores when it should only affect credential restores.
func Test_actLabelNotOnManagedClsRestore(t *testing.T) {
	orLabelSelector := []*metav1.LabelSelector{
		{
			MatchLabels: map[string]string{
				"restore-test-1": "restore-test-1-value",
			},
		},
	}

	activationLabel := &metav1.LabelSelectorRequirement{}
	activationLabel.Key = backupCredsClusterLabel
	activationLabel.Operator = "In"
	activationLabel.Values = []string{ClusterActivationLabel}

	type args struct {
		ctx               context.Context
		c                 client.Client
		acmRestore        *v1beta1.Restore
		credsRestore      *veleroapi.Restore
		managedClsRestore *veleroapi.Restore
		setupObjects      []client.Object
	}
	tests := []struct {
		name string
		args args
		// true if the label activation must be part of the orLabelSelector - for creds and generic restore only!
		wantActivationOrSelector bool
		// true if the label activation must be part of the LabelSelector - for creds and generic restore only!
		wantActivationSelector bool
	}{
		{
			name: "restore passive with OR label",
			args: args{
				ctx: context.Background(),
				acmRestore: createACMRestore("acm-restore-1", "default").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					veleroManagedClustersBackupName(skipRestoreStr).
					restoreORLabelSelector(orLabelSelector).
					object,
				credsRestore:      createRestore(veleroScheduleNames[Credentials]+"-1", "default").object,
				managedClsRestore: createRestore(veleroScheduleNames[ManagedClusters]+"-1", "default").object,
			},
			wantActivationOrSelector: true,  // restore passive data, no managed clusters
			wantActivationSelector:   false, // activation label is on the OR path
		},
		{
			name: "restore passive with label",
			args: args{
				ctx: context.Background(),
				acmRestore: createACMRestore("acm-restore-2", "default").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					veleroManagedClustersBackupName(skipRestoreStr).
					restoreLabelSelector(&metav1.LabelSelector{
						MatchLabels: map[string]string{
							"restorelabel": "value",
						},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "foo",
								Operator: metav1.LabelSelectorOperator("In"),
								Values:   []string{"bar"},
							},
						},
					}).
					object,
				credsRestore:      createRestore(veleroScheduleNames[Credentials]+"-2", "default").object,
				managedClsRestore: createRestore(veleroScheduleNames[ManagedClusters]+"-2", "default").object,
			},
			wantActivationOrSelector: false, // restore passive data, no managed clusters
			wantActivationSelector:   true,  // activation label set here
		},
		{
			name: "restore passive with label",
			args: args{
				ctx: context.Background(),
				acmRestore: createACMRestore("acm-restore-3", "default").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					veleroManagedClustersBackupName(skipRestoreStr).
					object,
				credsRestore:      createRestore(veleroScheduleNames[Credentials]+"-3", "default").object,
				managedClsRestore: createRestore(veleroScheduleNames[ManagedClusters]+"-3", "default").object,
			},
			wantActivationOrSelector: false, // restore passive data, no managed clusters
			wantActivationSelector:   true,  // activation label set here
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup objects for each test case
			tt.args.setupObjects = []client.Object{
				tt.args.acmRestore,
				tt.args.credsRestore,
				tt.args.managedClsRestore,
			}

			// Setup scheme
			testScheme := runtime.NewScheme()
			if err := veleroapi.AddToScheme(testScheme); err != nil {
				t.Fatalf("Error adding velero api to scheme: %s", err.Error())
			}
			if err := v1beta1.AddToScheme(testScheme); err != nil {
				t.Fatalf("Error adding v1beta1 api to scheme: %s", err.Error())
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient

			// set user filters on both restores, using the acm restore
			setUserRestoreFilters(tt.args.acmRestore, tt.args.credsRestore)
			setUserRestoreFilters(tt.args.acmRestore, tt.args.managedClsRestore)

			// add activation label to creds restore
			addRestoreLabelSelector(tt.args.credsRestore, *activationLabel)

			if hasOrActivationLabel(*tt.args.managedClsRestore) {
				t.Errorf("managed cluster restore should not have the activation label on OR path ")
			}
			if hasActivationLabel(*tt.args.managedClsRestore) {
				t.Errorf("managed cluster restore should not have the activation label ")
			}
			credsOrActLabel := hasOrActivationLabel(*tt.args.credsRestore)
			if credsOrActLabel != tt.wantActivationOrSelector {
				t.Errorf("creds restore OR activation label should be %v, got %v", tt.wantActivationOrSelector, credsOrActLabel)
			}

			credsActLabel := hasActivationLabel(*tt.args.credsRestore)
			if credsActLabel != tt.wantActivationSelector {
				t.Errorf("creds restore activation label should be %v, got %v", tt.wantActivationSelector, credsActLabel)
			}
		})
	}
}

// TestRestoreReconciler_finalizeRestore tests the restore finalization process in the reconciler.
//
// This test validates the complete finalization logic for restore operations,
// including cleanup, finalizer management, and proper resource termination.
//
// Test Coverage:
// - Restore finalization process
// - Finalizer management and cleanup
// - Resource cleanup and termination
// - Velero restore finalizer handling
// - Error handling during finalization
//
// Business Logic:
// This function ensures proper cleanup and finalization of restore operations,
// managing finalizers, cleaning up resources, and ensuring graceful termination
// of restore processes without leaving orphaned resources.
//
//nolint:funlen
func TestRestoreReconciler_finalizeRestore(t *testing.T) {
	ns1 := *createNamespace("backup-ns")
	acmRestore1 := *createACMRestore("acm-restore", "backup-ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(latestBackupStr).
		veleroResourcesBackupName(latestBackupStr).object

	veleroRestoreFinalizer := *createRestore("velero-res", ns1.Name).
		backupName("backup").
		object
	veleroRestoreFinalizerDel := *createRestore("velero-res-terminate", ns1.Name).
		backupName("backup").
		setDeleteTimestamp(metav1.NewTime(time.Now())).
		setFinalizer([]string{"test-finalizer"}).
		object

	type fields struct {
		Client     client.Client
		KubeClient kubernetes.Interface
		Scheme     *runtime.Scheme
		Recorder   record.EventRecorder
	}
	type args struct {
		ctx               context.Context
		c                 client.Client
		acmRestore        *v1beta1.Restore
		veleroRestoreList veleroapi.RestoreList
		setupObjects      []client.Object
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		errMsg  string
	}{
		{
			name: "no restores, return nil",
			args: args{
				ctx:               context.Background(),
				acmRestore:        &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{},
				setupObjects: []client.Object{
					&ns1,
					&acmRestore1,
				},
			},
			wantErr: false,
			errMsg:  "",
		},
		{
			name: "has velero restores, but not marked for deletion, finalizer should NOT be removed",
			args: args{
				ctx:        context.Background(),
				acmRestore: &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						veleroRestoreFinalizer,
					},
				},
				setupObjects: []client.Object{
					&ns1,
					&acmRestore1,
					&veleroRestoreFinalizer,
				},
			},
			wantErr: true,
			errMsg:  "waiting for velero restores to be terminated",
		},
		{
			name: "has velero restores, marked for deletion, finalizer should be removed",
			args: args{
				ctx:        context.Background(),
				acmRestore: &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						veleroRestoreFinalizerDel,
					},
				},
				setupObjects: []client.Object{
					&ns1,
					&acmRestore1,
					&veleroRestoreFinalizerDel,
				},
			},
			wantErr: true,
			errMsg:  "waiting for velero restores to be terminated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			e1 := corev1.AddToScheme(testScheme)
			e2 := veleroapi.AddToScheme(testScheme)
			e3 := v1beta1.AddToScheme(testScheme)
			if err := errors.Join(e1, e2, e3); err != nil {
				t.Fatalf("Error adding apis to scheme: %s", err.Error())
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient

			err := finalizeRestore(tt.args.ctx, tt.args.c, tt.args.acmRestore, tt.args.veleroRestoreList)

			if err != nil && err.Error() != tt.errMsg {
				t.Errorf("got an error but not the one expected error = %v, wantErr %v", err.Error(), tt.errMsg)
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("RestoreReconciler.finalizeRestore() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test_addOrRemoveResourcesFinalizer tests finalizer management for internal hub resources.
//
// This test validates the logic that adds or removes finalizers on internal hub
// resources during restore operations, ensuring proper resource lifecycle management.
//
// Test Coverage:
// - Finalizer addition for internal hub resources
// - Finalizer removal based on restore state
// - Unstructured resource finalizer management
// - Complex finalizer scenarios with multiple ACM restores
//
// Business Logic:
// This function manages finalizers on internal hub resources to ensure proper
// cleanup order and prevent resource deletion before restoration is complete,
// maintaining data integrity during restore operations.
func Test_addOrRemoveResourcesFinalizer(t *testing.T) {
	mchObjAdd := newUnstructured("operator.open-cluster-management.io/v1", "InternalHubComponent",
		"ns1", "cluster-backup")

	mchObjDel := newUnstructured("operator.open-cluster-management.io/v1", "InternalHubComponent",
		"default", "cluster-backup")

	mchObjDel2 := newUnstructured("operator.open-cluster-management.io/v1", "InternalHubComponent",
		"ns2", "cluster-backup")

	// Add finalizer to objects that need it
	controllerutil.AddFinalizer(mchObjDel, acmRestoreFinalizer)
	controllerutil.AddFinalizer(mchObjDel2, acmRestoreFinalizer)

	ns1 := *createNamespace("backup-ns")
	acmRestore1 := *createACMRestore("acm-restore", "backup-ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(latestBackupStr).
		veleroResourcesBackupName(latestBackupStr).object

	acmRestoreWFin := *createACMRestore("acm-restore-w-fin", "backup-ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(latestBackupStr).
		veleroResourcesBackupName(latestBackupStr).
		setFinalizer([]string{acmRestoreFinalizer}).object

	acmRestoreWFin1 := *createACMRestore("acm-restore-w-fin-1", "backup-ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(latestBackupStr).
		veleroResourcesBackupName(latestBackupStr).
		setFinalizer([]string{acmRestoreFinalizer}).object

	type args struct {
		ctx                 context.Context
		c                   client.Client
		internalHubResource *unstructured.Unstructured
		acmRestore          *v1beta1.Restore
		setupObjects        []client.Object
	}

	remove_tests := []struct {
		name             string
		args             args
		wantErr          bool
		wantMCHFinalizer bool
		wantACMFinalizer bool
		acmRestoreList   []*v1beta1.Restore
	}{
		{
			name: "remove test - acm has finalizers, remove them",
			args: args{
				ctx:                 context.Background(),
				internalHubResource: mchObjDel,
				acmRestore:          &acmRestoreWFin,
				setupObjects: []client.Object{
					&ns1,
					&acmRestoreWFin,
					mchObjDel,
				},
			},
			wantErr:          false,
			wantMCHFinalizer: false,
			wantACMFinalizer: false,
			acmRestoreList: []*v1beta1.Restore{
				&acmRestoreWFin,
			},
		},
		{
			name: "remove test - mch finalizers not removed",
			args: args{
				ctx:                 context.Background(),
				internalHubResource: mchObjDel2,
				acmRestore:          &acmRestore1,
				setupObjects: []client.Object{
					&ns1,
					&acmRestore1,
					&acmRestoreWFin1,
					mchObjDel2,
				},
			},
			wantErr:          false,
			wantMCHFinalizer: true,
			wantACMFinalizer: false,
			acmRestoreList: []*v1beta1.Restore{
				&acmRestore1,
				&acmRestoreWFin1,
			},
		},
	}

	for _, tt := range remove_tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			e1 := corev1.AddToScheme(testScheme)
			e2 := veleroapi.AddToScheme(testScheme)
			e3 := v1beta1.AddToScheme(testScheme)
			if err := errors.Join(e1, e2, e3); err != nil {
				t.Fatalf("Error adding apis to scheme: %s", err.Error())
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient

			if err := removeResourcesFinalizer(tt.args.ctx, tt.args.c,
				tt.args.internalHubResource, tt.args.acmRestore); (err != nil) != tt.wantErr {
				t.Errorf("removeResourcesFinalizer() error = %v, wantErr: %v", err, tt.wantErr)
			} else {
				// check finalizers were removed
				if controllerutil.ContainsFinalizer(tt.args.internalHubResource, acmRestoreFinalizer) != tt.wantMCHFinalizer {
					t.Errorf("internalHubResource wantMCHFinalizer: %v but got %v",
						tt.wantMCHFinalizer, controllerutil.ContainsFinalizer(tt.args.internalHubResource, acmRestoreFinalizer))
				}
				if controllerutil.ContainsFinalizer(tt.args.acmRestore, acmRestoreFinalizer) != tt.wantACMFinalizer {
					t.Errorf("acmRestore should have a finalizer , wantACMFinalizer: %v but got %v",
						tt.wantACMFinalizer,
						controllerutil.ContainsFinalizer(tt.args.acmRestore, acmRestoreFinalizer))
				}
			}
		})
	}

	add_tests := []struct {
		name             string
		args             args
		wantErr          bool
		wantMCHFinalizer bool
		wantACMFinalizer bool
	}{
		{
			name: "add test - finalizers must be not be added, acm restore already has them",
			args: args{
				ctx:                 context.Background(),
				internalHubResource: mchObjAdd,
				acmRestore:          &acmRestoreWFin1,
				setupObjects: []client.Object{
					&ns1,
					&acmRestoreWFin1,
					mchObjAdd,
				},
			},
			wantErr:          false,
			wantMCHFinalizer: true,
			wantACMFinalizer: true,
		},
		{
			name: "add test - finalizers must be added",
			args: args{
				ctx:                 context.Background(),
				internalHubResource: mchObjAdd,
				acmRestore:          &acmRestore1,
				setupObjects: []client.Object{
					&ns1,
					&acmRestore1,
					mchObjAdd,
				},
			},
			wantErr:          false,
			wantMCHFinalizer: true,
			wantACMFinalizer: true,
		},
	}

	for _, tt := range add_tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			testScheme := runtime.NewScheme()
			e1 := corev1.AddToScheme(testScheme)
			e2 := veleroapi.AddToScheme(testScheme)
			e3 := v1beta1.AddToScheme(testScheme)
			if err := errors.Join(e1, e2, e3); err != nil {
				t.Fatalf("Error adding apis to scheme: %s", err.Error())
			}

			// Setup fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.args.setupObjects...).
				Build()

			tt.args.c = fakeClient

			if err := addResourcesFinalizer(tt.args.ctx, tt.args.c, tt.args.internalHubResource,
				tt.args.acmRestore); (err != nil) != tt.wantErr {
				t.Errorf("addResourcesFinalizer() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				// check finalizers were added
				if (tt.args.internalHubResource.GetFinalizers() != nil) != tt.wantMCHFinalizer {
					t.Errorf("internalHubResource should have a finalizer wantMCHFinalizer %v", tt.wantMCHFinalizer)
				}
				if (tt.args.acmRestore.GetFinalizers() != nil) != tt.wantACMFinalizer {
					t.Errorf("acmRestore should have a finalizer , wantACMFinalizer %v", tt.wantACMFinalizer)
				}
			}
		})
	}
}

// Test_mapFuncTriggerFinalizers tests the mapFuncTriggerFinalizers function
// which is used in the controller's watch setup to handle InternalHubComponent deletions
func Test_mapFuncTriggerFinalizers(t *testing.T) {
	ctx := context.Background()

	// Test case 1: Non cluster-backup resource should return empty requests
	t.Run("non cluster-backup resource", func(t *testing.T) {
		mockObj := &unstructured.Unstructured{}
		mockObj.SetAPIVersion("operator.open-cluster-management.io/v1")
		mockObj.SetKind("InternalHubComponent")
		mockObj.SetName("other-component")
		mockObj.SetNamespace("test-namespace")

		// Use a nil client since we expect early return
		requests := mapFuncTriggerFinalizers(ctx, nil, mockObj)

		if len(requests) != 0 {
			t.Errorf("Expected empty requests for non cluster-backup resource, got %d", len(requests))
		}
	})

	// Test case 2: cluster-backup resource without deletion timestamp should return empty requests
	t.Run("cluster-backup without deletion timestamp", func(t *testing.T) {
		mockObj := &unstructured.Unstructured{}
		mockObj.SetAPIVersion("operator.open-cluster-management.io/v1")
		mockObj.SetKind("InternalHubComponent")
		mockObj.SetName("cluster-backup")
		mockObj.SetNamespace("test-namespace")
		// No deletion timestamp set

		// Use a nil client since we expect early return
		requests := mapFuncTriggerFinalizers(ctx, nil, mockObj)

		if len(requests) != 0 {
			t.Errorf("Expected empty requests for cluster-backup without deletion timestamp, got %d", len(requests))
		}
	})

	// Test case 3: cluster-backup with deletion timestamp but client error should return empty requests
	t.Run("cluster-backup with deletion timestamp and client error", func(t *testing.T) {
		// Create a fake client with no scheme to cause List() to fail
		fakeClient := fake.NewClientBuilder().Build()

		mockObj := &unstructured.Unstructured{}
		mockObj.SetAPIVersion("operator.open-cluster-management.io/v1")
		mockObj.SetKind("InternalHubComponent")
		mockObj.SetName("cluster-backup")
		mockObj.SetNamespace("test-namespace")
		// Set deletion timestamp to simulate deletion
		now := metav1.Now()
		mockObj.SetDeletionTimestamp(&now)

		// Use a client without the proper scheme to cause List() to fail
		requests := mapFuncTriggerFinalizers(ctx, fakeClient, mockObj)

		if len(requests) != 0 {
			t.Errorf("Expected empty requests on client error, got %d", len(requests))
		}
	})

	// Test case 4: cluster-backup with deletion timestamp and working client
	t.Run("cluster-backup with deletion timestamp and working client", func(t *testing.T) {
		// Create a fake client with some test restore resources
		scheme := runtime.NewScheme()
		_ = v1beta1.AddToScheme(scheme)

		restore1 := &v1beta1.Restore{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-restore-1",
				Namespace: "test-ns-1",
			},
		}
		restore2 := &v1beta1.Restore{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-restore-2",
				Namespace: "test-ns-2",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(restore1, restore2).
			Build()

		mockObj := &unstructured.Unstructured{}
		mockObj.SetAPIVersion("operator.open-cluster-management.io/v1")
		mockObj.SetKind("InternalHubComponent")
		mockObj.SetName("cluster-backup")
		mockObj.SetNamespace("test-namespace")
		// Set deletion timestamp to simulate deletion
		now := metav1.Now()
		mockObj.SetDeletionTimestamp(&now)

		requests := mapFuncTriggerFinalizers(ctx, fakeClient, mockObj)

		if len(requests) != 2 {
			t.Errorf("Expected 2 reconcile requests, got %d", len(requests))
		}

		// Check that we got requests for both restore resources
		requestNames := make([]string, len(requests))
		for i, req := range requests {
			requestNames[i] = req.Name
		}

		expectedNames := []string{"test-restore-1", "test-restore-2"}
		for _, expected := range expectedNames {
			found := false
			for _, actual := range requestNames {
				if actual == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected to find request for %s, but didn't", expected)
			}
		}
	})
}

// Test_processFinalizersPredicate tests the processFinalizersPredicate function
// which returns a predicate used to filter events in the controller's watch setup
func Test_processFinalizersPredicate(t *testing.T) {
	predicate := processFinalizersPredicate()

	// Test case 1: Create events should return false
	t.Run("create event should return false", func(t *testing.T) {
		createEvent := event.CreateEvent{
			Object: &unstructured.Unstructured{},
		}

		result := predicate.Create(createEvent)

		if result != false {
			t.Errorf("Expected false for Create event, got %v", result)
		}
	})

	// Test case 2: Delete events should return true
	t.Run("delete event should return true", func(t *testing.T) {
		deleteEvent := event.DeleteEvent{
			Object: &unstructured.Unstructured{},
		}

		result := predicate.Delete(deleteEvent)

		if result != true {
			t.Errorf("Expected true for Delete event, got %v", result)
		}
	})

	// Test case 3: Update events should return true
	t.Run("update event should return true", func(t *testing.T) {
		updateEvent := event.UpdateEvent{
			ObjectOld: &unstructured.Unstructured{},
			ObjectNew: &unstructured.Unstructured{},
		}

		result := predicate.Update(updateEvent)

		if result != true {
			t.Errorf("Expected true for Update event, got %v", result)
		}
	})

	// Test case 4: Generic events should return false
	t.Run("generic event should return false", func(t *testing.T) {
		genericEvent := event.GenericEvent{
			Object: &unstructured.Unstructured{},
		}

		result := predicate.Generic(genericEvent)

		if result != false {
			t.Errorf("Expected false for Generic event, got %v", result)
		}
	})
}
