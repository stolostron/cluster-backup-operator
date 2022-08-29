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
	"reflect"
	"testing"
	"time"

	"github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	fakediscovery "k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

func initBackupSchedule(cronString string) *v1beta1.BackupSchedule {
	return &v1beta1.BackupSchedule{
		Spec: v1beta1.BackupScheduleSpec{
			VeleroSchedule: cronString,
		},
		Status: v1beta1.BackupScheduleStatus{},
	}
}

func initVeleroScheduleList(
	phase veleroapi.SchedulePhase,
	cronSpec string,
) *veleroapi.ScheduleList {
	return &veleroapi.ScheduleList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "ScheduleList",
		},
		Items: []veleroapi.Schedule{
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Schedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "the-brand-new-schedule",
				},
				Spec: veleroapi.ScheduleSpec{
					Schedule: cronSpec,
				},
				Status: veleroapi.ScheduleStatus{
					Phase: phase,
				},
			},
		},
	}
}

func Test_parseCronSchedule(t *testing.T) {

	client := fakeclientset.NewSimpleClientset()
	fakeDiscovery, ok := client.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatalf("couldn't convert Discovery() to *FakeDiscovery")
	}

	testGitCommit := "v1.0.0"
	fakeDiscovery.FakedServerVersion = &version.Info{
		GitCommit: testGitCommit,
	}

	sv, err := client.Discovery().ServerVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sv.GitCommit != testGitCommit {
		t.Fatalf("unexpected faked discovery return value: %q", sv.GitCommit)
	}

	type args struct {
		ctx            context.Context
		backupSchedule *v1beta1.BackupSchedule
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "Empty cron",
			args: args{
				ctx:            context.TODO(),
				backupSchedule: initBackupSchedule(""),
			},
			want: []string{"Schedule must be a non-empty valid Cron expression"},
		},
		{
			name: "Wrong cron",
			args: args{
				ctx:            context.TODO(),
				backupSchedule: initBackupSchedule("WRONG"),
			},
			want: []string{"invalid schedule: expected exactly 5 fields, found 1: [WRONG]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCronSchedule(tt.args.ctx, tt.args.backupSchedule); !reflect.DeepEqual(
				got,
				tt.want,
			) {
				t.Errorf("parseCronSchedule() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_setSchedulePhase(t *testing.T) {
	type args struct {
		schedules      *veleroapi.ScheduleList
		backupSchedule *v1beta1.BackupSchedule
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "nil schedule",
			args: args{
				schedules:      nil,
				backupSchedule: initBackupSchedule("no matter"),
			},
		},
		{
			name: "new",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseNew, "0 8 * * *"),
				backupSchedule: initBackupSchedule("0 8 * * *"),
			},
		},
		{
			name: "failed validation",
			args: args{
				schedules: initVeleroScheduleList(
					veleroapi.SchedulePhaseFailedValidation,
					"0 8 * * *",
				),
				backupSchedule: initBackupSchedule("0 8 * * *"),
			},
		},
		{
			name: "enabled",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseEnabled, "0 8 * * *"),
				backupSchedule: initBackupSchedule("0 8 * * *"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setSchedulePhase(tt.args.schedules, tt.args.backupSchedule)
		})
	}
}

func Test_isScheduleSpecUpdated(t *testing.T) {
	type args struct {
		schedules      *veleroapi.ScheduleList
		backupSchedule *v1beta1.BackupSchedule
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		// TODO: Add test cases.
		{
			name: "no schedules",
			args: args{
				schedules: nil,
			},
			want: false,
		},
		{
			name: "cron spec updated",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseEnabled, "0 6 * * *"),
				backupSchedule: initBackupSchedule("0 8 * * *"),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isScheduleSpecUpdated(tt.args.schedules, tt.args.backupSchedule); got != tt.want {
				t.Errorf("isScheduleSpecUpdated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_deleteVeleroSchedules(t *testing.T) {

	veleroNamespaceName := "backup-ns"
	veleroNamespace := corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: veleroNamespaceName,
		},
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	k8sClient1.Create(context.Background(), &veleroNamespace)

	rhacmBackupSchedule := v1beta1.BackupSchedule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.open-cluster-management.io/v1beta1",
			Kind:       "BackupSchedule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backup-sch-to-error-restore",
			Namespace: veleroNamespaceName,
		},
		Spec: v1beta1.BackupScheduleSpec{
			VeleroSchedule: "backup-schedule",
			VeleroTTL:      metav1.Duration{Duration: time.Hour * 72},
		},
	}

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *v1beta1.BackupSchedule
		schedules      *veleroapi.ScheduleList
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "velero schedules is nil",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules:      nil,
			},
			want: false,
		},
		{
			name: "no velero schedules Items",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules:      &veleroapi.ScheduleList{},
			},
			want: false,
		},
		{
			name: "failed to delete the schedule, returns error",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules: &veleroapi.ScheduleList{
					Items: []veleroapi.Schedule{
						veleroapi.Schedule{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "Schedule",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "schedule-invalid-not-found",
								Namespace: veleroNamespaceName,
							},
						},
					},
				},
			},
			want: true,
		},
	}
	for index, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			if got := deleteVeleroSchedules(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule, tt.args.schedules); (got != nil) != tt.want {
				t.Errorf("deleteVeleroSchedules() = %v, want len of string is empty %v", got, tt.want)
			}
		})
		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
	}
}

func Test_isRestoreRunning(t *testing.T) {

	veleroNamespaceName := "backup-ns"
	veleroNamespace := corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: veleroNamespaceName,
		},
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	k8sClient1.Create(context.Background(), &veleroNamespace)

	rhacmBackupSchedule := v1beta1.BackupSchedule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.open-cluster-management.io/v1beta1",
			Kind:       "BackupSchedule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backup-sch-to-error-restore",
			Namespace: veleroNamespaceName,
		},
	}

	latestRestore := "latest"
	rhacmRestore := v1beta1.Restore{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.open-cluster-management.io/v1beta1",
			Kind:       "Restore",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "restore-name",
			Namespace: veleroNamespaceName,
		},
		Spec: v1beta1.RestoreSpec{
			CleanupBeforeRestore:            v1beta1.CleanupTypeRestored,
			VeleroManagedClustersBackupName: &latestRestore,
			VeleroCredentialsBackupName:     &latestRestore,
			VeleroResourcesBackupName:       &latestRestore,
		},
	}

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *v1beta1.BackupSchedule
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "velero has no restores",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
			},
			want: "",
		},
		{
			name: "velero restore has one restore and not completed",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
			},
			want: rhacmRestore.Name,
		},
	}
	for index, tt := range tests {

		if index == len(tests)-1 {
			k8sClient1.Create(tt.args.ctx, &rhacmRestore)
		}

		t.Run(tt.name, func(t *testing.T) {
			if got, _ := isRestoreRunning(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule); got != tt.want {
				t.Errorf("isRestoreRunning() = %v, want %v", got, tt.want)
			}
		})
		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
	}
}
