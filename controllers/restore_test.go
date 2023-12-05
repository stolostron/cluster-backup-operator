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
	"context"
	"path/filepath"
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).syncRestoreWithNewBackups(true).object,
			},
			want: false,
		},
		{
			name: "Credentials should be set to skip or latest",
			args: args{
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
					object,
			},
			want: true,
		},
		{
			name: "Do not skip all",
			args: args{
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(v1.Duration{Duration: time.Minute * 15}).
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
				restore: createACMRestore("Restore", "veleroNamespace").
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
				restore: createACMRestore("Restore", "veleroNamespace").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(v1.Duration{Duration: time.Minute * 15}).
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
				restore: createACMRestore("Restore", "veleroNamespace").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(v1.Duration{Duration: time.Minute * 15}).
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
				restore: createACMRestore("Restore", "veleroNamespace").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(v1.Duration{Duration: time.Minute * 15}).
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
				restore: createACMRestore("Restore", "veleroNamespace").
					syncRestoreWithNewBackups(true).
					restoreSyncInterval(v1.Duration{Duration: time.Minute * 15}).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseRunning).object,

				restoreList: &veleroapi.RestoreList{
					Items: []veleroapi.Restore{
						veleroapi.Restore{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "Restore",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "restore",
								Namespace: "veleroNamespace",
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
			if phase, cleanupOnEnabled := setRestorePhase(tt.args.restoreList, tt.args.restore); phase != tt.wantPhase || cleanupOnEnabled != tt.wantCleanupOnEnabled {
				t.Errorf("setRestorePhase() phase = %v, want %v, cleanupOnEnabled = %v, want %v", phase, tt.wantPhase, cleanupOnEnabled, tt.wantCleanupOnEnabled)
			}
		})
	}
}

func Test_getVeleroBackupName(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
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
			veleroapi.AddToScheme(scheme.Scheme)
		}
		if index == 2 {
			k8sClient1.Create(context.Background(), &veleroNamespace)
			k8sClient1.Create(context.Background(), &backup)
			k8sClient1.Create(context.Background(), &backupClsNoMatch)
		}
		if index == len(tests)-2 {
			k8sClient1.Create(context.Background(), &backupClsExactTime)
		}
		if index == len(tests)-1 {
			k8sClient1.Create(context.Background(), &backupClsExactWithin30s)
			k8sClient1.Delete(context.Background(), &backupClsExactTime)
		}
		t.Run(tt.name, func(t *testing.T) {
			veleroBackups := &veleroapi.BackupList{}
			tt.args.c.List(tt.args.ctx, veleroBackups, client.InNamespace(veleroNamespace.Name))
			if name, _, _ := getVeleroBackupName(tt.args.ctx, tt.args.c,
				tt.args.restoreNamespace, tt.args.resourceType, tt.args.backupName, veleroBackups); name != tt.want {
				t.Errorf("getVeleroBackupName() returns = %v, want %v", name, tt.want)
			}
		})
	}
	// clean up
	testEnv.Stop()
}

func Test_isNewBackupAvailable(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

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
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(latestBackup).
		veleroResourcesBackupName(latestBackup).object

	restoreCredSameBackup := *createACMRestore(passiveStr, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
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
			corev1.AddToScheme(scheme1)
			v1beta1.AddToScheme(scheme1)
			veleroapi.AddToScheme(scheme1)
		}
		if index == 2 {
			k8sClient1.Create(tt.args.ctx, &veleroNamespace)
			k8sClient1.Create(tt.args.ctx, &backup)
			k8sClient1.Create(context.Background(), &veleroRestore)
		}
		if index == len(tests)-1 {
			// create restore
			k8sClient1.Create(tt.args.ctx, &restoreCredNewBackup)
		}
		t.Run(tt.name, func(t *testing.T) {
			if got := isNewBackupAvailable(tt.args.ctx, tt.args.c,
				tt.args.restore, tt.args.resourceType); got != tt.want {
				t.Errorf("isNewBackupAvailable() returns = %v, want %v, %v", got, tt.want, tt.args.resourceType)
			}
		})
	}
	testEnv.Stop()

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
					v1beta1.BackupSchedule{
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

func Test_retrieveRestoreDetails(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
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
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(skipRestore).
		veleroResourcesBackupName(backupName).object

	restoreCredsInvalidBackupName := *createACMRestore("restore1", veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(skipRestore).
		veleroResourcesBackupName(invalidBackupName).object

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})
	k8sClient1.Create(context.Background(), &backup, &client.CreateOptions{})

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
			testEnv.Stop()
		}
	}

}

func Test_isOtherResourcesRunning(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	veleroNamespaceName := "default"
	restoreName := "restore-backup-name"
	backupName := "backup-name"

	restore := *createACMRestore(restoreName, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(skipRestoreStr).
		veleroResourcesBackupName(backupName).object

	restoreOther := *createACMRestore("other-"+restoreName, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestBackupStr).
		veleroCredentialsBackupName(skipRestoreStr).
		veleroResourcesBackupName(backupName).object

	backupCollision := *createBackupSchedule(backupName+"-collission", veleroNamespaceName).
		phase(v1beta1.SchedulePhaseBackupCollision).
		object
	backupFailed := *createBackupSchedule(backupName+"-failed", veleroNamespaceName).
		object

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

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
			name: "isOtherResourcesRunning has no errors, backup found but in collission state",
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
			want: "This resource is ignored because BackupSchedule resource backup-name-failed is currently active, before creating another resource verify that any active resources are removed.",
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
			want: "This resource is ignored because Restore resource other-restore-backup-name is currently active, before creating another resource verify that any active resources are removed.",
		},
	}

	for index, tt := range tests {

		if index == 1 {
			//create CRD
			v1beta1.AddToScheme(scheme1)
			if err := k8sClient1.Create(tt.args.ctx, &restore, &client.CreateOptions{}); err != nil {
				panic(err.Error())
			}

		}
		if index == 2 {
			//create backup in collission state
			k8sClient1.Create(tt.args.ctx, &backupCollision, &client.CreateOptions{})
			k8sClient1.Get(tt.args.ctx, types.NamespacedName{
				Name:      backupCollision.Name,
				Namespace: backupCollision.Namespace}, &backupCollision)
			backupCollision.Status.Phase = v1beta1.SchedulePhaseBackupCollision
			k8sClient1.Status().Update(tt.args.ctx, &backupCollision)

		}
		if index == 3 {
			k8sClient1.Create(tt.args.ctx, &backupFailed)
		}
		if index == len(tests)-2 {
			k8sClient1.Delete(tt.args.ctx, &backupFailed)
		}
		if index == len(tests)-1 {
			k8sClient1.Create(tt.args.ctx, &restoreOther)
		}

		t.Run(tt.name, func(t *testing.T) {
			if got, _ := isOtherResourcesRunning(tt.args.ctx, tt.args.c,
				tt.args.restore); got != tt.want {
				t.Errorf("isOtherResourcesRunning() returns = %v, want %v", got, tt.want)
			}
		})
	}
	// clean up
	testEnv.Stop()

}
