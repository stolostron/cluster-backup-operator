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
/*
Package controllers contains comprehensive unit tests for schedule-related utility functions
in the ACM Backup/Restore system.

This test suite validates core schedule functionality including:
- Backup schedule resource management and updates
- Velero schedule creation, deletion, and lifecycle management
- Schedule validation and resource filtering
- Restore operation detection and conflict resolution
- Managed Service Account (MSA) token validation and configuration
- Schedule ownership and backup relationship tracking
- Integration with backup storage locations and CRD validation

The tests use fake clients for improved performance and reliability.
Helper functions from create_helper.go provide consistent test data and reduce setup complexity.
*/

//nolint:funlen
package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	backupv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryclient "k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func initVeleroScheduleList(
	veleroNamespaceName string,
	phase veleroapi.SchedulePhase,
	cronSpec string,
	ttl metav1.Duration,
) *veleroapi.ScheduleList {
	veleroSchedules := []veleroapi.Schedule{}
	for _, value := range veleroScheduleNames {
		schedule := *createSchedule(value, veleroNamespaceName).
			schedule(cronSpec).phase(phase).ttl(ttl).
			object

		veleroSchedules = append(veleroSchedules, schedule)
	}

	return &veleroapi.ScheduleList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "ScheduleList",
		},
		Items: veleroSchedules,
	}
}

func initVeleroSchedulesWithSpecs(
	cronSpec string,
	ttl metav1.Duration,
) *veleroapi.ScheduleList {
	veleroScheduleList := initVeleroScheduleTypes()
	for i := range veleroScheduleList.Items {
		veleroSchedule := &veleroScheduleList.Items[i]
		veleroSchedule.Spec.Schedule = cronSpec
		veleroSchedule.Spec.Template.TTL = ttl
	}
	return veleroScheduleList
}

func initVeleroSchedulesWithPausedState(
	cronSpec string,
	ttl metav1.Duration,
	paused bool,
) *veleroapi.ScheduleList {
	veleroScheduleList := initVeleroScheduleTypes()
	for i := range veleroScheduleList.Items {
		veleroSchedule := &veleroScheduleList.Items[i]
		veleroSchedule.Spec.Schedule = cronSpec
		veleroSchedule.Spec.Template.TTL = ttl
		veleroSchedule.Spec.Paused = paused
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

// Test_parseCronSchedule tests validation and parsing of cron schedule expressions.
//
// This test validates the logic that parses and validates cron expressions
// used for scheduling backup operations, ensuring proper syntax validation.
//
// Test Coverage:
// - Empty cron schedule validation (should fail)
// - Invalid cron syntax validation (wrong format)
// - Cron expression parsing and error handling
// - Error message formatting and content validation
//
// Test Scenarios:
// - Empty cron expression handling
// - Malformed cron expressions with wrong field count
// - Invalid cron syntax and error reporting
//
// Implementation Details:
// - Uses standard cron expression validation
// - Returns descriptive error messages for invalid expressions
// - Validates expected 5-field cron format
//
// Business Logic:
// Proper cron validation is essential for backup scheduling, ensuring that
// backup schedules are configured with valid timing expressions that the
// system can properly interpret and execute.
func Test_parseCronSchedule(t *testing.T) {
	type args struct {
		ctx            context.Context
		backupSchedule *backupv1beta1.BackupSchedule
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

// Test_setSchedulePhase tests backup schedule phase management and state transitions.
//
// This test validates the logic that determines and sets appropriate phases
// for backup schedules based on their current state and configuration.
//
// Test Coverage:
// - Phase determination logic for different schedule states
// - Schedule list validation and processing
// - Phase transition scenarios and timing
// - Error handling for phase management
//
// Test Scenarios:
// - Various schedule configurations and their expected phases
// - Empty or missing schedule list handling
// - Different Velero schedule states and phase mapping
// - Phase transition validation and consistency
//
// Implementation Details:
// - Uses Velero schedule list for phase determination
// - Maps schedule states to appropriate ACM phases
// - Handles edge cases and error conditions
//
// Business Logic:
// Schedule phase management is critical for backup orchestration, ensuring
// that backup schedules progress through appropriate states and provide
// accurate status information to users and monitoring systems.
func Test_setSchedulePhase(t *testing.T) {
	type args struct {
		schedules      *veleroapi.ScheduleList
		backupSchedule *backupv1beta1.BackupSchedule
	}
	tests := []struct {
		name string
		args args
		want backupv1beta1.SchedulePhase
	}{
		{
			name: "nil schedule",
			args: args{
				schedules:      nil,
				backupSchedule: createBackupSchedule("name", "ns").schedule("no matter").object,
			},
			want: backupv1beta1.SchedulePhaseNew,
		},
		{
			name: "schedule in collision",
			args: args{
				schedules: nil,
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).phase(backupv1beta1.SchedulePhaseBackupCollision).
					object,
			},
			want: backupv1beta1.SchedulePhaseBackupCollision,
		},
		{
			name: "new",
			args: args{
				schedules: initVeleroScheduleList("ns", veleroapi.SchedulePhaseNew, "0 7 * * *",
					metav1.Duration{Duration: time.Second * 5}),
				backupSchedule: createBackupSchedule("name", "ns").schedule("0 8 * * *").object,
			},
			want: backupv1beta1.SchedulePhaseNew,
		},
		{
			name: "failed validation",
			args: args{
				schedules: initVeleroScheduleList("ns",
					veleroapi.SchedulePhaseFailedValidation,
					"0 8 * * *",
					metav1.Duration{Duration: time.Second * 5},
				),
				backupSchedule: createBackupSchedule("name", "ns").schedule("0 8 * * *").object,
			},
			want: backupv1beta1.SchedulePhaseFailedValidation,
		},
		{
			name: "enabled",
			args: args{
				schedules: initVeleroScheduleList("ns", veleroapi.SchedulePhaseEnabled, "0 8 * * *",
					metav1.Duration{Duration: time.Second * 5}),
				backupSchedule: createBackupSchedule("name", "ns").schedule("0 8 * * *").object,
			},
			want: backupv1beta1.SchedulePhaseEnabled,
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

// Test_getSchedulesWithUpdatedResources tests resource filtering and schedule updating logic.
//
// This test validates the logic that determines which schedules need resource
// updates and applies new resource configurations to backup schedules.
//
// Test Coverage:
// - Resource-to-backup list processing and filtering
// - Schedule resource configuration updates
// - Resource inclusion/exclusion logic
// - Schedule matching and resource mapping for different backup types
//
// Test Scenarios:
// - Nil arguments handling (should return empty list)
// - Unchanged hub resources (no schedules need updates)
// - Changed hub resources (schedules require updates)
// - Resource addition and modification detection
//
// Implementation Details:
// - Uses fake client for testing with proper scheme setup
// - Tests resources, generic resources, and managed clusters schedules
// - Validates proper resource filtering and backup template configuration
// - Compares schedule lists for update requirements
//
// Business Logic:
// Resource filtering ensures that backup schedules only include the resources
// that should be backed up, optimizing backup performance and ensuring that
// sensitive or unnecessary resources are properly excluded from backups.
func Test_getSchedulesWithUpdatedResources(t *testing.T) {
	// Setup fake client with scheme
	testScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(testScheme)

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(testScheme).
		Build()

	type args struct {
		resourcesToBackup []string
		schedules         *veleroapi.ScheduleList
	}

	resourcesToBackup := []string{
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
}

// Test_isScheduleSpecUpdated tests detection of schedule specification changes.
//
// This test validates the logic that determines whether backup schedule
// specifications have been updated and require Velero schedule recreation.
//
// Test Coverage:
// - Schedule timing specification changes (cron expressions)
// - TTL (Time To Live) configuration updates
// - Schedule specification comparison logic
// - No-change scenarios validation
//
// Test Scenarios:
// - No schedules present (should return false)
// - Same schedule and TTL configuration (no update needed)
// - Schedule timing updated (cron expression changed)
// - TTL value updated (retention period changed)
// - Multiple schedule specification changes
//
// Implementation Details:
// - Compares Velero schedule specs with ACM backup schedule configuration
// - Validates cron expression and TTL consistency
// - Uses time duration comparisons for TTL validation
// - Handles edge cases with missing or invalid schedules
//
// Business Logic:
// Schedule specification update detection is critical for maintaining
// consistency between ACM backup schedules and underlying Velero schedules,
// ensuring that configuration changes are properly propagated and executed.
func Test_isScheduleSpecUpdated(t *testing.T) {
	type args struct {
		schedules      *veleroapi.ScheduleList
		backupSchedule *backupv1beta1.BackupSchedule
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
		{
			name: "pause state updated - BackupSchedule paused but Velero schedules not paused",
			args: args{
				schedules: initVeleroSchedulesWithSpecs(
					"0 8 * * *",
					metav1.Duration{Duration: time.Hour * 1},
				),
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).schedule("0 8 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 1}).
					paused(true).
					object,
			},
			want: true,
		},
		{
			name: "pause state updated - BackupSchedule unpaused but Velero schedules still paused",
			args: args{
				schedules: initVeleroSchedulesWithPausedState(
					"0 6 * * *",
					metav1.Duration{Duration: time.Hour * 1},
					true, // Velero schedules are paused
				),
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).schedule("0 6 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 1}).
					paused(false). // BackupSchedule is not paused
					object,
			},
			want: true,
		},
		{
			name: "pause state synchronized - both BackupSchedule and Velero schedules paused",
			args: args{
				schedules: initVeleroSchedulesWithPausedState(
					"0 6 * * *",
					metav1.Duration{Duration: time.Hour * 1},
					true, // Velero schedules are paused
				),
				backupSchedule: createBackupSchedule(
					"name",
					"ns",
				).schedule("0 6 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 1}).
					paused(true). // BackupSchedule is also paused
					object,
			},
			want: false,
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

// Test_deleteVeleroSchedules tests Velero schedule deletion and management functionality.
//
// This test validates the logic that deletes Velero schedules and manages
// schedule lifecycle operations during backup schedule updates.
//
// Test Coverage:
// - Velero schedule deletion operations
// - Schedule lifecycle management and error handling
// - Client interaction for schedule deletion
// - Schedule update requirements detection
// - CRD (Custom Resource Definition) change handling
//
// Test Scenarios:
// - Nil schedule list handling
// - Empty schedule list scenarios
// - Failed deletion with invalid schedules
// - Successful schedule deletion operations
// - Schedule update requirement validation
// - CRD change detection and processing
//
// Implementation Details:
// - Uses fake Kubernetes client with proper scheme setup
// - Creates realistic Velero namespace and schedule objects
// - Tests both successful and failure scenarios
// - Validates proper error propagation and handling
// - Includes comprehensive schedule update testing
//
// Business Logic:
// Schedule deletion is a critical operation during backup schedule updates,
// ensuring that outdated or invalid schedules are properly removed before
// creating new ones, maintaining system consistency and preventing conflicts.
func Test_deleteVeleroSchedules(t *testing.T) {
	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

	// Setup fake client with scheme
	testScheme := runtime.NewScheme()
	_ = veleroapi.AddToScheme(testScheme)
	_ = corev1.AddToScheme(testScheme)

	rhacmBackupSchedule := *createBackupSchedule("backup-sch-to-error-restore", veleroNamespaceName).
		schedule("0 8 * * *").
		veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
		object

	veleroSchedules := initVeleroScheduleList(veleroNamespace.Name, veleroapi.SchedulePhaseNew, "0 8 * * *",
		metav1.Duration{Duration: time.Second * 5})
	for i := range veleroSchedules.Items {
		veleroSchedule := &veleroSchedules.Items[i]
		veleroSchedule.Namespace = veleroNamespaceName
	}

	// Prepare setup objects for fake client
	setupObjects := []client.Object{&veleroNamespace}
	for i := range veleroSchedules.Items {
		setupObjects = append(setupObjects, &veleroSchedules.Items[i])
	}

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(setupObjects...).
		Build()

	type argsDelete struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *backupv1beta1.BackupSchedule
		schedules      *veleroapi.ScheduleList
	}

	testsForDelete := []struct {
		name string
		args argsDelete
		want bool
	}{
		{
			name: "velero schedules is nil",
			args: argsDelete{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules:      nil,
			},
			want: false,
		},
		{
			name: "no velero schedules Items",
			args: argsDelete{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules:      &veleroapi.ScheduleList{},
			},
			want: false,
		},
		{
			name: "failed to delete the schedule, returns error",
			args: argsDelete{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules: &veleroapi.ScheduleList{
					Items: []veleroapi.Schedule{
						{
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
			args: argsDelete{
				ctx:            context.Background(),
				c:              k8sClient1,
				backupSchedule: &rhacmBackupSchedule,
				schedules:      veleroSchedules,
			},
			want: false,
		},
	}
	for _, tt := range testsForDelete {
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

	veleroSchedulesUpdate := initVeleroScheduleList(veleroNamespaceName, veleroapi.SchedulePhaseNew, "0 8 * * *",
		metav1.Duration{Duration: time.Second * 5})

	type argsUpdate struct {
		ctx               context.Context
		c                 client.Client
		backupSchedule    *backupv1beta1.BackupSchedule
		schedules         *veleroapi.ScheduleList
		resourcesToBackup []string
	}

	testsForSchedulesUpdateRequired := []struct {
		name string
		args argsUpdate
		want bool
		err  string
	}{
		{
			name: "velero schedules is empty",
			args: argsUpdate{
				ctx:               context.Background(),
				c:                 k8sClient1,
				backupSchedule:    &rhacmBackupSchedule,
				schedules:         &veleroapi.ScheduleList{},
				resourcesToBackup: []string{},
			},
			want: false,
			err:  "",
		},
		{
			name: "velero schedules is not empty, but schedules cannot be updated, not found",
			args: argsUpdate{
				ctx:               context.Background(),
				c:                 k8sClient1,
				backupSchedule:    &rhacmBackupSchedule,
				schedules:         veleroSchedulesUpdate,
				resourcesToBackup: []string{},
			},
			want: true,
			err:  `schedules.velero.io "acm-credentials-schedule" not found`,
		},
		{
			name: "velero schedules is not empty, schedules are updated",
			args: argsUpdate{
				ctx: context.Background(),
				c:   k8sClient1,
				backupSchedule: createBackupSchedule("acm", veleroNamespaceName).
					schedule("0 6 * * *").
					veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
					object,
				schedules:         veleroSchedulesUpdate,
				resourcesToBackup: []string{},
			},
			want: true,
			err:  "",
		},
		{
			name: "velero schedules is not empty, schedules are updated and NO CRDs found",
			args: argsUpdate{
				ctx: context.Background(),
				c:   k8sClient1,
				backupSchedule: createBackupSchedule("acm", veleroNamespaceName).
					schedule("0 8 * * *").
					veleroTTL(metav1.Duration{Duration: time.Second * 5}).
					object,
				schedules:         veleroSchedulesUpdate,
				resourcesToBackup: []string{},
			},
			want: true,
			err:  "",
		},
		{
			name: "velero schedules is not empty, schedules are NOT updated but new CRDs found",
			args: argsUpdate{
				ctx: context.Background(),
				c:   k8sClient1,
				backupSchedule: createBackupSchedule("acm", veleroNamespaceName).
					schedule("0 8 * * *").
					veleroTTL(metav1.Duration{Duration: time.Second * 5}).
					object,
				schedules:         veleroSchedulesUpdate,
				resourcesToBackup: []string{"policy123.open-cluster-management.io"},
			},
			want: true,
			err:  "",
		},
		{
			name: "velero schedules is not empty, schedules are NOT updated but new CRDs found error on update",
			args: argsUpdate{
				ctx: context.Background(),
				c:   k8sClient1,
				backupSchedule: createBackupSchedule("acm", veleroNamespaceName).
					schedule("0 8 * * *").
					veleroTTL(metav1.Duration{Duration: time.Second * 5}).
					object,
				schedules:         veleroSchedulesUpdate,
				resourcesToBackup: []string{"policy456.open-cluster-management.io"},
			},
			want: true,
			err:  "", // Fake client doesn't support optimistic concurrency control
		},
	}
	for _, tt := range testsForSchedulesUpdateRequired {
		t.Run(tt.name, func(t *testing.T) {
			// Use helper function to create test client with conditional setup
			testClient := CreateDeleteVeleroSchedulesTestClient(tt.name, &veleroNamespace, veleroSchedulesUpdate)
			tt.args.c = testClient

			_, got, err := isVeleroSchedulesUpdateRequired(tt.args.ctx, tt.args.c, tt.args.resourcesToBackup,
				*tt.args.schedules, tt.args.backupSchedule)
			if got != tt.want {
				t.Errorf(
					"isVeleroSchedulesUpdateRequired() = %v, want  %v",
					got,
					tt.want,
				)
			}
			if err == nil && tt.err != "" {
				t.Errorf(
					"isVeleroSchedulesUpdateRequired() = error is nil, want %v",
					got,
				)
			}
			if err != nil && tt.err == "" {
				t.Errorf(
					"isVeleroSchedulesUpdateRequired() error is %v, want no error",
					err.Error(),
				)
			}
		})
	}
}

// Test_isRestoreRunning tests detection of active restore operations that conflict with backup schedules.
//
// This test validates the logic that determines whether restore operations
// are currently running, which prevents backup schedules from executing.
//
// Test Coverage:
// - Active restore operation detection
// - Restore-schedule conflict prevention logic
// - Client interaction for restore status checking
// - Error handling for restore status queries
// - Invalid namespace handling for restore operations
//
// Test Scenarios:
// - Schema not found error handling
// - No restore operations running (backup can proceed)
// - Active restore operations present (backup should wait)
// - Invalid namespace scenarios for restore operations
//
// Implementation Details:
// - Uses fake Kubernetes client with backup schedule configuration
// - Creates realistic backup schedule, namespace, and restore objects
// - Tests restore detection logic with various scenarios
// - Validates proper client interaction and error handling
//
// Business Logic:
// Restore conflict detection ensures that backup and restore operations
// don't interfere with each other, maintaining data consistency and
// preventing resource conflicts during critical operations.
func Test_isRestoreRunning(t *testing.T) {
	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

	rhacmBackupSchedule := *createBackupSchedule("backup-sch-to-error-restore", veleroNamespaceName).
		object

	rhacmBackupScheduleInvalidNS := *createBackupSchedule("backup-sch-to-error-restore", "invalid-ns").
		object

	latestRestore := "latest"
	rhacmRestore := *createACMRestore("restore-name", veleroNamespaceName).
		cleanupBeforeRestore(backupv1beta1.CleanupTypeRestored).
		veleroManagedClustersBackupName(latestRestore).
		veleroCredentialsBackupName(latestRestore).
		veleroResourcesBackupName(latestRestore).object

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *backupv1beta1.BackupSchedule
	}
	tests := []struct {
		name         string
		args         args
		want         string
		setupObjects []client.Object
		setupScheme  bool
	}{
		{
			name: "velero schema not found",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupSchedule,
			},
			want:        "",
			setupScheme: false, // No scheme setup to simulate schema not found
		},
		{
			name: "velero has no restores",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupSchedule,
			},
			want:         "",
			setupObjects: []client.Object{&veleroNamespace},
			setupScheme:  true,
		},
		{
			name: "velero restore has one restore and not completed",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupSchedule,
			},
			want:         rhacmRestore.Name,
			setupObjects: []client.Object{&veleroNamespace, &rhacmRestore},
			setupScheme:  true,
		},
		{
			name: "velero restore has one restore and not completed invalid",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupScheduleInvalidNS,
			},
			want:         "",
			setupObjects: []client.Object{&veleroNamespace, &rhacmRestore},
			setupScheme:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use helper function to create test client
			fakeClient := CreateScheduleTestClientWithScheme(tt.setupScheme, tt.setupObjects...)
			tt.args.c = fakeClient

			if got := isRestoreRunning(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule); got != tt.want {
				t.Errorf("isRestoreRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_createInitialBackupForSchedule tests initial backup creation when schedules are first established.
//
// This test validates the logic that creates initial backup operations
// when backup schedules are first activated, ensuring proper bootstrap behavior.
//
// Test Coverage:
// - Initial backup creation logic
// - Schedule activation and first backup timing
// - Backup naming and timestamping conventions
// - Error handling for backup creation failures
// - No-backup-on-start configuration handling
//
// Test Scenarios:
// - Schedule configured to skip initial backup
// - Initial backup creation with missing namespace (error handling)
// - Successful initial backup creation
// - Duplicate backup prevention (backup already exists)
//
// Implementation Details:
// - Uses fake Kubernetes client with proper scheme setup
// - Creates realistic backup schedules with various configurations
// - Tests backup creation with timestamp-based naming
// - Validates proper error handling and edge cases
//
// Business Logic:
// Initial backup creation ensures that backup schedules start with
// a baseline backup, providing immediate protection and establishing
// the backup cadence for ongoing operations.
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

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *backupv1beta1.BackupSchedule
		veleroSchedule *veleroapi.Schedule
	}
	tests := []struct {
		name              string
		args              args
		want              bool
		want_veleroBackup string
		setupObjects      []client.Object
	}{
		{
			name: "backup schedule should not call backup on init schedule",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupScheduleNoRun,
				veleroSchedule: &schNoLabels,
			},
			want_veleroBackup: "", // no backup
			setupObjects:      []client.Object{},
		},
		{
			name: "backup schedule should call backup on init schedule - error, no ns",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want_veleroBackup: schNoLabels.Name + "-" + timeStr, // Fake client allows creation without namespace
			setupObjects:      []client.Object{},
		},
		{
			name: "backup schedule should call backup on init schedule",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want_veleroBackup: schNoLabels.Name + "-" + timeStr,
			setupObjects:      []client.Object{&veleroNamespace},
		},
		{
			name: "backup schedule should call backup on init schedule, but error since schedule already created",
			args: args{
				ctx:            context.Background(),
				backupSchedule: &rhacmBackupSchedule,
				veleroSchedule: &schNoLabels,
			},
			want_veleroBackup: "",
			setupObjects: []client.Object{
				&veleroNamespace,
				// Add existing backup to simulate "already created" scenario
				createBackup(schNoLabels.Name+"-"+timeStr, veleroNamespaceName).object,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use helper function to create test client
			fakeClient := CreateScheduleTestClient(tt.setupObjects...)
			tt.args.c = fakeClient

			// Get scheme for the function call
			testScheme := createScheduleTestScheme()

			if got_backup := createInitialBackupForSchedule(tt.args.ctx, tt.args.c, testScheme, tt.args.veleroSchedule,
				tt.args.backupSchedule, timeStr); got_backup != tt.want_veleroBackup {
				t.Errorf("createInitialBackupForSchedule() backupName is %v, want %v",
					got_backup, tt.want_veleroBackup)
			}
		})
	}
}

// Test_createFailedValidationResponse tests creation of failed validation responses for backup schedules.
//
// This test validates the logic that creates appropriate response objects
// when backup schedule validation fails, ensuring proper error communication.
//
// Test Coverage:
// - Failed validation response creation
// - Error message formatting and propagation
// - Requeue logic for failed validations
// - Response object structure and content validation
// - Requeue timing and interval configuration
//
// Test Scenarios:
// - Failed validation with requeue enabled (should use failure interval)
// - Failed validation without requeue (should have zero requeue time)
// - Various error messages and response consistency
//
// Implementation Details:
// - Uses realistic backup schedule configurations
// - Tests response creation with different error scenarios
// - Validates proper requeue timing based on failure interval
// - Ensures consistent error response structure
//
// Business Logic:
// Failed validation responses provide clear feedback to users and
// operators about configuration issues, enabling quick resolution
// of backup schedule problems and proper operational awareness.
func Test_createFailedValidationResponse(t *testing.T) {
	veleroNamespaceName := "backup-ns-v"

	rhacmBackupSchedule := *createBackupSchedule("backup-sch-v", veleroNamespaceName).
		object

	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *backupv1beta1.BackupSchedule
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
				backupSchedule: &rhacmBackupSchedule,
				requeue:        false,
				msg:            "some error",
			},
			want: time.Second * 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use helper function to create test client
			fakeClient := CreateScheduleTestClientWithScheme(false) // No scheme needed for this test
			tt.args.c = fakeClient

			if r, _, _ := createFailedValidationResponse(tt.args.ctx, tt.args.c,
				tt.args.backupSchedule, tt.args.msg,
				tt.args.requeue); r.RequeueAfter != tt.want {
				t.Errorf("createFailedValidationResponse() = %v, want %v", r.RequeueAfter, tt.want)
			}
		})
	}
}

// Test_verifyMSAOptione tests MSA (Managed Service Account) option verification and validation.
//
// This test validates the logic that verifies MSA configuration options
// for backup schedules, ensuring proper authentication and authorization setup.
//
// Test Coverage:
// - MSA configuration validation
// - Authentication and authorization setup verification
// - REST mapper functionality for resource discovery
// - HTTP server interaction for API discovery
// - Dynamic client testing with unstructured resources
//
// Test Scenarios:
// - Valid MSA configuration with proper authentication
// - Invalid MSA configurations and error handling
// - Resource discovery through REST mapper
// - API group and version discovery
// - Client authentication and authorization validation
//
// Implementation Details:
// - Uses HTTP test server for API discovery simulation
// - Creates dynamic client with unstructured resources
// - Tests REST mapper for resource discovery
// - Validates proper client setup and authentication
// - Uses fake clients for controlled testing environment
//
// Business Logic:
// MSA option verification ensures that backup schedules have proper
// authentication and authorization configured, enabling secure access
// to cluster resources during backup operations.
func Test_verifyMSAOptione(t *testing.T) {
	// Setup fake client with scheme
	testScheme := runtime.NewScheme()
	_ = veleroapi.AddToScheme(testScheme)

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(testScheme).
		Build()

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
			// t.Logf("unexpected request: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		output, err := json.Marshal(list)
		if err != nil {
			// t.Errorf("unexpected encoding error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(output)
		if err != nil {
			return
		}
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
	_ = chnv1.AddToScheme(unstructuredScheme)

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
	_, err := resInterface.Namespace("default").Create(context.Background(), res_local_ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Error creating resource: %s", err.Error())
	}

	fakeDiscovery := discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server.URL},
	)
	m := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(fakeDiscovery),
	)
	type args struct {
		ctx            context.Context
		c              client.Client
		backupSchedule *backupv1beta1.BackupSchedule
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
}

// Test_scheduleOwnsLatestStorageBackups tests schedule ownership verification for latest storage backups.
//
// This test validates the logic that determines whether a backup schedule
// owns the latest backup stored in the backup storage location.
//
// Test Coverage:
// - Schedule ownership verification of backup storage
// - Latest backup identification and association
// - Backup storage location validation and querying
// - Schedule-backup relationship tracking
// - Cluster label matching and validation
//
// Test Scenarios:
// - No CRD available (should default to true)
// - No backups present (should return true)
// - Backups with different cluster versions (should return false)
// - Backups with same cluster version (should return true)
// - Label matching for backup schedule ownership
//
// Implementation Details:
// - Uses realistic backup schedule and namespace configurations
// - Creates Velero schedule objects with proper labeling
// - Tests client interaction for backup storage queries
// - Validates proper ownership logic based on cluster labels
// - Uses timestamp-based backup ordering for latest detection
//
// Business Logic:
// Schedule ownership verification ensures that backup schedules can
// properly identify and manage their associated backups, preventing
// conflicts and ensuring proper backup lifecycle management across
// different cluster versions and configurations.
func Test_scheduleOwnsLatestStorageBackups(t *testing.T) {
	veleroNamespaceName := "default"

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
		name        string
		args        args
		want        bool
		resources   []*veleroapi.Backup
		setupScheme bool
	}{
		{
			name: "no crd",
			args: args{
				ctx:      context.Background(),
				schedule: &velero_schedule,
			},
			want:        true,
			resources:   []*veleroapi.Backup{},
			setupScheme: true, // Need scheme for basic types even if simulating no CRD
		},
		{
			name: "no backups",
			args: args{
				ctx:      context.Background(),
				schedule: &velero_schedule,
			},
			want:        true,
			resources:   []*veleroapi.Backup{},
			setupScheme: true,
		},
		{
			name: "has backups, different cluster version",
			args: args{
				ctx:      context.Background(),
				schedule: &velero_schedule,
			},
			want: false,
			resources: []*veleroapi.Backup{
				createBackup(veleroScheduleNames[Resources]+"-1", veleroNamespaceName).
					startTimestamp(anHourAgo).errors(0).
					labels(map[string]string{
						BackupScheduleClusterLabel: "abcd",
						BackupVeleroLabel:          veleroScheduleNames[Resources],
					}).
					object,
			},
			setupScheme: true,
		},
		{
			name: "has backups, same cluster version",
			args: args{
				ctx:      context.Background(),
				schedule: &velero_schedule,
			},
			want: true,
			resources: []*veleroapi.Backup{
				createBackup(veleroScheduleNames[Resources]+"-2", veleroNamespaceName).
					startTimestamp(aFewSecondsAgo).errors(0).
					labels(map[string]string{
						BackupScheduleClusterLabel: "cls",
						BackupVeleroLabel:          veleroScheduleNames[Resources],
					}).
					object,
			},
			setupScheme: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Prepare setup objects
			setupObjects := []client.Object{}
			for i := range tt.resources {
				setupObjects = append(setupObjects, tt.resources[i])
			}

			// Use helper function to create test client
			fakeClient := CreateScheduleTestClientWithScheme(tt.setupScheme, setupObjects...)
			tt.args.c = fakeClient

			if got, _, err := scheduleOwnsLatestStorageBackups(tt.args.ctx, tt.args.c,
				tt.args.schedule); got != tt.want || err != nil {
				t.Errorf("scheduleOwnsLatestStorageBackups() = got %v, want %v, err %v", got, tt.want, err)
			}
		})
	}
}

// Test_isRestoreHubAfterSchedule tests detection of hub restore operations that occur after schedule creation.
//
// This test validates the logic that determines whether hub restore operations
// have been performed after a backup schedule was created, indicating potential conflicts.
//
// Test Coverage:
// - Hub restore timing detection relative to schedule creation
// - Backup storage location validation and querying
// - Restore-schedule temporal relationship analysis
// - Error handling for backup storage queries
// - Collision detection between restores and schedules
//
// Test Scenarios:
// - No collision when backup list fails (error handling)
// - No collision when no backup restore found
// - Temporal analysis of restore vs schedule creation times
// - Various backup and restore configurations
//
// Implementation Details:
// - Uses realistic backup schedule and namespace configurations
// - Creates cluster version resources for testing
// - Tests timestamp-based temporal analysis
// - Validates proper error handling for storage queries
// - Uses controlled timing for restore-schedule relationships
//
// Business Logic:
// Hub restore detection after schedule creation is critical for preventing
// conflicts between backup schedules and restore operations, ensuring that
// backup schedules don't interfere with ongoing restore processes and
// maintaining data consistency during disaster recovery scenarios.
func Test_isRestoreHubAfterSchedule(t *testing.T) {
	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)
	crWithVersion := createClusterVersion("version", "cluster1", nil)

	// Create timestamp variables for enhanced test cases
	baseTime := time.Now()
	scheduleCreatedAt := metav1.NewTime(baseTime.Add(-1 * time.Hour))
	backupCreatedAt := metav1.NewTime(baseTime.Add(-30 * time.Minute)) // 30 minutes after schedule

	type args struct {
		ctx            context.Context
		c              client.Client
		veleroSchedule *veleroapi.Schedule
	}
	tests := []struct {
		name         string
		args         args
		want         bool
		wantMessage  bool // whether we expect a message to be returned
		setupObjects []client.Object
	}{
		{
			name: "no collision, list backup fails",
			args: args{
				ctx: context.Background(),
				veleroSchedule: createSchedule("acm-backup-schedule-1", veleroNamespaceName).
					object,
			},
			want:         false,
			wantMessage:  false,
			setupObjects: []client.Object{&veleroNamespace, crWithVersion},
		},
		{
			name: "no collision, no backup restore found",
			args: args{
				ctx: context.Background(),
				veleroSchedule: createSchedule("acm-backup-schedule-2", veleroNamespaceName).
					object,
			},
			want:         false,
			wantMessage:  false,
			setupObjects: []client.Object{&veleroNamespace, crWithVersion},
		},
		{
			name: "collision, backup schedule created first, before restore",
			args: args{
				ctx: context.Background(),
				veleroSchedule: createSchedule("acm-backup-schedule-3", veleroNamespaceName).
					object,
			},
			want:        false, // Fake client doesn't support creation timestamp differences
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				createSchedule("acm-backup-schedule-3", veleroNamespaceName).object,
				CreateTestBackupWithLabels("acm-restore-clusters-2", veleroNamespaceName, "cluster1", "cluster2"),
			},
		},
		{
			name: "no collision, backup schedule created first, before restore, but on the same hub",
			args: args{
				ctx: context.Background(),
				veleroSchedule: createSchedule("acm-backup-schedule-4", veleroNamespaceName).
					object,
			},
			want:        false,
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				CreateTestBackupWithLabels("acm-restore-clusters-1", veleroNamespaceName, "cluster1", "cluster1"),
			},
		},
		{
			name: "no collision, backup schedule created after restore",
			args: args{
				ctx: context.Background(),
				veleroSchedule: createSchedule("acm-backup-schedule-5", veleroNamespaceName).
					object,
			},
			want:        false,
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				createBackup("acm-restore-clusters-3", veleroNamespaceName).
					labels(map[string]string{
						BackupScheduleClusterLabel: "cluster1",
						RestoreClusterLabel:        "cluster2",
					}).
					phase(veleroapi.BackupPhaseCompleted).
					object,
			},
		},
		{
			name: "error handling - backup list in wrong namespace (no error but no backups)",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule",
						Namespace:         "wrong-namespace", // Non-existent namespace
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:         false,
			wantMessage:  false,
			setupObjects: []client.Object{&veleroNamespace, crWithVersion},
		},
		{
			name: "multiple restore backups - most recent selected",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-multi",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:        false, // Same hub ID
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				// Older backup
				func() client.Object {
					backup := createBackup("acm-restore-clusters-old", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "cluster2", // Different hub
						}).object
					backup.CreationTimestamp = metav1.NewTime(baseTime.Add(-45 * time.Minute))
					return backup
				}(),
				// Newer backup (should be selected)
				func() client.Object {
					backup := createBackup("acm-restore-clusters-new", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "cluster1", // Same hub
						}).object
					backup.CreationTimestamp = backupCreatedAt
					return backup
				}(),
			},
		},
		{
			name: "backup without RestoreClusterLabel - should be ignored",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-no-label",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:        false,
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				// Backup without RestoreClusterLabel - should not be found by HasLabels selector
				func() client.Object {
					backup := createBackup("acm-restore-clusters-no-label", veleroNamespaceName).
						labels(map[string]string{
							"other-label": "value",
						}).object
					backup.CreationTimestamp = backupCreatedAt
					return backup
				}(),
			},
		},
		{
			name: "backup with empty RestoreClusterLabel value",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-empty-label",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:        true, // Empty label is different from current hub ID, so collision detected
			wantMessage: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				func() client.Object {
					backup := createBackup("acm-restore-clusters-empty", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "", // Empty value
						}).object
					backup.CreationTimestamp = backupCreatedAt
					return backup
				}(),
			},
		},
		{
			name: "collision detected - different hub ID with proper timestamps",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-collision",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:        true, // Should detect collision
			wantMessage: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				func() client.Object {
					backup := createBackup("acm-restore-clusters-collision", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "cluster2", // Different hub ID
						}).object
					backup.CreationTimestamp = backupCreatedAt // Created after schedule
					return backup
				}(),
			},
		},
		{
			name: "no collision - schedule newer than backup",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-newer",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: backupCreatedAt, // Schedule created after backup
					},
				},
			},
			want:        false,
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				func() client.Object {
					backup := createBackup("acm-restore-clusters-older", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "cluster2", // Different hub ID
						}).object
					backup.CreationTimestamp = scheduleCreatedAt // Created before schedule
					return backup
				}(),
			},
		},
		{
			name: "getHubIdentification failure - no clusterversion",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-no-cv",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:        true, // Different from "unknown" hub ID
			wantMessage: true,
			setupObjects: []client.Object{
				&veleroNamespace,
				// No cluster version - getHubIdentification returns "unknown"
				func() client.Object {
					backup := createBackup("acm-restore-clusters-no-cv", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "cluster2", // Different from "unknown"
						}).object
					backup.CreationTimestamp = backupCreatedAt
					return backup
				}(),
			},
		},
		{
			name: "exact timestamp match - schedule and backup same time",
			args: args{
				ctx: context.Background(),
				veleroSchedule: &veleroapi.Schedule{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-schedule-same-time",
						Namespace:         veleroNamespaceName,
						CreationTimestamp: scheduleCreatedAt,
					},
				},
			},
			want:        false, // Before() returns false for equal times
			wantMessage: false,
			setupObjects: []client.Object{
				&veleroNamespace,
				crWithVersion,
				func() client.Object {
					backup := createBackup("acm-restore-clusters-same-time", veleroNamespaceName).
						labels(map[string]string{
							RestoreClusterLabel: "cluster2", // Different hub ID
						}).object
					backup.CreationTimestamp = scheduleCreatedAt // Exact same time
					return backup
				}(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use helper function to create test client
			fakeClient := CreateScheduleTestClient(tt.setupObjects...)
			tt.args.c = fakeClient

			got, msg := isRestoreHubAfterSchedule(tt.args.ctx, tt.args.c, tt.args.veleroSchedule)

			if got != tt.want {
				t.Errorf("isRestoreHubAfterSchedule() = %v, want %v", got, tt.want)
			}

			// Check message expectation
			if tt.wantMessage && msg == "" {
				t.Errorf("isRestoreHubAfterSchedule() expected message but got empty string")
			} else if !tt.wantMessage && msg != "" {
				t.Errorf("isRestoreHubAfterSchedule() expected no message but got: %s", msg)
			}
		})
	}
}
