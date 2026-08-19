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

// Package v1beta1 contains API Schema definitions for the backup v1beta1 API group
// +kubebuilder:object:generate=true
// +groupName=cluster.open-cluster-management.io
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "cluster.open-cluster-management.io", Version: "v1beta1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme. It
	// intentionally does not use sigs.k8s.io/controller-runtime/pkg/scheme.Builder
	// (deprecated as of controller-runtime v0.24.0) so that this API package keeps
	// its only non-stdlib dependency on k8s.io/apimachinery, per upstream guidance.
	SchemeBuilder = &schemeBuilder{}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// schemeBuilder reproduces the handful of controller-runtime pkg/scheme.Builder
// methods this package's *_types.go init() functions rely on, backed only by the
// plain apimachinery runtime.SchemeBuilder.
type schemeBuilder struct {
	runtime.SchemeBuilder
}

// Register adds the given types to the scheme under GroupVersion, matching the
// call signature the deprecated controller-runtime scheme.Builder.Register provided.
func (b *schemeBuilder) Register(objects ...runtime.Object) *schemeBuilder {
	b.SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(GroupVersion, objects...)
		metav1.AddToGroupVersion(scheme, GroupVersion)
		return nil
	})
	return b
}

// AddToScheme adds all registered types to s.
func (b *schemeBuilder) AddToScheme(s *runtime.Scheme) error {
	return b.SchemeBuilder.AddToScheme(s)
}
