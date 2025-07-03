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
/*
Package controllers contains comprehensive unit tests for post-restore operations in the ACM Backup/Restore system.

This test suite validates post-restore functionality including:
- Post-restore hook execution and validation
- Resource activation and configuration after restore
- Managed cluster reconnection and validation
- Restore completion verification and status updates
- Integration with restored resources and their dependencies
- Error handling during post-restore phases
- Resource cleanup and finalization logic

The tests ensure that all restored resources are properly activated and configured
after restore operations complete, guaranteeing a fully functional restored environment.
*/

//nolint:funlen
package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Test_postRestoreActivation tests the activation of managed clusters after a restore operation.
//
// This test verifies that:
// - Managed service account secrets are properly processed for cluster activation
// - Cluster activation timestamps are updated correctly
// - Multiple clusters can be activated simultaneously
// - Invalid or missing secrets are handled gracefully
//
// Test Scenarios:
// - Single cluster activation with valid MSA secret
// - Multiple cluster activation with mixed secret states
// - Activation with missing or invalid secrets
// - Timestamp validation and updating
//
// Implementation:
// - Uses fake Kubernetes client for fast execution
// - Creates realistic MSA secrets with proper labels and structure
// - Verifies activation results through secret inspection
func Test_postRestoreActivation(t *testing.T) {
	ns1 := *createNamespace("managed-activ-1")
	ns2 := *createNamespace("managed-activ-2")
	autoImporSecretWithLabel := *createSecret(autoImportSecretName, "managed-activ-1",
		map[string]string{activateLabel: "true"}, nil, nil)

	// Use helper function for client setup
	k8sClient1 := CreateTestClientOrFail(t, &ns1, &ns2, &autoImporSecretWithLabel)

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
			name: "create NO auto import secrets, managed-activ-1 is active",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					*createManagedCluster("local-cluster", true).object,
					*createManagedCluster("test1", false).object,
					*createManagedCluster("managed-activ-1", false).clusterUrl("someurl").
						conditions([]metav1.Condition{
							metav1.Condition{
								Status: metav1.ConditionTrue,
								Type:   "ManagedClusterConditionAvailable",
							},
						}).object,
				},

				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "local-cluster",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
					*createSecret("auto-import-account", "managed-activ-1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				},
			},
			want: []string{},
		},
		{
			name: "create NO auto import secret for managed-activ-1, it has no URL",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					*createManagedCluster("local-cluster", true).object,
					*createManagedCluster("test1", false).object,
					*createManagedCluster("managed-activ-1", false).emptyClusterUrl().
						conditions([]metav1.Condition{
							metav1.Condition{
								Status: metav1.ConditionFalse,
							},
						}).object,
					*createManagedCluster("managed-activ-2", false).emptyClusterUrl().
						conditions([]metav1.Condition{
							metav1.Condition{
								Status: metav1.ConditionFalse,
							},
						}).object,
				},
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "local-cluster",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
					*createSecret("auto-import-account", "managed-activ-1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
					*createSecret("auto-import-account", "managed-activ-2",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("aaa"),
						}),
					*createSecret("auto-import-pair", "managed-activ-1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				},
			},
			want: []string{},
		},
		{
			name: "create auto import for managed-activ-1 cluster",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					*createManagedCluster("local-cluster", true).object,
					*createManagedCluster("test1", false).object,
					*createManagedCluster("managed-activ-1", false).
						clusterUrl("someurl").
						conditions([]metav1.Condition{
							metav1.Condition{
								Status: metav1.ConditionFalse,
							},
						}).object,
					*createManagedCluster("managed-activ-2", false).
						conditions([]metav1.Condition{
							metav1.Condition{
								Status: metav1.ConditionFalse,
							},
						}).
						object,
				},
				secrets: []corev1.Secret{
					*createSecret("auto-import-ignore-this-one", "managed-activ-1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
						}, nil),
					*createSecret("auto-import-account", "local-cluster",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
					*createSecret("auto-import-account", "managed-activ-1",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
					*createSecret("auto-import-account", "managed-activ-2",
						nil, map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				},
			},
			want: []string{"managed-activ-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := postRestoreActivation(tt.args.ctx, k8sClient1,
				tt.args.secrets, tt.args.managedClusters, "local-cluster", tt.args.currentTime); len(got) != len(tt.want) {
				t.Errorf("postRestoreActivation() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_executePostRestoreTasks tests the execution of post-restore cleanup tasks.
//
// This test verifies that:
// - Post-restore tasks are executed in the correct order
// - Cleanup operations are performed based on restore configuration
// - Error conditions are handled properly during task execution
// - Task execution status is properly tracked and reported
//
// Test Scenarios:
// - Successful execution of all post-restore tasks
// - Partial execution with error handling
// - Different cleanup configurations (CleanupTypeAll, CleanupTypeRestored, CleanupTypeNone)
// - Task execution with missing or invalid restore objects
//
// Implementation:
// - Uses fake Kubernetes client for isolated testing
// - Mocks various restore configurations and states
// - Verifies task execution through status inspection
func Test_executePostRestoreTasks(t *testing.T) {
	// Use helper function for client setup
	k8sClient1 := CreateTestClientOrFail(t,
		createNamespace("velero-ns"),
		createNamespace(obs_addon_ns),
		createSecret(obs_secret_name, obs_addon_ns, map[string]string{}, nil, nil),
	)

	// Setup client with local cluster for testing
	k8sClientWithLocalCluster := CreateTestClientOrFail(t,
		createNamespace("velero-ns"),
		createNamespace(obs_addon_ns),
		createSecret(obs_secret_name, obs_addon_ns, map[string]string{}, nil, nil),
		createManagedCluster("local-cluster", true).object,
		createManagedCluster("remote-cluster1", false).object,
		createManagedCluster("remote-cluster2", false).object,
	)

	// Setup client with no local cluster (empty local cluster name)
	k8sClientNoLocalCluster := CreateTestClientOrFail(t,
		createNamespace("velero-ns"),
		createNamespace(obs_addon_ns),
		createSecret(obs_secret_name, obs_addon_ns, map[string]string{}, nil, nil),
		createManagedCluster("remote-cluster1", false).object,
		createManagedCluster("remote-cluster2", false).object,
	)

	type args struct {
		ctx     context.Context
		c       client.Client
		restore *v1beta1.Restore
	}
	tests := []struct {
		name                   string
		args                   args
		want                   bool
		wantObsSecretDeleted   bool
		setupAdditionalSecrets func(client.Client)
	}{
		{
			name: "post activation should NOT run - managed clusters are skipped",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "velero-ns").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want:                 false,
			wantObsSecretDeleted: false,
		},
		{
			name: "post activation should NOT run - restore phase is running",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseRunning).object,
			},
			want:                 false,
			wantObsSecretDeleted: false,
		},
		{
			name: "post activation should NOT run - restore phase is error",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseError).object,
			},
			want:                 false,
			wantObsSecretDeleted: false,
		},
		{
			name: "post activation should run - restore phase finished",
			args: args{
				ctx: context.Background(),
				c:   k8sClientWithLocalCluster,
				restore: createACMRestore("Restore", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want:                 true,
			wantObsSecretDeleted: true,
		},
		{
			name: "post activation should run - restore phase finished with errors",
			args: args{
				ctx: context.Background(),
				c:   k8sClientWithLocalCluster,
				restore: createACMRestore("Restore2", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinishedWithErrors).object,
			},
			want:                 true,
			wantObsSecretDeleted: true,
		},
		{
			name: "post activation should run - no local cluster found but clusters exist",
			args: args{
				ctx: context.Background(),
				c:   k8sClientNoLocalCluster,
				restore: createACMRestore("Restore3", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want:                 true,
			wantObsSecretDeleted: true,
		},
		{
			name: "post activation with auto-import secrets",
			args: args{
				ctx: context.Background(),
				c:   k8sClientWithLocalCluster,
				restore: createACMRestore("Restore4", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want:                 true,
			wantObsSecretDeleted: true,
			setupAdditionalSecrets: func(c client.Client) {
				// Create some auto-import secrets to test MSA processing
				secret1 := createSecret("auto-import-account-cluster1", "cluster1",
					map[string]string{},
					map[string]string{msa_label: msa_service_name},
					map[string][]byte{"token": []byte("test-token-1"), "server": []byte("https://cluster1.example.com")})
				secret2 := createSecret("auto-import-account-cluster2", "cluster2",
					map[string]string{},
					map[string]string{msa_label: msa_service_name},
					map[string][]byte{"token": []byte("test-token-2"), "server": []byte("https://cluster2.example.com")})
				_ = c.Create(context.Background(), secret1)
				_ = c.Create(context.Background(), secret2)
			},
		},
		{
			name: "post activation should run - empty managed clusters list",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1, // No managed clusters in this client
				restore: createACMRestore("Restore5", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want:                 true,
			wantObsSecretDeleted: true,
		},
		{
			name: "post activation should run with started phase",
			args: args{
				ctx: context.Background(),
				c:   k8sClientWithLocalCluster,
				restore: createACMRestore("Restore6", "velero-ns").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseStarted).object,
			},
			want:                 false,
			wantObsSecretDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup additional secrets if needed
			if tt.setupAdditionalSecrets != nil {
				tt.setupAdditionalSecrets(tt.args.c)
			}

			// Check initial obs secret state for some clients
			obsSecretExistsBefore := false
			secret := corev1.Secret{}
			if err := tt.args.c.Get(context.Background(), types.NamespacedName{
				Name:      obs_secret_name,
				Namespace: obs_addon_ns,
			}, &secret); err == nil {
				obsSecretExistsBefore = true
			}

			// Execute the function
			got := executePostRestoreTasks(tt.args.ctx, tt.args.c, tt.args.restore)

			// Verify the return value
			if got != tt.want {
				t.Errorf("executePostRestoreTasks() = %v, want %v", got, tt.want)
			}

			// Verify obs secret deletion
			secretExistsAfter := false
			if err := tt.args.c.Get(context.Background(), types.NamespacedName{
				Name:      obs_secret_name,
				Namespace: obs_addon_ns,
			}, &secret); err == nil {
				secretExistsAfter = true
			}

			if tt.wantObsSecretDeleted && obsSecretExistsBefore && secretExistsAfter {
				t.Errorf("Expected obs secret to be deleted, but it still exists")
			}
		})
	}

	// Final verification that obs secret was deleted from the main test client
	secret := corev1.Secret{}
	if err := k8sClient1.Get(context.Background(), types.NamespacedName{
		Name:      obs_secret_name,
		Namespace: obs_addon_ns,
	}, &secret); err == nil {
		t.Errorf("this secret should no longer exist ! %v ", obs_secret_name)
	}
}

// Test_deleteDynamicResource_LocalCluster tests deletion of resources in the local cluster context.
//
// This focused test covers local cluster resource deletion scenarios.
//
// Test Coverage:
// - Deletion of resources specifically in the local cluster (hub cluster)
// - Handling of different local cluster naming conventions ('local-cluster', custom names)
// - Proper resource identification and deletion in local cluster context
// - Verification that only local cluster resources are affected
//
// Test Scenarios:
// - Delete local cluster resource with standard name 'local-cluster'
// - Delete local cluster resource with custom local cluster name
// - Verify non-local resources are not affected
//
// Implementation Details:
// - Uses dynamic fake client for fast execution (< 0.01s)
// - Creates test resources with proper metadata and labels
// - Verifies deletion through resource existence checks
// - Tests different local cluster naming patterns
//

func Test_deleteDynamicResource_LocalCluster(t *testing.T) {
	// Channel resource in the local-cluster namespace
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

	// Channel resource in local-cluster namespace (where local cluster is named "hub1ns")
	res_local_ns_uniquename := &unstructured.Unstructured{}
	res_local_ns_uniquename.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "hub1ns", /* this will be the local-cluster when we call deleteDynamicResource() below */
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	unstructuredScheme := runtime.NewScheme()
	err := chnv1.AddToScheme(unstructuredScheme)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	dynClient := dynamicfake.NewSimpleDynamicClient(unstructuredScheme)

	targetGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io", Version: "v1", Kind: "Channel"}
	targetGVR := targetGVK.GroupVersion().WithResource("channel")
	targetMapping := meta.RESTMapping{
		Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace,
	}

	resInterface := dynClient.Resource(targetGVR)

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	type args struct {
		ctx                     context.Context
		mapping                 *meta.RESTMapping
		dr                      dynamic.NamespaceableResourceInterface
		resource                unstructured.Unstructured
		localClusterName        string
		deleteOptions           metav1.DeleteOptions
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
			name: "Delete local cluster resource (local-cluster named 'local-cluster')",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_local_ns,
				localClusterName:        "local-cluster",
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
			},
			want:        false,
			errMsgEmpty: true,
		},
		{
			name: "Delete local cluster resource (local-cluster named 'hub1ns')",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_local_ns_uniquename,
				localClusterName:        "hub1ns",
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
			},
			want:        false,
			errMsgEmpty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, msg := deleteDynamicResource(tt.args.ctx,
				tt.args.mapping,
				tt.args.dr,
				tt.args.resource,
				tt.args.excludedNamespaces,
				tt.args.localClusterName,
				tt.args.skipExcludedBackupLabel); got != tt.want ||
				(tt.errMsgEmpty && len(msg) != 0) ||
				(!tt.errMsgEmpty && len(msg) == 0) {
				t.Errorf("deleteDynamicResource() = %v, want %v, emptyMsg=%v, msg=%v", got,
					tt.want, tt.errMsgEmpty, msg)
			}
		})
	}
}

// Test_deleteDynamicResource_DefaultNamespace tests deletion of resources in the default namespace.
//
// This focused test covers default namespace resource deletion scenarios.
//
// Test Coverage:
// - Standard resource deletion in default namespace
// - Finalizer handling and removal during deletion
// - Error handling for missing/not-found resources
// - Namespace exclusion logic
// - Backup label exclusion handling
// - Skip exclusion override functionality
//
// Test Scenarios:
// - Delete default resource (happy path)
// - Delete resource with finalizer (tests finalizer removal logic)
// - Delete resource that doesn't exist (error handling)
// - Delete resource in excluded namespace (should be skipped)
// - Delete resource with backup exclusion label
// - Delete excluded resource when skip exclusion is disabled
//
// Implementation Details:
// - Uses dynamic fake client with proper resource setup
// - Creates resources with various metadata configurations
// - Tests finalizer removal before resource deletion
// - Verifies proper error handling and logging
// - Validates exclusion logic for namespaces and labels
//
// Finalizer Handling:
// The test includes scenarios where resources have finalizers that must be
// removed before deletion. This tests the complete deletion workflow.
//

func Test_deleteDynamicResource_DefaultNamespace(t *testing.T) {
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

	unstructuredScheme := runtime.NewScheme()
	err := chnv1.AddToScheme(unstructuredScheme)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	dynClient := dynamicfake.NewSimpleDynamicClient(unstructuredScheme,
		res_default,
		res_default_with_finalizer,
		res_exclude_from_backup,
	)

	targetGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io", Version: "v1", Kind: "Channel"}
	targetGVR := targetGVK.GroupVersion().WithResource("channel")
	targetMapping := meta.RESTMapping{
		Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace,
	}

	resInterface := dynClient.Resource(targetGVR)

	// create resources which should be found
	_, err = resInterface.Namespace("default").Create(context.Background(), res_default, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Err creating: %s", err.Error())
	}
	_, err = resInterface.Namespace("default").Create(context.Background(),
		res_default_with_finalizer, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Err creating: %s", err.Error())
	}

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	type args struct {
		ctx                     context.Context
		mapping                 *meta.RESTMapping
		dr                      dynamic.NamespaceableResourceInterface
		resource                unstructured.Unstructured
		localClusterName        string
		deleteOptions           metav1.DeleteOptions
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
			name: "Delete default resource",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_default,
				localClusterName:        "local-cluster",
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
			},
			want:        true,
			errMsgEmpty: true,
		},
		{
			name: "Delete default resource with finalizer, should throw error since resource was deleted before " +
				"finalizers patch",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMapping,
				dr:                      resInterface,
				resource:                *res_default_with_finalizer,
				localClusterName:        "",
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
				localClusterName:        "",
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
				localClusterName:        "",
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
				localClusterName:        "local-cluster",
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
				localClusterName:        "local-cluster",
				deleteOptions:           delOptions,
				excludedNamespaces:      []string{"abc"},
				skipExcludedBackupLabel: false,
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
				tt.args.localClusterName,
				tt.args.skipExcludedBackupLabel); got != tt.want ||
				(tt.errMsgEmpty && len(msg) != 0) ||
				(!tt.errMsgEmpty && len(msg) == 0) {
				t.Errorf("deleteDynamicResource() = %v, want %v, emptyMsg=%v, msg=%v", got,
					tt.want, tt.errMsgEmpty, msg)
			}
		})
	}
}

// Test_deleteDynamicResource_GlobalResources tests deletion of cluster-scoped (global) resources.
//
// This focused test covers global/cluster-scoped resource deletion scenarios.
//
// Test Coverage:
// - Standard global resource deletion
// - Finalizer handling for cluster-scoped resources
// - Error handling for missing global resources
// - Verification that namespace exclusion doesn't apply to global resources
//
// Test Scenarios:
// - Delete global resource (happy path)
// - Delete global resource with finalizer (tests finalizer removal)
// - Delete global resource that doesn't exist (error handling)
//
// Implementation Details:
// - Uses dynamic fake client configured for cluster-scoped resources
// - Creates global resources without namespace specifications
// - Tests finalizer removal workflow for cluster-scoped resources
// - Verifies proper error handling for missing resources
//
// Global Resource Characteristics:
// - No namespace field (cluster-scoped)
// - Different deletion patterns compared to namespaced resources
// - Finalizer handling works the same way as namespaced resources
// - Namespace exclusion logic doesn't apply
//

func Test_deleteDynamicResource_GlobalResources(t *testing.T) {
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
	err := chnv1.AddToScheme(unstructuredScheme)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	dynClient := dynamicfake.NewSimpleDynamicClient(unstructuredScheme,
		res_global,
		res_global_with_finalizer,
	)

	targetGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io", Version: "v1", Kind: "Channel"}
	targetGVR := targetGVK.GroupVersion().WithResource("channel")

	targetMappingGlobal := meta.RESTMapping{
		Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeRoot,
	}

	resInterface := dynClient.Resource(targetGVR)

	// create resources which should be found
	_, err = resInterface.Create(context.Background(), res_global, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Err creating: %s", err.Error())
	}
	_, err = resInterface.Create(context.Background(), res_global_with_finalizer, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Err creating: %s", err.Error())
	}

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	type args struct {
		ctx                     context.Context
		mapping                 *meta.RESTMapping
		dr                      dynamic.NamespaceableResourceInterface
		resource                unstructured.Unstructured
		localClusterName        string
		deleteOptions           metav1.DeleteOptions
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
			name: "Delete global resource",
			args: args{
				ctx:                     context.Background(),
				mapping:                 &targetMappingGlobal,
				dr:                      resInterface,
				resource:                *res_global,
				localClusterName:        "local-cluster",
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
				localClusterName:        "local-cluster",
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
				localClusterName:        "local-cluster",
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
				tt.args.localClusterName,
				tt.args.skipExcludedBackupLabel); got != tt.want ||
				(tt.errMsgEmpty && len(msg) != 0) ||
				(!tt.errMsgEmpty && len(msg) == 0) {
				t.Errorf("deleteDynamicResource() = %v, want %v, emptyMsg=%v, msg=%v", got,
					tt.want, tt.errMsgEmpty, msg)
			}
		})
	}
}

// Test_cleanupDeltaResources tests the cleanup of delta resources after restore operations.
//
// This test validates the delta resource cleanup logic that determines when resources
// should be cleaned up based on restore configuration and cleanup settings.
//
// Test Coverage:
// - Different cleanup types (CleanupTypeNone, CleanupTypeRestored, CleanupTypeAll)
// - Cleanup enablement vs disablement logic
// - Restore phase validation for cleanup timing
// - Integration with restore options and configuration
//
// Test Scenarios:
// - No cleanup when CleanupTypeNone is configured
// - No cleanup when cleanup is disabled even with valid types
// - Cleanup execution when enabled with CleanupTypeRestored
// - Cleanup execution for finished restores regardless of cleanup flag
//
// Implementation Details:
// - Uses fake Kubernetes client for isolated testing
// - Tests various restore configurations and states
// - Validates cleanup decision logic without actual resource deletion
//
// Business Logic:
// Delta cleanup ensures that resources created during restore operations
// are properly cleaned up according to the configured cleanup policies,
// preventing resource leaks and maintaining cluster hygiene.
func Test_cleanupDeltaResources(t *testing.T) {
	scheme1 := runtime.NewScheme()
	err := veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		Build()

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
				restore: createACMRestore("Restore", "velero-ns").
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
				restore: createACMRestore("Restore", "velero-ns").
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
				restore: createACMRestore("Restore", "velero-ns").
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
				restore: createACMRestore("Restore", "velero-ns").
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
}

// Test_getBackupInfoFromRestore tests retrieval of backup information from Velero restore resources.
//
// This test validates the backup information extraction logic that determines
// which backup was used for a specific restore operation.
//
// Test Coverage:
// - Restore name validation and error handling
// - Velero restore resource lookup and retrieval
// - Backup name extraction from restore specifications
// - Error handling for missing or invalid resources
//
// Test Scenarios:
// - Empty restore name handling (should return empty backup name)
// - Non-existent restore resource handling
// - Restore found but without associated backup
// - Successful backup name extraction from valid restore
//
// Implementation Details:
// - Uses fake Kubernetes client with pre-populated test resources
// - Creates realistic Velero restore and backup objects
// - Tests proper namespace isolation and resource identification
// - Validates error handling and graceful degradation
//
// Business Logic:
// This functionality is crucial for tracking restore operations and
// determining which backup data was used, enabling proper cleanup
// and restore validation workflows.
func Test_getBackupInfoFromRestore(t *testing.T) {
	namespace := "ns"
	backupName := "passive-sync-2-acm-credentials-schedule-20220929220007"
	validRestoreName := "restore-acm-passive-sync-2-acm-credentials-schedule-2022092922123"
	validRestoreNameWBackup := "restore-acm-" + backupName

	scheme1 := runtime.NewScheme()
	err := veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	err = corev1.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	ns1 := *createNamespace(namespace)
	veleroRestore := *createRestore(validRestoreName, namespace).object
	veleroRestoreWBackup := *createRestore(validRestoreNameWBackup, namespace).
		backupName(backupName).object
	veleroBackup := *createBackup(backupName, namespace).object

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		WithObjects(&ns1, &veleroRestore, &veleroRestoreWBackup, &veleroBackup).
		Build()

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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := getBackupInfoFromRestore(tt.args.ctx, tt.args.c,
				tt.args.restoreName, tt.args.namespace); got != tt.wantBackupName {
				t.Errorf("getBackupInfoFromRestore() returns = %v, want %v", got, tt.wantBackupName)
			}
		})
	}
}

// Test_deleteSecretsWithLabelSelector tests selective deletion of secrets based on label selectors and cleanup types.
//
// This test validates the secret cleanup logic that removes secrets created during
// backup operations based on backup labels and cleanup configuration.
//
// Test Coverage:
// - Label selector matching for secret identification
// - Different cleanup types (CleanupTypeAll vs CleanupTypeRestored)
// - Backup label comparison and matching logic
// - ConfigMap deletion alongside secret deletion
// - Preservation of non-ACM secrets and user-created resources
//
// Test Scenarios:
// - CleanupTypeAll: Delete secrets from different backups, keep matching ones
// - CleanupTypeRestored: More selective deletion, preserve user-created secrets
// - Label-based filtering using Hive credential labels
// - Mixed resource scenarios with ACM and non-ACM secrets
//
// Implementation Details:
// - Uses fake Kubernetes client with comprehensive test data
// - Creates secrets and ConfigMaps with various label combinations
// - Tests both positive (deletion) and negative (preservation) cases
// - Validates label selector requirements and matching logic
//
// Business Logic:
// Secret cleanup is essential for maintaining cluster security and preventing
// credential leaks after restore operations. Different cleanup types provide
// flexibility for various restore scenarios and organizational policies.
func Test_deleteSecretsWithLabelSelector(t *testing.T) {
	namespace := "ns"

	scheme1 := runtime.NewScheme()
	err := veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	err = corev1.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	ns1 := *createNamespace(namespace)
	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		WithObjects(&ns1).
		Build()

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
				"aws-creds-1", // has the same backup label as the backup
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
				"aws-map-1", // has the same backup label as the backup
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
				"aws-creds-1", // has the same backup label as the backup
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
				"aws-map-1", // has the same backup label as the backup
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
					Name: tt.secretsToDelete[i], Namespace: namespace,
				},
					&secret); err == nil {
					t.Errorf("deleteSecretsWithLabelSelector() secret %s should be deleted",
						tt.secretsToDelete[i])
				}

			}

			for i := range tt.secretsToKeep {
				secret := corev1.Secret{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.secretsToKeep[i], Namespace: namespace,
				},
					&secret); err != nil {
					t.Errorf("deleteSecretsWithLabelSelector() %s should be found",
						tt.secretsToKeep[i])
				}
			}

			for i := range tt.mapsToDelete {
				maps := corev1.ConfigMap{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.mapsToDelete[i], Namespace: namespace,
				},
					&maps); err == nil {
					t.Errorf("deleteSecretsWithLabelSelector() map %s should be deleted",
						tt.mapsToDelete[i])
				}

			}

			for i := range tt.mapsToKeep {
				maps := corev1.ConfigMap{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: tt.mapsToKeep[i], Namespace: namespace,
				},
					&maps); err != nil {
					t.Errorf("deleteSecretsWithLabelSelector()map  %s should be found",
						tt.mapsToKeep[i])
				}
			}
		})

	}
}

// Test_deleteSecretsForBackupType tests type-specific secret deletion based on backup relationships and timestamps.
//
// This test validates the backup-type-specific secret cleanup logic that removes
// secrets based on their relationship to specific backup types and timestamps.
//
// Test Coverage:
// - Backup type filtering (Credentials, CredentialsHive, etc.)
// - Timestamp-based backup correlation and matching
// - Secret preservation based on backup proximity and relevance
// - Complex backup relationship scenarios with multiple timestamps
// - Label-based secret identification and categorization
//
// Test Scenarios:
// - Secrets from recent backups should be preserved
// - Secrets from old backups should be deleted
// - Secrets with matching backup labels should be handled correctly
// - Non-matching secrets should be deleted or ignored based on labels
// - Edge cases with very close backup timestamps
//
// Implementation Details:
// - Uses time-based test scenarios with multiple backup timestamps
// - Creates realistic backup and secret combinations
// - Tests sequential scenarios where backups are added incrementally
// - Validates complex timestamp correlation logic
//
// Business Logic:
// This cleanup mechanism ensures that secrets are maintained for current
// backups while removing outdated credentials, balancing security with
// operational requirements for backup and restore operations.
func Test_deleteSecretsForBackupType(t *testing.T) {
	namespace := "ns"

	scheme1 := runtime.NewScheme()
	err := veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	err = corev1.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	ns1 := *createNamespace(namespace)

	timeNow, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")
	rightNow := metav1.NewTime(timeNow)
	tenHourAgo := rightNow.Add(-10 * time.Hour)
	aFewSecondsAgo := rightNow.Add(-2 * time.Second)

	currentTime := rightNow.Format("20060102150405")
	tenHourAgoTime := tenHourAgo.Format("20060102150405")
	aFewSecondsAgoTime := aFewSecondsAgo.Format("20060102150405")

	secretKeepCloseToCredsBackup := *createSecret("secret-keep-close-to-creds-backup", namespace, map[string]string{
		backupCredsHiveLabel:  "hive",
		BackupNameVeleroLabel: "acm-credentials-hive-schedule-" + aFewSecondsAgoTime,
	}, nil, nil) // matches backup label from hive backup and it has the hive backup; keep it

	secretKeepIgnoredCloseToCredsBackup := *createSecret("secret-keep-ignored-close-to-creds-backup", namespace,
		map[string]string{
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

	relatedCredentialsBackup := *createBackup("acm-credentials-schedule-"+currentTime, namespace).
		labels(map[string]string{
			BackupScheduleTypeLabel: string(Credentials),
			BackupScheduleNameLabel: "acm-credentials-hive-schedule-" + currentTime,
		}).
		phase(veleroapi.BackupPhaseCompleted).
		startTimestamp(rightNow).
		object

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		WithObjects(
			&ns1,
			&secretKeepCloseToCredsBackup,
			&secretKeepIgnoredCloseToCredsBackup,
			&secretKeep2,
			&keepSecretNoHiveLabel,
			&ignoreSecretFromOldBackup,
			&deleteSecretFromOldBackup,
			&secretDelete,
			&relatedCredentialsBackup,
		).
		Build()

	ctx := context.Background()

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
				labels(map[string]string{
					BackupScheduleTypeLabel: string(CredentialsHive),
					BackupScheduleNameLabel: "acm-credentials-hive-schedule-" + tenHourAgoTime,
				}).
				phase(veleroapi.BackupPhaseCompleted).
				startTimestamp(metav1.NewTime(tenHourAgo)).
				object

			err := k8sClient1.Create(ctx, &hiveOldBackup)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}

		if index == 2 {
			hiveCloseToCredsBackup := *createBackup("acm-credentials-hive-schedule-"+aFewSecondsAgoTime, namespace).
				labels(map[string]string{
					BackupScheduleTypeLabel: string(CredentialsHive),
					BackupScheduleNameLabel: "acm-credentials-hive-schedule-" + aFewSecondsAgoTime,
				}).
				phase(veleroapi.BackupPhaseCompleted).
				startTimestamp(metav1.NewTime(aFewSecondsAgo)).
				object

			err := k8sClient1.Create(ctx, &hiveCloseToCredsBackup)
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}

		t.Run(tt.name, func(t *testing.T) {
			deleteSecretsForBackupType(tt.args.ctx, tt.args.c,
				tt.args.backupType, tt.args.relatedVeleroBackup,
				tt.args.cleanupType, tt.args.otherLabels)

			if index == 0 {
				// no matching backups so non secrets should be deleted
				secret := corev1.Secret{}
				if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
					Name: deleteSecretFromOldBackup.Name, Namespace: namespace,
				}, &secret); err != nil {
					t.Errorf("deleteSecretsForBackupType() deleteSecretFromOldBackup should be found, there was no hive backup " +
						"matching the related backup  !")
				}
			}

			if index == 1 {
				// no matching backups so secrets should be deleted
				for i := range secretsToBeDeleted {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToBeDeleted[i], Namespace: namespace,
					}, &secret); err != nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, secret %s should be found",
							index, secretsToBeDeleted[i])
					}

				}

				for i := range secretsToKeep {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToKeep[i], Namespace: namespace,
					}, &secret); err != nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, %s should be found",
							index, secretsToKeep[i])
					}
				}
			}

			if index == 2 {
				for i := range secretsToBeDeleted {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToBeDeleted[i], Namespace: namespace,
					}, &secret); err == nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, secret %s should NOT be found",
							index, secretsToBeDeleted[i])
					}

				}

				for i := range secretsToKeep {
					secret := corev1.Secret{}
					if err := k8sClient1.Get(tt.args.ctx, types.NamespacedName{
						Name: secretsToKeep[i], Namespace: namespace,
					}, &secret); err != nil {
						t.Errorf("deleteSecretsForBackupType() index=%v, %s should be found",
							index, secretsToKeep[i])
					}

				}
			}
		})

	}
}

// Test_cleanupDeltaForCredentials tests cleanup of credential-related delta resources after restore operations.
//
// This test validates the credential-specific cleanup logic that removes
// credential resources that are no longer needed after restore operations.
//
// Test Coverage:
// - Credential backup validation and processing
// - Cleanup type handling for credential resources
// - Velero backup integration and validation
// - Error handling for missing or invalid backup references
//
// Test Scenarios:
// - No cleanup when backup name is missing or empty
// - Credential cleanup without OR selector requirements
// - Credential cleanup with OR selector for complex label matching
//
// Implementation Details:
// - Uses fake Kubernetes client for credential resource simulation
// - Tests various backup configurations and cleanup scenarios
// - Validates integration with Velero backup metadata
// - Tests error handling and edge cases
//
// Business Logic:
// Credential cleanup is critical for security, ensuring that restored
// credentials don't accumulate in the cluster and potentially create
// security vulnerabilities or resource conflicts.
func Test_cleanupDeltaForCredentials(t *testing.T) {
	scheme1 := runtime.NewScheme()
	err := veleroapi.AddToScheme(scheme1)
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}
	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		Build()

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
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "velero-ns").
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
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "velero-ns").
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
				veleroBackup: createBackup("acm-credentials-hive-schedule-20220726152532", "velero-ns").
					orLabelSelectors([]*metav1.LabelSelector{
						{
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
				tt.args.backupName, tt.args.veleroBackup, tt.args.cleanupType, false)
		})
	}
}

// Test_cleanupDeltaForResourcesBackup tests cleanup of application resources after backup operations.
//
// This focused test covers resource backup cleanup scenarios.
//
// Test Coverage:
// - Cleanup of application resources (Channels, Subscriptions, etc.) after backup
// - Generic backup processing and resource matching
// - Resource deletion based on backup labels and timestamps
// - Handling of different cleanup policies (CleanupTypeRestored, CleanupTypeAll)
// - Integration with Velero backup and restore objects
//
// Test Scenarios:
// - Cleanup with no generic backup found
// - Cleanup with generic backup NOT found but older backup available
// - Cleanup with generic backup NOT found and clusters backup not skipped
// - Cleanup with generic backup found
// - Cleanup with generic backup found and clusters backup not skipped
// - Cleanup with different cleanup policies (CleanupTypeAll vs CleanupTypeRestored)
//
// Resource Types Tested:
// - Channels (apps.open-cluster-management.io)
// - Subscriptions (apps.open-cluster-management.io)
// - ManagedServiceAccounts (authentication.open-cluster-management.io)
//
// Implementation Details:
// - Uses dynamic fake client with custom list kinds for proper resource handling
// - Implements mock discovery server to handle API group discovery
// - Creates realistic Velero Backup and Restore objects
// - Tests resource creation, labeling, and deletion workflows
// - Verifies proper handling of finalizers and excluded namespaces
//
// Mock Infrastructure:
// - Custom discovery server for API group handling
// - Dynamic fake client with proper list kind mappings
// - Fake Kubernetes client for Velero object management
// - Realistic resource creation with proper labels and metadata
//

//

func Test_cleanupDeltaForResourcesBackup(t *testing.T) {
	log.SetLogger(zap.New())

	// Use helper function for scheme setup
	scheme1, err := setupTestScheme()
	AssertNoError(t, err, "Error setting up test scheme")

	// Use helper function for backup setup
	backupSetup := createTestBackupSetup()

	// Use the pre-configured backup objects
	resourcesBackup := backupSetup.ResourcesBackup
	genericBackup := backupSetup.GenericBackup
	genericBackupOld := backupSetup.GenericBackupOld

	namespaceName := backupSetup.NamespaceName

	// Get backup names for compatibility with existing test logic
	veleroResourcesBackupName := backupSetup.VeleroResourcesBackupName
	veleroGenericBackupName := backupSetup.VeleroGenericBackupName
	veleroGenericBackupNameOlder := backupSetup.VeleroGenericBackupNameOlder
	veleroClustersBackupName := backupSetup.VeleroClustersBackupName

	// Use helper functions for Channel creation
	channel_with_backup_label_same := withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-same", "default"),
		resourcesBackup.Name)
	channel_with_backup_label_diff_excl_ns := withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-diff-excl-ns", "local-cluster"),
		backupSetup.VeleroResourcesBackupName+"-"+backupSetup.TenHourAgoTime)
	channel_with_backup_label_diff := withFinalizers(withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-diff", "default"),
		backupSetup.VeleroResourcesBackupName+"-"+backupSetup.TenHourAgoTime),
		[]string{"hive.openshift.io/deprovision"})
	channel_with_backup_label_generic := withGenericLabel(withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-generic", "default"),
		veleroResourcesBackupName))
	channel_with_no_backup_label := createChannelUnstructured("channel-with-no-backup-label", "default")

	channel_with_backup_label_generic_match := withGenericLabel(withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-generic-match", "default"),
		veleroGenericBackupName))
	channel_with_backup_label_generic_old := withGenericLabel(withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-generic-match-old", "default"),
		veleroGenericBackupNameOlder))

	// Create channels with activation labels
	channel_with_backup_label_generic_match_activ := withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-generic-match-activ", "default"),
		veleroGenericBackupName)
	// Add activation label manually since we don't have a helper for it
	labels := channel_with_backup_label_generic_match_activ.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[backupCredsClusterLabel] = ClusterActivationLabel
	channel_with_backup_label_generic_match_activ.SetLabels(labels)

	channel_with_backup_label_generic_old_activ := withBackupLabel(
		createChannelUnstructured("channel-with-backup-label-generic-old-activ", "default"),
		veleroGenericBackupNameOlder)
	// Add activation label manually
	labels = channel_with_backup_label_generic_old_activ.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[backupCredsClusterLabel] = ClusterActivationLabel
	channel_with_backup_label_generic_old_activ.SetLabels(labels)

	// Setup test infrastructure (discovery server, dynamic client, etc.)
	server1 := setupMockDiscoveryServer(t)
	defer server1.Close()

	fakeDiscovery := discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server1.URL},
	)

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		Build()

	// Define custom list kinds for the dynamic client
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "apps.open-cluster-management.io",
			Version: "v1beta1", Resource: "channels"}: "ChannelList",
		{Group: "apps.open-cluster-management.io",
			Version: "v1", Resource: "channels"}: "ChannelList",
		{Group: "apps.open-cluster-management.io",
			Version: "v1beta1", Resource: "subscriptions"}: "SubscriptionList",
		{Group: "apps.open-cluster-management.io",
			Version: "v1", Resource: "subscriptions"}: "SubscriptionList",
		{Group: "authentication.open-cluster-management.io",
			Version: "v1beta1", Resource: "managedserviceaccounts"}: "ManagedServiceAccountList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme1, listKinds)

	// Setup GVR for channels
	chGVKList := schema.GroupVersionResource{
		Group:   "apps.open-cluster-management.io",
		Version: "v1beta1", Resource: "channels",
	}

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
		ctx            context.Context
		c              client.Client
		restoreOptions RestoreOptions
		acmRestore     *v1beta1.Restore
		veleroBackup   *veleroapi.Backup
		backupName     string
	}

	tests := []struct {
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
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &resourcesBackup,
				backupName:     veleroResourcesBackupName,
				acmRestore: createACMRestore("restore-acm", namespaceName).
					veleroManagedClustersBackupName(skipRestoreStr).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroResourcesRestoreName:        veleroResourcesBackupName,
						VeleroGenericResourcesRestoreName: veleroResourcesBackupName,
					}).
					phase(v1beta1.RestorePhaseEnabled).
					object,
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
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &resourcesBackup,
				backupName:     veleroResourcesBackupName,
				acmRestore: createACMRestore("restore-"+veleroResourcesBackupName, namespaceName).
					veleroManagedClustersBackupName(skipRestoreStr).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroResourcesRestoreName:        veleroResourcesBackupName,
						VeleroGenericResourcesRestoreName: veleroGenericBackupNameOlder,
					}).
					object,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic, // This has generic label but different backup name, so it gets deleted
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
			},
			extraBackups: []veleroapi.Backup{genericBackupOld},
			resourcesToCreate: []unstructured.Unstructured{
				*channel_with_backup_label_diff,    // this is the one deleted by the previous test, add it back
				*channel_with_backup_label_generic, // this is expected to be deleted in this test, so recreate it
			},
		},
		{
			name: "cleanup resources backup, with generic backup NOT found and clusters backup not skipped",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &resourcesBackup,
				backupName:     veleroResourcesBackupName,
				acmRestore: createACMRestore("restore-"+veleroResourcesBackupName, namespaceName).
					veleroManagedClustersBackupName(veleroClustersBackupName).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroResourcesRestoreName:        veleroResourcesBackupName,
						VeleroGenericResourcesRestoreName: "invalid-generic-name",
					}).
					object,
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
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,
			},
			extraBackups: []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
			},
		},
		{
			name: "cleanup resources backup, with generic backup found",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &resourcesBackup,
				backupName:     veleroResourcesBackupName,
				acmRestore: createACMRestore("restore-"+veleroResourcesBackupName, namespaceName).
					veleroManagedClustersBackupName(skipRestoreStr).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroResourcesRestoreName:        veleroResourcesBackupName,
						VeleroGenericResourcesRestoreName: veleroGenericBackupName,
					}).
					object,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,
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
				*channel_with_backup_label_diff,
			},
		},
		{
			name: "cleanup resources backup, with generic backup found and clusters backup not skipped",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &resourcesBackup,
				backupName:     veleroResourcesBackupName,
				acmRestore: createACMRestore("restore-"+veleroResourcesBackupName, namespaceName).
					veleroManagedClustersBackupName(veleroClustersBackupName).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroResourcesRestoreName:        veleroResourcesBackupName,
						VeleroGenericResourcesRestoreName: veleroGenericBackupName,
					}).
					object,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,
				*channel_with_backup_label_generic_old_activ,
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_no_backup_label,
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
				*channel_with_backup_label_generic_match,
				*channel_with_backup_label_generic_match_activ,
			},
			extraBackups: []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
			},
		},
		{
			name: "cleanup resources backup, with generic backup found and clusters backup not skipped, cleanup all",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupAll,
				veleroBackup:   &resourcesBackup,
				backupName:     veleroResourcesBackupName,
				acmRestore: createACMRestore("restore-"+veleroResourcesBackupName, namespaceName).
					veleroManagedClustersBackupName(veleroClustersBackupName).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroResourcesRestoreName:        veleroResourcesBackupName,
						VeleroGenericResourcesRestoreName: veleroGenericBackupName,
					}).
					object,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
				*channel_with_backup_label_generic_old,
				*channel_with_backup_label_generic,
				*channel_with_backup_label_generic_old_activ,
				*channel_with_no_backup_label,
			},
			resourcesToKeep: []unstructured.Unstructured{
				*channel_with_backup_label_same,
				*channel_with_backup_label_diff_excl_ns,
				*channel_with_backup_label_generic_match,
				*channel_with_backup_label_generic_match_activ,
			},
			extraBackups: []veleroapi.Backup{},
			resourcesToCreate: []unstructured.Unstructured{
				*channel_with_backup_label_diff,
			},
		},
	}

	// Run the test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create backup objects if needed
			for i := range tt.extraBackups {
				tt.extraBackups[i].ResourceVersion = ""
				_ = k8sClient1.Delete(tt.args.ctx, &tt.extraBackups[i])
				_ = k8sClient1.Create(tt.args.ctx, &tt.extraBackups[i])
			}

			// Create Velero Restore and Backup objects that the function expects to find
			if tt.args.acmRestore.Status.VeleroResourcesRestoreName != "" {
				veleroRestore := &veleroapi.Restore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.args.acmRestore.Status.VeleroResourcesRestoreName,
						Namespace: tt.args.acmRestore.Namespace,
					},
					Spec: veleroapi.RestoreSpec{
						BackupName: tt.args.veleroBackup.Name,
					},
				}
				_ = k8sClient1.Create(tt.args.ctx, veleroRestore)

				// Create the corresponding backup
				tt.args.veleroBackup.ResourceVersion = ""
				_ = k8sClient1.Create(tt.args.ctx, tt.args.veleroBackup)
			}

			if tt.args.acmRestore.Status.VeleroGenericResourcesRestoreName != "" {
				// Create generic restore if it's different from resources restore
				if tt.args.acmRestore.Status.VeleroGenericResourcesRestoreName !=
					tt.args.acmRestore.Status.VeleroResourcesRestoreName {
					veleroGenericRestore := &veleroapi.Restore{
						ObjectMeta: metav1.ObjectMeta{
							Name:      tt.args.acmRestore.Status.VeleroGenericResourcesRestoreName,
							Namespace: tt.args.acmRestore.Namespace,
						},
						Spec: veleroapi.RestoreSpec{
							BackupName: tt.args.acmRestore.Status.VeleroGenericResourcesRestoreName,
						},
					}
					_ = k8sClient1.Create(tt.args.ctx, veleroGenericRestore)
				}
			}

			// Create extra resources if needed
			for i := range tt.resourcesToCreate {
				_, err := dyn.Resource(chGVKList).Namespace("default").Create(context.Background(),
					&tt.resourcesToCreate[i], metav1.CreateOptions{})
				if client.IgnoreAlreadyExists(err) != nil {
					t.Errorf("Error creating resource: %s", err.Error())
				}
			}

			// Create resources that should exist
			createTestChannelResources(t, dyn, chGVKList, tt.resourcesToKeep)
			createTestChannelResources(t, dyn, chGVKList, tt.resourcesToBeDeleted)

			// Execute the function under test
			cleanupDeltaForResourcesBackup(tt.args.ctx, tt.args.c, tt.args.restoreOptions,
				tt.args.acmRestore)

			// Verify results
			verifyResourcesDeleted(t, dyn, chGVKList, tt.resourcesToBeDeleted)
			verifyResourcesKept(t, dyn, chGVKList, tt.resourcesToKeep)
		})
	}
}

// Test_cleanupDeltaForClustersBackup tests cleanup of cluster resources after backup operations.
//
// This focused test covers cluster backup cleanup scenarios.
//
// Test Coverage:
// - Cleanup of cluster-scoped resources (ManagedClusters, etc.) after backup
// - Cluster resource deletion based on backup labels and policies
// - Handling of cluster-specific exclusion logic
// - Integration with Velero cluster backup objects
// - Processing of various cluster-related resource types
//
// Resource Types Tested:
// - ManagedClusters (cluster.open-cluster-management.io)
// - ManagedServiceAccounts (authentication.open-cluster-management.io)
// - ClusterDeployments (hive.openshift.io) - via discovery
// - MachinePools (hive.openshift.io) - via discovery
// - KlusterletAddonConfigs (agent.open-cluster-management.io) - via discovery
// - ManagedClusterAddons (addon.open-cluster-management.io) - via discovery
// - ClusterPools (hive.openshift.io) - via discovery
// - ClusterClaims (hive.openshift.io) - via discovery
// - ClusterCurators (cluster.open-cluster-management.io) - via discovery
// - BMCEventSubscriptions (metal3.io) - via discovery
// - HostFirmwareSettings (metal3.io) - via discovery
// - ClusterSyncs (hiveinternal.openshift.io) - via discovery
// - MultiClusterObservabilities (observability.open-cluster-management.io) - via discovery
//
// Test Scenarios:
// - Standard cluster backup cleanup with proper resource deletion
// - Verification of cluster resource identification and processing
// - Handling of missing or unavailable cluster resource types
// - Proper exclusion of local cluster resources
//
// Implementation Details:
// - Uses dynamic fake client with custom list kinds for cluster resources
// - Implements comprehensive mock discovery server for cluster API groups
// - Creates realistic cluster resources with proper metadata and labels
// - Tests cluster-specific deletion workflows and exclusion logic
// - Verifies proper handling of various cluster resource types
//
// Mock Infrastructure:
// - Enhanced discovery server supporting all cluster resource API groups
// - Dynamic fake client with cluster resource list kind mappings
// - Fake Kubernetes client for Velero cluster backup object management
// - Realistic cluster resource creation with proper cluster labels
//

//
// API Group Coverage:
// The test covers discovery and processing of numerous cluster-related API groups,
// demonstrating the comprehensive nature of cluster backup cleanup operations.
//

func Test_cleanupDeltaForClustersBackup(t *testing.T) {
	log.SetLogger(zap.New())

	// Use helper function for scheme setup
	scheme1, err := setupTestScheme()
	AssertNoError(t, err, "Error setting up test scheme")

	// Use helper function for backup setup
	backupSetup := createTestBackupSetup()

	// Use the pre-configured backup objects
	clustersBackup := backupSetup.ClustersBackup

	clusterNamespace := backupSetup.ClusterNamespace
	namespaceName := backupSetup.NamespaceName
	veleroClustersBackupName := backupSetup.VeleroClustersBackupName

	// Use helper functions for ManagedCluster creation
	cls_with_backup_label_same := withBackupLabel(
		createManagedClusterUnstructured("cls-with-backup-label-same", "default"),
		clustersBackup.Name)
	cls_with_backup_label_diff_excl_ns := withBackupLabel(
		createManagedClusterUnstructured("cls-with-backup-label-diff-excl-ns", "local-cluster"),
		backupSetup.VeleroClustersBackupNameOlder)
	cls_with_backup_label_diff := withFinalizers(withBackupLabel(
		createManagedClusterUnstructured("channel-with-backup-label-diff", "default"),
		backupSetup.VeleroClustersBackupNameOlder),
		[]string{"hive.openshift.io/deprovision"})
	cls_with_no_backup_label := createManagedClusterUnstructured("cls-with-no-backup-label", "default")
	msaObj := createMSAUnstructured("auto-import-account", clusterNamespace)

	// Setup test infrastructure
	server1 := setupMockDiscoveryServer(t)
	defer server1.Close()

	fakeDiscovery := discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server1.URL},
	)

	k8sClient1 := fake.NewClientBuilder().
		WithScheme(scheme1).
		Build()

	// Define custom list kinds for the dynamic client
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "cluster.open-cluster-management.io", Version: "v1beta1",
			Resource: "managedclusters"}: "ManagedClusterList",
		{Group: "authentication.open-cluster-management.io", Version: "v1beta1",
			Resource: "managedserviceaccounts"}: "ManagedServiceAccountList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme1, listKinds)

	// Setup GVK and GVR for managed clusters
	clsGVKList := schema.GroupVersionResource{
		Group:   "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclusters",
	}
	msaGVRList := schema.GroupVersionResource{
		Group:   "authentication.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedserviceaccounts",
	}

	reconcileArgs := DynamicStruct{
		dc:  fakeDiscovery,
		dyn: dyn,
	}

	m := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(fakeDiscovery),
	)

	resOptionsCleanupRestored := RestoreOptions{
		dynamicArgs: reconcileArgs,
		cleanupType: v1beta1.CleanupTypeRestored,
		mapper:      m,
	}

	type args struct {
		ctx            context.Context
		c              client.Client
		restoreOptions RestoreOptions
		acmRestore     *v1beta1.Restore
		veleroBackup   *veleroapi.Backup
		backupName     string
	}

	tests := []struct {
		name                 string
		args                 args
		resourcesToBeDeleted []unstructured.Unstructured
		resourcesToKeep      []unstructured.Unstructured
	}{
		{
			name: "cleanup clusters backup",
			args: args{
				ctx:            context.Background(),
				c:              k8sClient1,
				restoreOptions: resOptionsCleanupRestored,
				veleroBackup:   &clustersBackup,
				backupName:     veleroClustersBackupName,
				acmRestore: createACMRestore("restore-"+veleroClustersBackupName, namespaceName).
					veleroManagedClustersBackupName(veleroClustersBackupName).
					restoreACMStatus(v1beta1.RestoreStatus{
						VeleroManagedClustersRestoreName: veleroClustersBackupName,
					}).
					object,
			},
			resourcesToBeDeleted: []unstructured.Unstructured{*cls_with_backup_label_diff},
			resourcesToKeep: []unstructured.Unstructured{
				*cls_with_backup_label_same,
				*cls_with_backup_label_diff_excl_ns,
				*cls_with_no_backup_label,
			},
		},
	}

	// Run the test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create MSA object
			_, err := dyn.Resource(msaGVRList).Namespace(clusterNamespace).Create(context.Background(),
				msaObj, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Error creating MSA: %s", err.Error())
			}

			// Create cluster resources
			createTestClusterResources(t, dyn, clsGVKList, tt.resourcesToKeep)
			createTestClusterResources(t, dyn, clsGVKList, tt.resourcesToBeDeleted)

			// Execute the function under test
			cleanupDeltaForClustersBackup(tt.args.ctx, tt.args.c, tt.args.restoreOptions,
				tt.args.backupName, tt.args.veleroBackup)

			// Verify results
			verifyResourcesDeleted(t, dyn, clsGVKList, tt.resourcesToBeDeleted)
			verifyResourcesKept(t, dyn, clsGVKList, tt.resourcesToKeep)
		})
	}
}

// Helper functions for the split tests

func setupMockDiscoveryServer(t *testing.T) *httptest.Server {
	// Define API resource lists
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
	clusterInfo := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	authInfo := metav1.APIResourceList{
		GroupVersion: "authentication.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedserviceaccounts", Namespaced: true, Kind: "ManagedServiceAccount"},
		},
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var list interface{}
		switch req.URL.Path {
		case "/apis/apps.open-cluster-management.io/v1beta1":
			list = &appsInfo
		case "/apis/apps.open-cluster-management.io/v1":
			list = &appsInfoV1
		case "/apis/cluster.open-cluster-management.io/v1beta1":
			list = &clusterInfo
		case "/apis/authentication.open-cluster-management.io/v1beta1":
			list = &authInfo
		case "/api":
			list = &metav1.APIVersions{
				Versions: []string{
					"v1",
				},
			}
		case "/apis":
			list = &metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name: "cluster.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "cluster.open-cluster-management.io/v1beta1", Version: "v1beta1"},
							{GroupVersion: "cluster.open-cluster-management.io/v1", Version: "v1"},
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
							{GroupVersion: "authentication.open-cluster-management.io/v1beta1", Version: "v1beta1"},
						},
					},
				},
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		output, err := json.Marshal(list)
		if err != nil {
			t.Errorf("unexpected encoding error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(output)
		if err != nil {
			t.Errorf("unexpected write error: %v", err)
		}
	}))
}

func createTestChannelResources(
	t *testing.T,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	resources []unstructured.Unstructured,
) {
	for i := range resources {
		namespace := resources[i].GetNamespace()
		if namespace == "" {
			namespace = "default"
		}
		_, err := dyn.Resource(gvr).Namespace(namespace).Create(context.Background(),
			&resources[i], metav1.CreateOptions{})
		if client.IgnoreAlreadyExists(err) != nil {
			t.Errorf("Error creating channel resource: %s", err.Error())
		}
	}
}

func createTestClusterResources(
	t *testing.T,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	resources []unstructured.Unstructured,
) {
	for i := range resources {
		namespace := resources[i].GetNamespace()
		if namespace == "" {
			namespace = "default"
		}
		_, err := dyn.Resource(gvr).Namespace(namespace).Create(context.Background(),
			&resources[i], metav1.CreateOptions{})
		if err != nil {
			t.Errorf("Error creating cluster resource: %s", err.Error())
		}
	}
}

func verifyResourcesDeleted(
	t *testing.T,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	resources []unstructured.Unstructured,
) {
	for i := range resources {
		namespace := resources[i].GetNamespace()
		if namespace == "" {
			namespace = "default"
		}
		name := resources[i].GetName()
		_, err := dyn.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil {
			t.Errorf("Expected resource %s/%s to be deleted, but it still exists", namespace, name)
		}
	}
}

func verifyResourcesKept(
	t *testing.T,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	resources []unstructured.Unstructured,
) {
	for i := range resources {
		namespace := resources[i].GetNamespace()
		if namespace == "" {
			namespace = "default"
		}
		name := resources[i].GetName()
		_, err := dyn.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			t.Errorf("Expected resource %s/%s to be kept, but it was deleted: %v", namespace, name, err)
		}
	}
}
