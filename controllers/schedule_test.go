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
	"reflect"
	"testing"

	"github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func initBackupSchedule(cronString string) *v1beta1.BackupSchedule {
	return &v1beta1.BackupSchedule{
		Spec: v1beta1.BackupScheduleSpec{
			VeleroSchedule: cronString,
		},
		Status: v1beta1.BackupScheduleStatus{},
	}
}

func initVeleroScheduleList(phase veleroapi.SchedulePhase, cronSpec string) *veleroapi.ScheduleList {
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
			if got := parseCronSchedule(tt.args.ctx, tt.args.backupSchedule); !reflect.DeepEqual(got, tt.want) {
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
				schedules:      initVeleroScheduleList(veleroapi.SchedulePhaseFailedValidation, "0 8 * * *"),
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
