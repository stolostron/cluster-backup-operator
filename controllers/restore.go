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
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	activateLabel        = "cluster.open-cluster-management.io/msa-secret" // #nosec G101 -- This is a false positive
	autoImportSecretName = "auto-import-secret"                            // #nosec G101 -- This is a false positive
)

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

func isValidSyncOptions(restore *v1beta1.Restore) (bool, string) {

	if !restore.Spec.SyncRestoreWithNewBackups {
		return false, ""
	}

	if restore.Spec.VeleroManagedClustersBackupName == nil ||
		restore.Spec.VeleroCredentialsBackupName == nil ||
		restore.Spec.VeleroResourcesBackupName == nil {
		return false, "Some Velero backup names are not set."
	}

	backupName := ""

	backupName = *restore.Spec.VeleroManagedClustersBackupName
	backupName = strings.ToLower(strings.TrimSpace(backupName))

	if backupName != skipRestoreStr && backupName != latestBackupStr {
		return false, "VeleroManagedClustersBackupName should be set to skip or latest."
	}

	backupName = *restore.Spec.VeleroCredentialsBackupName
	backupName = strings.ToLower(strings.TrimSpace(backupName))

	if backupName != latestBackupStr {
		return false, "VeleroCredentialsBackupName should be set to latest."
	}

	backupName = *restore.Spec.VeleroResourcesBackupName
	backupName = strings.ToLower(strings.TrimSpace(backupName))

	if backupName != latestBackupStr {
		return false, "VeleroResourcesBackupName should be set to latest."
	}
	return true, ""
}

func isSkipAllRestores(restore *v1beta1.Restore) bool {

	backupName := ""

	if restore.Spec.VeleroManagedClustersBackupName != nil {
		backupName = *restore.Spec.VeleroManagedClustersBackupName
		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName != skipRestoreStr {
			return false
		}
	}

	if restore.Spec.VeleroCredentialsBackupName != nil {
		backupName = *restore.Spec.VeleroCredentialsBackupName
		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName != skipRestoreStr {
			return false
		}
	}

	if restore.Spec.VeleroResourcesBackupName != nil {
		backupName = *restore.Spec.VeleroResourcesBackupName
		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName != skipRestoreStr {
			return false
		}
	}

	return true
}

func updateRestoreStatus(
	logger logr.Logger,
	status v1beta1.RestorePhase,
	msg string,
	restore *v1beta1.Restore,
) {
	logger.Info(msg)

	restore.Status.Phase = status
	restore.Status.LastMessage = msg
}

// delete resource
func deleteDynamicResource(
	ctx context.Context,
	mapping *meta.RESTMapping,
	dr dynamic.NamespaceableResourceInterface,
	resource unstructured.Unstructured,
	deleteOptions v1.DeleteOptions,
	excludedNamespaces []string,
) (bool, bool) {
	logger := log.FromContext(ctx)
	localCluster := "local-cluster"
	processed := true

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
		return false, false
	}

	if resource.GetLabels() != nil &&
		(resource.GetLabels()["velero.io/exclude-from-backup"] == "true" ||
			resource.GetLabels()["installer.name"] == "multiclusterhub") {
		// do not cleanup resources with a velero.io/exclude-from-backup=true label, they are not backed up
		// do not backup subscriptions created by the mch in a separate NS
		logger.Info(nsSkipMsg)
		return false, false
	}

	nsScopedMsg := fmt.Sprintf(
		"Deleted resource %s [%s.%s]",
		resource.GetKind(),
		resource.GetName(),
		resource.GetNamespace())

	nsScopedPatchMsg := fmt.Sprintf(
		"Removed finalizers for %s [%s.%s]",
		resource.GetKind(),
		resource.GetName(),
		resource.GetNamespace())

	globalResourceMsg := fmt.Sprintf(
		"Deleted resource %s [%s]",
		resource.GetKind(),
		resource.GetName())

	globalResourcePatchMsg := fmt.Sprintf(
		"Removed finalizers for %s [%s]",
		resource.GetKind(),
		resource.GetName())

	patch := `[ { "op": "remove", "path": "/metadata/finalizers" } ]`
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		// namespaced resources should specify the namespace
		if err := dr.Namespace(resource.GetNamespace()).Delete(ctx, resource.GetName(), deleteOptions); err != nil {
			logger.Info(err.Error())
			return false, processed
		} else {
			logger.Info(nsScopedMsg)
			if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
				logger.Info(nsScopedPatchMsg)
				// delete finalizers and delete resource in this way
				if _, err := dr.Namespace(resource.GetNamespace()).Patch(ctx, resource.GetName(),
					types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
					logger.Info(err.Error())
					return false, processed
				}
			}
		}
	} else {
		// for cluster-wide resources
		if err := dr.Delete(ctx, resource.GetName(), deleteOptions); err != nil {
			logger.Info(err.Error())
			return false, processed
		} else {
			logger.Info(globalResourceMsg)
			if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
				// delete finalizers and delete resource in this way
				logger.Info(globalResourcePatchMsg)
				if _, err := dr.Patch(ctx, resource.GetName(),
					types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
					logger.Info(err.Error())
					return false, processed
				}
			}
		}
	}

	return true, processed
}

// clean up resources for the restored backup resources
func (r *RestoreReconciler) prepareRestoreForBackup(
	ctx context.Context,
	acmRestore *v1beta1.Restore,
	restoreOptions RestoreOptions,
	restoreType ResourceType,
	veleroBackup *veleroapi.Backup,
	additionalLabels string,
) {
	logger := log.FromContext(ctx)

	logger.Info("enter prepareForRestoreResources for " + string(restoreType))

	r.Recorder.Event(
		acmRestore,
		corev1.EventTypeNormal,
		"Prepare to restore:",
		"Cleaning up resources for backup "+veleroBackup.Name,
	)

	labelSelector := ""
	if restoreOptions.cleanupType == v1beta1.CleanupTypeRestored ||
		restoreType == ResourcesGeneric {
		// delete each resource from included resources, if it has a velero annotation
		// meaning that the resource was created by another restore
		labelSelector = "velero.io/backup-name,"
	}
	switch restoreType {
	case Resources:
		labelSelector = labelSelector + "!" + policyRootLabel
	case ResourcesGeneric:
		labelSelector = labelSelector + backupCredsClusterLabel
	case Credentials:
		labelSelector = labelSelector + backupCredsUserLabel
	case CredentialsHive:
		labelSelector = labelSelector + backupCredsHiveLabel
	case CredentialsCluster:
		labelSelector = labelSelector + backupCredsClusterLabel
	}
	labelSelector = strings.TrimSuffix(labelSelector, ",")

	if additionalLabels != "" {
		labelSelector = labelSelector + ", " + additionalLabels
	}

	var resources []string
	if restoreType != ResourcesGeneric {
		resources = veleroBackup.Spec.IncludedResources

		if restoreType == Resources {
			// include managed cluster resources, they need to be cleaned up even if the managed clusters are not restored now
			for i := range backupManagedClusterResources {
				resources = appendUnique(
					resources,
					backupManagedClusterResources[i],
				)
			}
		}
	} else {
		// for generic resources get all CRDs and exclude the ones in the veleroBackup.Spec.ExcludedResources
		resources, _ = getGenericCRDFromAPIGroups(ctx, restoreOptions.dynamicArgs.dc, veleroBackup)
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
		mapping, err := restoreOptions.dynamicArgs.mapper.RESTMapping(groupKind, "")
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get dynamic mapper for group=%s, error : %s",
				groupKind, err.Error()))
			continue
		}
		var dr = restoreOptions.dynamicArgs.dyn.Resource(mapping.Resource)
		if dr == nil {
			continue
		}

		var listOptions = v1.ListOptions{}
		if labelSelector != "" {
			listOptions = v1.ListOptions{LabelSelector: labelSelector}
		}

		dynamiclist, err := dr.List(ctx, listOptions)
		if err != nil {
			// ignore error
			continue
		}
		// get all items and delete them
		for i := range dynamiclist.Items {
			deleteDynamicResource(
				ctx,
				mapping,
				dr,
				dynamiclist.Items[i],
				restoreOptions.deleteOptions,
				veleroBackup.Spec.ExcludedNamespaces,
			)

		}

	}
	logger.Info("exit prepareForRestoreResources for " + string(restoreType))
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

		createAutoImport := true
		// see if an auto-import-secret already exists
		// delete and re-create if is from a previous post-restore activation
		secretIdentity := types.NamespacedName{
			Name:      autoImportSecretName,
			Namespace: clusterName,
		}
		autoImportSecret := &corev1.Secret{}
		if err := c.Get(ctx, secretIdentity, autoImportSecret); err == nil &&
			secret.GetLabels() != nil &&
			secret.GetLabels()[activateLabel] == "true" {
			// found secret
			if err := c.Delete(ctx, autoImportSecret); err != nil {
				logger.Error(
					err,
					fmt.Sprintf(
						"failed to delete the auto-import-secret from namespace %s",
						clusterName,
					),
				)
				createAutoImport = false
			} else {
				logger.Info("deleted auto-import-secret from namespace " + clusterName)
			}
		}

		if !createAutoImport {
			// should not create auto import secret for this managed cluster
			continue
		}

		autoImportSecretsCreated = append(autoImportSecretsCreated, clusterName)
		// create an auto-import-secret for this managed cluster
		if err := createAutoImportSecret(ctx, c, clusterName, accessToken, url); err != nil {
			logger.Error(err, "Error in creating AutoImportSecret")
		} else {
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

// check if there is any active resource on this cluster
func (r *RestoreReconciler) isOtherResourcesRunning(
	ctx context.Context,
	restore *v1beta1.Restore,
) (string, error) {
	// don't create restore if an active schedule exists
	backupScheduleName, err := r.isBackupScheduleRunning(ctx, restore)
	if err != nil {
		return "", err
	}
	if backupScheduleName != "" {
		msg := "This resource is ignored because BackupSchedule resource " + backupScheduleName + " is currently active, " +
			"before creating another resource verify that any active resources are removed."
		return msg, nil
	}

	// don't create restore if an active restore exists
	otherRestoreName, err := r.isOtherRestoresRunning(ctx, restore)
	if err != nil {
		return "", err
	}
	if otherRestoreName != "" {
		msg := "This resource is ignored because Restore resource " + otherRestoreName + " is currently active, " +
			"before creating another resource verify that any active resources are removed."
		return msg, nil
	}

	return "", nil
}

// check if there is a backup schedule running on this cluster
func (r *RestoreReconciler) isBackupScheduleRunning(
	ctx context.Context,
	restore *v1beta1.Restore,
) (string, error) {
	restoreLogger := log.FromContext(ctx)

	backupScheduleList := v1beta1.BackupScheduleList{}
	if err := r.List(
		ctx,
		&backupScheduleList,
		client.InNamespace(restore.Namespace),
	); err != nil {

		msg := "unable to list backup schedule resources " +
			"namespace:" + restore.Namespace

		restoreLogger.Error(
			err,
			msg,
		)
		return "", err
	}

	if len(backupScheduleList.Items) == 0 {
		return "", nil
	}

	for i := range backupScheduleList.Items {
		backupScheduleItem := backupScheduleList.Items[i]
		if backupScheduleItem.Status.Phase != v1beta1.SchedulePhaseBackupCollision {
			return backupScheduleItem.Name, nil
		}
	}

	return "", nil
}

// check if there are other restores that are not complete yet
func (r *RestoreReconciler) isOtherRestoresRunning(
	ctx context.Context,
	restore *v1beta1.Restore,
) (string, error) {
	restoreLogger := log.FromContext(ctx)

	restoreList := v1beta1.RestoreList{}
	if err := r.List(
		ctx,
		&restoreList,
		client.InNamespace(restore.Namespace),
	); err != nil {

		msg := "unable to list restore resources" +
			"namespace:" + restore.Namespace

		restoreLogger.Error(
			err,
			msg,
		)
		return "", err
	}

	if len(restoreList.Items) == 0 {
		return "", nil
	}

	for i := range restoreList.Items {
		restoreItem := restoreList.Items[i]
		if restoreItem.Name == restore.Name {
			continue
		}
		if restoreItem.Status.Phase != v1beta1.RestorePhaseFinished &&
			restoreItem.Status.Phase != v1beta1.RestorePhaseFinishedWithErrors {
			return restoreItem.Name, nil
		}
	}

	return "", nil
}

func (r *RestoreReconciler) isNewBackupAvailable(
	ctx context.Context,
	restore *v1beta1.Restore,
	resourceType ResourceType) bool {
	logger := log.FromContext(ctx)

	// get the latest Velero backup for this resourceType
	// this backup might be newer than the backup which
	// was used in the latest Velero restore for this resourceType
	newVeleroBackupName, newVeleroBackup, err := getVeleroBackupName(
		ctx,
		r.Client,
		restore.Namespace,
		resourceType,
		latestBackupStr,
	)
	if err != nil {
		logger.Error(
			err,
			"Failed to get new Velero backup for resource type "+string(resourceType),
		)
		return false
	}

	// find the latest velero restore for this resourceType
	latestVeleroRestoreName := ""
	switch resourceType {
	case Resources:
		latestVeleroRestoreName = restore.Status.VeleroResourcesRestoreName
	case Credentials:
		latestVeleroRestoreName = restore.Status.VeleroCredentialsRestoreName
	}
	if latestVeleroRestoreName == "" {
		logger.Info(
			fmt.Sprintf(
				"Failed to find the latest Velero restore name for resource type=%s, restore=%s",
				string(resourceType),
				restore.Name,
			),
		)
		return false
	}

	newVeleroRestoreName := getValidKsRestoreName(restore.Name, newVeleroBackupName)
	if latestVeleroRestoreName == newVeleroRestoreName {
		return false
	}

	latestVeleroRestore := veleroapi.Restore{}
	err = r.Get(
		ctx,
		types.NamespacedName{
			Name:      latestVeleroRestoreName,
			Namespace: restore.Namespace,
		},
		&latestVeleroRestore,
	)
	if err != nil {
		if errors.IsNotFound(err) {
			return true
		}
		logger.Error(
			err,
			"Failed to get Velero restore "+latestVeleroRestoreName,
		)
		return false
	}

	// compare the backup name and timestamp of newVeleroBackupName
	// with the backup used in the latestVeleroRestore
	if latestVeleroRestore.Spec.BackupName != newVeleroBackupName {
		latestVeleroBackup := veleroapi.Backup{}
		err := r.Get(
			ctx,
			types.NamespacedName{
				Name:      latestVeleroRestore.Spec.BackupName,
				Namespace: restore.Namespace,
			},
			&latestVeleroBackup,
		)
		if err != nil {
			if errors.IsNotFound(err) {
				return true
			}
			logger.Error(
				err,
				"Failed to get Velero backup "+latestVeleroRestore.Spec.BackupName,
			)
			return false
		}
		return latestVeleroBackup.Status.StartTimestamp.Before(
			newVeleroBackup.Status.StartTimestamp,
		)
	}

	return false
}

// validate if restore settings are valid and retry on error
func validateStorageSettings(
	ctx context.Context,
	c client.Client,
	name string,
	namespace string,
	restore *v1beta1.Restore,
) (string, bool) {

	retry := true
	msg := ""

	// don't create restores if backup storage location doesn't exist or is not avaialble
	veleroStorageLocations := &veleroapi.BackupStorageLocationList{}
	if err := c.List(ctx, veleroStorageLocations, &client.ListOptions{}); err != nil ||
		veleroStorageLocations == nil || len(veleroStorageLocations.Items) == 0 {

		msg = "velero.io.BackupStorageLocation resources not found. " +
			"Verify you have created a konveyor.openshift.io.Velero or oadp.openshift.io.DataProtectionApplications resource."

		return msg, retry
	}

	// look for available VeleroStorageLocation
	// and keep track of the velero oadp namespace
	isValidStorageLocation, veleroNamespace := isValidStorageLocationDefined(
		*veleroStorageLocations,
	)

	if !isValidStorageLocation {
		msg = "Backup storage location not available in namespace " + namespace +
			". Check velero.io.BackupStorageLocation and validate storage credentials."

		return msg, retry
	}

	// return error if the cluster restore file is not in the same namespace with velero
	if veleroNamespace != namespace {
		msg = fmt.Sprintf(
			"Restore resource [%s/%s] must be created in the velero namespace [%s]",
			namespace,
			name,
			veleroNamespace,
		)
		retry = false

		return msg, retry
	}

	return msg, retry
}

// getVeleroBackupName returns the name of velero backup will be restored
func getVeleroBackupName(
	ctx context.Context,
	c client.Client,
	restoreNamespace string,
	resourceType ResourceType,
	backupName string,
) (string, *veleroapi.Backup, error) {

	veleroBackups := &veleroapi.BackupList{}
	if err := c.List(ctx, veleroBackups, client.InNamespace(restoreNamespace)); err != nil {
		return "", nil, fmt.Errorf("unable to list velero backups: %v", err)
	}

	if len(veleroBackups.Items) == 0 {
		return "", nil, fmt.Errorf("no velero backups found")
	}

	if backupName == latestBackupStr {
		// backup name not available, find a proper backup
		// filter available backups to get only the ones related to this resource type
		relatedBackups := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
			return strings.HasPrefix(bkp.Name, veleroScheduleNames[resourceType]) &&
				(bkp.Status.Phase == veleroapi.BackupPhaseCompleted ||
					bkp.Status.Phase == veleroapi.BackupPhasePartiallyFailed)
		})
		if len(relatedBackups) == 0 {
			return "", nil, fmt.Errorf("no backups found")
		}
		sort.Sort(mostRecent(relatedBackups))
		return relatedBackups[0].Name, &relatedBackups[0], nil
	}

	// get the backup name for this type of resource, based on the requested resource timestamp
	if resourceType == CredentialsHive ||
		resourceType == CredentialsCluster ||
		resourceType == ResourcesGeneric {
		// first try to find a backup for this resourceType with the exact timestamp
		var computedName string
		backupTimestamp := strings.LastIndex(backupName, "-")
		if backupTimestamp != -1 {
			computedName = veleroScheduleNames[resourceType] + backupName[backupTimestamp:]
		}
		exactTimeBackup := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
			return computedName == bkp.Name
		})
		if len(exactTimeBackup) != 0 {
			return exactTimeBackup[0].Name, &exactTimeBackup[0], nil
		}
		// next try to find a backup with StartTimestamp in 30s range of the target timestamp
		targetTimestamp, err := getBackupTimestamp(backupName)
		if err != nil || targetTimestamp.IsZero() {
			return "", nil, fmt.Errorf(
				"cannot find %s Velero Backup for resourceType %s",
				backupName,
				string(resourceType),
			)
		}
		timeRangeBackups := filterBackups(veleroBackups.Items[:], func(bkp veleroapi.Backup) bool {
			if !strings.Contains(bkp.Name, veleroScheduleNames[resourceType]) ||
				bkp.Status.StartTimestamp == nil {
				return false
			}
			if targetTimestamp.Sub(bkp.Status.StartTimestamp.Time).Seconds() > 30 ||
				bkp.Status.StartTimestamp.Time.Sub(targetTimestamp).Seconds() > 30 {
				return false // not related, more then 30s appart
			}
			return true
		})
		if len(timeRangeBackups) != 0 {
			return timeRangeBackups[0].Name, &timeRangeBackups[0], nil
		}
		// if no backups within the range, return error
		return "", nil, fmt.Errorf(
			"cannot find %s Velero Backup for resourceType %s",
			backupName,
			string(resourceType),
		)
	}

	// for Credentials, Resources, ManagedClusters use the exact backupName set by the user
	veleroBackup := veleroapi.Backup{}
	err := c.Get(
		ctx,
		types.NamespacedName{Name: backupName, Namespace: restoreNamespace},
		&veleroBackup,
	)
	if err == nil {
		return backupName, &veleroBackup, nil
	}
	return "", nil, fmt.Errorf("cannot find %s Velero Backup: %v", backupName, err)
}
