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
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ocinfrav1 "github.com/openshift/api/config/v1"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

// isRestoreFinished returns true when Restore is finished
func isRestoreFinished(restore *v1beta1.Restore) bool {
	switch {
	case restore == nil:
		return false
	case restore.Status.VeleroRestore == nil:
		return false
	case restore.Status.VeleroRestore.Status.Phase == veleroapi.RestorePhaseNew ||
		restore.Status.VeleroRestore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return false
	}
	return true
}

type FilterSecretBind struct {
	FilteredSecrets []corev1.Secret
	Filter          func(secret *corev1.Secret) bool
}

// redefining const from apimachinery validation
const dns1123LabelFmt string = "[a-z0-9]([-a-z0-9]*[a-z0-9])?"
const dns1123SubdomainFmt string = dns1123LabelFmt + "(\\." + dns1123LabelFmt + ")*"

const adminKubeconfigSecretFmt string = dns1123SubdomainFmt + "(-admin-kubeconfig)"

var adminKubeconfigSecretRegex = regexp.MustCompile("^" + adminKubeconfigSecretFmt + "$")

const boostrapSATokenFmnt string = dns1123SubdomainFmt + "-bootstrap-sa-token" + "([-a-z0-9]*[a-z0-9])?"

var boostrapSATokenRegex = regexp.MustCompile("^" + boostrapSATokenFmnt + "$")

// filterSecrets filters a list of secrets getting
func filterSecrets(ctx context.Context, secrets *corev1.SecretList, adminKubeConfigSecrets *[]corev1.Secret, bootstrapSASecrets *[]corev1.Secret) error {
	for _, secret := range secrets.Items {
		if adminKubeconfigSecretRegex.Match([]byte(secret.Name)) {
			*adminKubeConfigSecrets = append(*adminKubeConfigSecrets, secret)
			continue
		}
		if boostrapSATokenRegex.Match([]byte(secret.Name)) {
			*bootstrapSASecrets = append(*bootstrapSASecrets, secret)
		}
	}
	return nil
}

// newBootstrapHubKubeconfig initialize a bootstrap-hub-kubeconfig secret to point to apiServer
func newBootstrapHubKubeconfig(ctx context.Context, apiServer string, secret corev1.Secret) (*corev1.Secret, error) {
	caCrt, ok := secret.Data["ca.crt"]
	if !ok {
		return nil, fmt.Errorf("unable to find ca.crt data in secret")
	}
	token, ok := secret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("unable to find token in data in secret")
	}
	apiConfig := &clientcmdapi.Config{
		Clusters:       make(map[string]*clientcmdapi.Cluster),
		AuthInfos:      make(map[string]*clientcmdapi.AuthInfo),
		Contexts:       make(map[string]*clientcmdapi.Context),
		CurrentContext: "default-context",
	}
	apiConfig.Clusters["default-cluster"] = &clientcmdapi.Cluster{
		Server:                   apiServer,
		CertificateAuthorityData: caCrt,
	}
	apiConfig.AuthInfos["default-auth"] = &clientcmdapi.AuthInfo{
		Token: string(token),
	}
	apiConfig.Contexts["default-context"] = &clientcmdapi.Context{
		Cluster:   "default-cluster",
		Namespace: "default",
		AuthInfo:  "default-auth",
	}

	data, err := clientcmd.Write(*apiConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create kubeconfig: %v", err)
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bootstrap-hub-kubeconfig",
			Namespace: "open-cluster-management-agent",
		},
		Data: map[string][]byte{
			"kubeconfig": data,
		},
	}, nil
}

// getKubeClientFromSecret initialize a kubernetes client from Secret containing a 'kubeconfig' data field
func getKubeClientFromSecret(kubeconfigSecret *corev1.Secret) (kubeclient.Interface, error) {
	kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s does not contain kubeconfig data", kubeconfigSecret.Namespace, kubeconfigSecret.Name)
	}
	apiConfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("unable to load kubeconfig from secret %s/%s: %v", kubeconfigSecret.Namespace, kubeconfigSecret.Name, err)
	}
	kubeconfig := clientcmd.NewDefaultClientConfig(*apiConfig, &clientcmd.ConfigOverrides{})
	restConfig, err := kubeconfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubeclient.NewForConfig(restConfig)

}

func getDefaultClusterServerFromKubeconfigSecret(kubeconfigSecret *corev1.Secret) (string, error) {
	kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		return "", fmt.Errorf("secret %s/%s does not contain kubeconfig data", kubeconfigSecret.Namespace, kubeconfigSecret.Name)
	}
	apiConfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return "", fmt.Errorf("unable to load kubeconfig from secret %s/%s: %v", kubeconfigSecret.Namespace, kubeconfigSecret.Name, err)
	}
	if value, ok := apiConfig.Clusters["default-cluster"]; ok {
		return value.Server, nil
	}
	return "", fmt.Errorf("unable to fetch server from default-cluster data form secret %s/%s: %v", kubeconfigSecret.Namespace, kubeconfigSecret.Name, err)
}

const (
	infrastructureConfigName = "cluster"
	apiserverConfigName      = "cluster"
	openshiftConfigNamespace = "openshift-config"
)

// getPublicAPIServerURL retrieve the public URL for the current APIServer
// oc get infrastructure cluster -o jsonpath='{.status.apiServerURL}'
func getPublicAPIServerURL(client client.Client) (string, error) {
	infraConfig := &ocinfrav1.Infrastructure{}
	if err := client.Get(context.TODO(),
		types.NamespacedName{
			Name: infrastructureConfigName,
		}, infraConfig); err != nil {
		return "", err
	}
	return infraConfig.Status.APIServerURL, nil
}
