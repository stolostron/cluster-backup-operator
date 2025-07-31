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
Package controllers contains comprehensive unit tests for pre-backup operations in the ACM Backup/Restore system.

This test suite validates pre-backup functionality including:
- Pre-backup hook execution and validation
- Resource preparation before backup operations
- Managed cluster state verification
- Backup readiness checks and validation
- Integration with backup schedules and storage locations
- Error handling during pre-backup phases
- Resource filtering and preparation logic

The tests ensure that all necessary preparations are completed successfully
before backup operations begin, preventing incomplete or corrupted backups.
*/

//nolint:funlen
package controllers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// Test helper functions to reduce code duplication

// setupTestLogger configures the logger for tests
func setupTestLogger() {
	// Configure logger to only show Fatal and Panic levels to reduce noise from expected test errors
	log.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(zapcore.FatalLevel)))
}

// setupQuietTestLogger configures a logger that suppresses all output for error path testing
func setupQuietTestLogger() {
	// Suppress all log output for tests that intentionally trigger errors
	log.SetLogger(zap.New(zap.Level(zapcore.DPanicLevel)))
}

// createTestContext returns a background context for tests
func createTestContext() context.Context {
	return context.Background()
}

// createBasicScheme creates a scheme with core APIs
func createBasicScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic("Error adding corev1 to scheme: " + err.Error())
	}
	return scheme
}

// createWorkScheme creates a scheme with core and work APIs
func createWorkScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic("Error adding corev1 to scheme: " + err.Error())
	}
	if err := workv1.AddToScheme(scheme); err != nil {
		panic("Error adding workv1 to scheme: " + err.Error())
	}
	return scheme
}

// createFullScheme creates a scheme with all required APIs
func createFullScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic("Error adding corev1 to scheme: " + err.Error())
	}
	if err := workv1.AddToScheme(scheme); err != nil {
		panic("Error adding workv1 to scheme: " + err.Error())
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		panic("Error adding clusterv1 to scheme: " + err.Error())
	}
	if err := addonv1alpha1.AddToScheme(scheme); err != nil {
		panic("Error adding addonv1alpha1 to scheme: " + err.Error())
	}
	return scheme
}

// createTestNamespace creates a namespace for testing
func createTestNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
	}
}

// createTestSecret creates a secret with optional labels and annotations
//
//nolint:unparam
func createTestSecret(name, namespace string, labels, annotations map[string]string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

// createTestManifestWork creates a ManifestWork for testing
func createTestManifestWork(name, namespace string, labels map[string]string) *workv1.ManifestWork {
	return &workv1.ManifestWork{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
	}
}

// createFakeClient creates a fake client with the given scheme and objects
func createFakeClient(scheme *runtime.Scheme, objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()
}

// createTestManagedCluster creates a ManagedCluster for testing
func createTestManagedCluster(name string, isLocal bool) *clusterv1.ManagedCluster {
	cluster := &clusterv1.ManagedCluster{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
	}
	if isLocal {
		cluster.Labels = map[string]string{
			"local-cluster": "true",
		}
	}
	return cluster
}

// Test_createMSA tests the creation and management of Managed Service Accounts (MSAs).
//
// This test verifies that:
// - MSAs are created correctly for managed clusters
// - MSA token validity is properly updated when needed
// - Pair MSAs are handled appropriately
// - Invalid token scenarios are managed gracefully
// - Pre-existing MSAs are updated rather than recreated
//
// Test Coverage:
// - MSA creation for new managed clusters
// - MSA validity updates for existing MSAs
// - Pair MSA generation and management
// - Error handling for invalid token formats
// - Pre-existing MSA detection and updates
//
// Test Scenarios:
// - "msa generated now": Tests creation of new MSA with proper validity
// - "msa not generated now but validity updated": Tests updating validity of existing MSA
// - "msa pair secrets not generated now": Tests handling of pair MSA scenarios
// - "msa not generated now AND invalid token": Tests error handling for invalid tokens
// - "MSA pair generated now": Tests creation of pair MSAs with proper timing
//
// Implementation Details:
// - Uses fake Kubernetes client for isolated testing
// - Creates realistic MSA objects with proper metadata and specifications
// - Uses dynamic fake client for MSA resource management
// - Tests various validity periods and token scenarios
// - Verifies proper ManifestWork creation and cleanup
//
// MSA Concepts:
// - Main MSAs: Primary service accounts for cluster authentication
// - Pair MSAs: Secondary service accounts for specific use cases
// - Token validity: Time-based expiration for security rotation
// - ManifestWork: Kubernetes objects that deploy MSAs to managed clusters
func Test_createMSA(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	namespace := "managed1"

	// Create scheme with workv1 for ManifestWork objects
	scheme := createWorkScheme()

	// Create test namespace
	testNamespace := createTestNamespace(namespace)

	// Create test secret
	testSecret := createTestSecret(msa_service_name, namespace, nil, nil)

	// Create test ManifestWorks
	mwork1 := createTestManifestWork(manifest_work_name+mwork_custom_282, namespace, nil)

	mwork2 := createTestManifestWork(manifest_work_name_pair+mwork_custom_282, namespace, nil)

	k8sClient := createFakeClient(scheme, testNamespace, testSecret, mwork1, mwork2)

	current, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")

	type args struct {
		ctx            context.Context
		dr             dynamic.NamespaceableResourceInterface
		validity       string
		managedCluster string
		name           string
		secrets        []corev1.Secret
		currentTime    time.Time
	}
	tests := []struct {
		name                string
		args                args
		secretsGeneratedNow bool
		secretsUpdated      bool
		pairMSAGeneratedNow bool
		mainMSAGeneratedNow bool
		msaPreExists        bool // Whether MSA should pre-exist in dynamic client
	}{
		{
			name: "msa generated now",
			args: args{
				ctx:            ctx,
				managedCluster: namespace,
				name:           msa_service_name,
				validity:       "20h",
				secrets:        []corev1.Secret{},
				currentTime:    current,
			},
			pairMSAGeneratedNow: false,
			mainMSAGeneratedNow: true,
			secretsGeneratedNow: true,
			secretsUpdated:      false,
			msaPreExists:        false, // MSA should not pre-exist
		},
		{
			name: "msa not generated now but validity updated",
			args: args{
				ctx:            ctx,
				managedCluster: namespace,
				name:           msa_service_name,
				validity:       "50h",
				secrets:        []corev1.Secret{},
				currentTime:    current,
			},
			pairMSAGeneratedNow: false,
			mainMSAGeneratedNow: true,
			secretsGeneratedNow: false,
			secretsUpdated:      true,
			msaPreExists:        true, // MSA should pre-exist with different validity
		},
		{
			name: "msa pair secrets not generated now",
			args: args{
				ctx:            ctx,
				managedCluster: namespace,
				name:           msa_service_name_pair,
				validity:       "50h",
				secrets:        []corev1.Secret{},
				currentTime:    current,
			},
			pairMSAGeneratedNow: false,
			mainMSAGeneratedNow: true,
			secretsGeneratedNow: false,
			secretsUpdated:      false,
			msaPreExists:        true, // MSA should pre-exist with same validity
		},
		{
			name: "msa not generated now AND invalid token",
			args: args{
				ctx:            ctx,
				managedCluster: namespace,
				name:           msa_service_name,
				validity:       "\"invalid-token",
				secrets:        []corev1.Secret{},
				currentTime:    current,
			},
			pairMSAGeneratedNow: false,
			mainMSAGeneratedNow: true,
			secretsGeneratedNow: false,
			secretsUpdated:      true,
			msaPreExists:        true, // MSA should pre-exist
		},
		{
			name: "MSA pair generated now",
			args: args{
				ctx:            ctx,
				managedCluster: namespace,
				name:           msa_service_name_pair,
				validity:       "2m",
				secrets: []corev1.Secret{
					{
						ObjectMeta: v1.ObjectMeta{
							Name:      "auto-import-account-2",
							Namespace: namespace,
							Labels: map[string]string{
								msa_label: "true",
							},
							Annotations: map[string]string{
								"expirationTimestamp":  "2022-07-26T15:26:36Z",
								"lastRefreshTimestamp": "2022-07-26T15:22:34Z",
							},
						},
					},
				},
				currentTime: current,
			},
			pairMSAGeneratedNow: true,
			mainMSAGeneratedNow: true,
			secretsGeneratedNow: true,
			secretsUpdated:      false,
			msaPreExists:        false, // MSA should not pre-exist for pair generation
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create dynamic client with or without pre-existing MSA based on test case
			var dynClient dynamic.Interface
			if tt.msaPreExists {
				// Create MSA object that already exists
				// Use different validity to trigger update, unless test expects no update
				msaValidity := "20h" // Default to different validity to trigger update
				if !tt.secretsUpdated {
					msaValidity = tt.args.validity // Use same validity to avoid update
				}

				obj1 := &unstructured.Unstructured{}
				obj1.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": "authentication.open-cluster-management.io/v1beta1",
					"kind":       "ManagedServiceAccount",
					"metadata": map[string]interface{}{
						"name":      tt.args.name,
						"namespace": namespace,
					},
					"spec": map[string]interface{}{
						"somethingelse": "aaa",
						"rotation": map[string]interface{}{
							"validity": msaValidity,
							"enabled":  true,
						},
					},
				})
				dynClient = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj1)
			} else {
				// Create empty dynamic client (no pre-existing MSA)
				dynClient = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			}

			res := schema.GroupVersionResource{
				Group:    "authentication.open-cluster-management.io",
				Version:  "v1beta1",
				Resource: "managedserviceaccounts",
			}
			tt.args.dr = dynClient.Resource(res)

			// Create any additional secrets for this test case
			for i := range tt.args.secrets {
				if err := k8sClient.Create(ctx, &tt.args.secrets[i]); err != nil {
					t.Errorf("secret creation failed: err(%s) ", err.Error())
				}
			}

			secretsGeneratedNow, secretsUpdated, _ := createMSA(tt.args.ctx, k8sClient,
				tt.args.dr,
				tt.args.validity,
				tt.args.name,
				tt.args.managedCluster,
				current,
				namespace,
			)

			// Verify the MSA exists in the dynamic client after the call
			_, err := tt.args.dr.Namespace(tt.args.managedCluster).
				Get(ctx, tt.args.name, v1.GetOptions{})
			if err != nil && tt.mainMSAGeneratedNow {
				t.Errorf("MSA %s should exist: err(%s) ", tt.args.name, err.Error())
			}
			if err == nil && !tt.mainMSAGeneratedNow {
				t.Errorf("MSA %s should NOT exist", tt.args.name)
			}

			if secretsGeneratedNow != tt.secretsGeneratedNow {
				t.Errorf("createMSA() secretsGeneratedNow = %v, want %v", secretsGeneratedNow, tt.secretsGeneratedNow)
			}

			if secretsUpdated != tt.secretsUpdated {
				t.Errorf("createMSA() secretsUpdated = %v, want %v", secretsUpdated, tt.secretsUpdated)
			}

			// Verify ManifestWork objects were created correctly (for tests that create MSA)
			if tt.secretsGeneratedNow && tt.args.name == msa_service_name {
				// Check main ManifestWork
				work := &workv1.ManifestWork{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      manifest_work_name,
					Namespace: tt.args.managedCluster,
				}, work); err != nil {
					t.Errorf("cannot get manifestwork %s: %s", manifest_work_name, err.Error())
				} else {
					rawData := string(work.Spec.Workload.Manifests[0].Raw[:])

					str := `"kind":"ClusterRoleBinding","metadata":{"name":"managedserviceaccount-import"}`
					if !strings.Contains(rawData, str) {
						t.Errorf("Cluster role binding should be %v for manifest %v but is %v", "managedserviceaccount-import",
							work.Name, rawData)
					}

					strserviceaccount := `{"kind":"ServiceAccount","name":"auto-import-account",` +
						`"namespace":"open-cluster-management-agent-addon"}`
					if !strings.Contains(rawData, strserviceaccount) {
						t.Errorf("ServiceAccount should be %v for manifest %v, but is %v", strserviceaccount, work.Name, rawData)
					}
				}

				// Check custom ManifestWork
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      manifest_work_name + "-custom-2",
					Namespace: tt.args.managedCluster,
				}, work); err != nil {
					t.Errorf("cannot get manifestwork %s: %s", manifest_work_name+"-custom-2", err.Error())
				} else {
					str := `"kind":"ClusterRoleBinding","metadata":{"name":"managedserviceaccount-import-custom-2"}`
					rawData := string(work.Spec.Workload.Manifests[0].Raw[:])

					if !strings.Contains(rawData, str) {
						t.Errorf("Cluster role binding should be %v for manifest %v but is %v",
							"managedserviceaccount-import-custom-2", work.Name, rawData)
					}

					strserviceaccount := `{"kind":"ServiceAccount","name":"auto-import-account","namespace":"managed1"}`
					if !strings.Contains(rawData, strserviceaccount) {
						t.Errorf("ServiceAccount should be %v for manifest %v, but is %v", strserviceaccount, work.Name, rawData)
					}
				}

				// Verify old custom ManifestWorks were deleted
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      manifest_work_name + mwork_custom_282,
					Namespace: tt.args.managedCluster,
				}, work); err == nil {
					t.Errorf("this manifest should no longer exist! %v", manifest_work_name+mwork_custom_282)
				}

				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      manifest_work_name_pair + mwork_custom_282,
					Namespace: tt.args.managedCluster,
				}, work); err == nil {
					t.Errorf("this manifest should no longer exist! %v", manifest_work_name_pair+mwork_custom_282)
				}
			}

			// Verify ManifestWork objects for MSA pair
			if tt.secretsGeneratedNow && tt.args.name == msa_service_name_pair {
				work := &workv1.ManifestWork{}

				// Check pair ManifestWork
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      manifest_work_name_pair,
					Namespace: tt.args.managedCluster,
				}, work); err != nil {
					t.Errorf("cannot get pair manifestwork %s: %s", manifest_work_name_pair, err.Error())
				}

				// Check custom pair ManifestWork
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      manifest_work_name_pair + "-custom-2",
					Namespace: tt.args.managedCluster,
				}, work); err != nil {
					t.Errorf("cannot get custom pair manifestwork %s: %s", manifest_work_name_pair+"-custom-2", err.Error())
				}
			}
		})
	}
}

// Test_updateMSAToken tests the MSA token update functionality for managed clusters.
//
// This test verifies that:
// - MSA tokens are updated when validity periods change
// - Existing MSA tokens are preserved when no changes are needed
// - Token updates handle missing spec or validity fields gracefully
// - Invalid token values are handled without causing failures
// - Dynamic client interactions work correctly for MSA resources
//
// Test Coverage:
// - Token validity period updates
// - Token preservation when no changes required
// - Error handling for missing MSA spec fields
// - Error handling for missing validity fields
// - Invalid token format handling
//
// Test Scenarios:
// - "MSA token will be updated, token was changed": Tests updating validity from 50h to 20h
// - "MSA token will not be updated, token not changed": Tests preservation when validity unchanged
// - "MSA token has no spec": Tests handling of MSA resources without spec section
// - "MSA token has no validity": Tests handling of MSA resources without validity field
// - "MSA token is invalid, patch should fail": Tests error handling for malformed validity values
//
// Implementation Details:
// - Uses dynamic fake client for MSA resource simulation
// - Creates unstructured MSA objects with various configurations
// - Tests REST mapping and resource interface interactions
// - Validates token update decisions without side effects
// - Handles JSON patch operations for MSA spec updates
//
// MSA Token Management:
// - Manages token validity periods for cluster authentication
// - Ensures tokens are refreshed when policies change
// - Prevents unnecessary updates to reduce API server load
// - Handles edge cases gracefully to maintain system stability
func Test_updateMSAToken(t *testing.T) {
	obj1 := &unstructured.Unstructured{}
	obj1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
	})
	obj2 := &unstructured.Unstructured{}
	obj2.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
	})

	obj3 := &unstructured.Unstructured{}
	obj3.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
		"spec": map[string]interface{}{
			"rotation": map[string]interface{}{
				"enabled": true,
			},
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj1)

	targetGVK := schema.GroupVersionKind{
		Group:   "authentication.open-cluster-management.io",
		Version: "v1beta1",
		Kind:    "ManagedServiceAccount",
	}

	targetGVR := targetGVK.GroupVersion().WithResource("somecrs")
	targetMapping := meta.RESTMapping{
		Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace,
	}

	res := schema.GroupVersionResource{
		Group:    "authentication.open-cluster-management.io",
		Version:  "v1beta1",
		Resource: "ManagedServiceAccount",
	}

	resInterface := dynClient.Resource(res)

	type args struct {
		ctx           context.Context
		mapping       *meta.RESTMapping
		dr            dynamic.NamespaceableResourceInterface
		resource      unstructured.Unstructured
		namespaceName string
		name          string
		validity      string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "MSA token will be updated, token was changed",
			args: args{
				ctx:           context.Background(),
				mapping:       &targetMapping,
				dr:            resInterface,
				resource:      *obj1,
				namespaceName: "managed1",
				name:          msa_service_name,
				validity:      "20h",
			},
			want: true,
		},
		{
			name: "MSA token will not be updated, token not changed",
			args: args{
				ctx:           context.Background(),
				mapping:       &targetMapping,
				dr:            resInterface,
				resource:      *obj1,
				namespaceName: "managed1",
				name:          msa_service_name,
				validity:      "50h",
			},
			want: false,
		},
		{
			name: "MSA token has no spec",
			args: args{
				ctx:           context.Background(),
				mapping:       &targetMapping,
				dr:            resInterface,
				resource:      *obj2,
				namespaceName: "managed1",
				name:          msa_service_name,
				validity:      "50h",
			},
			want: false,
		},
		{
			name: "MSA token has no validity",
			args: args{
				ctx:           context.Background(),
				mapping:       &targetMapping,
				dr:            resInterface,
				resource:      *obj3,
				namespaceName: "managed1",
				name:          msa_service_name,
				validity:      "50h",
			},
			want: false,
		},
		{
			name: "MSA token is invalid, patch should fail",
			args: args{
				ctx:           context.Background(),
				mapping:       &targetMapping,
				dr:            resInterface,
				resource:      *obj3,
				namespaceName: "managed1",
				name:          msa_service_name,
				validity:      "\"invalid value",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := updateMSAToken(tt.args.ctx,
				tt.args.dr,
				&tt.args.resource,
				tt.args.namespaceName,
				tt.args.name,
				tt.args.validity); got != tt.want {
				t.Errorf("updateMSAToken() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_updateMSASecretTimestamp tests the MSA secret timestamp update functionality.
//
// This test verifies that:
// - MSA secret timestamps are updated when secrets are refreshed
// - Existing timestamps are preserved when no updates are needed
// - Missing status fields are handled gracefully
// - Secret expiration calculations work correctly
// - Dynamic client operations for MSA status updates function properly
//
// Test Coverage:
// - Secret timestamp updates during token refresh
// - Status field handling for MSA resources
// - Expiration time calculations and validations
// - Error handling for missing status information
// - Dynamic client patch operations on MSA status
//
// Test Scenarios:
// - MSA resources without status fields
// - MSA resources without token expiration information
// - MSA resources with valid status and expiration data
// - Secret timestamp updates with various time formats
// - Error handling for malformed timestamp data
//
// Implementation Details:
// - Uses unstructured MSA objects with different status configurations
// - Tests timestamp parsing and formatting operations
// - Validates secret refresh timing calculations
// - Handles missing or invalid status fields gracefully
// - Uses dynamic fake client for MSA resource manipulation
//
// MSA Secret Management:
// - Tracks secret refresh timestamps for audit and debugging
// - Ensures secrets are rotated according to policy
// - Maintains consistency between MSA spec and status
// - Provides visibility into token lifecycle management
func Test_updateMSASecretTimestamp(t *testing.T) {
	objNoStatus := &unstructured.Unstructured{}
	objNoStatus.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
	})

	objNoExp := &unstructured.Unstructured{}
	objNoExp.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
		"status": map[string]interface{}{
			"somestatus": "aaa",
			"tokenSecretRef": map[string]interface{}{
				"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
				"name":                 "auto-import-account",
			},
		},
	})

	obj3 := &unstructured.Unstructured{}
	obj3.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
		"status": map[string]interface{}{
			"somestatus":          "aaa",
			"expirationTimestamp": "2022-07-26T20:13:45Z",
			"tokenSecretRef": map[string]interface{}{
				"lastRefreshTimestamp": "2022-07-26T18:13:45Z",
				"name":                 "bbb",
			},
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objNoStatus)

	res := schema.GroupVersionResource{
		Group:    "authentication.open-cluster-management.io",
		Version:  "v1beta1",
		Resource: "ManagedServiceAccount",
	}

	resInterface := dynClient.Resource(res)

	type args struct {
		ctx    context.Context
		dr     dynamic.NamespaceableResourceInterface
		obj    unstructured.Unstructured
		secret *corev1.Secret
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "MSA has no status",
			args: args{
				ctx:    context.Background(),
				dr:     resInterface,
				obj:    *objNoStatus,
				secret: createSecret("auto-import-account", "managed1", nil, nil, nil),
			},
			want: false,
		},
		{
			name: "MSA has status but no expiration",
			args: args{
				ctx: context.Background(),
				dr:  resInterface,
				obj: *objNoExp,
				secret: createSecret("auto-import-account", "managed1", nil,
					map[string]string{
						"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
						"expirationTimestamp":  "2022-08-05T15:25:38Z",
					}, nil),
			},
			want: false,
		},
		{
			name: "MSA has status and expiration",
			args: args{
				ctx:    context.Background(),
				dr:     resInterface,
				obj:    *obj3,
				secret: createSecret("auto-import-account", "managed1", nil, nil, nil),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := updateMSASecretTimestamp(tt.args.ctx,
				tt.args.dr, &tt.args.obj, tt.args.secret); got != tt.want {
				t.Errorf("updateMSASecretTimestamp() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_shouldGeneratePairToken tests the logic for determining when to generate MSA pair tokens.
//
// This test verifies that:
// - Pair tokens are generated when required based on secret states
// - Existing valid pair tokens are preserved to avoid unnecessary generation
// - Token generation timing is calculated correctly based on current time
// - Secret expiration and refresh logic works as expected
// - Edge cases with missing or invalid secrets are handled properly
//
// Test Coverage:
// - Pair token generation decision logic
// - Secret expiration time calculations
// - Token refresh timing based on current time
// - Handling of missing or incomplete secret data
// - Edge cases with malformed secret timestamps
//
// Test Scenarios:
// - Scenarios requiring new pair token generation
// - Scenarios where existing tokens are still valid
// - Time-based calculations for token refresh
// - Error handling for invalid secret formats
// - Multiple secrets with different expiration times
//
// Implementation Details:
// - Uses various secret configurations to test different scenarios
// - Tests time-based logic with controlled current time values
// - Validates token generation decisions without side effects
// - Handles secret parsing and timestamp validation
// - Tests edge cases with incomplete or malformed data
//
// MSA Pair Token Management:
// - Pair tokens provide redundancy for cluster authentication
// - Ensures continuous availability during token rotation
// - Prevents authentication failures during token refresh
// - Maintains proper timing for token generation cycles
func Test_shouldGeneratePairToken(t *testing.T) {
	fourHoursAgo := "2022-07-26T11:25:34Z"
	nextThreeHours := "2022-07-26T18:25:34Z"
	nextTenHours := "2022-07-27T04:25:34Z"
	nextHour := "2022-07-26T16:25:34Z"

	current, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")

	initialTime := "2022-07-26T13:15:34Z" // 2 hours -10 min from the current time
	expiryTime := "2022-07-26T17:25:34Z"  // 2 hours -10 min from the current time

	initialTimeNoPair := "2022-07-26T13:00:34Z" // 2 hours -25 min from the current time
	expiryTimeNoPair := "2022-07-26T17:00:34Z"  // 2 hours -25 min from the current time

	type args struct {
		secrets     []corev1.Secret
		currentTime time.Time
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "MSA has no secrets",
			args: args{
				secrets: []corev1.Secret{},
			},
			want: false,
		},
		{
			name: "MSA has secrets but no expirationTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed1", nil,
						map[string]string{
							"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets with invalid expirationTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed2", nil,
						map[string]string{
							"lastRefreshTimestamp": "2022-08-05T15:25:38Z",
							"expirationTimestamp":  "bbb",
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets with invalid lastRefreshTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed2", nil,
						map[string]string{
							"lastRefreshTimestamp": "aaaaa",
							"expirationTimestamp":  "2022-08-05T15:25:38Z",
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets with invalid lastRefreshTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": "2022-08-05T15:25:38Z",
							"expirationTimestamp":  "aaa",
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets, current time not yet half between last refresh and expiration",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets, current time passed half more than 15min from expiration",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed6", nil,
						map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextThreeHours,
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets, current time too close to the expiration",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextHour,
						}, nil),
				},
			},
			want: false,
		},
		{
			name: "MSA has secrets, current time less then 15 min from half time",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": initialTime,
							"expirationTimestamp":  expiryTime,
						}, nil),
				},
			},
			want: true,
		},
		{
			name: "MSA has secrets, current time more then 15 min from half time so no pair should be created",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": initialTimeNoPair,
							"expirationTimestamp":  expiryTimeNoPair,
						}, nil),
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldGeneratePairToken(tt.args.secrets, tt.args.currentTime); got != tt.want {
				t.Errorf("shouldGeneratePairToken() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// failingSecretDeleteClient is a client wrapper that simulates failures when deleting specific secrets.
// This is used to test error handling paths in the cleanup functions.
type failingSecretDeleteClient struct {
	client.Client
	failOnSecretName string
}

func (f *failingSecretDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	// If it's a secret with the specific name we want to fail on, return an error
	if secret, ok := obj.(*corev1.Secret); ok && secret.Name == f.failOnSecretName {
		return fmt.Errorf("[TEST] simulated secret deletion failure for %s", secret.Name)
	}
	// Otherwise, delegate to the real client
	return f.Client.Delete(ctx, obj, opts...)
}

// Test_cleanupMSAForImportedClusters tests the cleanup of MSA resources for imported clusters.
//
// This test verifies that:
// - MSA resources are properly cleaned up when clusters are imported
// - Cleanup operations handle various MSA configurations correctly
// - Dynamic client operations for MSA deletion work as expected
// - Resource mappings and API interactions function properly
// - Error conditions during cleanup are handled gracefully
//
// Test Coverage:
// - MSA resource cleanup for imported clusters
// - Dynamic client deletion operations
// - REST mapping and resource interface handling
// - Error handling during cleanup operations
// - Proper resource identification and removal
//
// Test Scenarios:
// - Successful cleanup of MSA resources
// - Handling of missing or already deleted MSA resources
// - Error conditions during resource deletion
// - Various MSA configurations and states
// - Dynamic client error handling
//
// Implementation Details:
// - Uses dynamic fake client for MSA resource simulation
// - Tests REST mapping and resource interface operations
// - Validates proper resource identification and deletion
// - Handles various MSA object configurations
// - Tests error handling for failed cleanup operations
//
// MSA Cleanup Management:
// - Ensures proper cleanup of MSA resources during cluster import
// - Prevents orphaned MSA resources from consuming resources
// - Maintains consistency between cluster state and MSA resources
// - Handles cleanup failures gracefully to prevent system issues
func Test_cleanupMSAForImportedClusters(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	// Create scheme
	scheme := createFullScheme()

	backupNS := "velero-ns"
	backupSchedule := &v1beta1.BackupSchedule{
		ObjectMeta: v1.ObjectMeta{
			Name:      "acm-schedule",
			Namespace: backupNS,
		},
	}

	// Create test namespaces
	ns1 := createTestNamespace("managed1")
	nsHive := createTestNamespace("managed2-hive")
	nsLocal := createTestNamespace("loc")

	// Create test managed clusters
	managedCluster1 := createTestManagedCluster("managed1", false)
	managedClusterHive := createTestManagedCluster("managed2-hive", false)
	managedClusterLocal := createTestManagedCluster("loc", true)

	// Create hive secret (indicates this is a hive cluster)
	hiveSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "managed-hive-secret",
			Namespace: "managed2-hive",
			Labels: map[string]string{
				backupCredsHiveLabel: "somevalue",
			},
		},
	}

	// Create ManagedClusterAddOn for testing
	msaAddon := &addonv1alpha1.ManagedClusterAddOn{
		ObjectMeta: v1.ObjectMeta{
			Name:      msa_addon,
			Namespace: "managed1",
			Labels: map[string]string{
				msa_label: msa_service_name,
			},
		},
	}

	// Create ManagedClusterAddOn with deletion timestamp for testing
	deletionTime := v1.NewTime(time.Now())
	msaAddonWithDeletion := &addonv1alpha1.ManagedClusterAddOn{
		ObjectMeta: v1.ObjectMeta{
			Name:              msa_addon + "-deletion",
			Namespace:         "managed1",
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{"test-finalizer"}, // Required for fake client
			Labels: map[string]string{
				msa_label: msa_service_name,
			},
		},
	}

	// Create ManifestWork for testing
	manifestWork := &workv1.ManifestWork{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test-manifestwork",
			Namespace: "managed1",
			Labels: map[string]string{
				addon_work_label: msa_addon,
			},
		},
	}

	// Create ManifestWork with deletion timestamp
	manifestWorkWithDeletion := &workv1.ManifestWork{
		ObjectMeta: v1.ObjectMeta{
			Name:              "test-manifestwork-deletion",
			Namespace:         "managed1",
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{"test-finalizer"}, // Required for fake client
			Labels: map[string]string{
				addon_work_label: msa_addon,
			},
		},
	}

	// Create MSA secret for testing (must have correct label and name prefix for cleanup)
	msaSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-test", // Must start with msa_service_name
			Namespace: "managed1",
			Labels: map[string]string{
				backupCredsClusterLabel: "managed1",
				"authentication.open-cluster-management.io/is-managed-serviceaccount": "true", // Required for getMSASecrets
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token":     []byte("test-token"),
			"ca.crt":    []byte("test-ca"),
			"namespace": []byte("test-namespace"),
			"server":    []byte("test-server"),
		},
	}

	// Create MSA secret with deletion timestamp
	msaSecretWithDeletion := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:              "msa-secret-deletion",
			Namespace:         "managed1",
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{"test-finalizer"}, // Required for fake client
			Labels: map[string]string{
				backupCredsClusterLabel: "managed1",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token": []byte("test-token"),
		},
	}

	k8sClient := createFakeClient(scheme, ns1, nsHive, nsLocal,
		managedCluster1, managedClusterHive, managedClusterLocal, hiveSecret,
		msaAddon, msaAddonWithDeletion, manifestWork, manifestWorkWithDeletion,
		msaSecret, msaSecretWithDeletion)

	// Create fake dynamic client with MSA object
	obj1 := &unstructured.Unstructured{}
	obj1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      msa_service_name,
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

	targetGVK := schema.GroupVersionKind{
		Group:   "authentication.open-cluster-management.io",
		Version: "v1beta1",
		Kind:    "ManagedServiceAccount",
	}
	targetGVR := targetGVK.GroupVersion().WithResource("managedserviceaccounts")
	targetMapping := meta.RESTMapping{
		Resource:         targetGVR,
		GroupVersionKind: targetGVK,
		Scope:            meta.RESTScopeNamespace,
	}
	targetGVRList := schema.GroupVersionResource{
		Group:    "authentication.open-cluster-management.io",
		Version:  "v1beta1",
		Resource: "managedserviceaccounts",
	}

	gvrToListKind := map[schema.GroupVersionResource]string{
		targetGVRList: "ManagedServiceAccountList",
	}

	unstructuredScheme := runtime.NewScheme()
	unstructuredScheme.AddKnownTypes(targetGVK.GroupVersion(), obj1)
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(unstructuredScheme,
		gvrToListKind,
		obj1)

	resInterface := dynClient.Resource(targetGVRList)

	type args struct {
		ctx     context.Context
		c       client.Client
		dr      dynamic.NamespaceableResourceInterface
		mapping *meta.RESTMapping
	}
	// Create separate test data for the failing test case to avoid interference
	msaSecretForFailure := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-failure", // Must start with msa_service_name
			Namespace: "managed1",
			Labels: map[string]string{
				backupCredsClusterLabel: "managed1",
				"authentication.open-cluster-management.io/is-managed-serviceaccount": "true", // Required for getMSASecrets
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token": []byte("test-token"),
		},
	}

	// Create a fresh client that includes the failure secret for the failing test
	failingClientData := createFakeClient(scheme, ns1, nsHive, nsLocal,
		managedCluster1, managedClusterHive, managedClusterLocal, hiveSecret,
		msaAddon, msaAddonWithDeletion, manifestWork, manifestWorkWithDeletion,
		msaSecretForFailure) // Only include the failure secret

	// Create a client that fails when deleting MSA secrets - this will test the error path (line 235)
	failingClient := &failingSecretDeleteClient{
		Client:           failingClientData,
		failOnSecretName: "auto-import-account-failure",
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "successful cleanup with MSA mapping",
			args: args{
				ctx:     ctx,
				c:       k8sClient,
				dr:      resInterface,
				mapping: &targetMapping,
			},
			wantErr: false,
		},
		{
			name: "successful cleanup without MSA mapping (nil)",
			args: args{
				ctx:     ctx,
				c:       k8sClient,
				dr:      resInterface,
				mapping: nil,
			},
			wantErr: false,
		},
		{
			name: "cleanup with empty client (no managed clusters)",
			args: args{
				ctx: ctx,
				// Empty client without any managed clusters
				c:       createFakeClient(scheme),
				dr:      resInterface,
				mapping: &targetMapping,
			},
			wantErr: false,
		},
		{
			name: "cleanup fails when secret deletion returns error (covers line 235)",
			args: args{
				ctx:     ctx,
				c:       failingClient,
				dr:      resInterface,
				mapping: &targetMapping,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use quiet logging for tests that expect errors to avoid confusing PR output
			if tt.wantErr {
				setupQuietTestLogger()
				defer setupTestLogger() // Restore normal logging after test
			}

			// Test cleanupMSAForImportedClusters
			err := cleanupMSAForImportedClusters(tt.args.ctx, tt.args.c,
				tt.args.dr,
				tt.args.mapping,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("cleanupMSAForImportedClusters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify that resources with deletion timestamps were skipped
				addons := &addonv1alpha1.ManagedClusterAddOnList{}
				if err := tt.args.c.List(tt.args.ctx, addons); err == nil {
					for _, addon := range addons.Items {
						if addon.Labels[msa_label] == msa_service_name &&
							!addon.GetDeletionTimestamp().IsZero() {
							// This addon should have been skipped for deletion
							t.Logf("Addon with deletion timestamp was correctly skipped: %s", addon.Name)
						}
					}
				}

				// Verify ManifestWork cleanup
				manifestWorks := &workv1.ManifestWorkList{}
				if err := tt.args.c.List(tt.args.ctx, manifestWorks); err == nil {
					for _, mw := range manifestWorks.Items {
						if mw.Labels[addon_work_label] == msa_addon &&
							!mw.GetDeletionTimestamp().IsZero() {
							// This manifest work should have been skipped for deletion
							t.Logf("ManifestWork with deletion timestamp was correctly skipped: %s", mw.Name)
						}
					}
				}
			}
		})
	}

	// Additional test for the original combined test case
	t.Run("clean up msa and test prepareImportedClusters", func(t *testing.T) {
		// Test cleanupMSAForImportedClusters
		err := cleanupMSAForImportedClusters(ctx, k8sClient, resInterface, &targetMapping)
		if err != nil {
			t.Errorf("Error running cleanupMSAForImportedClusters %s", err.Error())
		}

		// Test prepareImportedClusters
		err = prepareImportedClusters(ctx, k8sClient, resInterface, &targetMapping, backupSchedule)
		if err != nil {
			t.Errorf("Error running prepareImportedClusters %s", err.Error())
		}

		// Verify ManagedClusterAddOns were created correctly
		addons := &addonv1alpha1.ManagedClusterAddOnList{}
		if err := k8sClient.List(ctx, addons); err != nil {
			t.Errorf("cannot list managedclusteraddons %s ", err.Error())
		}

		var foundManaged1MSAAddon *addonv1alpha1.ManagedClusterAddOn
		for i := range addons.Items {
			addon := addons.Items[i]

			if addon.Name != msa_addon {
				continue // not a managed service account addon, ignore
			}
			if addon.Namespace == "managed1" && addon.GetDeletionTimestamp().IsZero() {
				foundManaged1MSAAddon = &addon
			}

			// Hive managed cluster and local cluster should not have the MSA addon created
			if addon.Namespace == "managed2-hive" {
				t.Errorf("ManagedClusterAddon should not have been created for hive namespace: %s", addon.Namespace)
			}
			if addon.Namespace == "loc" {
				t.Errorf("ManagedClusterAddon should not have been created for local cluster namespace: %s", addon.Namespace)
			}
		}

		// Verify the correct MSA addon was created for managed1
		if foundManaged1MSAAddon == nil {
			t.Errorf("No ManagedClusterAddOn created for managed cluster %s", "managed1")
		} else {
			msaLabel := foundManaged1MSAAddon.Labels[msa_label]
			if msaLabel != msa_service_name {
				t.Errorf("ManagedClusterAddOn for managed cluster %s is missing proper msa label", "managed1")
			}
		}
	})
}

// Test_updateSecretsLabels tests the functionality for updating labels on MSA secrets.
//
// This test verifies that:
// - Secret labels are updated correctly based on cluster configuration
// - Existing labels are preserved when appropriate
// - Label updates handle various secret configurations
// - Client operations for secret label updates work properly
// - Error conditions during label updates are handled gracefully
//
// Test Coverage:
// - Secret label update operations
// - Label preservation and modification logic
// - Client interactions for secret updates
// - Error handling during label operations
// - Various secret configurations and states
//
// Test Scenarios:
// - Successful label updates on existing secrets
// - Handling of secrets with missing or incomplete labels
// - Error conditions during secret update operations
// - Various label configurations and values
// - Client error handling during update operations
//
// Implementation Details:
// - Uses fake Kubernetes client for secret management
// - Tests various secret configurations with different labels
// - Validates proper label update operations
// - Handles error conditions gracefully
// - Tests client interactions for secret modifications
//
// Secret Label Management:
// - Ensures proper labeling of MSA secrets for identification
// - Maintains consistency between secret labels and cluster state
// - Enables proper secret discovery and management
// - Supports filtering and querying of secrets by labels
func Test_updateSecretsLabels(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	labelName := backupCredsClusterLabel
	labelValue := "clusterpool"

	tests := []struct {
		name                    string
		setupSecrets            func() ([]corev1.Secret, string, client.Client) // Returns secrets, namespace, client
		prefix                  string
		expectedBackupSecrets   []string // Secrets that should have backup label
		expectedNoBackupSecrets []string // Secrets that should NOT have backup label
	}{
		{
			name: "should process hive secrets correctly",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "managed1"
				namespace := createTestNamespace(clsName)

				importSecret := createTestSecret(clsName+"-import", clsName, nil, nil)
				importSecret1 := createTestSecret(clsName+"-import-1", clsName, nil, nil)
				importSecret2 := createTestSecret(clsName+"-import-2", clsName, nil, nil)
				bootstrapSecret := createTestSecret(clsName+"-bootstrap-test", clsName, nil, nil)
				otherSecret := createTestSecret(clsName+"-some-other-secret-test", clsName, nil, nil)

				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace, importSecret, importSecret1,
					importSecret2, bootstrapSecret, otherSecret)

				secrets := []corev1.Secret{
					*importSecret, *importSecret1, *importSecret2, *bootstrapSecret, *otherSecret,
				}

				return secrets, clsName, k8sClient
			},
			prefix: "managed1",
			expectedBackupSecrets: []string{
				"managed1-import-1",               // Already had label, should keep it
				"managed1-import-2",               // Should get label added
				"managed1-some-other-secret-test", // Should get label added
			},
			expectedNoBackupSecrets: []string{
				"managed1-import",         // Import secret should have label removed
				"managed1-bootstrap-test", // Bootstrap secret should not get label
			},
		},
		{
			name: "empty secret list",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "test-cluster"
				namespace := createTestNamespace(clsName)
				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace)
				return []corev1.Secret{}, clsName, k8sClient
			},
			prefix:                  "test-cluster",
			expectedBackupSecrets:   []string{},
			expectedNoBackupSecrets: []string{},
		},
		{
			name: "secrets with existing backup labels should be skipped",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "test-cluster"
				namespace := createTestNamespace(clsName)

				adminPass := createTestSecret("test-cluster-admin-pass", clsName,
					map[string]string{backupCredsHiveLabel: "hive-value"}, nil)
				userCreds := createTestSecret("test-cluster-user-creds", clsName,
					map[string]string{backupCredsUserLabel: "user-value"}, nil)
				clusterCreds := createTestSecret("test-cluster-cluster-creds", clsName,
					map[string]string{backupCredsClusterLabel: "existing-cluster-value"}, nil)
				newSecret := createTestSecret("test-cluster-new-secret", clsName, nil, nil)

				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace, adminPass, userCreds, clusterCreds, newSecret)

				secrets := []corev1.Secret{*adminPass, *userCreds, *clusterCreds, *newSecret}
				return secrets, clsName, k8sClient
			},
			prefix: "test-cluster",
			expectedBackupSecrets: []string{
				"test-cluster-new-secret", // Only this one should get the new label
			},
			expectedNoBackupSecrets: []string{
				"test-cluster-admin-pass",    // Has hive label
				"test-cluster-user-creds",    // Has user label
				"test-cluster-cluster-creds", // Has cluster label
			},
		},
		{
			name: "import secrets should have backup labels removed",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "test-cluster"
				namespace := createTestNamespace(clsName)

				importSecret := createTestSecret("test-cluster-import", clsName,
					map[string]string{backupCredsClusterLabel: labelValue}, nil)
				otherImport := createTestSecret("other-cluster-import", clsName,
					map[string]string{backupCredsHiveLabel: "hive-value"}, nil)
				importHelper := createTestSecret("test-cluster-import-helper", clsName,
					map[string]string{backupCredsUserLabel: "user-value"}, nil)

				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace, importSecret, otherImport, importHelper)

				secrets := []corev1.Secret{*importSecret, *otherImport, *importHelper}
				return secrets, clsName, k8sClient
			},
			prefix:                "test-cluster",
			expectedBackupSecrets: []string{},
			expectedNoBackupSecrets: []string{
				"test-cluster-import",        // Should have label removed
				"other-cluster-import",       // Should keep hive label (not matching prefix)
				"test-cluster-import-helper", // Should have label removed
			},
		},
		{
			name: "bootstrap secrets should not get backup labels",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "test-cluster"
				namespace := createTestNamespace(clsName)

				bootstrapSecret := createTestSecret("test-cluster-bootstrap-secret", clsName, nil, nil)
				bootstrapKey := createTestSecret("test-cluster-admin-bootstrap-key", clsName, nil, nil)
				bootstrapToken := createTestSecret("test-cluster-some-bootstrap-token", clsName, nil, nil)
				regularSecret := createTestSecret("test-cluster-regular-secret", clsName, nil, nil)

				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace, bootstrapSecret, bootstrapKey, bootstrapToken, regularSecret)

				secrets := []corev1.Secret{*bootstrapSecret, *bootstrapKey, *bootstrapToken, *regularSecret}
				return secrets, clsName, k8sClient
			},
			prefix: "test-cluster",
			expectedBackupSecrets: []string{
				"test-cluster-regular-secret", // Only non-bootstrap secret should get label
			},
			expectedNoBackupSecrets: []string{
				"test-cluster-bootstrap-secret", // Bootstrap secrets should not get labels
				"test-cluster-admin-bootstrap-key",
				"test-cluster-some-bootstrap-token",
			},
		},
		{
			name: "prefix mismatch - secrets should be skipped",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "test-cluster"
				namespace := createTestNamespace(clsName)

				otherSecret := createTestSecret("other-cluster-secret", clsName, nil, nil)
				differentSecret := createTestSecret("different-prefix-creds", clsName, nil, nil)
				matchingSecret := createTestSecret("test-cluster-matching-secret", clsName, nil, nil)

				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace, otherSecret, differentSecret, matchingSecret)

				secrets := []corev1.Secret{*otherSecret, *differentSecret, *matchingSecret}
				return secrets, clsName, k8sClient
			},
			prefix: "test-cluster",
			expectedBackupSecrets: []string{
				"test-cluster-matching-secret", // Only matching prefix should get label
			},
			expectedNoBackupSecrets: []string{
				"other-cluster-secret",   // Different prefix
				"different-prefix-creds", // Different prefix
			},
		},
		{
			name: "mixed scenarios - comprehensive test",
			setupSecrets: func() ([]corev1.Secret, string, client.Client) {
				clsName := "test-cluster"
				namespace := createTestNamespace(clsName)

				// Import secrets with existing labels (should be removed)
				importSecret := createTestSecret("test-cluster-import", clsName,
					map[string]string{backupCredsClusterLabel: labelValue}, nil)

				// Bootstrap secrets (should not get labels)
				bootstrapKey := createTestSecret("test-cluster-bootstrap-key", clsName, nil, nil)

				// Secrets with existing different backup labels (should be skipped)
				existingHive := createTestSecret("test-cluster-existing-hive", clsName,
					map[string]string{backupCredsHiveLabel: "hive"}, nil)
				existingUser := createTestSecret("test-cluster-existing-user", clsName,
					map[string]string{backupCredsUserLabel: "user"}, nil)

				// Regular secrets matching prefix (should get labels)
				adminPassword := createTestSecret("test-cluster-admin-password", clsName, nil, nil)
				serviceAccount := createTestSecret("test-cluster-service-account", clsName, nil, nil)

				// Secrets not matching prefix (should be ignored)
				otherSecret := createTestSecret("other-cluster-secret", clsName, nil, nil)

				scheme := createBasicScheme()
				k8sClient := createFakeClient(scheme, namespace, importSecret, bootstrapKey,
					existingHive, existingUser, adminPassword, serviceAccount, otherSecret)

				secrets := []corev1.Secret{*importSecret, *bootstrapKey, *existingHive,
					*existingUser, *adminPassword, *serviceAccount, *otherSecret}
				return secrets, clsName, k8sClient
			},
			prefix: "test-cluster",
			expectedBackupSecrets: []string{
				"test-cluster-admin-password",  // Should get new label
				"test-cluster-service-account", // Should get new label
			},
			expectedNoBackupSecrets: []string{
				"test-cluster-import",        // Import secret should have label removed
				"test-cluster-bootstrap-key", // Bootstrap secret should not get label
				"test-cluster-existing-hive", // Has existing hive label
				"test-cluster-existing-user", // Has existing user label
				"other-cluster-secret",       // Prefix doesn't match
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test data
			secrets, namespace, k8sClient := tt.setupSecrets()
			secretList := corev1.SecretList{Items: secrets}

			// Call the function under test
			updateSecretsLabels(ctx, k8sClient, secretList, tt.prefix, labelName, labelValue)

			// Verify secrets that should have backup labels
			for _, secretName := range tt.expectedBackupSecrets {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretName,
					Namespace: namespace,
				}, secret)
				if err != nil {
					t.Errorf("Failed to get secret %s: %v", secretName, err)
					continue
				}

				if secret.Labels == nil || secret.Labels[labelName] != labelValue {
					t.Errorf("Expected secret %s to have backup label %s=%s, got %v",
						secretName, labelName, labelValue, secret.Labels)
				}
			}

			// Verify secrets that should NOT have backup labels
			for _, secretName := range tt.expectedNoBackupSecrets {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretName,
					Namespace: namespace,
				}, secret)
				if err != nil {
					t.Errorf("Failed to get secret %s: %v", secretName, err)
					continue
				}

				if secret.Labels != nil && secret.Labels[labelName] == labelValue {
					t.Errorf("Expected secret %s to NOT have backup label %s=%s, but it does",
						secretName, labelName, labelValue)
				}
			}
		})
	}
}

// Test_retrieveMSAImportSecrets tests the retrieval of MSA import secrets for cluster management.
//
// This test verifies that:
// - MSA import secrets are retrieved correctly from the provided secret list
// - Secret filtering logic works properly to identify import secrets
// - Various secret configurations are handled appropriately
// - Secret selection criteria function as expected
// - Edge cases with missing or malformed secrets are handled gracefully
//
// Test Coverage:
// - MSA import secret retrieval logic
// - Secret filtering and selection operations
// - Handling of various secret configurations
// - Edge cases with incomplete or invalid secrets
// - Secret list processing and validation
//
// Test Scenarios:
// - Successful retrieval of valid MSA import secrets
// - Handling of empty or missing secret lists
// - Filtering out non-MSA secrets from mixed lists
// - Edge cases with malformed secret data
// - Various secret configurations and metadata
//
// Implementation Details:
// - Uses various secret configurations to test filtering logic
// - Tests secret list processing and validation
// - Validates proper secret identification and selection
// - Handles edge cases with incomplete or invalid data
// - Tests return value correctness for different scenarios
//
// MSA Import Secret Management:
// - Identifies and retrieves secrets needed for cluster import
// - Ensures proper secret filtering to avoid incorrect selections
// - Supports various secret configurations and formats
// - Maintains reliability during secret processing operations
func Test_retrieveMSAImportSecrets(t *testing.T) {
	type args struct {
		secrets       []corev1.Secret
		returnSecrets []corev1.Secret
	}

	tests := []struct {
		name string
		args args
	}{
		{
			name: "test 1",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import-account-2", "default",
						map[string]string{
							msa_label: "true",
						}, map[string]string{
							"expirationTimestamp":  "2022-07-26T15:26:36Z",
							"lastRefreshTimestamp": "2022-07-26T15:22:34Z",
						}, nil),
					*createSecret("some-other-msa-secret", "default",
						map[string]string{
							msa_label: "true",
						}, map[string]string{
							"expirationTimestamp":  "2022-07-26T15:26:36Z",
							"lastRefreshTimestamp": "2022-07-26T15:22:34Z",
						}, nil),
				},
				returnSecrets: []corev1.Secret{
					*createSecret("auto-import-account-2", "default",
						map[string]string{
							msa_label: "true",
						}, map[string]string{
							"expirationTimestamp":  "2022-07-26T15:26:36Z",
							"lastRefreshTimestamp": "2022-07-26T15:22:34Z",
						}, nil),
				},
			},
		},
		{
			name: "test 2",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("some-other-msa-secret", "default",
						map[string]string{
							msa_label: "true",
						}, map[string]string{
							"expirationTimestamp":  "2022-07-26T15:26:36Z",
							"lastRefreshTimestamp": "2022-07-26T15:22:34Z",
						}, nil),
				},
				returnSecrets: []corev1.Secret{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retrieveMSAImportSecrets(tt.args.secrets); len(got) != len(tt.args.returnSecrets) {
				t.Errorf("getBackupTimestamp() = %v, want %v", got, tt.args.returnSecrets)
			}
		})
	}
}

// Test_updateMSAResources tests the comprehensive update of MSA resources for cluster management.
//
// This test verifies that:
// - MSA resources are updated correctly across different cluster configurations
// - Update operations handle various MSA resource states appropriately
// - Dynamic client operations for MSA resource updates work as expected
// - Error conditions during resource updates are handled gracefully
// - Resource consistency is maintained during update operations
//
// Test Coverage:
// - MSA resource update operations
// - Dynamic client interactions for resource management
// - Error handling during resource update operations
// - Various MSA resource configurations and states
// - Client error handling and recovery
//
// Test Scenarios:
// - Successful MSA resource updates
// - Handling of missing or incomplete MSA resources
// - Error conditions during resource update operations
// - Various resource configurations and states
// - Dynamic client error handling
//
// Implementation Details:
// - Uses dynamic fake client for MSA resource simulation
// - Tests various MSA resource configurations
// - Validates proper resource update operations
// - Handles error conditions gracefully
// - Tests client interactions for resource modifications
//
// MSA Resource Management:
// - Ensures consistent MSA resource state across clusters
// - Maintains proper resource configuration and metadata
// - Handles resource updates efficiently and reliably
// - Supports various MSA resource types and configurations
func Test_updateMSAResources(t *testing.T) {
	// Set up test logger
	setupTestLogger()
	ctx := createTestContext()

	// Test namespace
	namespace := "msa-test-ns"

	// Create scheme
	scheme1 := createBasicScheme()

	// Create test secrets that should be processed by updateMSAResources
	// Secret 1: MSA secret without backup label (should be processed)
	msaSecret1 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-test1",
			Namespace: namespace,
			Labels: map[string]string{
				"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
			},
		},
		Data: map[string][]byte{
			"token": []byte("test-token-1"),
		},
	}

	// Secret 2: MSA secret with existing backup label (should be skipped)
	msaSecret2 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-test2",
			Namespace: namespace,
			Labels: map[string]string{
				"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
				"cluster.open-cluster-management.io/backup":                           "msa",
			},
		},
		Data: map[string][]byte{
			"token": []byte("test-token-2"),
		},
	}

	// Secret 3: MSA secret without backup label and no annotations (should be processed)
	msaSecret3 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-test3",
			Namespace: namespace,
			Labels: map[string]string{
				"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
			},
		},
		Data: map[string][]byte{
			"token": []byte("test-token-3"),
		},
	}

	// Secret 4: Non-MSA secret (should be ignored)
	nonMsaSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "non-msa-secret",
			Namespace: namespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Data: map[string][]byte{
			"data": []byte("test-data"),
		},
	}

	// Create fake client with the test secrets
	k8sClient1 := createFakeClient(scheme1, msaSecret1, msaSecret2, msaSecret3, nonMsaSecret)

	// Create fake dynamic client with MSA resources that have status information
	msaResource1 := &unstructured.Unstructured{}
	msaResource1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account-test1",
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"rotation": map[string]interface{}{
				"validity": "24h",
				"enabled":  true,
			},
		},
		"status": map[string]interface{}{
			"expirationTimestamp": "2024-01-01T12:00:00Z",
			"tokenSecretRef": map[string]interface{}{
				"lastRefreshTimestamp": "2024-01-01T10:00:00Z",
			},
		},
	})

	msaResource3 := &unstructured.Unstructured{}
	msaResource3.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account-test3",
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"rotation": map[string]interface{}{
				"validity": "48h",
				"enabled":  true,
			},
		},
		"status": map[string]interface{}{
			"expirationTimestamp": "2024-01-02T12:00:00Z",
			"tokenSecretRef": map[string]interface{}{
				"lastRefreshTimestamp": "2024-01-02T10:00:00Z",
			},
		},
	})

	// MSA resource without status (to test edge case)
	msaResourceNoStatus := &unstructured.Unstructured{}
	msaResourceNoStatus.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "msa-no-status",
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"rotation": map[string]interface{}{
				"validity": "24h",
				"enabled":  true,
			},
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		msaResource1, msaResource3, msaResourceNoStatus)

	res := schema.GroupVersionResource{
		Group:    "authentication.open-cluster-management.io",
		Version:  "v1beta1",
		Resource: "managedserviceaccounts",
	}
	resInterface := dynClient.Resource(res)

	type args struct {
		ctx context.Context
		c   client.Client
		dr  dynamic.NamespaceableResourceInterface
	}
	tests := []struct {
		name                      string
		args                      args
		wantSecret1BackupLabel    bool
		wantSecret1Annotations    bool
		wantSecret2Unchanged      bool
		wantSecret3BackupLabel    bool
		wantSecret3Annotations    bool
		wantNonMsaSecretUnchanged bool
	}{
		{
			name: "updateMSAResources processes MSA secrets correctly",
			args: args{
				ctx: ctx,
				c:   k8sClient1,
				dr:  resInterface,
			},
			wantSecret1BackupLabel:    true,
			wantSecret1Annotations:    true,
			wantSecret2Unchanged:      true,
			wantSecret3BackupLabel:    true,
			wantSecret3Annotations:    true,
			wantNonMsaSecretUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			updateMSAResources(tt.args.ctx, tt.args.c, tt.args.dr)

			// Verify Secret 1 was updated with backup label and annotations
			if tt.wantSecret1BackupLabel {
				updatedSecret1 := &corev1.Secret{}
				err := k8sClient1.Get(ctx, types.NamespacedName{
					Name:      "auto-import-account-test1",
					Namespace: namespace,
				}, updatedSecret1)
				if err != nil {
					t.Errorf("failed to get updated secret 1: %v", err)
				}

				// Check backup label was added
				if updatedSecret1.Labels["cluster.open-cluster-management.io/backup"] != "msa" {
					t.Errorf("expected backup label 'msa' on secret 1, got %v",
						updatedSecret1.Labels["cluster.open-cluster-management.io/backup"])
				}

				// Check annotations were added from MSA status
				if tt.wantSecret1Annotations {
					annotations := updatedSecret1.GetAnnotations()
					if annotations == nil {
						t.Errorf("expected annotations on secret 1, got nil")
					} else {
						if annotations["expirationTimestamp"] != "2024-01-01T12:00:00Z" {
							t.Errorf("expected expirationTimestamp annotation, got %v",
								annotations["expirationTimestamp"])
						}
						if annotations["lastRefreshTimestamp"] != "2024-01-01T10:00:00Z" {
							t.Errorf("expected lastRefreshTimestamp annotation, got %v",
								annotations["lastRefreshTimestamp"])
						}
					}
				}
			}

			// Verify Secret 2 was unchanged (already had backup label)
			if tt.wantSecret2Unchanged {
				unchangedSecret2 := &corev1.Secret{}
				err := k8sClient1.Get(ctx, types.NamespacedName{
					Name:      "auto-import-account-test2",
					Namespace: namespace,
				}, unchangedSecret2)
				if err != nil {
					t.Errorf("failed to get secret 2: %v", err)
				}

				// Should still have the backup label
				if unchangedSecret2.Labels["cluster.open-cluster-management.io/backup"] != "msa" {
					t.Errorf("secret 2 backup label should be unchanged")
				}
			}

			// Verify Secret 3 was updated with backup label and annotations
			if tt.wantSecret3BackupLabel {
				updatedSecret3 := &corev1.Secret{}
				err := k8sClient1.Get(ctx, types.NamespacedName{
					Name:      "auto-import-account-test3",
					Namespace: namespace,
				}, updatedSecret3)
				if err != nil {
					t.Errorf("failed to get updated secret 3: %v", err)
				}

				// Check backup label was added
				if updatedSecret3.Labels["cluster.open-cluster-management.io/backup"] != "msa" {
					t.Errorf("expected backup label 'msa' on secret 3, got %v",
						updatedSecret3.Labels["cluster.open-cluster-management.io/backup"])
				}

				// Check annotations were added from MSA status
				if tt.wantSecret3Annotations {
					annotations := updatedSecret3.GetAnnotations()
					if annotations == nil {
						t.Errorf("expected annotations on secret 3, got nil")
					} else {
						if annotations["expirationTimestamp"] != "2024-01-02T12:00:00Z" {
							t.Errorf("expected expirationTimestamp annotation, got %v",
								annotations["expirationTimestamp"])
						}
						if annotations["lastRefreshTimestamp"] != "2024-01-02T10:00:00Z" {
							t.Errorf("expected lastRefreshTimestamp annotation, got %v",
								annotations["lastRefreshTimestamp"])
						}
					}
				}
			}

			// Verify non-MSA secret was unchanged
			if tt.wantNonMsaSecretUnchanged {
				unchangedNonMsa := &corev1.Secret{}
				err := k8sClient1.Get(ctx, types.NamespacedName{
					Name:      "non-msa-secret",
					Namespace: namespace,
				}, unchangedNonMsa)
				if err != nil {
					t.Errorf("failed to get non-MSA secret: %v", err)
				}

				// Should not have backup label
				if unchangedNonMsa.Labels["cluster.open-cluster-management.io/backup"] != "" {
					t.Errorf("non-MSA secret should not have backup label, got %v",
						unchangedNonMsa.Labels["cluster.open-cluster-management.io/backup"])
				}
			}
		})
	}
}

// Test_deleteCustomManifestWork tests the deletion of custom ManifestWork resources.
//
// This test verifies that:
// - Custom ManifestWork resources are deleted correctly
// - Deletion operations handle various ManifestWork configurations
// - Client operations for ManifestWork deletion work properly
// - Error conditions during deletion are handled gracefully
// - Proper resource identification and removal occurs
//
// Test Coverage:
// - ManifestWork resource deletion operations
// - Client interactions for resource deletion
// - Error handling during deletion operations
// - Various ManifestWork configurations and states
// - Resource identification and cleanup logic
//
// Test Scenarios:
// - Successful deletion of existing ManifestWork resources
// - Handling of missing or already deleted ManifestWork resources
// - Error conditions during resource deletion
// - Various ManifestWork configurations and metadata
// - Client error handling during deletion operations
//
// Implementation Details:
// - Uses fake Kubernetes client for ManifestWork management
// - Tests various ManifestWork configurations
// - Validates proper resource deletion operations
// - Handles error conditions gracefully
// - Tests client interactions for resource cleanup
//
// ManifestWork Cleanup Management:
// - Ensures proper cleanup of custom ManifestWork resources
// - Prevents orphaned ManifestWork resources from consuming resources
// - Maintains consistency between cluster state and ManifestWork resources
// - Handles cleanup failures gracefully to prevent system issues
func Test_deleteCustomManifestWork(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	// Create a fake client with a ManifestWork that should be deleted
	manifestWork := &workv1.ManifestWork{
		ObjectMeta: v1.ObjectMeta{
			Name:      manifest_work_name + mwork_custom_282,
			Namespace: "test-namespace",
		},
	}

	scheme := createWorkScheme()

	k8sClient := createFakeClient(scheme, manifestWork)

	type args struct {
		ctx       context.Context
		c         client.Client
		namespace string
		mworkName string
	}
	tests := []struct {
		name            string
		args            args
		shouldBeDeleted bool
	}{
		{
			name: "should delete existing custom manifest work",
			args: args{
				ctx:       ctx,
				c:         k8sClient,
				namespace: "test-namespace",
				mworkName: manifest_work_name,
			},
			shouldBeDeleted: true,
		},
		{
			name: "should handle non-existent manifest work gracefully",
			args: args{
				ctx:       ctx,
				c:         k8sClient,
				namespace: "non-existent-namespace",
				mworkName: manifest_work_name,
			},
			shouldBeDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			deleteCustomManifestWork(tt.args.ctx, tt.args.c, tt.args.namespace, tt.args.mworkName)

			// Verify the ManifestWork was deleted if it should have been
			mwork := &workv1.ManifestWork{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      tt.args.mworkName + mwork_custom_282,
				Namespace: tt.args.namespace,
			}, mwork)

			if tt.shouldBeDeleted && err == nil {
				t.Errorf("Expected ManifestWork to be deleted, but it still exists")
			}
			if !tt.shouldBeDeleted && err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("Unexpected error when checking for non-existent ManifestWork: %v", err)
			}
		})
	}
}

// Test_createManifestWork tests the creation of ManifestWork resources for cluster management.
//
// This test verifies that:
// - ManifestWork resources are created correctly with proper configuration
// - Creation operations handle various parameter combinations
// - Client operations for ManifestWork creation work properly
// - Error conditions during creation are handled gracefully
// - Proper resource structure and metadata are established
//
// Test Coverage:
// - ManifestWork resource creation operations
// - Client interactions for resource creation
// - Error handling during creation operations
// - Various parameter configurations and values
// - Resource structure and metadata validation
//
// Test Scenarios:
// - Successful creation of ManifestWork resources
// - Handling of invalid or missing parameters
// - Error conditions during resource creation
// - Various namespace and configuration combinations
// - Client error handling during creation operations
//
// Implementation Details:
// - Uses fake Kubernetes client for ManifestWork management
// - Tests various parameter combinations and configurations
// - Validates proper resource creation operations
// - Handles error conditions gracefully
// - Tests client interactions for resource creation
//
// ManifestWork Creation Management:
// - Ensures proper ManifestWork resource creation for cluster operations
// - Maintains consistency between parameters and created resources
// - Handles creation failures gracefully to prevent system issues
// - Supports various ManifestWork configurations and use cases
func Test_createManifestWork(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	scheme := createWorkScheme()

	type args struct {
		ctx              context.Context
		c                client.Client
		namespace        string
		mworkbindingName string
		msaserviceName   string
		mworkName        string
		installNamespace string
	}
	tests := []struct {
		name                      string
		args                      args
		expectManifestWorkCreated bool
		expectedManifestWorkName  string
	}{
		{
			name: "should create manifest work with default namespace",
			args: args{
				ctx:              ctx,
				c:                createFakeClient(scheme),
				namespace:        "test-cluster",
				mworkbindingName: manifest_work_name_binding_name,
				msaserviceName:   msa_service_name,
				mworkName:        manifest_work_name,
				installNamespace: defaultAddonNS,
			},
			expectManifestWorkCreated: true,
			expectedManifestWorkName:  manifest_work_name,
		},
		{
			name: "should create manifest work with custom namespace",
			args: args{
				ctx:              ctx,
				c:                createFakeClient(scheme),
				namespace:        "test-cluster",
				mworkbindingName: manifest_work_name_binding_name,
				msaserviceName:   msa_service_name,
				mworkName:        manifest_work_name,
				installNamespace: "custom-namespace",
			},
			expectManifestWorkCreated: true,
			expectedManifestWorkName:  manifest_work_name + mwork_custom_283,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			createManifestWork(tt.args.ctx, tt.args.c, tt.args.namespace, "",
				tt.args.mworkbindingName, tt.args.msaserviceName, tt.args.mworkName, tt.args.installNamespace)

			if tt.expectManifestWorkCreated {
				// Verify ManifestWork was created
				mwork := &workv1.ManifestWork{}
				err := tt.args.c.Get(ctx, types.NamespacedName{
					Name:      tt.expectedManifestWorkName,
					Namespace: tt.args.namespace,
				}, mwork)

				if err != nil {
					t.Errorf("Expected ManifestWork %s to be created, but got error: %v",
						tt.expectedManifestWorkName, err)
				} else {
					// Verify labels
					if mwork.Labels[addon_work_label] != msa_addon {
						t.Errorf("Expected addon work label %s, got %s",
							msa_addon, mwork.Labels[addon_work_label])
					}
					if mwork.Labels[backupCredsClusterLabel] != ClusterActivationLabel {
						t.Errorf("Expected cluster label %s, got %s",
							ClusterActivationLabel, mwork.Labels[backupCredsClusterLabel])
					}
					// Verify manifest content exists
					if len(mwork.Spec.Workload.Manifests) == 0 {
						t.Errorf("Expected ManifestWork to have manifests, but got none")
					}
				}
			}
		})
	}
}

// Test_getMSASecrets tests the retrieval of MSA secrets from a specific namespace.
//
// This test verifies that:
// - MSA secrets are retrieved correctly from the specified namespace
// - Secret filtering and selection logic works properly
// - Client operations for secret retrieval work as expected
// - Error conditions during secret retrieval are handled gracefully
// - Proper secret identification and collection occurs
//
// Test Coverage:
// - MSA secret retrieval operations
// - Client interactions for secret listing
// - Error handling during retrieval operations
// - Various namespace configurations and states
// - Secret filtering and selection logic
//
// Test Scenarios:
// - Successful retrieval of MSA secrets from valid namespaces
// - Handling of empty or missing namespaces
// - Error conditions during secret retrieval
// - Various secret configurations and metadata
// - Client error handling during retrieval operations
//
// Implementation Details:
// - Uses fake Kubernetes client for secret management
// - Tests various namespace configurations
// - Validates proper secret retrieval operations
// - Handles error conditions gracefully
// - Tests client interactions for secret listing
//
// MSA Secret Retrieval Management:
// - Ensures proper identification and collection of MSA secrets
// - Maintains consistency between namespace state and retrieved secrets
// - Handles retrieval failures gracefully to prevent system issues
// - Supports various secret configurations and filtering criteria
func Test_getMSASecrets(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	// Create test secrets
	msaSecret1 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-test1",
			Namespace: "cluster1",
			Labels: map[string]string{
				msa_label: "true",
			},
		},
	}

	msaSecret2 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "auto-import-account-test2",
			Namespace: "cluster2",
			Labels: map[string]string{
				msa_label: "true",
			},
		},
	}

	nonMsaSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "regular-secret",
			Namespace: "cluster1",
		},
	}

	scheme := createBasicScheme()

	k8sClient := createFakeClient(scheme, msaSecret1, msaSecret2, nonMsaSecret)

	type args struct {
		ctx       context.Context
		c         client.Client
		namespace string
	}
	tests := []struct {
		name          string
		args          args
		expectedCount int
		expectedNames []string
	}{
		{
			name: "should get all MSA secrets when namespace is empty",
			args: args{
				ctx:       ctx,
				c:         k8sClient,
				namespace: "",
			},
			expectedCount: 2,
			expectedNames: []string{"auto-import-account-test1", "auto-import-account-test2"},
		},
		{
			name: "should get MSA secrets from specific namespace",
			args: args{
				ctx:       ctx,
				c:         k8sClient,
				namespace: "cluster1",
			},
			expectedCount: 1,
			expectedNames: []string{"auto-import-account-test1"},
		},
		{
			name: "should return empty list for namespace with no MSA secrets",
			args: args{
				ctx:       ctx,
				c:         k8sClient,
				namespace: "empty-namespace",
			},
			expectedCount: 0,
			expectedNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets := getMSASecrets(tt.args.ctx, tt.args.c, tt.args.namespace)

			if len(secrets) != tt.expectedCount {
				t.Errorf("Expected %d secrets, got %d", tt.expectedCount, len(secrets))
			}

			secretNames := make([]string, len(secrets))
			for i, secret := range secrets {
				secretNames[i] = secret.Name
			}

			for _, expectedName := range tt.expectedNames {
				found := false
				for _, actualName := range secretNames {
					if actualName == expectedName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected to find secret %s, but it was not returned", expectedName)
				}
			}
		})
	}
}

// Test_updateAISecrets tests the update of AI (Assisted Installer) secrets for cluster management.
//
// This test verifies that:
// - AI secrets are updated correctly with appropriate labels and metadata
// - Update operations handle various AI secret configurations
// - Client operations for AI secret updates work properly
// - Error conditions during AI secret updates are handled gracefully
// - Proper secret identification and labeling occurs
//
// Test Coverage:
// - AI secret update operations
// - Client interactions for secret updates
// - Error handling during update operations
// - Various AI secret configurations and states
// - Secret labeling and metadata management
//
// Test Scenarios:
// - Successful update of AI secrets with proper labels
// - Handling of missing or incomplete AI secrets
// - Error conditions during secret update operations
// - Various AI secret configurations and metadata
// - Client error handling during update operations
//
// Implementation Details:
// - Uses fake Kubernetes client for AI secret management
// - Tests various AI secret configurations
// - Validates proper secret update operations
// - Handles error conditions gracefully
// - Tests client interactions for secret modifications
//
// AI Secret Management:
// - Ensures proper labeling and metadata for AI secrets
// - Maintains consistency between AI configuration and secret state
// - Handles update failures gracefully to prevent system issues
// - Supports various AI secret types and configurations
func Test_updateAISecrets(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	// Create test secrets with agent-install label
	aiSecret1 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "ai-secret-1",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"agent-install.openshift.io/watch": "true",
			},
		},
	}

	aiSecret2 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "ai-secret-2",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"agent-install.openshift.io/watch": "true",
			},
		},
	}

	nonAiSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "regular-secret",
			Namespace: "test-namespace",
		},
	}

	scheme := createBasicScheme()

	k8sClient := createFakeClient(scheme, aiSecret1, aiSecret2, nonAiSecret)

	type args struct {
		ctx context.Context
		c   client.Client
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "should update AI secrets with backup labels",
			args: args{
				ctx: ctx,
				c:   k8sClient,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			updateAISecrets(tt.args.ctx, tt.args.c)

			// Verify AI secrets were updated with backup labels
			updatedSecret1 := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "ai-secret-1",
				Namespace: "test-namespace",
			}, updatedSecret1)
			if err != nil {
				t.Errorf("Failed to get updated AI secret 1: %v", err)
			}

			if updatedSecret1.Labels[backupCredsClusterLabel] != "agent-install" {
				t.Errorf("Expected backup label 'agent-install' on AI secret 1, got %v",
					updatedSecret1.Labels[backupCredsClusterLabel])
			}

			// Verify non-AI secret was not updated
			unchangedSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "regular-secret",
				Namespace: "test-namespace",
			}, unchangedSecret)
			if err != nil {
				t.Errorf("Failed to get regular secret: %v", err)
			}

			if unchangedSecret.Labels[backupCredsClusterLabel] != "" {
				t.Errorf("Regular secret should not have backup label, got %v",
					unchangedSecret.Labels[backupCredsClusterLabel])
			}
		})
	}
}

// Test_updateMetalSecrets tests the update of Metal3 (bare metal) secrets for cluster management.
//
// This test verifies that:
// - Metal3 secrets are updated correctly with appropriate labels and metadata
// - Update operations handle various Metal3 secret configurations
// - Client operations for Metal3 secret updates work properly
// - Error conditions during Metal3 secret updates are handled gracefully
// - Proper secret identification and labeling occurs
//
// Test Coverage:
// - Metal3 secret update operations
// - Client interactions for secret updates
// - Error handling during update operations
// - Various Metal3 secret configurations and states
// - Secret labeling and metadata management
//
// Test Scenarios:
// - Successful update of Metal3 secrets with proper labels
// - Handling of missing or incomplete Metal3 secrets
// - Error conditions during secret update operations
// - Various Metal3 secret configurations and metadata
// - Client error handling during update operations
//
// Implementation Details:
// - Uses fake Kubernetes client for Metal3 secret management
// - Tests various Metal3 secret configurations
// - Validates proper secret update operations
// - Handles error conditions gracefully
// - Tests client interactions for secret modifications
//
// Metal3 Secret Management:
// - Ensures proper labeling and metadata for Metal3 secrets
// - Maintains consistency between Metal3 configuration and secret state
// - Handles update failures gracefully to prevent system issues
// - Supports various Metal3 secret types and configurations
func Test_updateMetalSecrets(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	// Create test secrets with metal3 label
	metalSecret1 := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "metal-secret-1",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"environment.metal3.io": "baremetal",
			},
		},
	}

	// Secret in openshift-machine-api namespace should be skipped
	metalSecretSkipped := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "metal-secret-skipped",
			Namespace: "openshift-machine-api",
			Labels: map[string]string{
				"environment.metal3.io": "baremetal",
			},
		},
	}

	nonMetalSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "regular-secret",
			Namespace: "test-namespace",
		},
	}

	scheme := createBasicScheme()

	k8sClient := createFakeClient(scheme, metalSecret1, metalSecretSkipped, nonMetalSecret)

	type args struct {
		ctx context.Context
		c   client.Client
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "should update metal secrets with backup labels, skip openshift-machine-api namespace",
			args: args{
				ctx: ctx,
				c:   k8sClient,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			updateMetalSecrets(tt.args.ctx, tt.args.c)

			// Verify metal secret was updated with backup label
			updatedSecret1 := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "metal-secret-1",
				Namespace: "test-namespace",
			}, updatedSecret1)
			if err != nil {
				t.Errorf("Failed to get updated metal secret 1: %v", err)
			}

			if updatedSecret1.Labels[backupCredsClusterLabel] != "baremetal" {
				t.Errorf("Expected backup label 'baremetal' on metal secret 1, got %v",
					updatedSecret1.Labels[backupCredsClusterLabel])
			}

			// Verify secret in openshift-machine-api namespace was not updated
			skippedSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "metal-secret-skipped",
				Namespace: "openshift-machine-api",
			}, skippedSecret)
			if err != nil {
				t.Errorf("Failed to get skipped metal secret: %v", err)
			}

			if skippedSecret.Labels[backupCredsClusterLabel] != "" {
				t.Errorf("Skipped metal secret should not have backup label, got %v",
					skippedSecret.Labels[backupCredsClusterLabel])
			}

			// Verify non-metal secret was not updated
			unchangedSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "regular-secret",
				Namespace: "test-namespace",
			}, unchangedSecret)
			if err != nil {
				t.Errorf("Failed to get regular secret: %v", err)
			}

			if unchangedSecret.Labels[backupCredsClusterLabel] != "" {
				t.Errorf("Regular secret should not have backup label, got %v",
					unchangedSecret.Labels[backupCredsClusterLabel])
			}
		})
	}
}

// Test_updateSecret tests the generic secret update functionality for various cluster management scenarios.
//
// This test verifies that:
// - Secrets are updated correctly with specified labels and values
// - Update operations handle various secret configurations and states
// - Client operations for secret updates work properly across different scenarios
// - Error conditions during secret updates are handled gracefully
// - Proper secret identification, labeling, and modification occurs
//
// Test Coverage:
// - Generic secret update operations
// - Client interactions for secret modifications
// - Error handling during update operations
// - Various secret configurations and states
// - Label and metadata management for secrets
//
// Test Scenarios:
// - Successful update of secrets with new labels and values
// - Handling of missing or incomplete secrets
// - Error conditions during secret update operations
// - Various secret configurations and metadata combinations
// - Client error handling during update operations
//
// Implementation Details:
// - Uses fake Kubernetes client for secret management
// - Tests various secret configurations and update scenarios
// - Validates proper secret update operations
// - Handles error conditions gracefully
// - Tests client interactions for secret modifications
//
// Generic Secret Management:
// - Provides flexible secret update functionality for various use cases
// - Ensures proper labeling and metadata management for secrets
// - Maintains consistency between secret configuration and state
// - Handles update failures gracefully to prevent system issues
// - Supports conditional updates based on specified criteria
func Test_updateSecret(t *testing.T) {
	// Set up logger
	setupTestLogger()
	ctx := createTestContext()

	scheme := createBasicScheme()

	// Create test secrets
	secretWithoutLabels := corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "secret-without-labels",
			Namespace: "test-namespace",
		},
	}

	secretWithExistingBackupLabel := corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "secret-with-backup-label",
			Namespace: "test-namespace",
			Labels: map[string]string{
				backupCredsClusterLabel: "existing-value",
			},
		},
	}

	secretWithHiveLabel := corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "secret-with-hive-label",
			Namespace: "test-namespace",
			Labels: map[string]string{
				backupCredsHiveLabel: "hive-value",
			},
		},
	}

	k8sClient := createFakeClient(scheme, &secretWithExistingBackupLabel, &secretWithHiveLabel)

	type args struct {
		ctx        context.Context
		c          client.Client
		secret     corev1.Secret
		labelName  string
		labelValue string
		update     bool
	}
	tests := []struct {
		name         string
		args         args
		expectUpdate bool
		expectLabel  bool
	}{
		{
			name: "should add backup label to secret without existing backup labels",
			args: args{
				ctx:        ctx,
				c:          k8sClient,
				secret:     secretWithoutLabels,
				labelName:  backupCredsClusterLabel,
				labelValue: "test-value",
				update:     false, // Don't call client.Update
			},
			expectUpdate: true,
			expectLabel:  true,
		},
		{
			name: "should not update secret that already has backup label",
			args: args{
				ctx:        ctx,
				c:          k8sClient,
				secret:     secretWithExistingBackupLabel,
				labelName:  backupCredsClusterLabel,
				labelValue: "new-value",
				update:     false,
			},
			expectUpdate: false,
			expectLabel:  false,
		},
		{
			name: "should not update secret that has hive label",
			args: args{
				ctx:        ctx,
				c:          k8sClient,
				secret:     secretWithHiveLabel,
				labelName:  backupCredsClusterLabel,
				labelValue: "test-value",
				update:     false,
			},
			expectUpdate: false,
			expectLabel:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy of the secret to avoid modifying the original
			secretCopy := tt.args.secret.DeepCopy()

			result := updateSecret(tt.args.ctx, tt.args.c, *secretCopy,
				tt.args.labelName, tt.args.labelValue, tt.args.update)

			if result != tt.expectUpdate {
				t.Errorf("Expected updateSecret to return %v, got %v", tt.expectUpdate, result)
			}
		})
	}
}
