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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	dynamicfake "k8s.io/client-go/dynamic/fake"
)

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
				validity:      "50h",
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
				tt.args.validity); got != tt.want {
				t.Errorf("deleteDynamicResource() returns = %v, want %v", got, tt.want)
			}
		})
	}

}

func Test_updateMSASecretTimestamp(t *testing.T) {

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
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
		"status": map[string]interface{}{
			"somestatus": "aaa",
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
			"expirationTimestamp": "20d",
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj1)

	var res = schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version:  "v1alpha1",
		Resource: "ManagedServiceAccount"}

	resInterface := dynClient.Resource(res)

	type args struct {
		ctx    context.Context
		dr     dynamic.NamespaceableResourceInterface
		obj    unstructured.Unstructured
		secret corev1.Secret
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "MSA has no status",
			args: args{
				ctx: context.Background(),
				dr:  resInterface,
				obj: *obj1,
				secret: corev1.Secret{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Secret",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "auto-import-account",
						Namespace: "managed1",
					},
				}},
			want: false,
		},
		{
			name: "MSA has status but no expiration",
			args: args{
				ctx: context.Background(),
				dr:  resInterface,
				obj: *obj2,
				secret: corev1.Secret{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Secret",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "auto-import-account",
						Namespace: "managed1",
					},
				}},
			want: false,
		},
		{
			name: "MSA has status and expiration",
			args: args{
				ctx: context.Background(),
				dr:  resInterface,
				obj: *obj3,
				secret: corev1.Secret{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Secret",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "auto-import-account",
						Namespace: "managed1",
					},
				}},
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
