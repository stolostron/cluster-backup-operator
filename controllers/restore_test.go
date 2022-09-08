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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

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
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).syncRestoreWithNewBackups(true).
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
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).
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
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).
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
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).
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
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).
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
			"name":      "channel-new-default",
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_default_with_finalizer := &unstructured.Unstructured{}
	res_default_with_finalizer.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":       "channel-new-default-with-finalizer",
			"namespace":  "default",
			"finalizers": []interface{}{"aaa", "bbb"},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_default_notfound := &unstructured.Unstructured{}
	res_default_notfound.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new-default-not-found",
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
			"name":      "channel-new-default-excluded",
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
			"name": "channel-new-global",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_global_with_finalizer := &unstructured.Unstructured{}
	res_global_with_finalizer.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":       "channel-new-global-with-finalizer",
			"finalizers": []interface{}{"aaa", "bbb"},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_global_notfound := &unstructured.Unstructured{}
	res_global_notfound.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name": "channel-new-global-not-found",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	unstructuredScheme := runtime.NewScheme()
	chnv1.AddToScheme(unstructuredScheme)

	dynClient := dynamicfake.NewSimpleDynamicClient(unstructuredScheme,
		res_default,
		res_default_with_finalizer,
		res_global,
		res_global_with_finalizer,
		res_exclude_from_backup,
	)

	targetGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io", Version: "v1", Kind: "Channel"}
	targetGVR := targetGVK.GroupVersion().WithResource("channel")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}

	targetMappingGlobal := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeRoot}

	resInterface := dynClient.Resource(targetGVR)

	// create resources which should be found
	resInterface.Namespace("default").Create(context.Background(), res_default, v1.CreateOptions{})
	resInterface.Namespace("default").Create(context.Background(), res_default_with_finalizer, v1.CreateOptions{})
	resInterface.Create(context.Background(), res_global, v1.CreateOptions{})
	resInterface.Create(context.Background(), res_global_with_finalizer, v1.CreateOptions{})

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
		name        string
		args        args
		want        bool
		errMsgEmpty bool
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
			want:        false,
			errMsgEmpty: true,
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
			want:        true,
			errMsgEmpty: true,
		},
		{
			name: "Delete default resource with finalizer, should throw error since resource was deleted before finalizers patch",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default_with_finalizer,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete default resource NOT FOUND",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default_notfound,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete default resource with ns excluded",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default_notfound,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"default"},
			},
			want:        false,
			errMsgEmpty: true,
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
			want:        false,
			errMsgEmpty: true,
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
			want:        true,
			errMsgEmpty: true,
		},
		{
			name: "Delete global resource with finalizer, throws error since res is deleted before finalizers patch",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMappingGlobal,
				dr:                 resInterface,
				resource:           *res_global_with_finalizer,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{},
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete global resource NOT FOUND",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMappingGlobal,
				dr:                 resInterface,
				resource:           *res_global_notfound,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{},
			},
			want:        true,
			errMsgEmpty: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, msg := deleteDynamicResource(tt.args.ctx,
				tt.args.mapping,
				tt.args.dr,
				tt.args.resource,
				tt.args.deleteOptions,
				tt.args.excludedNamespaces); got != tt.want ||
				(tt.errMsgEmpty && len(msg) != 0) ||
				(!tt.errMsgEmpty && len(msg) == 0) {
				t.Errorf("deleteDynamicResource() = %v, want %v, emptyMsg=%v, msg=%v", got,
					tt.want, tt.errMsgEmpty, msg)
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
		name string
		args args
		want v1beta1.RestorePhase
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
			want: v1beta1.RestorePhaseFinished,
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

func Test_postRestoreActivation(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	ns1 := *createNamespace("managed1")
	ns2 := *createNamespace("managed2")
	autoImporSecretWithLabel := *createSecret(autoImportSecretName, "managed1",
		map[string]string{activateLabel: "true"}, nil, nil)
	autoImporSecretWithoutLabel := *createSecret(autoImportSecretName, "managed2",
		nil, nil, nil)

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	k8sClient1.Create(context.Background(), &ns1)
	k8sClient1.Create(context.Background(), &ns2)
	k8sClient1.Create(context.Background(), &autoImporSecretWithLabel)
	k8sClient1.Create(context.Background(), &autoImporSecretWithoutLabel)

	fourHoursAgo := "2022-07-26T11:25:34Z"
	nextTenHours := "2022-07-27T04:25:34Z"

	current, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")

	type args struct {
		ctx             context.Context
		secrets         []corev1.Secret
		managedClusters []clusterv1.ManagedCluster
		currentTime     time.Time
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "create NO auto import secrets, managed1 is active",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					*createManagedCluster("local-cluster").object,
					*createManagedCluster("test1").object,
					*createManagedCluster("managed1").clusterUrl("someurl").
						conditions([]metav1.Condition{
							v1.Condition{
								Status: v1.ConditionTrue,
								Type:   "ManagedClusterConditionAvailable",
							},
						}).object,
				},

				secrets: []corev1.Secret{
					*createSecret("auto-import", "local-cluster",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
					*createSecret("auto-import", "managed1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				}},
			want: []string{},
		},
		{
			name: "create NO auto import secret for managed1, it has no URL",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					*createManagedCluster("local-cluster").object,
					*createManagedCluster("test1").object,
					*createManagedCluster("managed1").emptyClusterUrl().
						conditions([]metav1.Condition{
							v1.Condition{
								Status: v1.ConditionFalse,
							},
						}).object,
				},
				secrets: []corev1.Secret{
					*createSecret("auto-import", "local-cluster",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
					*createSecret("auto-import", "managed1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
					*createSecret("auto-import", "managed2",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("aaa"),
						}),
					*createSecret("auto-import-pair", "managed1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				}},
			want: []string{},
		},
		{
			name: "create auto import for managed1 cluster",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					*createManagedCluster("local-cluster").object,
					*createManagedCluster("test1").object,
					*createManagedCluster("managed1").
						clusterUrl("someurl").
						conditions([]metav1.Condition{
							v1.Condition{
								Status: v1.ConditionFalse,
							},
						}).
						object,
					*createManagedCluster("managed2").clusterUrl("someurl").
						conditions([]metav1.Condition{
							v1.Condition{
								Status: v1.ConditionFalse,
							},
						}).
						object,
				},
				secrets: []corev1.Secret{
					*createSecret("auto-import", "local-cluster",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
					*createSecret("auto-import", "managed1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
					*createSecret("auto-import", "managed2",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				}},
			want: []string{"managed1"},
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postRestoreActivation(tt.args.ctx, k8sClient1,
				tt.args.secrets, tt.args.managedClusters, tt.args.currentTime); len(got) != len(tt.want) {
				t.Errorf("postRestoreActivation() returns = %v, want %v", got, tt.want)
			}
		})

		if index == len(tests)-1 {
			testEnv.Stop()
		}
	}

}

func Test_getVeleroBackupName(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

	backup := *createBackup("acm-credentials-cluster-schedule-20220922170041", veleroNamespaceName).
		labels(map[string]string{
			"velero.io/schedule-name":  "aa",
			BackupScheduleClusterLabel: "abcd",
		}).
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
			name: "found backup item ",
			args: args{
				ctx:              context.Background(),
				c:                k8sClient1,
				resourceType:     CredentialsCluster,
				backupName:       latestBackupStr,
				restoreNamespace: veleroNamespaceName,
			},
			want: backup.Name,
		},
	}

	for index, tt := range tests {

		if index == 1 {
			veleroapi.AddToScheme(scheme.Scheme)
		}
		if index == 2 {
			k8sClient1.Create(context.Background(), &veleroNamespace)
			k8sClient1.Create(context.Background(), &backup)
		}
		t.Run(tt.name, func(t *testing.T) {
			if name, _, _ := getVeleroBackupName(tt.args.ctx, tt.args.c,
				tt.args.restoreNamespace, tt.args.resourceType, tt.args.backupName); name != tt.want {
				t.Errorf("getVeleroBackupName() returns = %v, want %v", name, tt.want)
			}
		})
		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
	}

}

func Test_isNewBackupAvailable(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})

	skipRestore := "skip"
	latestBackup := "latest"

	veleroNamespaceName := "backup-ns"
	veleroNamespace := *createNamespace(veleroNamespaceName)

	passiveStr := "passive"
	backupName := "acm-credentials-schedule-20220922170041"
	restoreName := passiveStr + "-" + backupName

	backup := *createBackup(backupName, veleroNamespaceName).
		labels(map[string]string{
			"velero.io/schedule-name":  "aa",
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
		cleanupBeforeRestore(v1beta1.CleanupTypeAll).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(latestBackup).
		veleroResourcesBackupName(latestBackup).object

	restoreCredSameBackup := *createACMRestore(passiveStr, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeAll).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(latestBackup).
		veleroResourcesBackupName(latestBackup).
		veleroCredentialsBackupName(veleroRestore.Name).object

	restoreCredNewBackup := *createACMRestore(passiveStr, veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		cleanupBeforeRestore(v1beta1.CleanupTypeAll).
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
			v1beta1.AddToScheme(scheme.Scheme)
			veleroapi.AddToScheme(scheme.Scheme)
		}
		if index == 2 {
			k8sClient1.Create(tt.args.ctx, &veleroNamespace)
			k8sClient1.Create(tt.args.ctx, &backup)
			k8sClient1.Create(context.Background(), &veleroRestore)
		}
		if index == len(tests)-1 {
			// create restore
			k8sClient1.Create(context.Background(), &restoreCredNewBackup)
		}
		t.Run(tt.name, func(t *testing.T) {
			if got := isNewBackupAvailable(tt.args.ctx, tt.args.c,
				tt.args.restore, tt.args.resourceType); got != tt.want {
				t.Errorf("isNewBackupAvailable() returns = %v, want %v, %v", got, tt.want, tt.args.resourceType)
			}
		})
		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
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
							Phase: v1beta1.SchedulePhaseBackupCollision,
						},
					},
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

func Test_isValidCleanupOption(t *testing.T) {
	type args struct {
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "restore has invalid cleanup option",
			args: args{
				restore: createACMRestore("some-name", "ns").
					cleanupBeforeRestore("someWrongValue").object,
			},
			want: false,
		},
		{
			name: "restore has cleanup option, should cleanup ",
			args: args{
				restore: createACMRestore("some-name", "ns").
					cleanupBeforeRestore(v1beta1.CleanupTypeAll).object,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidCleanupOption(tt.args.restore); len(got) == 0 != tt.want {
				t.Errorf("isValidCleanupOption() = %v, want len of string is empty %v", len(got) == 0, tt.want)
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
		cleanupBeforeRestore(v1beta1.CleanupTypeAll).
		veleroManagedClustersBackupName(skipRestore).
		veleroCredentialsBackupName(skipRestore).
		veleroResourcesBackupName(backupName).object

	restoreCredsInvalidBackupName := *createACMRestore("restore1", veleroNamespaceName).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(v1.Duration{Duration: time.Minute * 20}).
		cleanupBeforeRestore(v1beta1.CleanupTypeAll).
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
			if _, _, got := retrieveRestoreDetails(tt.args.ctx, tt.args.c,
				tt.args.s, tt.args.restore, tt.args.restoreOnlyManagedClusters); (got == nil) != tt.want {
				t.Errorf("retrieveRestoreDetails() returns = %v, want %v", got == nil, tt.want)
			}
		})
		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
	}

}
