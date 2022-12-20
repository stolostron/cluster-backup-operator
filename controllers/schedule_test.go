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

	"github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	fakediscovery "k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	corev1 "k8s.io/api/core/v1"
)

func createNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func createSecret(name string, ns string,
	labels map[string]string,
	annotations map[string]string,
	data map[string][]byte) *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	if labels != nil {
		secret.Labels = labels
	}
	if annotations != nil {
		secret.Annotations = annotations
	}
	if data != nil {
		secret.Data = data
	}

	return secret

}

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

func Test_updateSecretsLabels(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	scheme1 := runtime.NewScheme()

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})
	clusterv1.AddToScheme(scheme1)
	corev1.AddToScheme(scheme1)

	labelName := backupCredsClusterLabel
	labelValue := "clusterpool"
	clsName := "managed1"

	if err := k8sClient1.Create(context.Background(), createNamespace(clsName)); err != nil {
		t.Errorf("cannot create ns %s ", err.Error())
	}

	hiveSecrets := corev1.SecretList{
		Items: []corev1.Secret{
			*createSecret(clsName+"-import", clsName, map[string]string{
				labelName: labelValue,
			}, nil, nil), // do not back up, name is cls-import
			*createSecret(clsName+"-import-1", clsName, map[string]string{
				labelName: labelValue,
			}, nil, nil), // back it up
			*createSecret(clsName+"-import-2", clsName, nil, nil, nil),               // back it up
			*createSecret(clsName+"-bootstrap-test", clsName, nil, nil, nil),         // do not backup
			*createSecret(clsName+"-some-other-secret-test", clsName, nil, nil, nil), // backup
		},
	}

	type args struct {
		ctx     context.Context
		c       client.Client
		secrets corev1.SecretList
		prefix  string
		lName   string
		lValue  string
	}
	tests := []struct {
		name          string
		args          args
		backupSecrets []string // what should be backed up
	}{
		{
			name: "hive secrets 1",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				secrets: hiveSecrets,
				lName:   labelName,
				lValue:  labelValue,
				prefix:  clsName,
			},
			backupSecrets: []string{"managed1-import-1", "managed1-import-2", "managed1-some-other-secret-test"},
		},
	}
	for _, tt := range tests {
		for index := range hiveSecrets.Items {
			if err := k8sClient1.Create(context.Background(), &hiveSecrets.Items[index]); err != nil {
				t.Errorf("cannot create %s ", err.Error())
			}
		}

		t.Run(tt.name, func(t *testing.T) {
			updateSecretsLabels(tt.args.ctx, k8sClient1,
				tt.args.secrets,
				tt.args.prefix,
				tt.args.lName,
				tt.args.lValue,
			)
		})

		result := []string{}
		for index := range hiveSecrets.Items {
			secret := hiveSecrets.Items[index]
			if err := k8sClient1.Get(context.Background(), types.NamespacedName{Name: secret.Name,
				Namespace: secret.Namespace}, &secret); err != nil {
				t.Errorf("cannot get secret %s ", err.Error())
			}
			if secret.GetLabels()[labelName] == labelValue {
				// it was backed up
				result = append(result, secret.Name)
			}
		}

		if !reflect.DeepEqual(result, tt.backupSecrets) {
			t.Errorf("updateSecretsLabels() = %v want %v", result, tt.backupSecrets)
		}
	}
	testEnv.Stop()

}
