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

package controllers

import (
	"context"
	"fmt"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// execute any tasks after restore is done
func executePostRestoreTasks(
	ctx context.Context,
	c client.Client,
	acmRestore *v1beta1.Restore,
) bool {

	processed := false

	if (acmRestore.Status.Phase == v1beta1.RestorePhaseFinished ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors) &&
		*acmRestore.Spec.VeleroManagedClustersBackupName != skipRestoreStr {

		// get all managed clusters and run the auto import for imported clusters
		managedClusters := &clusterv1.ManagedClusterList{}
		if err := c.List(ctx, managedClusters, &client.ListOptions{}); err == nil {
			processed = true
			// this cluster was activated so try to auto import pending managed clusters
			postRestoreActivation(ctx, c, getMSASecrets(ctx, c, ""),
				managedClusters.Items, time.Now().In(time.UTC))
		}
	}
	return processed
}

// clean up resources with a restore label
// but not part of the latest restore
// these are delta resources that need to be cleaned up
func cleanupDeltaResources(
	ctx context.Context,
	c client.Client,
	acmRestore *v1beta1.Restore,
	cleanupOnRestore bool,
	restoreOptions RestoreOptions,
) {
	if cleanupOnRestore ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseFinished ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseEnabled {
		// clean up delta resources, restored resources not created by the latest restore
		logger := log.FromContext(ctx)
		logger.Info("enter cleanupDeltaResources ")
	}
}

// activate managed clusters by creating auto-import-secret
func postRestoreActivation(
	ctx context.Context,
	c client.Client,
	msaSecrets []corev1.Secret,
	managedClusters []clusterv1.ManagedCluster,
	currentTime time.Time,
) []string {
	logger := log.FromContext(ctx)
	logger.Info("enter postRestoreActivation")
	// return the list of auto import secrets created here
	autoImportSecretsCreated := []string{}

	processedClusters := []string{}
	for s := range msaSecrets {
		secret := msaSecrets[s]

		clusterName := secret.Namespace
		if findValue(processedClusters, clusterName) ||
			clusterName == "local-cluster" {
			// this cluster should not be processed
			continue
		}
		accessToken := ""
		if accessToken = findValidMSAToken([]corev1.Secret{secret}, currentTime); accessToken == "" {
			// this secret should not be processed
			continue
		}

		// found a valid access token for this cluster name, add it to the list
		processedClusters = append(processedClusters, clusterName)

		reimport, url := managedClusterShouldReimport(ctx, managedClusters, clusterName)
		if !reimport {
			// no need to reimport this managed cluster
			// the cluster is already active or the url is not set
			continue
		}

		// see if an auto-import-secret already exists
		// delete and re-create if is from a previous post-restore activation
		secretIdentity := types.NamespacedName{
			Name:      autoImportSecretName,
			Namespace: clusterName,
		}
		autoImportSecret := &corev1.Secret{}
		if err := c.Get(ctx, secretIdentity, autoImportSecret); err == nil &&
			autoImportSecret.GetLabels() != nil &&
			autoImportSecret.GetLabels()[activateLabel] == "true" {
			// found secret
			if err := c.Delete(ctx, autoImportSecret); err != nil {
				logger.Error(
					err,
					fmt.Sprintf(
						"failed to delete the auto-import-secret from namespace %s",
						clusterName,
					),
				)
			} else {
				logger.Info("deleted auto-import-secret from namespace " + clusterName)
			}
		}

		// create an auto-import-secret for this managed cluster
		if err := createAutoImportSecret(ctx, c, clusterName, accessToken, url); err != nil {
			logger.Error(err, "Error in creating AutoImportSecret")
		} else {
			autoImportSecretsCreated = append(autoImportSecretsCreated, clusterName)
			logger.Info("created auto-import-secret for managed cluster " + clusterName)
		}
	}

	return autoImportSecretsCreated
}

// create an autoImportSecret using the url and accessToken
func createAutoImportSecret(
	ctx context.Context,
	c client.Client,
	namespace string,
	accessToken string,
	url string,
) error {
	autoImportSecret := &corev1.Secret{}
	autoImportSecret.Name = autoImportSecretName
	autoImportSecret.Namespace = namespace
	autoImportSecret.Type = corev1.SecretTypeOpaque
	// set labels
	labels := make(map[string]string)
	labels[activateLabel] = "true"
	autoImportSecret.SetLabels(labels)
	// set data
	stringData := make(map[string]string)
	stringData["autoImportRetry"] = "5"
	stringData["server"] = url
	stringData["token"] = accessToken
	autoImportSecret.StringData = stringData

	return c.Create(ctx, autoImportSecret, &client.CreateOptions{})
}

func isValidCleanupOption(
	acmRestore *v1beta1.Restore,
) string {

	if ok := findValue([]string{v1beta1.CleanupTypeAll,
		v1beta1.CleanupTypeNone,
		v1beta1.CleanupTypeRestored},
		string(acmRestore.Spec.CleanupBeforeRestore)); !ok {

		msg := "invalid CleanupBeforeRestore value : " +
			string(acmRestore.Spec.CleanupBeforeRestore)
		return msg

	}

	return ""
}

// delete resource
// returns bool - resource was processed
// exception during execution
func deleteDynamicResource(
	ctx context.Context,
	mapping *meta.RESTMapping,
	dr dynamic.NamespaceableResourceInterface,
	resource unstructured.Unstructured,
	deleteOptions v1.DeleteOptions,
	excludedNamespaces []string,
) (bool, string) {
	logger := log.FromContext(ctx)
	localCluster := "local-cluster"

	nsSkipMsg := fmt.Sprintf(
		"Skipping resource %s [%s.%s]",
		resource.GetKind(),
		resource.GetName(),
		resource.GetNamespace())

	if resource.GetName() == localCluster ||
		(mapping.Scope.Name() == meta.RESTScopeNameNamespace &&
			(resource.GetNamespace() == localCluster ||
				findValue(excludedNamespaces, resource.GetNamespace()))) {
		// do not clean up local-cluster resources or resources from excluded NS
		logger.Info(nsSkipMsg)
		return false, ""
	}

	if resource.GetLabels() != nil &&
		(resource.GetLabels()["velero.io/exclude-from-backup"] == "true" ||
			resource.GetLabels()["installer.name"] == "multiclusterhub") {
		// do not cleanup resources with a velero.io/exclude-from-backup=true label, they are not backed up
		// do not backup subscriptions created by the mch in a separate NS
		logger.Info(nsSkipMsg)
		return false, ""
	}

	nsScopedMsg := fmt.Sprintf(
		"Deleting resource %s [%s.%s]",
		resource.GetKind(),
		resource.GetName(),
		resource.GetNamespace())

	nsScopedPatchMsg := fmt.Sprintf(
		"Removing finalizers for %s [%s.%s]",
		resource.GetKind(),
		resource.GetName(),
		resource.GetNamespace())

	globalResourceMsg := fmt.Sprintf(
		"Deleting resource %s [%s]",
		resource.GetKind(),
		resource.GetName())

	globalResourcePatchMsg := fmt.Sprintf(
		"Removing finalizers for %s [%s]",
		resource.GetKind(),
		resource.GetName())

	errMsg := ""
	patch := `[ { "op": "remove", "path": "/metadata/finalizers" } ]`
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		// namespaced resources should specify the namespace
		logger.Info(nsScopedMsg)
		if err := dr.Namespace(resource.GetNamespace()).Delete(ctx, resource.GetName(), deleteOptions); err != nil {
			errMsg = err.Error()
		} else {
			if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
				logger.Info(nsScopedPatchMsg)
				// delete finalizers and delete resource in this way
				if _, err := dr.Namespace(resource.GetNamespace()).Patch(ctx, resource.GetName(),
					types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
					errMsg = err.Error()
				}
			}
		}
	} else {
		// for cluster-wide resources
		logger.Info(globalResourceMsg)
		if err := dr.Delete(ctx, resource.GetName(), deleteOptions); err != nil {
			errMsg = err.Error()
		} else {
			if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
				// delete finalizers and delete resource in this way
				logger.Info(globalResourcePatchMsg)
				if _, err := dr.Patch(ctx, resource.GetName(),
					types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
					errMsg = err.Error()
				}
			}
		}
	}
	if errMsg != "" {
		logger.Info(errMsg)
	}
	return true, errMsg
}
