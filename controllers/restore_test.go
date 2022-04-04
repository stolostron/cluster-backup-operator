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
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	dynamicfake "k8s.io/client-go/dynamic/fake"
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
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: false,
		},
		{
			name: "No backup name",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups: true,
						CleanupBeforeRestore:      v1beta1.CleanupTypeNone,
					},
				},
			},
			want: false,
		},
		{
			name: "Credentials should be set to skip or latest",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &backupName,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Resources should be set to latest",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: false,
		},
		{
			name: "InValid config, no sync",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Valid config",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
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
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: true,
		},
		{
			name: "No backup name",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
				},
			},
			want: true,
		},
		{
			name: "Do not skip all",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Managed clusters name is not skip",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &latestBackup,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Resources is not skip",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
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

func Test_deleteDynamicResource(t *testing.T) {

	res_local_ns := &unstructured.Unstructured{}
	res_local_ns.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "local-cluster",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_default := &unstructured.Unstructured{}
	res_default.SetUnstructuredContent(map[string]interface{}{
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

	res_exclude_from_backup := &unstructured.Unstructured{}
	res_exclude_from_backup.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "default",
			"labels": map[string]interface{}{
				"velero.io/exclude-from-backup": "true",
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_global := &unstructured.Unstructured{}
	res_global.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name": "channel-new",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), res_local_ns)

	targetGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io", Version: "v1", Kind: "Channel"}
	targetGVR := targetGVK.GroupVersion().WithResource("somecrs")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}

	targetMappingGlobal := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeRoot}

	var monboDBResource = schema.GroupVersionResource{Group: "apps.open-cluster-management.io", Version: "v1", Resource: "channel"}

	resInterface := dynClient.Resource(monboDBResource)

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	type args struct {
		ctx                context.Context
		mapping            *meta.RESTMapping
		dr                 dynamic.NamespaceableResourceInterface
		resource           unstructured.Unstructured
		deleteOptions      v1.DeleteOptions
		excludedNamespaces []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Delete local cluster resource",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_local_ns,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want: false,
		},
		{
			name: "Delete default resource",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want: true,
		},
		{
			name: "Delete default resource with ns excluded",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"default"},
			},
			want: false,
		},
		{
			name: "Delete default resource, excluded from backup",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_exclude_from_backup,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want: false,
		},
		{
			name: "Delete global resource",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMappingGlobal,
				dr:                 resInterface,
				resource:           *res_global,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := deleteDynamicResource(tt.args.ctx,
				tt.args.mapping,
				tt.args.dr,
				tt.args.resource,
				tt.args.deleteOptions,
				tt.args.excludedNamespaces); got != tt.want {
				t.Errorf("deleteDynamicResource() = %v, want %v", got, tt.want)
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
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						RestoreSyncInterval:             v1.Duration{Duration: time.Minute * 15},
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseEnabled,
					},
				},
				err: nil,
			},
			want: nil,
		},
		{
			name: "Skip restore again",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseFinished,
					},
				},
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
		name string
		args args
		want v1beta1.RestorePhase
	}{
		{
			name: "Restore list empty and skip all, return finished phase",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						RestoreSyncInterval:             v1.Duration{Duration: time.Minute * 15},
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseRunning,
					},
				},
				restoreList: nil,
			},
			want: v1beta1.RestorePhaseFinished,
		},
		{
			name: "Restore list empty and NOT skip all, return finished RestorePhaseStarted",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						RestoreSyncInterval:             v1.Duration{Duration: time.Minute * 15},
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &latestBackupStr,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseRunning,
					},
				},
				restoreList: nil,
			},
			want: v1beta1.RestorePhaseStarted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if phase := setRestorePhase(tt.args.restoreList, tt.args.restore); phase != tt.want {
				t.Errorf("setRestorePhase() = %v, want %v", phase, tt.want)
			}
		})
	}
}
