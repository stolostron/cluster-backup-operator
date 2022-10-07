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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func Test_createMSA(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})

	obj1 := &unstructured.Unstructured{}
	obj1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      msa_service_name,
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

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj1)

	var res = schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version:  "v1alpha1",
		Resource: "ManagedServiceAccount"}

	resInterface := dynClient.Resource(res)

	type args struct {
		ctx            context.Context
		dr             dynamic.NamespaceableResourceInterface
		validity       string
		managedCluster string
		name           string
	}
	tests := []struct {
		name                string
		args                args
		secretsGeneratedNow bool
		secretsUpdated      bool
	}{
		{
			name: "msa generated now",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
				managedCluster: "managed1",
				name:           msa_service_name,
				validity:       "20h",
			},
			secretsGeneratedNow: true,
			secretsUpdated:      false,
		},
		{
			name: "msa not generated now but validity updated",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
				managedCluster: "managed1",
				name:           msa_service_name,
				validity:       "50h",
			},
			secretsGeneratedNow: false,
			secretsUpdated:      true,
		},
		{
			name: "msa pair secrets not generated now",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
				managedCluster: "managed1",
				name:           msa_service_name_pair,
				validity:       "50h",
			},
			secretsGeneratedNow: false,
			secretsUpdated:      false,
		},
		{
			name: "msa not generated now AND invalid token",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
				managedCluster: "managed1",
				name:           msa_service_name,
				validity:       "\"invalid-token",
			},
			secretsGeneratedNow: false,
			secretsUpdated:      true,
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretsGeneratedNow, secretsUpdated, _ := createMSA(tt.args.ctx, k8sClient1,
				tt.args.dr,
				tt.args.validity,
				tt.args.name,
				tt.args.managedCluster,
			)
			if secretsGeneratedNow != tt.secretsGeneratedNow {
				t.Errorf("createMSA() returns secretsGeneratedNow = %v, want %v", secretsGeneratedNow, tt.secretsGeneratedNow)
			}
			if secretsUpdated != tt.secretsUpdated {
				t.Errorf("createMSA() returns secretsUpdated = %v, want %v", secretsUpdated, tt.secretsUpdated)
			}
		})

		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
	}

}

func Test_updateMSAToken(t *testing.T) {

	obj1 := &unstructured.Unstructured{}
	obj1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
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
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
		},
	})

	obj3 := &unstructured.Unstructured{}
	obj3.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
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

	targetGVK := schema.GroupVersionKind{Group: "authentication.open-cluster-management.io",
		Version: "v1alpha1",
		Kind:    "ManagedServiceAccount"}

	targetGVR := targetGVK.GroupVersion().WithResource("somecrs")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}

	var res = schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version:  "v1alpha1",
		Resource: "ManagedServiceAccount"}

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

func Test_updateMSASecretTimestamp(t *testing.T) {

	objNoStatus := &unstructured.Unstructured{}
	objNoStatus.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
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
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
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
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
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

	var res = schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version:  "v1alpha1",
		Resource: "ManagedServiceAccount"}

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
					*createSecret("auto-import", "managed1", nil,
						map[string]string{
							"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets with invalid expirationTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed2", nil,
						map[string]string{
							"lastRefreshTimestamp": "2022-08-05T15:25:38Z",
							"expirationTimestamp":  "bbb",
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets with invalid lastRefreshTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed2", nil,
						map[string]string{
							"lastRefreshTimestamp": "aaaaa",
							"expirationTimestamp":  "2022-08-05T15:25:38Z",
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets with invalid lastRefreshTimestamp",
			args: args{
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": "2022-08-05T15:25:38Z",
							"expirationTimestamp":  "aaa",
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets, current time not yet half between last refresh and expiration",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextTenHours,
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets, current time pased half more than 15min from expiration",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed6", nil,
						map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextThreeHours,
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets, current time too close to the expiration",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": fourHoursAgo,
							"expirationTimestamp":  nextHour,
						}, nil),
				}},
			want: false,
		},
		{
			name: "MSA has secrets, current time less then 15 min from half time",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": initialTime,
							"expirationTimestamp":  expiryTime,
						}, nil),
				}},
			want: true,
		},
		{
			name: "MSA has secrets, current time more then 15 min from half time so no pair should be created",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import", "managed3", nil,
						map[string]string{
							"lastRefreshTimestamp": initialTimeNoPair,
							"expirationTimestamp":  expiryTimeNoPair,
						}, nil),
				}},
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

func Test_cleanupMSAForImportedClusters(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	unstructuredScheme := runtime.NewScheme()

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: unstructuredScheme})

	obj1 := &unstructured.Unstructured{}
	obj1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1alpha1",
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

	targetGVK := schema.GroupVersionKind{Group: "authentication.open-cluster-management.io",
		Version: "v1alpha1", Kind: "ManagedServiceAccount"}
	targetGVR := targetGVK.GroupVersion().WithResource("managedserviceaccount")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}
	targetGVRList := schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version: "v1alpha1", Resource: "managedserviceaccounts"}

	gvrToListKind := map[schema.GroupVersionResource]string{
		targetGVRList: "ManagedServiceAccountList",
	}

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
	tests := []struct {
		name string
		args args
	}{
		{
			name: "clean up msa",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				dr:      resInterface,
				mapping: &targetMapping,
			},
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanupMSAForImportedClusters(tt.args.ctx, k8sClient1,
				tt.args.dr,
				tt.args.mapping,
			)
		})

		if index == len(tests)-1 {
			// clean up
			testEnv.Stop()
		}
	}

}
