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
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func Test_createMSA(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Errorf("Error starting testEnv: %s", err.Error())
	}
	scheme1 := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme1); err != nil {
		t.Errorf("Error adding core apis to scheme: %s", err.Error())
	}
	if err := workv1.AddToScheme(scheme1); err != nil {
		t.Errorf("Error adding workv1 apis to scheme: %s", err.Error())
	}
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Errorf("Error starting client: %s", err.Error())
	}

	namespace := "managed1"
	if err := k8sClient1.Create(context.Background(), createNamespace(namespace)); err != nil {
		t.Errorf("cannot create ns %s", err.Error())
	}
	if err := k8sClient1.Create(context.Background(),
		createSecret(msa_service_name, namespace, nil, nil, nil)); err != nil {
		t.Errorf("cannot create secret %s", err.Error())
	}

	if err := k8sClient1.Create(context.Background(),
		createMWork(manifest_work_name+mwork_custom_282, namespace)); err != nil {
		t.Errorf("cannot create mwork %s", err.Error())
	}

	if err := k8sClient1.Create(context.Background(),
		createMWork(manifest_work_name_pair+mwork_custom_282, namespace)); err != nil {
		t.Errorf("cannot create mwork %s", err.Error())
	}

	obj1 := &unstructured.Unstructured{}
	obj1.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      msa_service_name,
			"namespace": namespace,
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
		Version:  "v1beta1",
		Resource: "ManagedServiceAccount"}

	resInterface := dynClient.Resource(res)
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
	}{
		{
			name: "msa generated now",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
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
		},
		{
			name: "msa not generated now but validity updated",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
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
		},
		{
			name: "msa pair secrets not generated now",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
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
		},
		{
			name: "msa not generated now AND invalid token",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
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
		},
		{
			name: "MSA pair generated now",
			args: args{
				ctx:            context.Background(),
				dr:             resInterface,
				managedCluster: namespace,
				name:           msa_service_name_pair,
				validity:       "2m",
				secrets: []corev1.Secret{
					*createSecret("auto-import-account-2", namespace,
						map[string]string{
							msa_label: "true",
						}, map[string]string{
							"expirationTimestamp":  "2022-07-26T15:26:36Z",
							"lastRefreshTimestamp": "2022-07-26T15:22:34Z",
						}, nil),
				},
				currentTime: current,
			},
			pairMSAGeneratedNow: true,
			mainMSAGeneratedNow: true,
			secretsGeneratedNow: true,
			secretsUpdated:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			for i := range tt.args.secrets {
				if err := k8sClient1.Create(context.Background(), &tt.args.secrets[i]); err != nil {
					t.Errorf("secret creation failed: err(%s) ", err.Error())
				}
			}

			secretsGeneratedNow, secretsUpdated, _ := createMSA(tt.args.ctx, k8sClient1,
				tt.args.dr,
				tt.args.validity,
				tt.args.name,
				tt.args.managedCluster,
				current,
				namespace,
			)

			_, err := tt.args.dr.Namespace(tt.args.managedCluster).
				Get(context.Background(), msa_service_name, v1.GetOptions{})
			if err != nil && tt.mainMSAGeneratedNow {
				t.Errorf("MSA %s should exist: err(%s) ", msa_service_name, err.Error())
			}
			if err == nil && !tt.mainMSAGeneratedNow {
				t.Errorf("MSA %s should NOT exist", msa_service_name)
			}

			_, errPair := tt.args.dr.Namespace(tt.args.managedCluster).
				Get(context.Background(), msa_service_name_pair, v1.GetOptions{})
			if errPair != nil && tt.pairMSAGeneratedNow {
				t.Errorf("MSA %s should exist: err(%s) ", msa_service_name_pair, errPair.Error())
			}
			if errPair == nil && !tt.pairMSAGeneratedNow {
				t.Errorf("MSA %s should NOT exist", msa_service_name_pair)
			}

			if secretsGeneratedNow != tt.secretsGeneratedNow {
				t.Errorf("createMSA() returns secretsGeneratedNow = %v, want %v", secretsGeneratedNow, tt.secretsGeneratedNow)
			}
			if secretsUpdated != tt.secretsUpdated {
				t.Errorf("createMSA() returns secretsUpdated = %v, want %v", secretsUpdated, tt.secretsUpdated)
			}

			work := &workv1.ManifestWork{}
			if err := k8sClient1.Get(context.Background(), types.NamespacedName{Name: manifest_work_name,
				Namespace: tt.args.managedCluster}, work); err != nil {
				t.Errorf("cannot get manifestwork %s ", err.Error())
			} else {
				rawData := string(work.Spec.Workload.Manifests[0].Raw[:])

				str := `"kind":"ClusterRoleBinding","metadata":{"name":"managedserviceaccount-import"}`
				if !strings.Contains(rawData, str) {
					t.Errorf("Cluster role binding should be %v for manifest %v but is %v", "managedserviceaccount-import", work.Name, rawData)
				}

				strserviceaccount := `{"kind":"ServiceAccount","name":"auto-import-account","namespace":"open-cluster-management-agent-addon"}`
				if !strings.Contains(rawData, strserviceaccount) {
					t.Errorf("ServiceAccount should be %v for manifest %v, but is %v", strserviceaccount, work.Name, rawData)
				}

			}

			if err := k8sClient1.Get(context.Background(), types.NamespacedName{Name: manifest_work_name + "-custom-2",
				Namespace: tt.args.managedCluster}, work); err != nil {
				t.Errorf("cannot get manifestwork %s ", err.Error())
			} else {
				str := `"kind":"ClusterRoleBinding","metadata":{"name":"managedserviceaccount-import-custom-2"}`
				rawData := string(work.Spec.Workload.Manifests[0].Raw[:])

				if !strings.Contains(rawData, str) {
					t.Errorf("Cluster role binding should be %v for manifest %v but is %v", "managedserviceaccount-import-custom-2", work.Name, rawData)
				}

				strserviceaccount := `{"kind":"ServiceAccount","name":"auto-import-account","namespace":"managed1"}`
				if !strings.Contains(rawData, strserviceaccount) {
					t.Errorf("ServiceAccount should be %v for manifest %v, but is %v", strserviceaccount, work.Name, rawData)
				}

			}

			// this should be deleted
			if err := k8sClient1.Get(context.Background(), types.NamespacedName{Name: manifest_work_name + mwork_custom_282,
				Namespace: tt.args.managedCluster}, work); err == nil {
				t.Errorf("this manifest should no longer exist ! %v ", manifest_work_name+mwork_custom_282)
			}

			if err := k8sClient1.Get(context.Background(), types.NamespacedName{Name: manifest_work_name_pair + mwork_custom_282,
				Namespace: tt.args.managedCluster}, work); err == nil {
				t.Errorf("this manifest should no longer exist ! %v ", manifest_work_name_pair+mwork_custom_282)
			}

		})
	}

	if err := testEnv.Stop(); err != nil {
		t.Errorf("Error stopping testenv: %s", err.Error())
	}
}

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

	targetGVK := schema.GroupVersionKind{Group: "authentication.open-cluster-management.io",
		Version: "v1beta1",
		Kind:    "ManagedServiceAccount"}

	targetGVR := targetGVK.GroupVersion().WithResource("somecrs")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}

	var res = schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version:  "v1beta1",
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

	var res = schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version:  "v1beta1",
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
					*createSecret("auto-import-account", "managed1", nil,
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
					*createSecret("auto-import-account", "managed2", nil,
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
					*createSecret("auto-import-account", "managed2", nil,
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
					*createSecret("auto-import-account", "managed3", nil,
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
					*createSecret("auto-import-account", "managed3", nil,
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
					*createSecret("auto-import-account", "managed6", nil,
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
					*createSecret("auto-import-account", "managed3", nil,
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
					*createSecret("auto-import-account", "managed3", nil,
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
					*createSecret("auto-import-account", "managed3", nil,
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
	log.SetLogger(zap.New())

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	unstructuredScheme := runtime.NewScheme()

	cfg, err := testEnv.Start()
	if err != nil {
		t.Errorf("Error starting testEnv: %s", err.Error())
	}
	k8sClient1, err := client.New(cfg, client.Options{Scheme: unstructuredScheme})
	if err != nil {
		t.Errorf("Error starting client: %s", err.Error())
	}
	e1 := clusterv1.AddToScheme(unstructuredScheme)
	e2 := workv1.AddToScheme(unstructuredScheme)
	e3 := corev1.AddToScheme(unstructuredScheme)
	e4 := addonv1alpha1.AddToScheme(unstructuredScheme)
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		t.Errorf("Error adding apis to scheme: %s", err.Error())
	}

	backupNS := "velero-ns"
	backupSchedule := *createBackupSchedule("acm-schedule", backupNS).object

	if err := k8sClient1.Create(context.Background(), createNamespace("managed1")); err != nil {
		t.Errorf("cannot create ns %s ", err.Error())
	}
	if err := k8sClient1.Create(context.Background(), createManagedCluster("managed1", false).object); err != nil {
		t.Errorf("cannot create %s ", err.Error())
	}

	// Create a "hive" managedcluster
	if err := k8sClient1.Create(context.Background(), createNamespace("managed2-hive")); err != nil {
		t.Errorf("cannot create ns %s ", err.Error())
	}
	if err := k8sClient1.Create(context.Background(), createManagedCluster("managed2-hive", false).object); err != nil {
		t.Errorf("cannot create %s ", err.Error())
	}
	// For hive cluster, we need a secret in the namespace with the hive label on it
	hiveLabels := map[string]string{
		backupCredsHiveLabel: "somevalue",
	}
	if err := k8sClient1.Create(context.Background(), createSecret("managed-hive-secret", "managed2-hive",
		hiveLabels, nil, nil)); err != nil {
		t.Errorf("cannot create %s ", err.Error())
	}

	// Create a local managedcluster
	if err := k8sClient1.Create(context.Background(), createNamespace("loc")); err != nil {
		t.Errorf("cannot create ns %s ", err.Error())
	}
	if err := k8sClient1.Create(context.Background(), createManagedCluster("loc", true /* local cluster */).object); err != nil {
		t.Errorf("cannot create %s ", err.Error())
	}

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

	targetGVK := schema.GroupVersionKind{Group: "authentication.open-cluster-management.io",
		Version: "v1beta1", Kind: "ManagedServiceAccount"}
	targetGVR := targetGVK.GroupVersion().WithResource("managedserviceaccount")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}
	targetGVRList := schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedserviceaccounts"}

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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cleanupMSAForImportedClusters(tt.args.ctx, k8sClient1,
				tt.args.dr,
				tt.args.mapping,
			)
			if err != nil {
				t.Errorf("Error running cleanupMSAForImportedClusters %s", err.Error())
			}
			// cover the path where c.List for ManagedClusterAddOnList fails
			err = prepareImportedClusters(tt.args.ctx, k8sClient1,
				tt.args.dr,
				tt.args.mapping, &backupSchedule)
			if err != nil {
				t.Errorf("Error running prepareImportedClusters %s", err.Error())
			}
		})

		// List all mgd cluster addons - make sure the correct ones were created by prepareImportedClusters()
		addons := &addonv1alpha1.ManagedClusterAddOnList{}
		if err := tt.args.c.List(tt.args.ctx, addons); err != nil {
			t.Errorf("cannot list managedclusteraddons %s ", err.Error())
		}
		var foundManaged1MSAAddon *addonv1alpha1.ManagedClusterAddOn
		for i := range addons.Items {
			addon := addons.Items[i]

			if addon.Name != msa_addon {
				continue // not a managed service account addon, ignore
			}
			if addon.Namespace == "managed1" {
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
		if foundManaged1MSAAddon == nil {
			t.Errorf("No ManagedClusterAddOn created for managed cluster %s", "managed1")
		} else {
			msaLabel := foundManaged1MSAAddon.Labels[msa_label]
			if msaLabel != msa_service_name {
				t.Errorf("ManagedClusterAddOn for managed cluster %s is missing proper msa label", "managed1")
			}
		}
	}
	if err := testEnv.Stop(); err != nil {
		t.Errorf("Error stopping testenv: %s", err.Error())
	}
}

func Test_updateSecretsLabels(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	scheme1 := runtime.NewScheme()

	cfg, e1 := testEnv.Start()
	k8sClient1, e2 := client.New(cfg, client.Options{Scheme: scheme1})
	e3 := clusterv1.AddToScheme(scheme1)
	e4 := corev1.AddToScheme(scheme1)
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		t.Errorf("Error setting up testenv: %s", err.Error())
	}

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

	if err := testEnv.Stop(); err != nil {
		t.Errorf("Error stopping testenv: %s", err.Error())
	}
}

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
