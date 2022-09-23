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

func initBackupScheduleWithStatus(phase v1beta1.SchedulePhase) *v1beta1.BackupSchedule {
	return &v1beta1.BackupSchedule{
		Status: v1beta1.BackupScheduleStatus{
			Phase: phase,
		},
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
		want v1beta1.SchedulePhase
	}{
		{
			name: "nil schedule",
			args: args{
				schedules:      nil,
				backupSchedule: initBackupSchedule("no matter"),
			},
			want: v1beta1.SchedulePhaseNew,
		},
		{
			name: "schedule in collision",
			args: args{
				schedules:      nil,
				backupSchedule: initBackupScheduleWithStatus(v1beta1.SchedulePhaseBackupCollision),
			},
			want: v1beta1.SchedulePhaseBackupCollision,
		},
		{
			name: "new",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseNew, "0 8 * * *"),
				backupSchedule: initBackupSchedule("0 8 * * *"),
			},
			want: v1beta1.SchedulePhaseNew,
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
			want: v1beta1.SchedulePhaseFailedValidation,
		},
		{
			name: "enabled",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseEnabled, "0 8 * * *"),
				backupSchedule: initBackupSchedule("0 8 * * *"),
			},
			want: v1beta1.SchedulePhaseEnabled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := setSchedulePhase(tt.args.schedules, tt.args.backupSchedule); got != tt.want {
				t.Errorf("setSchedulePhase() = %v, want %v", got, tt.want)
			}
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
	veleroNamespace := *createNamespace(veleroNamespaceName)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	k8sClient1.Create(context.Background(), &veleroNamespace)

	rhacmBackupSchedule := *createBackupSchedule("backup-sch-to-error-restore", veleroNamespaceName).
		schedule("backup-schedule").
		veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
		object

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
	veleroNamespace := *createNamespace(veleroNamespaceName)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	k8sClient1.Create(context.Background(), &veleroNamespace)

	rhacmBackupSchedule := *createBackupSchedule("backup-sch-to-error-restore", veleroNamespaceName).
		object

	rhacmBackupScheduleInvalidNS := *createBackupSchedule("backup-sch-to-error-restore", "invalid-ns").
		object

	latestRestore := "latest"
	rhacmRestore := *createACMRestore("restore-name", veleroNamespaceName).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestRestore).
		veleroCredentialsBackupName(latestRestore).
		veleroResourcesBackupName(latestRestore).object

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
		{
			name: "velero restore has one restore and not completed invalid",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupScheduleInvalidNS,
			},
			want: "",
		},
	}
	for index, tt := range tests {

		if index == 1 {
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

func Test_createInitialBackupForSchedule(t *testing.T) {

	timeStr := "20220912191647"
	veleroNamespaceName := "backup-ns"
	rhacmBackupSchedule := *createBackupSchedule("backup-sch", veleroNamespaceName).
		object

	rhacmBackupScheduleNoRun := *createBackupSchedule("backup-sch", veleroNamespaceName).
		noBackupOnStart(true).
		object

	schNoLabels := *createSchedule("acm-credentials-cluster-schedule", veleroNamespaceName).
		scheduleLabels(map[string]string{BackupScheduleNameLabel: "aa"}).
		object

	veleroNamespace := *createNamespace(veleroNamespaceName)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *v1beta1.BackupSchedule
		veleroSchedule *veleroapi.Schedule
	}
	tests := []struct {
		name              string
		args              args
		want              bool
		want_veleroBackup string
	}{
		{
			name: "backup schedule should not call backup on init schedule",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient,
				backupSchedule: &rhacmBackupScheduleNoRun,
				veleroSchedule: &schNoLabels,
			},
			want:              true,
			want_veleroBackup: "", // no backup
		},
		{
			name: "backup schedule should call backup on init schedule - error, no ns",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want:              false,
			want_veleroBackup: schNoLabels.Name + "-" + timeStr,
		},
		{
			name: "backup schedule should call backup on init schedule",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want:              true,
			want_veleroBackup: schNoLabels.Name + "-" + timeStr,
		},
	}
	for index, tt := range tests {

		if index == 2 {
			k8sClient1.Create(context.Background(), &veleroNamespace)
		}
		t.Run(tt.name, func(t *testing.T) {
			if got_backup, got := createInitialBackupForSchedule(tt.args.ctx, tt.args.c, tt.args.veleroSchedule,
				tt.args.backupSchedule, timeStr); (got == nil) != tt.want ||
				got_backup.Name != tt.want_veleroBackup {

				t.Errorf("createInitialBackupForSchedule() err is %v, want %v, backupName is %v, want %v",
					got, tt.want, got_backup.Name, tt.want_veleroBackup)
				if got != nil {
					t.Errorf("error %v", got.Error())
				}
			}
		})
	}

	// clean up
	testEnv.Stop()
}

func Test_createFailedValidationResponse(t *testing.T) {

	veleroNamespaceName := "backup-ns-v"

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient2, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})

	rhacmBackupSchedule := *createBackupSchedule("backup-sch-v", veleroNamespaceName).
		object

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *v1beta1.BackupSchedule
		phase          v1beta1.SchedulePhase
		msg            string
		requeue        bool
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "reque failure interval",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient2,
				backupSchedule: &rhacmBackupSchedule,
				requeue:        true,
				msg:            "some error",
				phase:          v1beta1.SchedulePhaseBackupCollision,
			},
			want: failureInterval,
		},
		{
			name: "do not reque",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient2,
				backupSchedule: &rhacmBackupSchedule,
				requeue:        false,
				msg:            "some error",
				phase:          v1beta1.SchedulePhaseBackupCollision,
			},
			want: time.Second * 0,
		},
	}
	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			if r, _, _ := createFailedValidationResponse(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule, tt.args.msg,
				tt.args.requeue); r.RequeueAfter != tt.want {
				t.Errorf("createFailedValidationResponse() = %v, want %v", r.RequeueAfter, tt.want)
			}
		})

	}
	// clean up
	testEnv.Stop()

}
