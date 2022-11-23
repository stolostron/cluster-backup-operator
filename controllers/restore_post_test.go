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
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	discoveryclient "k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := postRestoreActivation(tt.args.ctx, k8sClient1,
				tt.args.secrets, tt.args.managedClusters, tt.args.currentTime); len(got) != len(tt.want) {
				t.Errorf("postRestoreActivation() returns = %v, want %v", got, tt.want)
			}
		})
	}
	testEnv.Stop()
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
				ExcludeBackupLabel: "true",
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
		ctx                     context.Context
		mapping                 *meta.RESTMapping
		dr                      dynamic.NamespaceableResourceInterface
		resource                unstructured.Unstructured
		deleteOptions           v1.DeleteOptions
		excludedNamespaces      []string
		skipExcludedBackupLabel bool
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
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_local_ns,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
			},
			want:        false,
			errMsgEmpty: true,
		},
		{
			name: "Delete default resource",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_default,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
			},
			want:        true,
			errMsgEmpty: true,
		},
		{
			name: "Delete default resource with finalizer, should throw error since resource was deleted before finalizers patch",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_default_with_finalizer,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: true,
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete default resource NOT FOUND",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_default_notfound,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: true,
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete default resource with ns excluded",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_default_notfound,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"default"},
				skipExcludedBackupLabel: true,
			},
			want:        false,
			errMsgEmpty: true,
		},
		{
			name: "Delete default resource, excluded from backup",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_exclude_from_backup,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: true,
			},
			want:        false,
			errMsgEmpty: true,
		},
		{
			name: "Delete res_default_exclude_label, ExcludedBackupLabel is set but asked not to skip",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_exclude_from_backup,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete global resource",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMappingGlobal,
				dr:                      resInterface,
				resource:                *res_global,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{},
				skipExcludedBackupLabel: true,
			},
			want:        true,
			errMsgEmpty: true,
		},
		{
			name: "Delete global resource with finalizer, throws error since res is deleted before finalizers patch",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMappingGlobal,
				dr:                      resInterface,
				resource:                *res_global_with_finalizer,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{},
				skipExcludedBackupLabel: true,
			},
			want:        true,
			errMsgEmpty: false,
		},
		{
			name: "Delete global resource NOT FOUND",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMappingGlobal,
				dr:                      resInterface,
				resource:                *res_global_notfound,
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{},
				skipExcludedBackupLabel: true,
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
				tt.args.excludedNamespaces,
				tt.args.skipExcludedBackupLabel); got != tt.want ||
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
			name: "post activation  should NOT run now, CleanupTypeNone",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseEnabled).object,
				cleanupOnRestore: true,
				restoreOptions:   RestoreOptions{},
			},
			want: false,
		},
		{
			name: "post activation  should NOT run now, state is enabled but cleanup is false",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
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
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
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
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
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

	ns1 := *createNamespace(namespace)
	k8sClient1.Create(context.Background(), &ns1)

	hiveCredsLabel, _ := labels.NewRequirement(backupCredsHiveLabel,
		selection.Exists, []string{})

	type args struct {
		ctx         context.Context
		c           client.Client
		backupName  string
		cleanupType v1beta1.CleanupType
		otherLabels []labels.Requirement
	}
	tests := []struct {
		name            string
		args            args
		secretsToKeep   []string
		secretsToDelete []string
		secretsToCreate []corev1.Secret
		mapsToKeep      []string
		mapsToDelete    []string
		mapsToCreate    []corev1.ConfigMap
	}{
		{
			name: "keep secrets with no backup or backup matching cleanupAll",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				backupName:  "name1",
				cleanupType: v1beta1.CleanupTypeAll,
				otherLabels: []labels.Requirement{*hiveCredsLabel},
			},
			secretsToKeep: []string{
				"aws-creds-1", //has the same backup label as the backup
				"aws-creds-2", // this is not an ACM secret, no ACM label
				"aws-creds-3", // this is not an ACM secret, no ACM label

			},
			secretsToDelete: []string{
				"aws-creds-4", // ACM secret and different backup
				"aws-creds-5", // ACM secret no backup label
			},
			secretsToCreate: []corev1.Secret{
				*createSecret("aws-creds-1", namespace, map[string]string{
					BackupNameVeleroLabel: "name1",
					backupCredsHiveLabel:  "hive",
				}, nil, nil),
				*createSecret("aws-creds-2", namespace, map[string]string{
					"velero.io/backup-name-dummy": "name2",
				}, nil, nil), // no backup label
				*createSecret("aws-creds-3", namespace, map[string]string{
					BackupNameVeleroLabel: "name2",
				}, nil, nil), // has backup label but no ACM secret label
				*createSecret("aws-creds-4", namespace, map[string]string{
					BackupNameVeleroLabel: "name2",
					backupCredsHiveLabel:  "hive",
				}, nil, nil),
				*createSecret("aws-creds-5", namespace, map[string]string{
					backupCredsHiveLabel: "hive",
				}, nil, nil),
			},
			mapsToKeep: []string{
				"aws-map-1", //has the same backup label as the backup
				"aws-map-2", // this is not an ACM map, no ACM label
				"aws-map-3", // this is not an ACM map, no ACM label

			},
			mapsToDelete: []string{
				"aws-map-4", // ACM secret and different backup
				"aws-ap-5",  // ACM map no backup label
			},
			mapsToCreate: []corev1.ConfigMap{
				*createConfigMap("aws-map-1", namespace, map[string]string{
					BackupNameVeleroLabel: "name1",
					backupCredsHiveLabel:  "hive",
				}),
				*createConfigMap("aws-map-2", namespace, map[string]string{
					"velero.io/backup-name-dummy": "name2",
				}), // no backup label
				*createConfigMap("aws-map-3", namespace, map[string]string{
					BackupNameVeleroLabel: "name2",
				}), // has backup label but no ACM secret label
				*createConfigMap("aws-map-4", namespace, map[string]string{
					BackupNameVeleroLabel: "name2",
					backupCredsHiveLabel:  "hive",
				}),
				*createConfigMap("aws-map-5", namespace, map[string]string{
					backupCredsHiveLabel: "hive",
				}),
			},
		},
		{
			name: "keep secrets with no backup or backup matching cleanupRestored",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				backupName:  "name1",
				cleanupType: v1beta1.CleanupTypeRestored,
				otherLabels: []labels.Requirement{*hiveCredsLabel},
			},
			secretsToKeep: []string{
				"aws-creds-1", //has the same backup label as the backup
				"aws-creds-2", // this is not an ACM secret, no ACM label
				"aws-creds-3", // this is not an ACM secret, no ACM label
				"aws-creds-5", // user created secret, not deleted when CleanupTypeRestored
			},
			secretsToDelete: []string{
				"aws-creds-4", // ACM secret and different backup
			},
			secretsToCreate: []corev1.Secret{
				*createSecret("aws-creds-4", namespace, map[string]string{
					BackupNameVeleroLabel: "name2",
					backupCredsHiveLabel:  "hive",
				}, nil, nil),
				*createSecret("aws-creds-5", namespace, map[string]string{
					backupCredsHiveLabel: "hive",
				}, nil, nil),
			},
			mapsToKeep: []string{
				"aws-map-1", //has the same backup label as the backup
				"aws-map-2", // this is not an ACM map, no ACM label
				"aws-map-3", // this is not an ACM map, no ACM label
				"aws-map-5", // user created map, not deleted when CleanupTypeRestored
			},
			mapsToDelete: []string{
				"aws-map-4", // ACM map and different backup
			},
			mapsToCreate: []corev1.ConfigMap{
				*createConfigMap("aws-map-4", namespace, map[string]string{
					BackupNameVeleroLabel: "name2",
					backupCredsHiveLabel:  "hive",
				}),
				*createConfigMap("aws-map-5", namespace, map[string]string{
					backupCredsHiveLabel: "hive",
				}),
			},
		},
	}

	for _, tt := range tests {

		for i := range tt.secretsToCreate {
			if err := k8sClient1.Create(context.Background(), &tt.secretsToCreate[i]); err != nil {
				t.Errorf("failed to create secret %s ", err.Error())

			}
		}

		for i := range tt.mapsToCreate {
			if err := k8sClient1.Create(context.Background(), &tt.mapsToCreate[i]); err != nil {
				t.Errorf("failed to create map %s ", err.Error())

			}
		}

		t.Run(tt.name, func(t *testing.T) {
			deleteSecretsWithLabelSelector(tt.args.ctx, tt.args.c,
				tt.args.backupName, tt.args.cleanupType, tt.args.otherLabels)

			// no matching backups so secrets should be deleted
			for i := range tt.secretsToDelete {
				secret := corev1.Secret{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.secretsToDelete[i], Namespace: namespace},
					&secret); err == nil {
					t.Errorf("deleteSecretsWithLabelSelector() secret %s should be deleted",
						tt.secretsToDelete[i])
				}

			}

			for i := range tt.secretsToKeep {
				secret := corev1.Secret{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.secretsToKeep[i], Namespace: namespace},
					&secret); err != nil {
					t.Errorf("deleteSecretsWithLabelSelector() %s should be found",
						tt.secretsToKeep[i])
				}
			}

			for i := range tt.mapsToDelete {
				maps := corev1.ConfigMap{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.mapsToDelete[i], Namespace: namespace},
					&maps); err == nil {
					t.Errorf("deleteSecretsWithLabelSelector() map %s should be deleted",
						tt.mapsToDelete[i])
				}

			}

			for i := range tt.mapsToKeep {
				maps := corev1.ConfigMap{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.mapsToKeep[i], Namespace: namespace},
					&maps); err != nil {
					t.Errorf("deleteSecretsWithLabelSelector()map  %s should be found",
						tt.mapsToKeep[i])
				}
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
		backupCredsHiveLabel:  "hive",
		BackupNameVeleroLabel: "acm-credentials-hive-schedule-" + aFewSecondsAgoTime,
	}, nil, nil) // matches backup label from hive backup and it has the hive backup; keep it

	secretKeepIgnoredCloseToCredsBackup := *createSecret("secret-keep-ignored-close-to-creds-backup", namespace, map[string]string{
		BackupNameVeleroLabel: "acm-credentials-hive-schedule-" + aFewSecondsAgoTime,
	}, nil, nil) // matches backup label from hive backup and it DOES NOT have the hive backup; keep it, ignore it

	secretKeep2 := *createSecret("aws-creds-keep2", namespace, map[string]string{
		"velero.io/backup-name-dummy": "name2",
	}, nil, nil) // no backup label

	deleteSecretFromOldBackup := *createSecret("delete-secret-from-old-backup", namespace, map[string]string{
		backupCredsHiveLabel:  "hive",
		BackupNameVeleroLabel: "acm-credentials-hive-schedule-" + tenHourAgoTime,
	}, nil, nil) // from the old backup, delete

	ignoreSecretFromOldBackup := *createSecret("ignore-secret-from-old-backup", namespace, map[string]string{
		BackupNameVeleroLabel: "acm-credentials-hive-schedule-" + tenHourAgoTime,
	}, nil, nil) // from the old backup, ignore since it doesn't have the hive label

	secretDelete := *createSecret("creds-delete", namespace, map[string]string{
		backupCredsHiveLabel:  "hive",
		BackupNameVeleroLabel: "name2", // doesn't match the CloseToCredsBackup and it has the hive label
	}, nil, nil)

	keepSecretNoHiveLabel := *createSecret("keep-secret-no-hive-label", namespace, map[string]string{
		BackupNameVeleroLabel: "name2", // doesn't match the CloseToCredsBackup but it DOES NOT have the hive label, ignore it
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
		cleanupType         v1beta1.CleanupType
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
				cleanupType:         v1beta1.CleanupTypeAll,
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
				cleanupType:         v1beta1.CleanupTypeAll,
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
				cleanupType:         v1beta1.CleanupTypeAll,
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
				tt.args.backupType, tt.args.relatedVeleroBackup,
				tt.args.cleanupType, tt.args.otherLabels)

			if index == 0 {
				// no matching backups so non secrets should be deleted
				secret := corev1.Secret{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: deleteSecretFromOldBackup.Name, Namespace: namespace}, &secret); err != nil {
					t.Errorf("deleteSecretsForBackupType() deleteSecretFromOldBackup should be found, there was no hive backup matching the related backup  !")
				}
			}

			if index == 1 {
				// no matching backups so secrets should be deleted
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
		cleanupType  v1beta1.CleanupType
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "no backup name, return",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				cleanupType: v1beta1.CleanupTypeAll,
				backupName:  "",
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "veleroNamespace").
					object,
			},
		},
		{
			name: "with backup name, no ORSelector",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				cleanupType: v1beta1.CleanupTypeAll,
				backupName:  "acm-credentials-hive-schedule-20220726152532",
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "veleroNamespace").
					object,
			},
		},
		{
			name: "with backup name, and ORSelector",
			args: args{
				ctx:         context.Background(),
				c:           k8sClient1,
				cleanupType: v1beta1.CleanupTypeAll,
				backupName:  "acm-credentials-hive-schedule-20220726152532",
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
				tt.args.backupName, tt.args.veleroBackup, tt.args.cleanupType)
		})

	}
	testEnv.Stop()

}

func Test_cleanupDeltaForResourcesAndClustersBackup(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	corev1.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	namespaceName := "open-cluster-management-backup"
	timeNow, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")
	rightNow := v1.NewTime(timeNow)
	tenHourAgo := rightNow.Add(-10 * time.Hour)
	aFewSecondsAgo := rightNow.Add(-2 * time.Second)

	currentTime := rightNow.Format("20060102150405")
	tenHourAgoTime := tenHourAgo.Format("20060102150405")
	aFewSecondsAgoTime := aFewSecondsAgo.Format("20060102150405")

	veleroResourcesBackupNameOlder := veleroScheduleNames[Resources] + "-" + tenHourAgoTime
	veleroResourcesBackupName := veleroScheduleNames[Resources] + "-" + currentTime

	veleroGenericBackupNameOlder := veleroScheduleNames[ResourcesGeneric] + "-" + tenHourAgoTime
	veleroGenericBackupName := veleroScheduleNames[ResourcesGeneric] + "-" + aFewSecondsAgoTime

	veleroClustersBackupNameOlder := veleroScheduleNames[ManagedClusters] + "-" + tenHourAgoTime
	veleroClustersBackupName := veleroScheduleNames[ManagedClusters] + "-" + aFewSecondsAgoTime

	// create a resources backup
	resources := []string{
		"crd-not-found.apps.open-cluster-management.io",
		"channel.apps.open-cluster-management.io",
	}
	resources = append(resources, backupResources...)

	resourcesBackup := *createBackup(veleroResourcesBackupName, namespaceName).
		includedResources(resources).
		startTimestamp(rightNow).
		excludedNamespaces([]string{"local-cluster", "open-cluster-management-backup"}).
		labels(map[string]string{
			BackupScheduleTypeLabel: string(Resources),
		}).
		phase(veleroapi.BackupPhaseCompleted).
		object

	genericBackup := *createBackup(veleroGenericBackupName, namespaceName).
		excludedResources(backupManagedClusterResources).
		startTimestamp(v1.NewTime(aFewSecondsAgo)).
		labels(map[string]string{
			BackupScheduleTypeLabel: string(ResourcesGeneric),
		}).
		phase(veleroapi.BackupPhaseCompleted).
		object

	genericBackupOld := *createBackup(veleroGenericBackupNameOlder, namespaceName).
		excludedResources(backupManagedClusterResources).
		startTimestamp(v1.NewTime(tenHourAgo)).
		labels(map[string]string{
			BackupScheduleTypeLabel: string(ResourcesGeneric),
		}).
		object

	clustersBackup := *createBackup(veleroClustersBackupName, namespaceName).
		includedResources(backupManagedClusterResources).
		excludedNamespaces([]string{"local-cluster"}).
		startTimestamp(v1.NewTime(aFewSecondsAgo)).
		labels(map[string]string{
			BackupScheduleTypeLabel: string(ManagedClusters),
		}).
		phase(veleroapi.BackupPhaseCompleted).
		object

	clustersBackupOld := *createBackup(veleroClustersBackupNameOlder, namespaceName).
		includedResources(backupManagedClusterResources).
		excludedNamespaces([]string{"local-cluster"}).
		startTimestamp(v1.NewTime(tenHourAgo)).
		labels(map[string]string{
			BackupScheduleTypeLabel: string(ManagedClusters),
		}).
		object

	res_NS := &unstructured.Unstructured{}
	res_NS.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": namespaceName,
		},
	})

	cls_with_backup_label_same := &unstructured.Unstructured{}
	cls_with_backup_label_same.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1beta1",
		"kind":       "ManagedCluster",
		"metadata": map[string]interface{}{
			"name":      "cls-with-backup-label-same",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel: clustersBackup.Name,
			},
		},
	})
	cls_with_backup_label_diff_excl_ns := &unstructured.Unstructured{}
	cls_with_backup_label_diff_excl_ns.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1beta1",
		"kind":       "ManagedCluster",
		"metadata": map[string]interface{}{
			"name":      "cls-with-backup-label-diff-excl-ns",
			"namespace": "local-cluster",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel: veleroClustersBackupNameOlder,
			},
		},
	})
	cls_with_backup_label_diff := &unstructured.Unstructured{}
	cls_with_backup_label_diff.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1beta1",
		"kind":       "ManagedCluster",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-diff",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel: veleroClustersBackupNameOlder,
			},
			"finalizers": []interface{}{"hive.openshift.io/deprovision"},
		},
	})

	cls_with_no_backup_label := &unstructured.Unstructured{}
	cls_with_no_backup_label.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1beta1",
		"kind":       "ManagedCluster",
		"metadata": map[string]interface{}{
			"name":      "cls-with-no-backup-label",
			"namespace": "default",
		},
	})

	channel_with_backup_label_same := &unstructured.Unstructured{}
	channel_with_backup_label_same.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-same",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel: resourcesBackup.Name,
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})
	channel_with_backup_label_diff_excl_ns := &unstructured.Unstructured{}
	channel_with_backup_label_diff_excl_ns.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-diff-excl-ns",
			"namespace": "local-cluster",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel: veleroResourcesBackupNameOlder,
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})
	channel_with_backup_label_diff := &unstructured.Unstructured{}
	channel_with_backup_label_diff.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-diff",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel: veleroResourcesBackupNameOlder,
			},
			"finalizers": []interface{}{"hive.openshift.io/deprovision"},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})
	channel_with_backup_label_generic := &unstructured.Unstructured{}
	channel_with_backup_label_generic.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-generic",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel:   veleroResourcesBackupName,
				backupCredsClusterLabel: "i-am-a-generic-resource",
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	channel_with_no_backup_label := &unstructured.Unstructured{}
	channel_with_no_backup_label.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-no-backup-label",
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	msaObj := &unstructured.Unstructured{}
	msaObj.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import",
			"namespace": "managed1",
			"labels": map[string]interface{}{
				msa_label: msa_service_name,
			},
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
	})

	channel_with_backup_label_generic_match := &unstructured.Unstructured{}
	channel_with_backup_label_generic_match.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-generic-match",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel:   veleroGenericBackupName,
				backupCredsClusterLabel: "i-am-a-generic-resource",
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	channel_with_backup_label_generic_old := &unstructured.Unstructured{}
	channel_with_backup_label_generic_old.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-generic-match-old",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel:   veleroGenericBackupNameOlder,
				backupCredsClusterLabel: "i-am-a-generic-resource",
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	channel_with_backup_label_generic_match_activ := &unstructured.Unstructured{}
	channel_with_backup_label_generic_match_activ.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-generic-match-activ",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel:   veleroGenericBackupName,
				backupCredsClusterLabel: ClusterActivationLabel,
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	channel_with_backup_label_generic_old_activ := &unstructured.Unstructured{}
	channel_with_backup_label_generic_old_activ.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-with-backup-label-generic-old-activ",
			"namespace": "default",
			"labels": map[string]interface{}{
				BackupNameVeleroLabel:   veleroGenericBackupNameOlder,
				backupCredsClusterLabel: ClusterActivationLabel,
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	clsvObj := &unstructured.Unstructured{}
	clsvObj.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "config.openshift.io/v1",
		"kind":       "ClusterVersion",
		"metadata": map[string]interface{}{
			"name": "version",
		},
		"spec": map[string]interface{}{
			"clusterID": "1234",
		},
	})

	msaGVK := schema.GroupVersionKind{Group: "authentication.open-cluster-management.io",
		Version: "v1alpha1", Kind: "ManagedServiceAccount"}
	msaGVRList := schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version: "v1alpha1", Resource: "managedserviceaccounts"}

	// cluster deployments
	clsDGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "ClusterDeployment"}
	clsDGVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "clusterdeployments"}

	// placements
	plsGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "Placement"}
	plsGVKGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "placements"}
	//curators
	crGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "ClusterCurator"}
	crGVKGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "clustercurators"}
	//channels
	chGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Kind: "Channel"}
	chGVKList := schema.GroupVersionResource{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Resource: "channels"}
	//subs
	subsGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Kind: "Subscription"}
	subsGVKList := schema.GroupVersionResource{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Resource: "subscriptions"}

	//managed clusters
	clsGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "ManagedCluster"}
	clsGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclusters"}
	//managed clusters sets
	clsSGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "ManagedClusterSet"}
	clsSGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclustersets"}
	//backups
	bsSGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "BackupSchedule"}
	bsSGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "backupschedules"}
	//mutators
	mGVK := schema.GroupVersionKind{Group: "admission.cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "AdmissionReview"}
	mVKList := schema.GroupVersionResource{Group: "admission.cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclustermutators"}
	//pools
	cpGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "ClusterPool"}
	cpVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "clusterpools"}
	//dns
	dnsGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "DNSZone"}
	dnsVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "dnszones"}
	//image set
	imgGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "ClusterImageSet"}
	imgVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "clusterimageset"}
	//hive config
	hGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "HiveConfig"}
	hVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "hiveconfig"}

	//addon
	aoGVK := schema.GroupVersionKind{Group: "addon.open-cluster-management.io",
		Version: "v1alpha1", Kind: "ManagedClusterAddOn"}
	aoVKList := schema.GroupVersionResource{Group: "addon.open-cluster-management.io",
		Version: "v1alpha1", Resource: "managedclusteraddons"}

	//cluster version
	clsVGVK := schema.GroupVersionKind{Group: "config.openshift.io",
		Version: "v1", Kind: "ClusterVersion"}
	clsVGVKList := schema.GroupVersionResource{Group: "config.openshift.io",
		Version: "v1", Resource: "clusterversions"}

	///
	gvrToListKind := map[schema.GroupVersionResource]string{
		msaGVRList:    "ManagedServiceAccountList",
		clsVGVKList:   "ClusterVersionList",
		clsDGVKList:   "ClusterDeploymentList",
		plsGVKGVKList: "PlacementList",
		crGVKGVKList:  "ClusterCuratorList",
		chGVKList:     "ChannelList",
		clsGVKList:    "ManagedClusterList",
		clsSGVKList:   "ManagedClusterSetList",
		bsSGVKList:    "BackupScheduleList",
		mVKList:       "AdmissionReviewList",
		cpVKList:      "ClusterPoolList",
		dnsVKList:     "DNSZoneList",
		imgVKList:     "ClusterImageSetList",
		hVKList:       "HiveConfigList",
		subsGVKList:   "SubscriptionList",
		aoVKList:      "ManagedClusterAddOnList",
	}

	channelRuntimeObjects := []runtime.Object{
		channel_with_backup_label_same,
		channel_with_backup_label_diff,
		channel_with_backup_label_diff_excl_ns,
		channel_with_backup_label_generic,
		channel_with_no_backup_label,
		channel_with_backup_label_generic_match,
		channel_with_backup_label_generic_old,
		channel_with_backup_label_generic_match_activ,
		channel_with_backup_label_generic_old_activ,
	}
	clusterRuntimeObjects := []runtime.Object{
		cls_with_backup_label_same,
		cls_with_backup_label_diff,
		cls_with_backup_label_diff_excl_ns,
		cls_with_no_backup_label,
	}
	unstructuredScheme := runtime.NewScheme()
	unstructuredScheme.AddKnownTypes(msaGVK.GroupVersion(), msaObj)
	unstructuredScheme.AddKnownTypes(clsVGVK.GroupVersion(), clsvObj)
	unstructuredScheme.AddKnownTypes(clsDGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(plsGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(crGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(chGVK.GroupVersion(), channelRuntimeObjects...)
	unstructuredScheme.AddKnownTypes(clsGVK.GroupVersion(), clusterRuntimeObjects...)
	unstructuredScheme.AddKnownTypes(clsSGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(bsSGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(mGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(cpGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(dnsGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(imgGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(hGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(subsGVK.GroupVersion())
	unstructuredScheme.AddKnownTypes(aoGVK.GroupVersion())

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(unstructuredScheme,
		gvrToListKind,
		channelRuntimeObjects...)

	argov1alphaInfo := metav1.APIResourceList{
		GroupVersion: "argoproj.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "applications", Namespaced: true, Kind: "Application"},
			{Name: "applicationsets", Namespaced: true, Kind: "ApplicationSet"},
			{Name: "argocds", Namespaced: true, Kind: "Argocd"},
		},
	}
	openshiftv1Info :=
		metav1.APIResourceList{
			GroupVersion: "config.openshift.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "clusterversions", Namespaced: false, Kind: "ClusterVersion"},
			},
		}
	appsInfo := metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "channels", Namespaced: true, Kind: "Channel"},
			{Name: "subscriptions", Namespaced: true, Kind: "Subscription"},
		},
	}
	appsInfoV1 := metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "channels", Namespaced: true, Kind: "Channel"},
			{Name: "subscriptions", Namespaced: true, Kind: "Subscription"},
		},
	}
	addonInfo := metav1.APIResourceList{
		GroupVersion: "addon.open-cluster-management.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "managedclusteraddons", Namespaced: true, Kind: "ManagedClusterAddOn"},
		},
	}
	clusterv1beta1Info := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "placements", Namespaced: true, Kind: "Placement"},
			{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
			{Name: "managedclustersets", Namespaced: false, Kind: "ManagedClusterSet"},
			{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	clusterv1Info := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "placements", Namespaced: true, Kind: "Placement"},
			{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
			{Name: "managedclustersets", Namespaced: false, Kind: "ManagedClusterSet"},
			{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	excluded := metav1.APIResourceList{
		GroupVersion: "admission.cluster.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedclustermutators", Namespaced: false, Kind: "AdmissionReview"},
		},
	}
	hiveInfo := metav1.APIResourceList{
		GroupVersion: "hive.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "clusterpools", Namespaced: true, Kind: "ClusterPool"},
			{Name: "clusterdeployments", Namespaced: false, Kind: "ClusterDeployment"},
			{Name: "dnszones", Namespaced: false, Kind: "DNSZone"},
			{Name: "clusterimageset", Namespaced: false, Kind: "ClusterImageSet"},
			{Name: "hiveconfig", Namespaced: false, Kind: "HiveConfig"},
		},
	}
	authAlpha1 := metav1.APIResourceList{
		GroupVersion: "authentication.open-cluster-management.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "managedserviceaccounts", Namespaced: true, Kind: "ManagedServiceAccount"},
		},
	}

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var list interface{}
		switch req.URL.Path {
		case "/apis/cluster.open-cluster-management.io/v1beta1":
			list = &clusterv1beta1Info
		case "/apis/cluster.open-cluster-management.io/v1":
			list = &clusterv1Info
		case "/apis/admission.cluster.open-cluster-management.io/v1beta1":
			list = &excluded
		case "/apis/hive.openshift.io/v1":
			list = &hiveInfo
		case "/apis/authentication.open-cluster-management.io/v1alpha1":
			list = authAlpha1
		case "/apis/apps.open-cluster-management.io/v1beta1":
			list = &appsInfo
		case "/apis/apps.open-cluster-management.io/v1":
			list = &appsInfoV1
		case "/apis/argoproj.io/v1alpha1":
			list = &argov1alphaInfo
		case "/apis/config.openshift.io/v1":
			list = &openshiftv1Info
		case "/apis/addon.open-cluster-management.io/v1alpha1":
			list = &addonInfo

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
						Name: "argoproj.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "argoproj.io/v1",
								Version:      "v1",
							},
							{
								GroupVersion: "argoproj.io/v1beta1",
								Version:      "v1beta1",
							},
						},
					},
					{
						Name: "cluster.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "cluster.open-cluster-management.io/v1beta1",
								Version:      "v1beta1",
							},
							{
								GroupVersion: "cluster.open-cluster-management.io/v1",
								Version:      "v1",
							},
						},
					},
					{
						Name: "admission.cluster.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "admission.cluster.open-cluster-management.io/v1beta1",
								Version:      "v1beta1",
							},
							{
								GroupVersion: "admission.cluster.open-cluster-management.io/v1",
								Version:      "v1",
							},
						},
					},
					{
						Name: "hive.openshift.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "hive.openshift.io/v1", Version: "v1"},
							{GroupVersion: "hive.openshift.io/v1beta1", Version: "v1beta1"},
						},
					},
					{
						Name: "apps.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "apps.open-cluster-management.io/v1beta1", Version: "v1beta1"},
							{GroupVersion: "apps.open-cluster-management.io/v1", Version: "v1"},
						},
					},
					{
						Name: "authentication.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "authentication.open-cluster-management.io/v1alpha1", Version: "v1alpha1"},
						},
					},
					{
						Name: "addon.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "addon.open-cluster-management.io/v1alpha1", Version: "v1alpha1"},
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

	fakeDiscovery := discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server1.URL},
	)

	testRequest := "authentication.open-cluster-management.io/v1alpha1"
	fakeDiscovery.ServerResourcesForGroupVersion(testRequest)

	//create some channel resources
	dyn.Resource(chGVKList).Namespace("default").Create(context.Background(),
		channel_with_backup_label_same, v1.CreateOptions{})
	dyn.Resource(chGVKList).Namespace("default").Create(context.Background(),
		channel_with_backup_label_diff, v1.CreateOptions{})
	dyn.Resource(chGVKList).Namespace("local-cluster").Create(context.Background(),
		channel_with_backup_label_diff_excl_ns, v1.CreateOptions{})
	dyn.Resource(chGVKList).Namespace("default").Create(context.Background(),
		channel_with_backup_label_generic, v1.CreateOptions{})
	dyn.Resource(chGVKList).Namespace("default").Create(context.Background(),
		channel_with_no_backup_label, v1.CreateOptions{})

	//create some cluster resources
	dyn.Resource(clsGVKList).Namespace("default").Create(context.Background(),
		cls_with_backup_label_diff, v1.CreateOptions{})
	dyn.Resource(clsGVKList).Namespace("default").Create(context.Background(),
		cls_with_backup_label_same, v1.CreateOptions{})
	dyn.Resource(clsGVKList).Namespace("local-cluster").Create(context.Background(),
		cls_with_backup_label_diff_excl_ns, v1.CreateOptions{})
	dyn.Resource(clsGVKList).Namespace("default").Create(context.Background(),
		cls_with_no_backup_label, v1.CreateOptions{})

	//
	dyn.Resource(msaGVRList).Namespace("managed1").Create(context.Background(),
		msaObj, v1.CreateOptions{})

	reconcileArgs := DynamicStruct{
		dc:  fakeDiscovery,
		dyn: dyn,
	}

	m := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(fakeDiscovery),
	)

	resOptionsCleanupAll := RestoreOptions{
		dynamicArgs: reconcileArgs,
		cleanupType: v1beta1.CleanupTypeAll,
		mapper:      m,
	}

	resOptionsCleanupRestored := RestoreOptions{
		dynamicArgs: reconcileArgs,
		cleanupType: v1beta1.CleanupTypeRestored,
		mapper:      m,
	}

	type args struct {
		ctx                    context.Context
		c                      client.Client
		restoreOptions         RestoreOptions
		veleroBackup           *veleroapi.Backup
		backupName             string
		managedClustersSkipped bool
	}
	testsResources := []struct {
		name                 string
		args                 args
		resourcesToBeDeleted []unstructured.Unstructured
		resourcesToKeep      []unstructured.Unstructured
		extraBackups         []veleroapi.Backup
		resourcesToCreate    []unstructured.Unstructured
	}{
		{
			name: "cleanup resources backup, no generic backup found",
			args: args{
				ctx:                    context.Background(),
				c:                      k8sClient1,
				restoreOptions:         resOptionsCleanupRestored,
				veleroBackup:           &resourcesBackup,
				backupName:             veleroResourcesBackupName,
				managedClustersSkipped: true,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{*channel_with_backup_label_diff},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_generic,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
			},
			extraBackups:      []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{},
		},
		{
			name: "cleanup resources backup, with generic backup NOT found, old one available",
			args: args{
				ctx:                    context.Background(),
				c:                      k8sClient1,
				restoreOptions:         resOptionsCleanupRestored,
				veleroBackup:           &resourcesBackup,
				backupName:             veleroResourcesBackupName,
				managedClustersSkipped: true,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{*channel_with_backup_label_diff},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_generic,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
			},
			extraBackups: []veleroapi.Backup{genericBackupOld},
			resourcesToCreate: []unstructured.Unstructured{
				*channel_with_backup_label_diff, // this is the one deleted by the previous test, add it back
			},
		},
		{
			name: "cleanup resources backup, with generic backup NOT found and clusters backup not skipped",
			args: args{
				ctx:                    context.Background(),
				c:                      k8sClient1,
				restoreOptions:         resOptionsCleanupRestored,
				veleroBackup:           &resourcesBackup,
				backupName:             veleroResourcesBackupName,
				managedClustersSkipped: false,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
				*channel_with_backup_label_generic_match,
				*channel_with_backup_label_generic_match_activ,
				*channel_with_backup_label_generic_old, // should not be deleted since the matching generic backup was not found
				*channel_with_backup_label_generic,     // should not be deleted since the matching generic backup was not found

			},
			extraBackups: []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{ // this is the one deleted by the previous test, add it back
				*channel_with_backup_label_diff,
			},
		},
		{
			name: "cleanup resources backup, with generic backup found",
			args: args{
				ctx:                    context.Background(),
				c:                      k8sClient1,
				restoreOptions:         resOptionsCleanupRestored,
				veleroBackup:           &resourcesBackup,
				backupName:             veleroResourcesBackupName,
				managedClustersSkipped: true,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic, // it's deleted bc the backup name doesn't match the generic backup
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
				*channel_with_backup_label_generic_match,
				*channel_with_backup_label_generic_match_activ,
				*channel_with_backup_label_generic_old_activ,
			},
			extraBackups: []veleroapi.Backup{genericBackup},
			resourcesToCreate: []unstructured.Unstructured{
				*channel_with_backup_label_diff, // this is the one deleted by the previous test, add it back
			},
		},
		{
			name: "cleanup resources backup, with generic backup found and clusters backup not skipped",
			args: args{
				ctx:                    context.Background(),
				c:                      k8sClient1,
				restoreOptions:         resOptionsCleanupRestored,
				veleroBackup:           &resourcesBackup,
				backupName:             veleroResourcesBackupName,
				managedClustersSkipped: false,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,           // it's deleted bc the backup name doesn't match the generic backup
				*channel_with_backup_label_generic_old_activ, // deleted bc the clusters backup is enabled
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
				*channel_with_backup_label_generic_match,
				*channel_with_backup_label_generic_match_activ,
			},
			extraBackups: []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{ // this is the one deleted by the previous test, add it back
				*channel_with_backup_label_diff,
			},
		},
		{
			name: "cleanup resources backup, with generic backup found and clusters backup not skipped, cleanup all",
			args: args{
				ctx:                    context.Background(),
				c:                      k8sClient1,
				restoreOptions:         resOptionsCleanupAll,
				veleroBackup:           &resourcesBackup,
				backupName:             veleroResourcesBackupName,
				managedClustersSkipped: false,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,           // it's deleted bc the backup name doesn't match the generic backup
				*channel_with_backup_label_generic_old_activ, // deleted bc the clusters backup is enabled
				*channel_with_no_backup_label,                // delete all, even the ones with no label
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
				*channel_with_backup_label_generic_match,
				*channel_with_backup_label_generic_match_activ,
			},
			extraBackups: []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{ // this is the one deleted by the previous test, add it back
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,           // it's deleted bc the backup name doesn't match the generic backup
				*channel_with_backup_label_generic_old_activ, // deleted bc the clusters backup is enabled
			},
		},
	}

	testsClusters := []struct {
		name                 string
		args                 args
		resourcesToBeDeleted []unstructured.Unstructured
		resourcesToKeep      []unstructured.Unstructured
		extraBackups         []veleroapi.Backup
		resourcesToCreate    []unstructured.Unstructured
	}{
		{
			name: "cleanup clusters backup",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &clustersBackup,
				backupName:     veleroClustersBackupName,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{*cls_with_backup_label_diff},
			resourcesToKeep: []unstructured.Unstructured{
				*cls_with_no_backup_label,
				*cls_with_backup_label_same,
				*cls_with_backup_label_diff_excl_ns,
			},
			extraBackups:      []veleroapi.Backup{clustersBackupOld},
			resourcesToCreate: []unstructured.Unstructured{},
		},
	}

	if err := k8sClient1.Create(context.Background(), createNamespace(genericBackup.GetNamespace())); err != nil {
		t.Errorf("cannot create ns %s ", err.Error())
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(resOptionsCleanupRestored.dynamicArgs.dc),
	)

	for _, tt := range testsResources {

		k8sClient1.Create(tt.args.ctx, tt.args.veleroBackup)
		// create extra backups for this test
		for i := range tt.extraBackups {
			if err := k8sClient1.Create(tt.args.ctx, &tt.extraBackups[i]); err != nil {
				t.Errorf("cannot create backup %s ", err.Error())
			}
		}

		// create resources for this test
		for i := range tt.resourcesToCreate {
			if _, err := dyn.Resource(chGVKList).Namespace(tt.resourcesToCreate[i].GetNamespace()).Create(context.Background(),
				&tt.resourcesToCreate[i], v1.CreateOptions{}); err != nil {
				t.Errorf("cannot create resource %s ", err.Error())
			}
		}

		t.Run(tt.name, func(t *testing.T) {

			cleanupDeltaForResourcesBackup(tt.args.ctx,
				tt.args.c,
				tt.args.restoreOptions,
				tt.args.backupName,
				tt.args.veleroBackup,
				tt.args.managedClustersSkipped)
		})

		groupKind := schema.GroupKind{
			Group: chGVK.Group,
			Kind:  chGVK.Kind,
		}

		mapping, _ := mapper.RESTMapping(groupKind, "")
		var dr = tt.args.restoreOptions.dynamicArgs.dyn.Resource(mapping.Resource)

		for i := range tt.resourcesToBeDeleted {
			if _, err := dr.Namespace(tt.resourcesToBeDeleted[i].GetNamespace()).
				Get(tt.args.ctx, tt.resourcesToBeDeleted[i].GetName(), v1.GetOptions{}); err == nil {
				t.Errorf("cleanupDeltaForResourcesBackup(%s) resource %s should NOT be found",
					tt.name, tt.resourcesToBeDeleted[i])
			}
		}

		for i := range tt.resourcesToKeep {
			if _, err := dr.Namespace(tt.resourcesToKeep[i].GetNamespace()).
				Get(tt.args.ctx, tt.resourcesToKeep[i].GetName(), v1.GetOptions{}); err != nil {
				t.Errorf("cleanupDeltaForResourcesBackup(%s) resource %s should be found ! they were deleted",
					tt.name, tt.resourcesToKeep[i].GetName())
			}
		}

	}

	for _, tt := range testsClusters {

		k8sClient1.Create(tt.args.ctx, tt.args.veleroBackup)
		// create extra backups for this test
		for i := range tt.extraBackups {
			if err := k8sClient1.Create(tt.args.ctx, &tt.extraBackups[i]); err != nil {
				t.Errorf("cannot create backup %s ", err.Error())
			}
		}

		// create resources for this test
		for i := range tt.resourcesToCreate {
			if _, err := dyn.Resource(chGVKList).Namespace(tt.resourcesToCreate[i].GetNamespace()).Create(context.Background(),
				&tt.resourcesToCreate[i], v1.CreateOptions{}); err != nil {
				t.Errorf("cannot create resource %s ", err.Error())
			}
		}

		t.Run(tt.name, func(t *testing.T) {

			cleanupDeltaForClustersBackup(tt.args.ctx,
				tt.args.c,
				tt.args.restoreOptions,
				tt.args.backupName,
				tt.args.veleroBackup)
		})

		// managed cluster group
		groupKind := schema.GroupKind{
			Group: clsGVK.Group,
			Kind:  clsGVK.Kind,
		}
		mapping, _ := mapper.RESTMapping(groupKind, "")
		var dr = tt.args.restoreOptions.dynamicArgs.dyn.Resource(mapping.Resource)

		for i := range tt.resourcesToBeDeleted {
			if _, err := dr.Namespace(tt.resourcesToBeDeleted[i].GetNamespace()).
				Get(tt.args.ctx, tt.resourcesToBeDeleted[i].GetName(), v1.GetOptions{}); err == nil {
				t.Errorf("cleanupDeltaForClustersBackup(%s) resource %s should NOT be found",
					tt.name, tt.resourcesToBeDeleted[i])
			}
		}

		for i := range tt.resourcesToKeep {
			if _, err := dr.Namespace(tt.resourcesToKeep[i].GetNamespace()).
				Get(tt.args.ctx, tt.resourcesToKeep[i].GetName(), v1.GetOptions{}); err != nil {
				t.Errorf("cleanupDeltaForClustersBackup(%s) resource %s should be found ! they were deleted",
					tt.name, tt.resourcesToKeep[i].GetName())
			}
		}

	}
	testEnv.Stop()
}
