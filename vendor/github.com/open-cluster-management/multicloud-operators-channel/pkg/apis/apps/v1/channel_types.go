// Copyright 2019 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.
var (
	// KeyChannelSource is the namespaced name of a Deployable from which
	// a child resource (deployable) is created by a channel.
	KeyChannelSource = SchemeGroupVersion.Group + "/hosting-deployable"

	// KeyChannel is namespacedname tells the source of the channel
	KeyChannel = SchemeGroupVersion.Group + "/channel"

	// KeyChannelType is the type of the source of the channel
	KeyChannelType = SchemeGroupVersion.Group + "/channel-type"

	// KeyChannelPath is the filter reference path of GitHub type channel
	KeyChannelPath = SchemeGroupVersion.Group + "/channel-path"

	// ServingChannel indicates the channel that the secrect or configMap
	// reference.
	ServingChannel = SchemeGroupVersion.Group + "/serving-channel"
)

// ChannelType defines types of channel
type ChannelType string

const (
	// Type defines type name of namespace channel
	ChannelTypeNamespace = "namespace"

	// Type defines type name of helm repository channel
	ChannelTypeHelmRepo = "helmrepo"

	// Type defines type name of bucket in object store
	ChannelTypeObjectBucket = "objectbucket"

	// Type defines type name of GitHub repository
	ChannelTypeGitHub = "github"

	// Type defines type name of Git repository
	ChannelTypeGit = "git"
)

// ChannelGate defines criteria for promoting a Deployable to Channel
type ChannelGate struct {
	Name string `json:"name,omitempty"`

	// A label selector for selecting the Deployables.
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// The annotations which must present on a Deployable for it to be
	// eligible for promotion.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ChannelSpec defines the desired state of Channel
type ChannelSpec struct {
	// A string representation of the channel type. Valid values include:
	// `namespace`, `helmrepo`, `objectbucket` and `github`.

	// +kubebuilder:validation:Enum={Namespace,HelmRepo,ObjectBucket,GitHub,Git,namespace,helmrepo,objectbucket,github,git}
	Type ChannelType `json:"type"`

	// For a `namespace` channel, pathname is the name of the namespace;
	// For a `helmrepo` or `github` channel, pathname is the remote URL
	// for the channel contents;
	// For a `objectbucket` channel, pathname is the URL and name of the bucket.
	Pathname string `json:"pathname"`

	// Skip server TLS certificate verification for Git or Helm channel.
	InsecureSkipVerify bool `json:"insecureSkipVerify"`

	// For a `github` channel or a `helmrepo` channel on github, this
	// can be used to reference a Secret which contains the credentials for
	// authentication, i.e. `user` and `accessToken`.
	// For a `objectbucket` channel, this can be used to reference a
	// Secret which contains the AWS credentials, i.e. `AccessKeyID` and
	// `SecretAccessKey`.
	// +optional
	SecretRef *corev1.ObjectReference `json:"secretRef,omitempty"`

	// Reference to a ConfigMap which contains additional settings for
	// accessing the channel. For example, the `insecureSkipVerify` option
	// for accessing HTTPS endpoints can be set in the ConfigMap to
	// indicate a insecure connection.
	ConfigMapRef *corev1.ObjectReference `json:"configMapRef,omitempty"`

	// Criteria for promoting a Deployable from the sourceNamespaces to Channel.
	// +optional
	Gates *ChannelGate `json:"gates,omitempty"`

	// A list of namespace names from which Deployables can be promoted.
	// +optional
	// +listType=set
	SourceNamespaces []string `json:"sourceNamespaces,omitempty"`
}

// ChannelStatus defines the observed state of Channel
type ChannelStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Channel is the Schema for the channels API
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type",description="type of the channel"
// +kubebuilder:printcolumn:name="Pathname",type="string",JSONPath=".spec.pathname",description="pathname of the channel"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
type Channel struct {
	// The most recent observed status of the Channel.
	Status ChannelStatus `json:"status,omitempty"`

	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ChannelSpec `json:"spec,omitempty"`

	// Specification for the Channel.
	metav1.TypeMeta `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ChannelList contains a list of Channel
type ChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// A list of Channel objects.
	// +listType=set
	Items []Channel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Channel{}, &ChannelList{})
}
