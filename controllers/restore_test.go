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
	"path/filepath"
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

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

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})

	type args struct {
		ctx              context.Context
		c                client.Client
		restoreNamespace string
		resourceType     ResourceType
		backupName       string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "no kind is registered for the type v1.BackupList",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: "",
		},
		{
			name: "no backup items",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: "",
		},
		{
			name: "found backup item but time is not matching",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       "acm-credentials-schedule-20220822170041",
				restoreNamespace: veleroNamespaceName,
			},
			want: "",
		},
		{
			name: "found backup item for credentials",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     Credentials,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: backup.Name,
		},
		{
			name: "NOT found backup item for credentials hive",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsHive,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: "",
		},
		{
			name: "found backup item for credentials cluster NOT found no exact match on timestamp and not within 30s",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: "",
		},
		{
			name: "found backup item for credentials cluster when exact match on timestamp",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: backupClsExactTime.Name,
		},
		{
			name: "found backup item for credentials cluster when NOT exact match on timestamp but withn 30s",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: backupClsExactWithin30s.Name,
		},
	}

	for index, tt := range tests {

		if index == 1 {
			if err := veleroapi.AddToScheme(scheme.Scheme); err != nil {
				t.Errorf("Error adding api to scheme: %s", err.Error())
			}
		}
		if index == 2 {
			err := k8sClient1.Create(context.Background(), &veleroNamespace)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
			err = k8sClient1.Create(context.Background(), &backup)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
			err = k8sClient1.Create(context.Background(), &backupClsNoMatch)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		if index == len(tests)-2 {
			err := k8sClient1.Create(context.Background(), &backupClsExactTime)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		if index == len(tests)-1 {
			err := k8sClient1.Create(context.Background(), &backupClsExactWithin30s)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
			err = k8sClient1.Delete(context.Background(), &backupClsExactTime)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		t.Run(tt.name, func(t *testing.T) {
			veleroBackups := &veleroapi.BackupList{}
			if index > 0 { // First test intentionally doesn't have velero in client scheme
				err := tt.args.c.List(tt.args.ctx, veleroBackups, client.InNamespace(veleroNamespace.Name))
				if err != nil {
					t.Errorf("Error listing veleroBackups: %s", err.Error())
				}
			}
			if name, _, _ := getVeleroBackupName(tt.args.ctx, tt.args.c,
				tt.args.restoreNamespace, tt.args.resourceType, tt.args.backupName, veleroBackups); name != tt.want {
				t.Errorf("getVeleroBackupName() returns = %v, want %v", name, tt.want)
			}
		})
	}
	// clean up
	if err := testEnv.Stop(); err != nil {
		t.Errorf("Error stopping testenv: %s", err.Error())
	}
}

func Test_isNewBackupAvailable(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	scheme1 := runtime.NewScheme()
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}

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

	type args struct {
		ctx          context.Context
		c            client.Client
		restore      *v1beta1.Restore
		resourceType ResourceType
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "no kind is registered for the type v1.BackupList",
			args: args{
				ctx:          context.Background(),
				c:            k8sClient1,
				restore:      &restoreCreds,
				resourceType: CredentialsCluster,
			},
			want: false,
		},
		{
			name: "no backup items",
			args: args{
				ctx:          context.Background(),
				c:            k8sClient1,
				resourceType: CredentialsCluster,
				restore:      &restoreCreds,
			},
			want: false,
		},

		{
			name: "NOT found restore item ",
			args: args{
				ctx:          context.Background(),
				c:            k8sClient1,
				resourceType: CredentialsCluster,
				restore:      &restoreCreds,
			},
			want: false,
		},
		{
			name: "found restore item but not the latest backup",
			args: args{
				ctx:          context.Background(),
				c:            k8sClient1,
				resourceType: Credentials,
				restore:      &restoreCredSameBackup,
			},
			want: false,
		},
		{
			name: "found restore item AND new backup",
			args: args{
				ctx:          context.Background(),
				c:            k8sClient1,
				resourceType: Credentials,
				restore:      &restoreCredNewBackup,
			},
			want: true,
		},
		{
			name: "found restore item AND new backup, with restore found",
			args: args{
				ctx:          context.Background(),
				c:            k8sClient1,
				resourceType: Credentials,
				restore:      &restoreCredNewBackup,
			},
			want: false,
		},
	}

	for index, tt := range tests {

		if index == 1 {
			err := corev1.AddToScheme(scheme1)
			if err != nil {
				t.Errorf("Error adding api to scheme: %s", err.Error())
			}
			err = v1beta1.AddToScheme(scheme1)
			if err != nil {
				t.Errorf("Error adding api to scheme: %s", err.Error())
			}
			err = veleroapi.AddToScheme(scheme1)
			if err != nil {
				t.Errorf("Error adding api to scheme: %s", err.Error())
			}
		}
		if index == 2 {
			err := k8sClient1.Create(tt.args.ctx, &veleroNamespace)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
			err = k8sClient1.Create(tt.args.ctx, &backup)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
			err = k8sClient1.Create(context.Background(), &veleroRestore)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		if index == len(tests)-1 {
			// create restore
			err := k8sClient1.Create(tt.args.ctx, &restoreCredNewBackup)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		t.Run(tt.name, func(t *testing.T) {
			if got := isNewBackupAvailable(tt.args.ctx, tt.args.c,
				tt.args.restore, tt.args.resourceType); got != tt.want {
				t.Errorf("isNewBackupAvailable() returns = %v, want %v, %v", got, tt.want, tt.args.resourceType)
			}
		})
	}

	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
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
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	skipRestore := "skip"
	veleroNamespaceName := "default"
	invalidBackupName := ""
	backupName := "backup-name"

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

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	scheme1 := runtime.NewScheme()
	err = veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}
	err = k8sClient1.Create(context.Background(), &backup, &client.CreateOptions{})
	if err != nil {
		t.Fatalf("Error creating backup: %s", err.Error())
	}

	type args struct {
		ctx                        context.Context
		c                          client.Client
		s                          *runtime.Scheme
		restore                    *v1beta1.Restore
		restoreOnlyManagedClusters bool
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
				c:                          k8sClient1,
				s:                          scheme1,
				restore:                    &restoreCredsNoError,
				restoreOnlyManagedClusters: false,
			},
			want: false, // has error, restore not found
		},

		{
			name: "retrieveRestoreDetails has error, no backup name",
			args: args{
				ctx:                        context.Background(),
				c:                          k8sClient1,
				s:                          scheme1,
				restore:                    &restoreCredsInvalidBackupName,
				restoreOnlyManagedClusters: false,
			},
			want: false, // has error, backup name is invalid
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, _, got := retrieveRestoreDetails(tt.args.ctx, tt.args.c,
				tt.args.s, tt.args.restore, tt.args.restoreOnlyManagedClusters)
			if (got == nil) != tt.want {
				t.Errorf("retrieveRestoreDetails() returns = %v, want %v", got == nil, tt.want)
			}
			if keys[0] != Credentials {
				t.Errorf("retrieveRestoreDetails() error, Credentials should be first key to restore ")
			}
			if keys[len(keys)-1] != ManagedClusters {
				t.Errorf("retrieveRestoreDetails() error, ManagedClusters should be last key for restore ")
			}
		})
		if index == len(tests)-1 {
			// clean up
			if err := testEnv.Stop(); err != nil {
				t.Errorf("Error stopping testenv: %s", err.Error())
			}
		}
	}
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}
}

func Test_isOtherResourcesRunning(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

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

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	scheme1 := runtime.NewScheme()
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}

	type args struct {
		ctx     context.Context
		c       client.Client
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "isOtherResourcesRunning has error, CRD not installed",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				restore: &restore,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has no errors, no backups found",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				restore: &restore,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has no errors, backup found but in collision state",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				restore: &restore,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has errors, backup found and not in collision state",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				restore: &restore,
			},
			want: "This resource is ignored because BackupSchedule resource backup-name-failed is currently active, " +
				"before creating another resource verify that any active resources are removed.",
		},
		{
			name: "isOtherResourcesRunning has no errors, no another restore is running",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				restore: &restore,
			},
			want: "",
		},
		{
			name: "isOtherResourcesRunning has errors, another restore is running",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				restore: &restore,
			},
			want: "This resource is ignored because Restore resource other-restore-backup-name is currently active, " +
				"before creating another resource verify that any active resources are removed.",
		},
	}

	for index, tt := range tests {

		if index == 1 {
			// create CRD
			if err := v1beta1.AddToScheme(scheme1); err != nil {
				t.Errorf("Error adding api to scheme: %s", err.Error())
			}
			if err := k8sClient1.Create(tt.args.ctx, &restore, &client.CreateOptions{}); err != nil {
				t.Errorf("Error creating restore: %s", err.Error())
			}

		}
		if index == 2 {
			// create backup in collision state
			err := k8sClient1.Create(tt.args.ctx, &backupCollision, &client.CreateOptions{})
			if err != nil {
				t.Errorf("Error creating backupschedule: %s", err.Error())
			}
			err = k8sClient1.Get(tt.args.ctx, types.NamespacedName{
				Name:      backupCollision.Name,
				Namespace: backupCollision.Namespace,
			}, &backupCollision)
			if err != nil {
				t.Errorf("Error getting backupschedule: %s", err.Error())
			}
			backupCollision.Status.Phase = v1beta1.SchedulePhaseBackupCollision
			err = k8sClient1.Status().Update(tt.args.ctx, &backupCollision)
			if err != nil {
				t.Errorf("Error updating backupschedule: %s", err.Error())
			}
		}
		if index == 3 {
			err := k8sClient1.Create(tt.args.ctx, &backupFailed)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		if index == len(tests)-2 {
			err := k8sClient1.Delete(tt.args.ctx, &backupFailed)
			if err != nil {
				t.Errorf("Error deleting: %s", err.Error())
			}
		}
		if index == len(tests)-1 {
			err := k8sClient1.Create(tt.args.ctx, &restoreOther)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}

		t.Run(tt.name, func(t *testing.T) {
			if got, _ := isOtherResourcesRunning(tt.args.ctx, tt.args.c,
				tt.args.restore); got != tt.want {
				t.Errorf("isOtherResourcesRunning() returns = %v, want %v", got, tt.want)
			}
		})
	}
	// clean up
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
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
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}

	type args struct {
		ctx              context.Context
		c                client.Client
		restoreName      string
		restoreNamespace string
		veleroRestore    veleroapi.Restore
		pvMap            corev1.ConfigMap
		pvc              corev1.PersistentVolumeClaim
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
				c:                k8sClient1,
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
				c:                k8sClient1,
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
				c:                k8sClient1,
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
				c:                k8sClient1,
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
				c:                k8sClient1,
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

		if err := k8sClient1.Create(context.Background(), &tt.args.veleroRestore); err != nil {
			t.Errorf("cannot create resource %v", err.Error())
		}

		if err := k8sClient1.Create(context.Background(), &tt.args.pvMap); err != nil {
			t.Errorf("cannot create resource %v", err.Error())
		}

		if err := k8sClient1.Create(context.Background(), &tt.args.pvc); err != nil {
			t.Errorf("cannot create resource %v", err.Error())
		}

		t.Run(tt.name, func(t *testing.T) {
			if shouldWait, msg := processRestoreWait(tt.args.ctx, tt.args.c,
				tt.args.restoreName, tt.args.restoreNamespace); shouldWait != tt.want {
				t.Errorf("processRestoreWait() returns = %v, want %v, msg=%v", shouldWait, tt.want, msg)
			}
		})
	}
	// clean up
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
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
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	scheme1 := runtime.NewScheme()
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}
	err = veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	err = v1beta1.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

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
				c:   k8sClient1,
				acmRestore: createACMRestore("acm-restore", "default").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					veleroManagedClustersBackupName(skipRestoreStr).
					restoreORLabelSelector(orLabelSelector).
					object,
				credsRestore:      createRestore(veleroScheduleNames[Credentials], "default").object,
				managedClsRestore: createRestore(veleroScheduleNames[ManagedClusters], "default").object,
			},
			wantActivationOrSelector: true,  // restore passive data, no managed clusters
			wantActivationSelector:   false, // activation label is on the OR path
		},
		{
			name: "restore passive with label",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				acmRestore: createACMRestore("acm-restore", "default").
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
				credsRestore:      createRestore(veleroScheduleNames[Credentials], "default").object,
				managedClsRestore: createRestore(veleroScheduleNames[ManagedClusters], "default").object,
			},
			wantActivationOrSelector: false, // restore passive data, no managed clusters
			wantActivationSelector:   true,  // activation label set here
		},
		{
			name: "restore passive with label",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				acmRestore: createACMRestore("acm-restore", "default").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					veleroManagedClustersBackupName(skipRestoreStr).
					object,
				credsRestore:      createRestore(veleroScheduleNames[Credentials], "default").object,
				managedClsRestore: createRestore(veleroScheduleNames[ManagedClusters], "default").object,
			},
			wantActivationOrSelector: false, // restore passive data, no managed clusters
			wantActivationSelector:   true,  // activation label set here
		},
	}

	for _, tt := range tests {

		if err := k8sClient1.Create(context.Background(), tt.args.acmRestore); err != nil {
			t.Errorf("cannot create resource %v", err.Error())
		}

		if err := k8sClient1.Create(context.Background(), tt.args.credsRestore); err != nil {
			t.Errorf("cannot create resource %v", err.Error())
		}
		if err := k8sClient1.Create(context.Background(), tt.args.managedClsRestore); err != nil {
			t.Errorf("cannot create resource %v", err.Error())
		}

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

		if err := k8sClient1.Delete(context.Background(), tt.args.acmRestore); err != nil {
			t.Errorf("cannot delete resource %v", err.Error())
		}

		if err := k8sClient1.Delete(context.Background(), tt.args.credsRestore); err != nil {
			t.Errorf("cannot delete resource %v", err.Error())
		}
		if err := k8sClient1.Delete(context.Background(), tt.args.managedClsRestore); err != nil {
			t.Errorf("cannot delete resource %v", err.Error())
		}
	}
	// clean up
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}
}

//nolint:funlen
func TestRestoreReconciler_finalizeRestore(t *testing.T) {
	logf.SetLogger(klog.NewKlogr())
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	ns1 := *createNamespace("backup-ns")
	acmRestore1 := *createACMRestore("acm-restore", "backup-ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(latestBackupStr).
		veleroResourcesBackupName(latestBackupStr).object

	veleroRestoreFinalizer := *createRestore("velero-res", ns1.Name).
		backupName("backup").
		setFinalizer([]string{restoreFinalizer}).
		object
	veleroRestoreFinalizerDel := *createRestore("velero-res-terminate", ns1.Name).
		backupName("backup").
		setFinalizer([]string{restoreFinalizer}).
		setDeleteTimestamp(metav1.NewTime(time.Now())).
		object

	scheme1 := runtime.NewScheme()
	e1 := corev1.AddToScheme(scheme1)
	e2 := veleroapi.AddToScheme(scheme1)
	e3 := v1beta1.AddToScheme(scheme1)
	if err := errors.Join(e1, e2, e3); err != nil {
		t.Fatalf("Error adding apis to scheme: %s", err.Error())
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}
	errs := []error{}
	errs = append(errs, k8sClient1.Create(context.Background(), &ns1))
	errs = append(errs, k8sClient1.Create(context.Background(), &acmRestore1))
	errs = append(errs, k8sClient1.Create(context.Background(), &veleroRestoreFinalizer))
	errs = append(errs, k8sClient1.Create(context.Background(), &veleroRestoreFinalizerDel))
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Error creating objects for test setup: %s", err.Error())
	}

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
				c:                 k8sClient1,
				acmRestore:        &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{},
			},
			wantErr: false,
			errMsg:  "",
		},
		{
			name: "has velero restores, but not marked for deletion, finalizer should NOT be removed",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				acmRestore: &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						veleroRestoreFinalizer,
					},
				},
			},
			wantErr: true,
			errMsg:  "waiting for velero restores to be terminated",
		},
		{
			name: "has velero restores, marked for deletion, but resource not found",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				acmRestore: &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("velero-1", ns1.Name).
							backupName("backup").
							setFinalizer([]string{restoreFinalizer}).
							setDeleteTimestamp(metav1.NewTime(time.Now())).
							object,
					},
				},
			},
			wantErr: true,
			errMsg:  `restores.velero.io "velero-1" not found`,
		},
		{
			name: "has velero restores, not marked for deletion and resource not found, delete must fail",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				acmRestore: &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						*createRestore("velero-1", ns1.Name).
							backupName("backup").
							setFinalizer([]string{restoreFinalizer}).
							object,
					},
				},
			},
			wantErr: true,
			errMsg:  `restores.velero.io "velero-1" not found`,
		},
		{
			name: "has velero restores, marked for deletion, finalizer should be removed",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				acmRestore: &acmRestore1,
				veleroRestoreList: veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						veleroRestoreFinalizerDel,
					},
				},
			},
			wantErr: true,
			errMsg:  "waiting for velero restores to be terminated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			err := finalizeRestore(tt.args.ctx, tt.args.c, tt.args.acmRestore, tt.args.veleroRestoreList)

			if err != nil && err.Error() != tt.errMsg {
				t.Errorf("got an error but not the one expected error = %v, wantErr %v", err.Error(), tt.errMsg)
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("RestoreReconciler.finalizeRestore() error = %v, wantErr %v", err, tt.wantErr)
			}

			for _, veleroRestore := range tt.args.veleroRestoreList.Items {
				if controllerutil.ContainsFinalizer(&veleroRestore, restoreFinalizer) &&
					veleroRestore.GetDeletionTimestamp() != nil &&
					veleroRestore.Name != "velero-1" {
					t.Errorf("Velero restore marked for deletion but finalizer not removed name=%v",
						veleroRestore.Name)

				}
			}
		})
	}
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}
}

func Test_addOrRemoveResourcesFinalizer(t *testing.T) {

	logf.SetLogger(klog.NewKlogr())

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	mchObjAdd := newUnstructured("operator.open-cluster-management.io/v1", "InternalHubComponent",
		"ns1", "cluster-backup")

	mchObjDel := newUnstructured("operator.open-cluster-management.io/v1", "InternalHubComponent",
		"default", "cluster-backup")

	mchGVRList := schema.GroupVersionResource{Group: "operator.open-cluster-management.io",
		Version: "v1", Resource: "internalhubcomponents"}

	gvrToListKindR := map[schema.GroupVersionResource]string{
		mchGVRList: "InternalHubComponentList",
	}

	unstructuredScheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(unstructuredScheme,
		gvrToListKindR,
	)
	resInterface := dynClient.Resource(mchGVRList)

	if _, err := dynClient.Resource(mchGVRList).Namespace("default").Create(context.Background(),
		mchObjDel, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Err creating: %s", err.Error())
	}
	if _, err := dynClient.Resource(mchGVRList).Namespace("ns1").Create(context.Background(),
		mchObjAdd, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Err creating: %s", err.Error())
	}

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

	acmRestoreWDelFin := *createACMRestore("acm-restore-w-del-fin", "backup-ns").
		cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(latestBackupStr).
		veleroResourcesBackupName(latestBackupStr).
		setFinalizer([]string{acmRestoreFinalizer}).
		setDeleteTimestamp(metav1.NewTime(time.Now())).object

	scheme1 := runtime.NewScheme()
	e1 := corev1.AddToScheme(scheme1)
	e2 := veleroapi.AddToScheme(scheme1)
	e3 := v1beta1.AddToScheme(scheme1)
	if err := errors.Join(e1, e2, e3); err != nil {
		t.Fatalf("Error adding apis to scheme: %s", err.Error())
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}
	errs := []error{}
	errs = append(errs, k8sClient1.Create(context.Background(), &ns1))
	errs = append(errs, k8sClient1.Create(context.Background(), &acmRestore1))
	errs = append(errs, k8sClient1.Create(context.Background(), &acmRestoreWFin))

	if err := errors.Join(errs...); err != nil {
		t.Errorf("Error creating objs to setup for test: %s", err.Error())
	}

	type args struct {
		ctx                 context.Context
		c                   client.Client
		internalHubResource unstructured.Unstructured
		dr                  dynamic.NamespaceableResourceInterface
		acmRestore          *v1beta1.Restore
	}

	remove_tests := []struct {
		name             string
		args             args
		wantErr          bool
		wantMCHFinalizer bool
		wantACMFinalizer bool
	}{
		{
			name: "remove test - finalizers",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				internalHubResource: *mchObjDel,
				dr:                  resInterface,
				acmRestore:          &acmRestore1,
			},
			wantErr:          false,
			wantMCHFinalizer: false,
			wantACMFinalizer: false,
		},
		{
			name: "remove test - acm has finalizers",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				internalHubResource: *mchObjDel,
				dr:                  resInterface,
				acmRestore:          &acmRestoreWFin,
			},
			wantErr:          false,
			wantMCHFinalizer: false,
			wantACMFinalizer: false,
		},
	}

	for _, tt := range remove_tests {

		t.Run(tt.name, func(t *testing.T) {

			if err := removeResourcesFinalizer(tt.args.ctx, tt.args.c, tt.args.internalHubResource,
				tt.args.dr, tt.args.acmRestore); (err != nil) != tt.wantErr {
				t.Errorf("removeResourcesFinalizer() error = %v, wantErr %v", err, tt.wantErr)
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

	errs = append(errs, k8sClient1.Create(context.Background(), &acmRestoreWFin1))
	errs = append(errs, k8sClient1.Create(context.Background(), &acmRestoreWDelFin))
	if err := errors.Join(errs...); err != nil {
		t.Errorf("Error creating objs to setup for test: %s", err.Error())
	}
	add_tests := []struct {
		name             string
		args             args
		wantErr          bool
		wantMCHFinalizer bool
		wantACMFinalizer bool
	}{
		{
			name: "add test - finalizers must be not be added, acm restore is deleted",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				internalHubResource: *mchObjAdd,
				dr:                  resInterface,
				acmRestore:          &acmRestoreWDelFin,
			},
			wantErr:          false,
			wantMCHFinalizer: false,
			wantACMFinalizer: true,
		},
		{
			name: "add test - finalizers must be not be added, acm restore already has them",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				internalHubResource: *mchObjAdd,
				dr:                  resInterface,
				acmRestore:          &acmRestoreWFin1,
			},
			wantErr:          false,
			wantMCHFinalizer: false,
			wantACMFinalizer: true,
		},
		{
			name: "add test - finalizers must be added",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				internalHubResource: *mchObjAdd,
				dr:                  resInterface,
				acmRestore:          &acmRestore1,
			},
			wantErr:          false,
			wantMCHFinalizer: true,
			wantACMFinalizer: true,
		},
	}

	for _, tt := range add_tests {

		t.Run(tt.name, func(t *testing.T) {
			if err := addResourcesFinalizer(tt.args.ctx, tt.args.c, tt.args.internalHubResource,
				tt.args.dr, tt.args.acmRestore); (err != nil) != tt.wantErr {
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

	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}

}
