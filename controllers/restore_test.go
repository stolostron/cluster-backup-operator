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
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
			wantResName: "credentials-restore-active",
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
			got := updateLabelsForActiveResources(tt.args.acmRestore, tt.args.restype, tt.args.veleroRestoresToCreate)
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

func Test_processRestoreWait(t *testing.T) {
	logf.SetLogger(klog.NewKlogr())

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

func Test_actLabelNotOnManagedClsRestore(t *testing.T) {
	logf.SetLogger(klog.NewKlogr())

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

//nolint:funlen
func TestRestoreReconciler_finalizeRestore(t *testing.T) {
	logf.SetLogger(klog.NewKlogr())

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

func Test_addOrRemoveResourcesFinalizer(t *testing.T) {
	logf.SetLogger(klog.NewKlogr())

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
