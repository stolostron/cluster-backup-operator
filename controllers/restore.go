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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ocinfrav1 "github.com/openshift/api/config/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
)

// isRestoreFinished returns true when Restore is finished
func isRestoreFinished(restore *v1beta1.Restore) bool {
	for _, c := range restore.Status.Conditions {
		if (c.Type == v1beta1.RestoreComplete || c.Type == v1beta1.RestoreFailed) && v1.ConditionStatus(c.Status) == v1.ConditionTrue {
			return true
		}
	}
	return false
}

func isVeleroRestoreFinished(restore *veleroapi.Restore) bool {
	switch {
	case restore == nil:
		return false
	case restore.Status.Phase == veleroapi.RestorePhaseNew ||
		restore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return false
	}
	return true
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
			Name:      BootstrapHubKubeconfigSecretName,
			Namespace: OpenClusterManagementAgentNamespaceName,
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

// acceptManagedCluster accepts the managed cluster
func acceptManagedCluster(ctx context.Context, k8sClient client.Client, managedClusterName string) error {
	managedCluster := clusterv1.ManagedCluster{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "", Name: managedClusterName}, &managedCluster); err != nil {
		return fmt.Errorf("unable to retrieve managed cluster during update %s: %v", managedClusterName, err)
	}
	managedCluster.Spec.HubAcceptsClient = true
	if err := k8sClient.Update(ctx, &managedCluster, &client.UpdateOptions{}); err != nil {
		return fmt.Errorf("unable to update managed cluster , %s: %v", managedClusterName, err)
	}
	// generate an event...
	return nil
}

// approveManagedClusterCSR approves the CSR for the managed cluster
func approveManagedClusterCSR(ctx context.Context, k8sClient client.Client, managedClusterName string) error {
	csrList := certsv1.CertificateSigningRequestList{}
	if err := k8sClient.List(ctx, &csrList, &client.ListOptions{}); err != nil {
		return fmt.Errorf("unable to list CSR: %v", err)
	}
	clusterNameCSRRegex, err := regexp.Compile("^" + managedClusterName + "-")
	if err != nil {
		return fmt.Errorf("unable to initialize REGEX to filter CSR for cluster %s: %v", managedClusterName, err)
	}
	for i := range csrList.Items {
		if clusterNameCSRRegex.Match([]byte(csrList.Items[i].Name)) {
			csrList.Items[i].Status.Conditions = append(csrList.Items[i].Status.Conditions, certsv1.CertificateSigningRequestCondition{
				Type:           certsv1.CertificateApproved,
				Status:         corev1.ConditionTrue,
				Reason:         v1beta1.CSRReasonApprovedReason,
				Message:        "cluster-backup-operator approved during restore",
				LastUpdateTime: metav1.Now(),
			})
			if err := k8sClient.Update(ctx, &csrList.Items[i], &client.UpdateOptions{}); err != nil {
				return fmt.Errorf("unable to update CSR %s for: %v", csrList.Items[i].Name, err)
			}
			return nil // we approve only the first one
		}
	}
	return fmt.Errorf("could not approve any csr")
}

func createClusterRoleIfNeeded(ctx context.Context, k8sClient client.Client, clusterRole *rbacv1.ClusterRole) error {
	t := &rbacv1.ClusterRole{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterRole.Name}, t); err != nil {
		if apierrors.IsNotFound(err) {
			if err := k8sClient.Create(ctx, clusterRole, &client.CreateOptions{}); err != nil {
				return fmt.Errorf("couldn't create the clusterrole: %v", err)
			}
		}
		return fmt.Errorf("couldn't verify if cluster ole exists: %v", err)
	}
	return nil
}

func createClusterRoleBindingIfNeeded(ctx context.Context, k8sClient client.Client, clusterRoleBinding *rbacv1.ClusterRoleBinding) error {
	t := &rbacv1.ClusterRoleBinding{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterRoleBinding.Name}, t); err != nil {
		if apierrors.IsNotFound(err) {
			if err := k8sClient.Create(ctx, clusterRoleBinding, &client.CreateOptions{}); err != nil {
				return fmt.Errorf("couldn't create the clusterrole binding: %v", err)
			}
		}
		return fmt.Errorf("couldn't verify if clusterrole binding exists: %v", err)
	}
	return nil
}

func initManagedClusterBoostrapClusterRole(managedClusterName string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("system:open-cluster-management:managedcluster:bootstrap:%s", managedClusterName),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"certificates.k8s.io"},
				Resources: []string{"certificatesigningrequests"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
			{
				APIGroups: []string{"cluster.open-cluster-management.io"},
				Resources: []string{"managedclusters"},
				Verbs:     []string{"create", "get"},
			},
		},
	}
}

func initManagedClusterBoostrapClusterRoleBinding(managedClusterName string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("osystem:open-cluster-management:managedcluster:bootstrap:%s", managedClusterName),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     fmt.Sprintf("system:open-cluster-management:managedcluster:bootstrap:%s", managedClusterName),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      fmt.Sprintf("system:open-cluster-management:%s", managedClusterName),
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
			Name: fmt.Sprintf("system:open-cluster-management:managedcluster:%s", managedClusterName),
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
				APIGroups:     []string{"cluster.open-cluster-management.io"},
				Resources:     []string{"managedclusters"},
				ResourceNames: []string{managedClusterName},
				Verbs:         []string{"get", "list", "update", "watch"},
			},
			{
				APIGroups:     []string{"cluster.open-cluster-management.io"},
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
			Name: fmt.Sprintf("open-cluster-management:managedcluster:%s", managedClusterName),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     fmt.Sprintf("open-cluster-management:managedcluster:%s", managedClusterName),
		},
		Subjects: []rbacv1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Group",
				Name:     fmt.Sprintf("system:open-cluster-management:%s", managedClusterName),
			},
		},
	}
}
