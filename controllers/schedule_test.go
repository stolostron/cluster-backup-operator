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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stolostron/cluster-backup-operator/api/v1beta1"
	backupv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryclient "k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

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

func initVeleroSchedulesWithSpecs(
	cronSpec string,
	ttl metav1.Duration) *veleroapi.ScheduleList {
	veleroScheduleList := initVeleroScheduleTypes()
	for i := range veleroScheduleList.Items {
		veleroSchedule := &veleroScheduleList.Items[i]
		veleroSchedule.Spec.Schedule = cronSpec
		veleroSchedule.Spec.Template.TTL = ttl
	}
	return veleroScheduleList
}

func initVeleroScheduleTypes() *veleroapi.ScheduleList {
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
					Name: "acm-credentials-schedule",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Schedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "acm-resources-schedule",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Schedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "acm-resources-generic-schedule",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Schedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "acm-managed-clusters-schedule",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Schedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "acm-validation-policy-schedule",
				},
			},
		},
	}
}

func Test_parseCronSchedule(t *testing.T) {

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
				backupSchedule: createBackupSchedule("acm", "ns").schedule("").object,
			},
			want: []string{"Schedule must be a non-empty valid Cron expression"},
		},
		{
			name: "Wrong cron",
			args: args{
				ctx:            context.TODO(),
				backupSchedule: createBackupSchedule("acm", "ns").schedule("WRONG").object,
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
				backupSchedule: createBackupSchedule("name", "ns").schedule("no matter").object,
			},
			want: v1beta1.SchedulePhaseNew,
		},
		{
			name: "schedule in collision",
			args: args{
				schedules: nil,
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).phase(v1beta1.SchedulePhaseBackupCollision).
					object,
			},
			want: v1beta1.SchedulePhaseBackupCollision,
		},
		{
			name: "new",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseNew, "0 8 * * *"),
				backupSchedule: createBackupSchedule("name", "ns").schedule("0 8 * * *").object,
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
				backupSchedule: createBackupSchedule("name", "ns").schedule("0 8 * * *").object,
			},
			want: v1beta1.SchedulePhaseFailedValidation,
		},
		{
			name: "enabled",
			args: args{
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseEnabled, "0 8 * * *"),
				backupSchedule: createBackupSchedule("name", "ns").schedule("0 8 * * *").object,
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

func Test_getSchedulesWithUpdatedResources(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})

	type args struct {
		resourcesToBackup []string
		schedules         *veleroapi.ScheduleList
	}

	resourcesToBackup := []string{
		"managedproxyserviceresolver.proxy.open-cluster-management.io",
		"channel.apps.open-cluster-management.io",
		"iampolicy.policy.open-cluster-management.io",
		"agentclusterinstall.extensions.hive.openshift.io",
		"application.app.k8s.io",
		"agentserviceconfig.agent-install.openshift.io",
		"observatorium.core.observatorium.io",
	}
	schedules := initVeleroScheduleTypes()

	veleroSchedulesToUpdate := make([]veleroapi.Schedule, 0)

	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]
		veleroBackupTemplate := &veleroapi.BackupSpec{}

		switch veleroSchedule.Name {
		case veleroScheduleNames[Resources]:
			setResourcesBackupInfo(context.Background(), veleroBackupTemplate, resourcesToBackup,
				"open-cluster-management-backup", k8sClient1)
			veleroSchedulesToUpdate = append(
				veleroSchedulesToUpdate,
				*veleroSchedule,
			)
		case veleroScheduleNames[ResourcesGeneric]:
			setGenericResourcesBackupInfo(veleroBackupTemplate, resourcesToBackup)
			veleroSchedulesToUpdate = append(
				veleroSchedulesToUpdate,
				*veleroSchedule,
			)
		case veleroScheduleNames[ManagedClusters]:
			setManagedClustersBackupInfo(veleroBackupTemplate, resourcesToBackup)
			veleroSchedulesToUpdate = append(
				veleroSchedulesToUpdate,
				*veleroSchedule,
			)
		}

		veleroSchedule.Spec.Template = *veleroBackupTemplate
	}

	var newResourcesToBackup []string
	newResourcesToBackup = append(newResourcesToBackup, resourcesToBackup...)
	newResourcesToBackup = append(
		newResourcesToBackup,
		"baremetalasset.inventory.open-cluster-management.io",
	)
	newResourcesToBackup = append(newResourcesToBackup, "x.hive.openshift.io")
	newResourcesToBackup = append(newResourcesToBackup, "y.hive.openshift.io")

	tests := []struct {
		name string
		args args
		want []veleroapi.Schedule
	}{
		{
			name: "nil arguments",
			args: args{
				resourcesToBackup: nil,
				schedules:         nil,
			},
			want: []veleroapi.Schedule{},
		},
		{
			name: "unchanged hub resources",
			args: args{
				resourcesToBackup: resourcesToBackup,
				schedules:         schedules,
			},
			want: []veleroapi.Schedule{},
		},
		{
			name: "changed hub resources",
			args: args{
				resourcesToBackup: newResourcesToBackup,
				schedules:         schedules,
			},
			want: veleroSchedulesToUpdate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSchedulesWithUpdatedResources(tt.args.resourcesToBackup, tt.args.schedules)
			if len(got) != len(tt.want) {
				t.Errorf("getSchedulesWithUpdatedResources() = %v, want %v", got, tt.want)
			}
		})
	}
	// clean up
	testEnv.Stop()
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
		{
			name: "no schedules",
			args: args{
				schedules: nil,
			},
			want: false,
		},
		{
			name: "same schedule and ttl",
			args: args{
				schedules: initVeleroSchedulesWithSpecs(
					"0 6 * * *",
					metav1.Duration{Duration: time.Hour * 1},
				),
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).schedule("0 6 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 1}).
					object,
			},
			want: false,
		},
		{
			name: "schedule updated",
			args: args{
				schedules: initVeleroSchedulesWithSpecs(
					"0 6 * * *",
					metav1.Duration{Duration: time.Hour * 1},
				),
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).schedule("0 8 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 1}).
					object,
			},
			want: true,
		},
		{
			name: "ttl updated",
			args: args{
				schedules: initVeleroSchedulesWithSpecs(
					"0 6 * * *",
					metav1.Duration{Duration: time.Hour * 1},
				),
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).schedule("0 6 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 2}).
					object,
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

	veleroSchedules := initVeleroScheduleList(veleroapi.SchedulePhaseNew, "0 8 * * *")
	for i := range veleroSchedules.Items {
		veleroSchedule := &veleroSchedules.Items[i]
		veleroSchedule.Namespace = veleroNamespaceName
		k8sClient1.Create(context.Background(), veleroSchedule)
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
		{
			name: "successfully delete the schedule, returns no error",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules:      veleroSchedules,
			},
			want: false,
		},
	}
	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			if got := deleteVeleroSchedules(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule, tt.args.schedules); (got != nil) != tt.want {
				t.Errorf(
					"deleteVeleroSchedules() = %v, want len of string is empty %v",
					got,
					tt.want,
				)
			}
		})
	}
	testEnv.Stop()
}

func Test_isRestoreRunning(t *testing.T) {

	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	scheme2 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme2)
	corev1.AddToScheme(scheme2)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme2})

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
			name: "velero schema not found",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
			},
			want: "",
		},
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
			k8sClient1.Create(context.Background(), &veleroNamespace)
		}
		if index == 2 {
			backupv1beta1.AddToScheme(scheme2)
			k8sClient1.Create(tt.args.ctx, &rhacmRestore)
		}

		t.Run(tt.name, func(t *testing.T) {
			if got := isRestoreRunning(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule); got != tt.want {
				t.Errorf("isRestoreRunning() = %v, want %v", got, tt.want)
			}
		})
	}
	testEnv.Stop()
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
	scheme1 := runtime.NewScheme()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})
	corev1.AddToScheme(scheme1)
	veleroapi.AddToScheme(scheme1)

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
			want_veleroBackup: "",
		},
		{
			name: "backup schedule should call backup on init schedule",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want_veleroBackup: schNoLabels.Name + "-" + timeStr,
		},
		{
			name: "backup schedule should call backup on init schedule, but error since schedule already created",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want_veleroBackup: "",
		},
	}
	for index, tt := range tests {

		if index == 2 {
			k8sClient1.Create(context.Background(), &veleroNamespace)
		}
		t.Run(tt.name, func(t *testing.T) {
			if got_backup := createInitialBackupForSchedule(tt.args.ctx, tt.args.c, tt.args.veleroSchedule,
				tt.args.backupSchedule, timeStr); got_backup != tt.want_veleroBackup {

				t.Errorf("createInitialBackupForSchedule() backupName is %v, want %v",
					got_backup, tt.want_veleroBackup)

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

func Test_verifyMSAOptione(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	appsInfo := metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "channels", Namespaced: true, Kind: "Channel"},
			{Name: "subscriptions", Namespaced: true, Kind: "Subscription"},
		},
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var list interface{}
		switch req.URL.Path {
		case "/apis/apps.open-cluster-management.io/v1beta1":
			list = &appsInfo
		case "/api":
			list = &metav1.APIVersions{
				Versions: []string{
					"v1",
					"v1beta1",
				},
			}
		case "/apis":
			list = &metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name: "config.openshift.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "config.openshift.io/v1",
								Version:      "v1",
							},
						},
					},
					{
						Name: "apps.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "apps.open-cluster-management.io/v1beta1",
								Version:      "v1beta1",
							},
							{GroupVersion: "apps.open-cluster-management.io/v1", Version: "v1"},
						},
					},
				},
			}
		default:
			//t.Logf("unexpected request: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		output, err := json.Marshal(list)
		if err != nil {
			//t.Errorf("unexpected encoding error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(output)
	}))

	res_local_ns := &unstructured.Unstructured{}
	res_local_ns.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	unstructuredScheme := runtime.NewScheme()
	chnv1.AddToScheme(unstructuredScheme)

	dynClient := dynamicfake.NewSimpleDynamicClient(unstructuredScheme, res_local_ns)

	targetGVK := schema.GroupVersionKind{
		Group:   "apps.open-cluster-management.io",
		Version: "v1",
		Kind:    "Channel",
	}
	targetGVR := targetGVK.GroupVersion().WithResource("channel")

	resInterface := dynClient.Resource(targetGVR)

	sch := *createBackupSchedule("name", "ns").
		schedule("0 */6 * * *").object

	sch_msa := *createBackupSchedule("name", "ns").
		useManagedServiceAccount(true).
		schedule("0 */6 * * *").object

	// create resources which should be found
	resInterface.Namespace("default").Create(context.Background(), res_local_ns, v1.CreateOptions{})

	fakeDiscovery := discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server.URL},
	)
	m := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(fakeDiscovery),
	)
	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *v1beta1.BackupSchedule
		mapper         *restmapper.DeferredDiscoveryRESTMapper
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "no CRD found for MSA, but MSA not enabled",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				mapper:         m,
				backupSchedule: &sch,
			},
			want: true,
		},
		{
			name: "no CRD found for MSA, MSA enabled",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				mapper:         m,
				backupSchedule: &sch_msa,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got, _ := verifyMSAOption(tt.args.ctx,
				tt.args.c,
				tt.args.mapper,
				tt.args.backupSchedule,
			); got != tt.want {
				t.Errorf("verifyMSAOption() = %v, want %v", got,
					tt.want)
			}
		})
	}

	testEnv.Stop()
}

func Test_scheduleOwnsLatestStorageBackups(t *testing.T) {

	veleroNamespaceName := "default"

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	velero_schedule := *createSchedule(veleroScheduleNames[Resources], veleroNamespaceName).
		scheduleLabels(map[string]string{BackupScheduleClusterLabel: "cls"}).
		object

	aFewSecondsAgo := metav1.NewTime(time.Now().Add(-2 * time.Second))
	anHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))

	type args struct {
		ctx      context.Context
		c        client.Client
		schedule *veleroapi.Schedule
	}
	tests := []struct {
		name      string
		args      args
		want      bool
		resources []*veleroapi.Backup
	}{
		{
			name: "no crd",
			args: args{
				ctx:      context.Background(),
				c:        k8sClient1,
				schedule: &velero_schedule,
			},
			want:      true,
			resources: []*veleroapi.Backup{},
		},
		{
			name: "no backups",
			args: args{
				ctx:      context.Background(),
				c:        k8sClient1,
				schedule: &velero_schedule,
			},
			want:      true,
			resources: []*veleroapi.Backup{},
		},
		{
			name: "has backups, diferent cluster version",
			args: args{
				ctx:      context.Background(),
				c:        k8sClient1,
				schedule: &velero_schedule,
			},
			want: false,
			resources: []*veleroapi.Backup{
				createBackup(veleroScheduleNames[Resources]+"-1", veleroNamespaceName).
					startTimestamp(anHourAgo).errors(0).
					labels(map[string]string{BackupScheduleClusterLabel: "abcd",
						BackupVeleroLabel: veleroScheduleNames[Resources]}).
					object,
			},
		},
		{
			name: "has backups, same cluster version",
			args: args{
				ctx:      context.Background(),
				c:        k8sClient1,
				schedule: &velero_schedule,
			},
			want: true,
			resources: []*veleroapi.Backup{
				createBackup(veleroScheduleNames[Resources]+"-2", veleroNamespaceName).
					startTimestamp(aFewSecondsAgo).errors(0).
					labels(map[string]string{BackupScheduleClusterLabel: "cls",
						BackupVeleroLabel: veleroScheduleNames[Resources]}).
					object,
			},
		},
	}
	for index, tt := range tests {

		if index == 1 {
			veleroapi.AddToScheme(scheme1)
		}
		for i := range tt.resources {
			if err := k8sClient1.Create(tt.args.ctx, tt.resources[i]); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}

		}

		t.Run(tt.name, func(t *testing.T) {
			if got, _ := scheduleOwnsLatestStorageBackups(tt.args.ctx, tt.args.c,
				tt.args.schedule); got != tt.want {
				t.Errorf("scheduleOwnsLatestStorageBackups() = got %v, want %v", got, tt.want)
			}
		})

	}
	// clean up
	testEnv.Stop()

}
