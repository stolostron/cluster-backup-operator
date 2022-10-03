/*
Copyright 2022.

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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

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
					*createSecret("auto-import-ignore-this-one", "managed1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
						}, nil),
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

func Test_executePostRestoreTasks(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	clusterv1.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx     context.Context
		c       client.Client
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "post activation  should NOT run now, managed clusters are skipped",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want: false,
		},
		{
			name: "post activation  should run now",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := executePostRestoreTasks(tt.args.ctx, tt.args.c,
				tt.args.restore); got != tt.want {
				t.Errorf("executePostRestoreTasks() returns = %v, want %v", got, tt.want)
			}
		})

	}
	testEnv.Stop()

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

func Test_cleanupDeltaResources(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx              context.Context
		c                client.Client
		restore          *v1beta1.Restore
		cleanupOnRestore bool
		restoreOptions   RestoreOptions
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "post activation  should NOT run now, state is enabled but cleanup is false",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseEnabled).object,
				cleanupOnRestore: false,
				restoreOptions:   RestoreOptions{},
			},
			want: false,
		},
		{
			name: "post activation  should run now, state is enabled AND cleanup is true",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseEnabled).object,
				cleanupOnRestore: true,
				restoreOptions:   RestoreOptions{},
			},
			want: true,
		},
		{
			name: "post activation should run now, state is Finished - and cleanup is false, not counting",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
				cleanupOnRestore: false,
				restoreOptions:   RestoreOptions{},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanupDeltaResources(tt.args.ctx, tt.args.c,
				tt.args.restore, tt.args.cleanupOnRestore, tt.args.restoreOptions); got != tt.want {
				t.Errorf("cleanupDeltaResources() returns = %v, want %v", got, tt.want)
			}
		})

	}
	testEnv.Stop()

}

func Test_getBackupInfoFromRestore(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	namespace := "ns"
	backupName := "passive-sync-2-acm-credentials-schedule-20220929220007"
	validRestoreName := "restore-acm-passive-sync-2-acm-credentials-schedule-2022092922123"
	validRestoreNameWBackup := "restore-acm-" + backupName

	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	corev1.AddToScheme(scheme1)

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx         context.Context
		c           client.Client
		restoreName string
		namespace   string
	}
	tests := []struct {
		name           string
		args           args
		wantBackupName string
	}{
		{
			name: "restore name is empty",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				restoreName: "",
				namespace:   namespace,
			},
			wantBackupName: "",
		},
		{
			name: "restore name not found",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				restoreName: "some-restore",
				namespace:   namespace,
			},
			wantBackupName: "",
		},
		{
			name: "restore name not found",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				restoreName: "some-restore",
				namespace:   namespace,
			},
			wantBackupName: "",
		},
		{
			name: "restore found but no backup",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				restoreName: validRestoreName,
				namespace:   namespace,
			},
			wantBackupName: "",
		},
		{
			name: "restore found with backup",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				restoreName: validRestoreNameWBackup,
				namespace:   namespace,
			},
			wantBackupName: backupName,
		},
	}

	for index, tt := range tests {
		if index == 0 {
			ns1 := *createNamespace(namespace)
			veleroRestore := *createRestore(validRestoreName,
				namespace).object

			veleroRestoreWBackup := *createRestore(validRestoreNameWBackup,
				namespace).
				backupName(backupName).
				object

			veleroBackup := *createBackup(backupName, namespace).object

			k8sClient1.Create(tt.args.ctx, &ns1)
			k8sClient1.Create(tt.args.ctx, &veleroRestore)
			k8sClient1.Create(tt.args.ctx, &veleroRestoreWBackup)
			k8sClient1.Create(tt.args.ctx, &veleroBackup)
		}
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := getBackupInfoFromRestore(tt.args.ctx, tt.args.c,
				tt.args.restoreName, tt.args.namespace); got != tt.wantBackupName {
				t.Errorf("getBackupInfoFromRestore() returns = %v, want %v", got, tt.wantBackupName)
			}
		})

	}
	testEnv.Stop()

}

func Test_deleteSecretsWithLabelSelector(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	namespace := "ns"

	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	corev1.AddToScheme(scheme1)

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx         context.Context
		c           client.Client
		backupName  string
		otherLabels []labels.Requirement
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "keep secrets with no backup or backup matching",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				backupName:  "name1",
				otherLabels: []labels.Requirement{},
			},
		},
	}

	for index, tt := range tests {
		if index == 0 {
			ns1 := *createNamespace(namespace)

			secretKeep := *createSecret("aws-creds-keep", namespace, map[string]string{
				"velero.io/backup-name": "name1",
			}, nil, nil) // matches backup label
			secretKeep2 := *createSecret("aws-creds-keep2", namespace, map[string]string{
				"velero.io/backup-name-dummy": "name2",
			}, nil, nil) // no backup label
			secretDelete := *createSecret("aws-creds-delete", namespace, map[string]string{
				"velero.io/backup-name": "name2",
			}, nil, nil)
			configMapDelete := *createConfigMap("aws-cmap-delete", namespace, map[string]string{
				"velero.io/backup-name": "name2",
			})
			cmapKeep := *createConfigMap("aws-map-keep", namespace, map[string]string{
				"velero.io/backup-name": "name1",
			}) // matches backup label

			k8sClient1.Create(tt.args.ctx, &ns1)
			k8sClient1.Create(tt.args.ctx, &secretKeep)
			k8sClient1.Create(tt.args.ctx, &secretKeep2)
			k8sClient1.Create(tt.args.ctx, &secretDelete)
			k8sClient1.Create(tt.args.ctx, &configMapDelete)
			k8sClient1.Create(tt.args.ctx, &cmapKeep)

		}
		t.Run(tt.name, func(t *testing.T) {
			deleteSecretsWithLabelSelector(tt.args.ctx, tt.args.c,
				tt.args.backupName, tt.args.otherLabels)

			secret := corev1.Secret{}
			if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
				Name: "aws-creds-keep", Namespace: namespace}, &secret); err != nil {
				t.Errorf("deleteSecretsWithLabelSelector() aws-creds-delete should be found !")
			}

			secret = corev1.Secret{}
			if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
				Name: "aws-creds-delete", Namespace: namespace}, &secret); err == nil {
				t.Errorf("deleteSecretsWithLabelSelector() aws-creds-delete should not be found, it was deleted !")
			}

			cmap := corev1.ConfigMap{}
			if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
				Name: "aws-map-keep", Namespace: namespace}, &cmap); err != nil {
				t.Errorf("deleteSecretsWithLabelSelector() aws-cmap-delete should be found !")
			}
			cmap = corev1.ConfigMap{}
			if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
				Name: "aws-cmap-delete", Namespace: namespace}, &cmap); err == nil {
				t.Errorf("deleteSecretsWithLabelSelector() aws-cmap-delete should not be found, it was deleted !")
			}

		})

	}
	testEnv.Stop()

}

func Test_deleteSecretsForBackupType(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	namespace := "ns"

	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	corev1.AddToScheme(scheme1)

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	ns1 := *createNamespace(namespace)

	timeNow, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")
	rightNow := v1.NewTime(timeNow)
	tenHourAgo := rightNow.Add(-10 * time.Hour)
	aFewSecondsAgo := rightNow.Add(-2 * time.Second)

	currentTime := rightNow.Format("20060102150405")
	tenHourAgoTime := tenHourAgo.Format("20060102150405")
	aFewSecondsAgoTime := aFewSecondsAgo.Format("20060102150405")

	secretKeepCloseToCredsBackup := *createSecret("secret-keep-close-to-creds-backup", namespace, map[string]string{
		backupCredsHiveLabel:    "hive",
		"velero.io/backup-name": "acm-credentials-hive-schedule-" + aFewSecondsAgoTime,
	}, nil, nil) // matches backup label from hive backup and it has the hive backup; keep it

	secretKeepIgnoredCloseToCredsBackup := *createSecret("secret-keep-ignored-close-to-creds-backup", namespace, map[string]string{
		"velero.io/backup-name": "acm-credentials-hive-schedule-" + aFewSecondsAgoTime,
	}, nil, nil) // matches backup label from hive backup and it DOES NOT have the hive backup; keep it, ignore it

	secretKeep2 := *createSecret("aws-creds-keep2", namespace, map[string]string{
		"velero.io/backup-name-dummy": "name2",
	}, nil, nil) // no backup label

	deleteSecretFromOldBackup := *createSecret("delete-secret-from-old-backup", namespace, map[string]string{
		backupCredsHiveLabel:    "hive",
		"velero.io/backup-name": "acm-credentials-hive-schedule-" + tenHourAgoTime,
	}, nil, nil) // from the old backup, delete

	ignoreSecretFromOldBackup := *createSecret("ignore-secret-from-old-backup", namespace, map[string]string{
		"velero.io/backup-name": "acm-credentials-hive-schedule-" + tenHourAgoTime,
	}, nil, nil) // from the old backup, ignore since it doesn't have the hive label

	secretDelete := *createSecret("creds-delete", namespace, map[string]string{
		backupCredsHiveLabel:    "hive",
		"velero.io/backup-name": "name2", // doesn't match the CloseToCredsBackup and it has the hive label
	}, nil, nil)

	keepSecretNoHiveLabel := *createSecret("keep-secret-no-hive-label", namespace, map[string]string{
		"velero.io/backup-name": "name2", // doesn't match the CloseToCredsBackup but it DOES NOT have the hive label, ignore it
	}, nil, nil)

	ctx := context.Background()
	k8sClient1.Create(ctx, &ns1)
	// keep secrets
	k8sClient1.Create(ctx, &secretKeepCloseToCredsBackup)
	k8sClient1.Create(ctx, &secretKeepIgnoredCloseToCredsBackup)
	k8sClient1.Create(ctx, &secretKeep2)
	k8sClient1.Create(ctx, &keepSecretNoHiveLabel)
	k8sClient1.Create(ctx, &ignoreSecretFromOldBackup)
	// delete secrets
	k8sClient1.Create(ctx, &deleteSecretFromOldBackup)
	k8sClient1.Create(ctx, &secretDelete)

	relatedCredentialsBackup := *createBackup("acm-credentials-schedule-"+currentTime, namespace).
		labels(map[string]string{BackupScheduleTypeLabel: string(Credentials),
			BackupScheduleNameLabel: "acm-credentials-hive-schedule-" + currentTime}).
		phase(veleroapi.BackupPhaseCompleted).
		startTimestamp(rightNow).
		object

	k8sClient1.Create(ctx, &relatedCredentialsBackup)

	hiveCredsLabel, _ := labels.NewRequirement(backupCredsHiveLabel,
		selection.Exists, []string{})

	// no matching backups so non secrets should be deleted
	secretsToBeDeleted := []string{
		deleteSecretFromOldBackup.Name,
		secretDelete.Name,
	}
	secretsToKeep := []string{
		secretKeepCloseToCredsBackup.Name,
		secretKeepIgnoredCloseToCredsBackup.Name,
		secretKeep2.Name,
		keepSecretNoHiveLabel.Name,
		ignoreSecretFromOldBackup.Name,
	}

	type args struct {
		ctx                 context.Context
		c                   client.Client
		backupType          ResourceType
		relatedVeleroBackup veleroapi.Backup
		otherLabels         []labels.Requirement
	}
	tests := []struct {
		name string
		args args
	}{

		{
			name: "keep secrets with no backup or backup matching",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				backupType:          CredentialsHive,
				relatedVeleroBackup: relatedCredentialsBackup,
				otherLabels:         []labels.Requirement{*hiveCredsLabel},
			},
		},
		{
			name: "keep secrets with no backup or backup matching, added an old backup",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				backupType:          CredentialsHive,
				relatedVeleroBackup: relatedCredentialsBackup,
				otherLabels:         []labels.Requirement{*hiveCredsLabel},
			},
		},
		{
			name: "keep secrets with no backup or backup matching, added a matching backup and an old backup",
			args: args{
				ctx:                 context.Background(),
				c:                   k8sClient1,
				backupType:          CredentialsHive,
				relatedVeleroBackup: relatedCredentialsBackup,
				otherLabels:         []labels.Requirement{*hiveCredsLabel},
			},
		},
	}

	for index, tt := range tests {

		if index == 1 {
			hiveOldBackup := *createBackup("acm-credentials-hive-schedule-"+tenHourAgoTime, namespace).
				labels(map[string]string{BackupScheduleTypeLabel: string(CredentialsHive),
					BackupScheduleNameLabel: "acm-credentials-hive-schedule-" + tenHourAgoTime}).
				phase(veleroapi.BackupPhaseCompleted).
				startTimestamp(v1.NewTime(tenHourAgo)).
				object

			k8sClient1.Create(ctx, &hiveOldBackup)

		}

		if index == 2 {
			hiveCloseToCredsBackup := *createBackup("acm-credentials-hive-schedule-"+aFewSecondsAgoTime, namespace).
				labels(map[string]string{BackupScheduleTypeLabel: string(CredentialsHive),
					BackupScheduleNameLabel: "acm-credentials-hive-schedule-" + aFewSecondsAgoTime}).
				phase(veleroapi.BackupPhaseCompleted).
				startTimestamp(v1.NewTime(aFewSecondsAgo)).
				object

			k8sClient1.Create(ctx, &hiveCloseToCredsBackup)

		}

		t.Run(tt.name, func(t *testing.T) {
			deleteSecretsForBackupType(tt.args.ctx, tt.args.c,
				tt.args.backupType, tt.args.relatedVeleroBackup, tt.args.otherLabels)

			if index == 0 {
				// no matching backups so non secrets should be deleted
				secret := corev1.Secret{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: deleteSecretFromOldBackup.Name, Namespace: namespace}, &secret); err != nil {
					t.Errorf("deleteSecretsForBackupType() deleteSecretFromOldBackup should be found, there was no hive backup matching the related backup  !")
				}
			}

			if index == 1 {
				// no matching backups so non secrets should be deleted
				for i := range secretsToBeDeleted {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToBeDeleted[i], Namespace: namespace}, &secret); err != nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, secret %s should be found",
							index, secretsToBeDeleted[i])
					}

				}

				for i := range secretsToKeep {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToKeep[i], Namespace: namespace}, &secret); err != nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, %s should be found",
							index, secretsToKeep[i])
					}
				}
			}

			if index == 2 {
				for i := range secretsToBeDeleted {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToBeDeleted[i], Namespace: namespace}, &secret); err == nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, secret %s should NOT be found",
							index, secretsToBeDeleted[i])
					}

				}

				for i := range secretsToKeep {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToKeep[i], Namespace: namespace}, &secret); err != nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, %s should be found",
							index, secretsToKeep[i])
					}

				}
			}
		})

	}
	testEnv.Stop()

}

func Test_cleanupDeltaForCredentials(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx          context.Context
		c            client.Client
		veleroBackup *veleroapi.Backup
		backupName   string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "no backup name, return",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				backupName: "",
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "veleroNamespace").
					object,
			},
		},
		{
			name: "with backup name, no ORSelector",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				backupName: "acm-credentials-hive-schedule-20220726152532",
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "veleroNamespace").
					object,
			},
		},
		{
			name: "with backup name, and ORSelector",
			args: args{
				ctx:        context.Background(),
				c:          k8sClient1,
				backupName: "acm-credentials-hive-schedule-20220726152532",
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "veleroNamespace").
					orLabelSelectors([]*metav1.LabelSelector{
						&metav1.LabelSelector{
							MatchLabels: map[string]string{
								"test": "test",
							},
						},
					}).
					object,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanupDeltaForCredentials(tt.args.ctx, tt.args.c,
				tt.args.backupName, tt.args.veleroBackup)
		})

	}
	testEnv.Stop()

}
