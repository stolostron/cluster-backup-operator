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

	certsv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ocinfrav1 "github.com/openshift/api/config/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
)

// ManagedClusterHandler is handler for attaching managed clusters
type ManagedClusterHandler struct {
	Client kubeclient.Interface

	adminKubeconfigSecrets  *[]corev1.Secret
	bootstrapSATokenSecrets *[]corev1.Secret
}

// NewManagedClusterHandler returns ManagedClusterHandler
func NewManagedClusterHandler(ctx context.Context,
	managedClusterName string,
	secrets *v1.SecretList) (*ManagedClusterHandler, error) {
	adminKubeconfigSecrets := []corev1.Secret{}
	bootstrapSATokenSecrets := []corev1.Secret{}
	if err := filterSecrets(ctx, secrets, &adminKubeconfigSecrets, &bootstrapSATokenSecrets); err != nil {
		return nil, fmt.Errorf("unable to get kubeconfig secrets for managed cluster %s: %v",
			managedClusterName, err)
	}

	var (
		managedClusterKubeClient kubeclient.Interface = nil
		errors                                        = []error{}
		err                      error
	)
	for i := range adminKubeconfigSecrets {
		if managedClusterKubeClient, err = getKubeClientFromSecret(&adminKubeconfigSecrets[i]); err == nil {
			break // for the moment we get the first one
		}
		errors = append(errors, fmt.Errorf("unable to get kubernetes client: %v", err))
	}
	if managedClusterKubeClient == nil {
		return nil, utilerrors.NewAggregate(errors)
	}
	// TODO retrieve operatorv1.Klusterlet

	return &ManagedClusterHandler{
		Client:                  managedClusterKubeClient,
		bootstrapSATokenSecrets: &bootstrapSATokenSecrets,
		adminKubeconfigSecrets:  &adminKubeconfigSecrets,
	}, nil
}

// GetNewBoostrapHubKubeconfigSecret is the function to get new boostrap-hub-kubeconfig
func (mc *ManagedClusterHandler) GetNewBoostrapHubKubeconfigSecret(
	ctx context.Context,
) (*corev1.Secret, error) {
	var (
		errors []error
	)
	for _, s := range *mc.bootstrapSATokenSecrets {
		newBoostrapHubKubeconfigSecret, err := newBootstrapHubKubeconfig(ctx, PublicAPIServerURL, s)
		if err == nil {
			return newBoostrapHubKubeconfigSecret, nil
		}
		errors = append(errors, fmt.Errorf("unable to create new boostrap-hub-kubeconfig: %v", err))
	}
	return nil, utilerrors.NewAggregate(errors)
}

func isVeleroRestoreFinished(restore *veleroapi.Restore) bool {
	switch {
	case restore == nil:
		return false
	case len(restore.Status.Phase) == 0 || // if no restore exists
		restore.Status.Phase == veleroapi.RestorePhaseNew ||
		restore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return false
	}
	return true
}

func isVeleroRestoreRunning(restore *veleroapi.Restore) bool {
	switch {
	case restore == nil:
		return false
	case
		restore.Status.Phase == veleroapi.RestorePhaseNew ||
			restore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return true
	}
	return false
}

// redefining const from apimachinery validation
const (
	dns1123LabelFmt          string = "[a-z0-9]([-a-z0-9]*[a-z0-9])?"
	dns1123SubdomainFmt      string = dns1123LabelFmt + "(\\." + dns1123LabelFmt + ")*"
	adminKubeconfigSecretFmt string = dns1123SubdomainFmt + "(-admin-kubeconfig)"
	boostrapSATokenFmnt      string = dns1123SubdomainFmt + "-bootstrap-sa-token" + "([-a-z0-9]*[a-z0-9])?"
)

var (
	adminKubeconfigSecretRegex = regexp.MustCompile("^" + adminKubeconfigSecretFmt + "$")
	boostrapSATokenRegex       = regexp.MustCompile("^" + boostrapSATokenFmnt + "$")
)

// filterSecrets filters a list of secrets getting
func filterSecrets(
	ctx context.Context,
	secrets *corev1.SecretList,
	adminKubeConfigSecrets *[]corev1.Secret,
	bootstrapSASecrets *[]corev1.Secret,
) error {
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
func newBootstrapHubKubeconfig(
	ctx context.Context,
	apiServer string,
	secret corev1.Secret,
) (*corev1.Secret, error) {
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
			Name:      BootstrapHubKubeconfigSecretName,
			Namespace: OpenClusterManagementAgentNamespaceName,
		},
		Data: map[string][]byte{
			"kubeconfig": data,
		},
	}, nil
}

// getKubeClientFromSecret initialize a kubernetes client from
// Secret containing a 'kubeconfig' data field
func getKubeClientFromSecret(kubeconfigSecret *corev1.Secret) (kubeclient.Interface, error) {
	kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s does not contain kubeconfig data",
			kubeconfigSecret.Namespace, kubeconfigSecret.Name)
	}
	apiConfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("unable to load kubeconfig from secret %s/%s: %v",
			kubeconfigSecret.Namespace, kubeconfigSecret.Name, err)
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
		return "", fmt.Errorf("secret %s/%s does not contain kubeconfig data",
			kubeconfigSecret.Namespace, kubeconfigSecret.Name)
	}
	apiConfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return "", fmt.Errorf("unable to load kubeconfig from secret %s/%s: %v",
			kubeconfigSecret.Namespace, kubeconfigSecret.Name, err)
	}
	if value, ok := apiConfig.Clusters["default-cluster"]; ok {
		return value.Server, nil
	}
	return "", fmt.Errorf("unable to fetch server from default-cluster data form secret %s/%s: %v",
		kubeconfigSecret.Namespace, kubeconfigSecret.Name, err)
}

const (
	infrastructureConfigName = "cluster"
	apiserverConfigName      = "cluster"
	openshiftConfigNamespace = "openshift-config"
)

// getPublicAPIServerURL retrieve the public URL for the current APIServer
// simlar to `oc get infrastructure cluster -o jsonpath='{.status.apiServerURL}'`` shell command
func getPublicAPIServerURL(client client.Client) (string, error) {
	if PublicAPIServerURL != "" {
		return PublicAPIServerURL, nil
	}
	infraConfig := &ocinfrav1.Infrastructure{}
	if err := client.Get(context.TODO(),
		types.NamespacedName{
			Name: infrastructureConfigName,
		}, infraConfig); err != nil {
		return "", err
	}
	if infraConfig.Status.APIServerURL != "" {
		PublicAPIServerURL = infraConfig.Status.APIServerURL
	}
	return PublicAPIServerURL, nil
}

// IsCertificateRequestApproved returns true if a certificate request has been "Approved" and not Denied
func isCertificateRequestApproved(csr *certsv1.CertificateSigningRequest) bool {
	approved, denied := getCertApprovalCondition(&csr.Status)
	return approved && !denied
}

func getCertApprovalCondition(
	status *certsv1.CertificateSigningRequestStatus,
) (approved bool, denied bool) {
	for _, c := range status.Conditions {
		if c.Type == certsv1.CertificateApproved {
			approved = true
		}
		if c.Type == certsv1.CertificateDenied {
			denied = true
		}
	}
	return
}

func initManagedClusterBootstrapClusterRole(managedClusterName string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:open-cluster-management:managedcluster:bootstrap:" + managedClusterName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"certificates.k8s.io"},
				Resources: []string{"certificatesigningrequests"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
			{
				APIGroups: []string{v1beta1.GroupVersion.Group},
				Resources: []string{"managedclusters"},
				Verbs:     []string{"create", "get"},
			},
		},
	}
}

func initManagedClusterBootstrapClusterRoleBinding(
	managedClusterName string,
) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:open-cluster-management:managedcluster:bootstrap:" + managedClusterName,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:open-cluster-management:managedcluster:bootstrap:" + managedClusterName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      managedClusterName + "-bootstrap-sa",
				Namespace: managedClusterName,
			},
		},
	}
}

func initManagedClusterClusterRole(managedClusterName string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:managedcluster:" + managedClusterName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"certificates.k8s.io"},
				Resources: []string{"certificatesigningrequests"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
			{
				APIGroups: []string{"register.open-cluster-management.io"},
				Resources: []string{"managedclusters/clientcertificates"},
				Verbs:     []string{"renew"},
			},
			{
				APIGroups:     []string{v1beta1.GroupVersion.Group},
				Resources:     []string{"managedclusters"},
				ResourceNames: []string{managedClusterName},
				Verbs:         []string{"get", "list", "update", "watch"},
			},
			{
				APIGroups:     []string{v1beta1.GroupVersion.Group},
				Resources:     []string{"managedclusters/status"},
				ResourceNames: []string{managedClusterName},
				Verbs:         []string{"patch", "update"},
			},
		},
	}
}

func initManagedClusterClusterRoleBinding(managedClusterName string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:managedcluster:" + managedClusterName,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:managedcluster:" + managedClusterName,
		},
		Subjects: []rbacv1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Group",
				Name:     "system:open-cluster-management:" + managedClusterName,
			},
		},
	}
}

func initManagedClusterAdminClusterRole(managedClusterName string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:admin:" + managedClusterName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{v1beta1.GroupVersion.Group},
				Resources:     []string{"managedclusters"},
				ResourceNames: []string{managedClusterName},
				Verbs: []string{
					"create",
					"delete",
					"get",
					"list",
					"patch",
					"update",
					"watch",
				},
			},
			{
				APIGroups: []string{"clusterview.open-cluster-management.io"},
				Resources: []string{"managedclusters"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

func initManagedClusterViewClusterRole(managedClusterName string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:view:" + managedClusterName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{v1beta1.GroupVersion.Group},
				Resources:     []string{"managedclusters"},
				ResourceNames: []string{managedClusterName},
				Verbs:         []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"clusterview.open-cluster-management.io"},
				Resources: []string{"managedclusters"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}
