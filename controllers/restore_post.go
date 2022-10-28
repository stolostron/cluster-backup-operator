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
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
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
			_, activationMessages := postRestoreActivation(ctx, c, getMSASecrets(ctx, c, ""),
				managedClusters.Items, time.Now().In(time.UTC))
			acmRestore.Status.Messages = activationMessages
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
) bool {
	processed := false

	if acmRestore.Spec.CleanupBeforeRestore == v1beta1.CleanupTypeNone {
		// request to not process cleanup, return now
		return processed
	}
	restoreCompleted := (acmRestore.Status.Phase == v1beta1.RestorePhaseFinished ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors)

	if cleanupOnRestore || restoreCompleted {
		// clean up delta resources, restored resources not created by the latest restore
		processed = true
		logger := log.FromContext(ctx)
		logger.Info("enter cleanupDeltaResources ")

		// clean up credentials
		backupName, veleroBackup := getBackupInfoFromRestore(ctx, c,
			acmRestore.Status.VeleroCredentialsRestoreName, acmRestore.Namespace)
		cleanupDeltaForCredentials(ctx, c,
			backupName, veleroBackup, acmRestore.Spec.CleanupBeforeRestore)

		// clean up resources and generic resources
		backupName, veleroBackup = getBackupInfoFromRestore(ctx, c,
			acmRestore.Status.VeleroResourcesRestoreName, acmRestore.Namespace)
		cleanupDeltaForResourcesBackup(ctx, c, restoreOptions,
			backupName, veleroBackup,
			*acmRestore.Spec.VeleroManagedClustersBackupName == skipRestoreStr)

		// clean up managed cluster resources
		backupName, veleroBackup = getBackupInfoFromRestore(ctx, c,
			acmRestore.Status.VeleroManagedClustersRestoreName, acmRestore.Namespace)
		cleanupDeltaForClustersBackup(ctx, c, restoreOptions,
			backupName, veleroBackup)

		logger.Info("exit cleanupDeltaResources ")
	}
	return processed
}

func cleanupDeltaForCredentials(
	ctx context.Context,
	c client.Client,
	backupName string,
	veleroBackup *veleroapi.Backup,
	cleanupType v1beta1.CleanupType,
) {

	if backupName == "" {
		// nothing to clean up
		return
	}

	// this is the user credentials backup, delete user credentials
	userCredsLabel, _ := labels.NewRequirement(backupCredsUserLabel,
		selection.Exists, []string{})
	deleteSecretsWithLabelSelector(ctx, c, backupName, cleanupType, []labels.Requirement{
		*userCredsLabel,
	})

	// check if this is a credentials backup using  the OrLabelSelectors
	// which means all credentials are in one backup
	if len(veleroBackup.Spec.OrLabelSelectors) > 0 {
		// cleanup ALL ACM credentials if they have the velero label
		// but don't match the current backup
		// it means those resources were removed and they should be cleaned up

		// hive credentials
		hiveCredsLabel, _ := labels.NewRequirement(backupCredsHiveLabel,
			selection.Exists, []string{})
		deleteSecretsWithLabelSelector(ctx, c, backupName, cleanupType,
			[]labels.Requirement{*hiveCredsLabel})

		// cluster credentials
		clsCredsLabel, _ := labels.NewRequirement(backupCredsClusterLabel,
			selection.Exists, []string{})
		deleteSecretsWithLabelSelector(ctx, c, backupName, cleanupType,
			[]labels.Requirement{*clsCredsLabel})

	} else {
		// clean up credentials based on backup type, secrets should be stored in 3 separate files

		// now get related backups
		// get hive backup and delete related secrets
		hiveCredsLabel, _ := labels.NewRequirement(backupCredsHiveLabel,
			selection.Exists, []string{})
		deleteSecretsForBackupType(ctx, c, CredentialsHive, *veleroBackup,
			cleanupType,
			[]labels.Requirement{
				*hiveCredsLabel,
			})
		///

		// get cluster secrets backup and delete related secrets
		clsCredsLabel, _ := labels.NewRequirement(backupCredsClusterLabel,
			selection.Exists, []string{})
		deleteSecretsForBackupType(ctx, c, CredentialsCluster, *veleroBackup,
			cleanupType,
			[]labels.Requirement{
				*clsCredsLabel,
			})
		///
	}

}

// for the specified backup type, find corresponding backup and send a request to
// delete delta secrets
func deleteSecretsForBackupType(
	ctx context.Context,
	c client.Client,
	backupType ResourceType,
	relatedVeleroBackup veleroapi.Backup,
	cleanupType v1beta1.CleanupType,
	secretsSelector []labels.Requirement,

) {
	backupLabel, _ := labels.NewRequirement(BackupScheduleTypeLabel,
		selection.Equals, []string{string(backupType)})
	backupSelector := labels.NewSelector()
	backupSelector = backupSelector.Add(*backupLabel)

	veleroBackups := &veleroapi.BackupList{}
	if err := c.List(ctx, veleroBackups, client.InNamespace(relatedVeleroBackup.Namespace),
		&client.ListOptions{LabelSelector: backupSelector}); err == nil {

		if backupName, _, _ := getVeleroBackupName(ctx, c, relatedVeleroBackup.Namespace,
			backupType,
			relatedVeleroBackup.Name,
			veleroBackups); backupName != "" {
			deleteSecretsWithLabelSelector(ctx, c, backupName, cleanupType, secretsSelector)
		}
	}
}

// delete all secrets matching the label selectors
func deleteSecretsWithLabelSelector(
	ctx context.Context,
	c client.Client,
	backupName string,
	cleanupType v1beta1.CleanupType,
	otherLabels []labels.Requirement,
) {
	logger := log.FromContext(ctx)

	labelSelector := labels.NewSelector()

	if cleanupType != v1beta1.CleanupTypeAll {
		// if cleanup is all, get all secrets, even the ones without a restore label
		veleroRestoreLabelExists, _ := labels.NewRequirement(BackupNameVeleroLabel,
			selection.Exists, []string{})
		labelSelector = labelSelector.Add(*veleroRestoreLabelExists)

	}
	veleroRestoreLabel, _ := labels.NewRequirement(BackupNameVeleroLabel,
		selection.NotEquals, []string{backupName})
	labelSelector = labelSelector.Add(*veleroRestoreLabel)

	labelSelector = labelSelector.Add(otherLabels...)

	secrets := &corev1.SecretList{}
	if err := c.List(ctx, secrets, &client.ListOptions{LabelSelector: labelSelector}); err == nil {
		for s := range secrets.Items {
			secret := secrets.Items[s]
			err := c.Delete(ctx, &secret, &client.DeleteOptions{})
			if err == nil {
				logger.Info("deleted secret " + secret.Name)
			}
		}
	}
	// delete config maps, they are also backed up here
	configmaps := &corev1.ConfigMapList{}
	if err := c.List(ctx, configmaps, &client.ListOptions{LabelSelector: labelSelector}); err == nil {
		for s := range configmaps.Items {
			cmap := configmaps.Items[s]
			err := c.Delete(ctx, &cmap, &client.DeleteOptions{})
			if err == nil {
				logger.Info("deleted configmap " + cmap.Name)
			}
		}
	}
}

func cleanupDeltaForResourcesBackup(
	ctx context.Context,
	c client.Client,
	restoreOptions RestoreOptions,
	backupName string,
	veleroBackup *veleroapi.Backup,
	managedClustersSkipped bool,
) {
	if backupName == "" {
		// nothing to clean up
		return
	}

	deleteDynamicResourcesForBackup(ctx, c, restoreOptions, veleroBackup, "")

	backupLabel, _ := labels.NewRequirement(BackupScheduleTypeLabel,
		selection.Equals, []string{string(ResourcesGeneric)})
	backupSelector := labels.NewSelector()
	backupSelector = backupSelector.Add(*backupLabel)

	// delete generic resources
	veleroBackups := &veleroapi.BackupList{}
	if err := c.List(ctx, veleroBackups, client.InNamespace(veleroBackup.Namespace),
		&client.ListOptions{LabelSelector: backupSelector}); err == nil {

		if genericBackupName, genericBackup, _ := getVeleroBackupName(ctx, c, veleroBackup.Namespace,
			ResourcesGeneric,
			veleroBackup.Name,
			veleroBackups); genericBackupName != "" {
			otherLabels := ""
			if managedClustersSkipped {
				// don't clean up activation resources
				otherLabels = fmt.Sprintf("%s notin (cluster-activation)", backupCredsClusterLabel)
			}
			deleteDynamicResourcesForBackup(ctx, c, restoreOptions, genericBackup, otherLabels)
		}
	}

}

func cleanupDeltaForClustersBackup(
	ctx context.Context,
	c client.Client,
	restoreOptions RestoreOptions,
	backupName string,
	veleroBackup *veleroapi.Backup,
) {
	if backupName == "" {
		// nothing to clean up
		return
	}

	deleteDynamicResourcesForBackup(ctx, c, restoreOptions, veleroBackup, "")
}

func deleteDynamicResourcesForBackup(
	ctx context.Context,
	c client.Client,
	restoreOptions RestoreOptions,
	veleroBackup *veleroapi.Backup,
	otherLabels string,
) {
	logger := log.FromContext(ctx)

	backupName := veleroBackup.Name
	resources := veleroBackup.Spec.IncludedResources

	// delete each resource from included resources, if it has a velero annotation
	// and velero annotation has a different backup name then the current backup
	// this means that the resource was created by another restore so it should be deleted now

	// resources with a backupCredsClusterLabel should be processed
	// by the generic backup
	genericLabel := fmt.Sprintf("!%s", backupCredsClusterLabel)
	if veleroBackup.GetLabels()[BackupScheduleTypeLabel] == string(ResourcesGeneric) {
		// we want the resources with the backupCredsClusterLabel here
		genericLabel = backupCredsClusterLabel

		// for generic resources get all CRDs and exclude the ones in the veleroBackup.Spec.ExcludedResources
		resources, _ = getGenericCRDFromAPIGroups(ctx, restoreOptions.dynamicArgs.dc, veleroBackup)
	}
	labelSelector := fmt.Sprintf("%s, %s notin (%s), %s",
		BackupNameVeleroLabel, BackupNameVeleroLabel, backupName, genericLabel)
	if restoreOptions.cleanupType == v1beta1.CleanupTypeAll {
		// get all resources, including user created
		labelSelector = genericLabel
	}
	if otherLabels != "" {
		labelSelector = fmt.Sprintf("%s, %s", labelSelector, otherLabels)
	}

	for i := range resources {
		kind, groupName := getResourceDetails(resources[i])

		if kind == "clusterimageset" || kind == "hiveconfig" {
			// ignore clusterimagesets and hiveconfig
			continue
		}

		if kind == "clusterdeployment" || kind == "machinepool" {
			// old backups have a short version for these resource
			groupName = "hive.openshift.io"
		}

		groupKind := schema.GroupKind{
			Group: groupName,
			Kind:  kind,
		}
		mapping, err := restoreOptions.mapper.RESTMapping(groupKind, "")
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get dynamic mapper for group=%s, error : %s",
				groupKind, err.Error()))
			continue
		}
		invokeDynamicDelete(ctx, restoreOptions, labelSelector,
			veleroBackup, mapping)
	}

}

func invokeDynamicDelete(
	ctx context.Context,
	restoreOptions RestoreOptions,
	labelSelector string,
	veleroBackup *veleroapi.Backup,
	mapping *meta.RESTMapping,
) {

	backupName := veleroBackup.Name
	if dr := restoreOptions.dynamicArgs.dyn.Resource(mapping.Resource); dr != nil {

		var listOptions = v1.ListOptions{}
		if labelSelector != "" {
			listOptions = v1.ListOptions{LabelSelector: labelSelector}
		}
		if dynamiclist, err := dr.List(ctx, listOptions); err == nil {
			// get all items and delete them
			for i := range dynamiclist.Items {

				item := dynamiclist.Items[i]
				if restoreOptions.cleanupType == v1beta1.CleanupTypeAll &&
					item.GetLabels()[BackupNameVeleroLabel] == backupName {
					// exclude here resources with the same backup as the last restore
					continue
				}

				deleteDynamicResource(
					ctx,
					mapping,
					dr,
					item,
					veleroBackup.Spec.ExcludedNamespaces,
					true, // skip resource if ExcludeBackupLabel is set
				)
			}
		}
	}

}

// get the backup used by this restore
func getBackupInfoFromRestore(
	ctx context.Context,
	c client.Client,
	restoreName string,
	namespace string,

) (string, *veleroapi.Backup) {

	backupName := ""
	veleroBackup := veleroapi.Backup{}
	if restoreName != "" {
		veleroRestore := veleroapi.Restore{}
		if err := c.Get(ctx, types.NamespacedName{Name: restoreName,
			Namespace: namespace}, &veleroRestore); err == nil {

			if err := c.Get(ctx, types.NamespacedName{Name: veleroRestore.Spec.BackupName,
				Namespace: namespace}, &veleroBackup); err == nil {
				backupName = veleroBackup.Name
			}
		}
	}
	return backupName, &veleroBackup
}

// activate managed clusters by creating auto-import-secret
func postRestoreActivation(
	ctx context.Context,
	c client.Client,
	msaSecrets []corev1.Secret,
	managedClusters []clusterv1.ManagedCluster,
	currentTime time.Time,
) ([]string, []string) {
	logger := log.FromContext(ctx)
	logger.Info("enter postRestoreActivation")
	// return the list of auto import secrets created here
	autoImportSecretsCreated := []string{}

	activationMessages := []string{}

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
			msg := fmt.Sprintf("Skip MSA access token for secret (%s:%s) no loger valid!",
				secret.Namespace, secret.Name)
			activationMessages = append(activationMessages, msg)
			logger.Info(msg)
			continue
		}

		// found a valid access token for this cluster name, add it to the list
		processedClusters = append(processedClusters, clusterName)

		reimport, url, message := managedClusterShouldReimport(ctx, managedClusters, clusterName)
		if message != "" {
			activationMessages = append(activationMessages, message)
		}
		if !reimport {
			// no need to reimport this managed cluster
			// the cluster is already active or the url is not set
			logger.Info(fmt.Sprintf("Will not reimport cluster (%s) the cluster is already active or the server url is not set",
				clusterName))
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

			msg := fmt.Sprintf(fmt.Sprintf(
				"failed to delete the auto-import-secret from namespace %s",
				clusterName,
			))
			// found secret
			if err := c.Delete(ctx, autoImportSecret); err == nil {
				msg = "deleted auto-import-secret from namespace " + clusterName
			}
			logger.Info(msg)
			activationMessages = append(activationMessages, msg)
		}

		// create an auto-import-secret for this managed cluster
		if err := createAutoImportSecret(ctx, c, clusterName, accessToken, url); err != nil {
			msg := fmt.Sprintf("Failed to create auto-import-secret for (%s)",
				clusterName)
			activationMessages = append(activationMessages, msg)
			logger.Error(err, msg)
		} else {
			autoImportSecretsCreated = append(autoImportSecretsCreated, clusterName)
			msg := fmt.Sprintf("Created auto-import-secret for (%s)",
				clusterName)
			activationMessages = append(activationMessages, msg)
			logger.Info(msg)
		}
	}
	logger.Info("exit postRestoreActivation")

	return autoImportSecretsCreated, activationMessages
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

	if ok := findValue([]string{
		v1beta1.CleanupTypeNone,
		v1beta1.CleanupTypeRestored,
		v1beta1.CleanupTypeAll},
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
	excludedNamespaces []string,
	skipExcludedBackupLabel bool,
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
		((resource.GetLabels()[ExcludeBackupLabel] == "true" && skipExcludedBackupLabel) ||
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

	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

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
