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
	"maps"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	/* #nosec G101 -- This is a false positive */
	activateLabel = "cluster.open-cluster-management.io/restore-auto-import-secret"
	/* #nosec G101 -- This is a false positive */
	keepAutoImportSecret = "managedcluster-import-controller.open-cluster-management.io/keeping-auto-import-secret"
	/* #nosec G101 -- This is a false positive */
	autoImportSecretName = "auto-import-secret"
)

// resources should be restored in this order, higher priority starting from 0
var ResourceTypePriority map[ResourceType]int = map[ResourceType]int{
	Credentials:        0,
	CredentialsHive:    1,
	CredentialsCluster: 2,
	Resources:          3,
	ResourcesGeneric:   4,
	ManagedClusters:    5,
}

var (
	// used for restore purposes; it differs from the
	// veleroScheduleNames by having the hive and cluster credentials backups
	// which are generated by the acm2.6 - oadp 1.0
	veleroBackupNames = map[ResourceType]string{
		Credentials:        "acm-credentials-schedule",
		CredentialsHive:    "acm-credentials-hive-schedule",
		CredentialsCluster: "acm-credentials-cluster-schedule",
		Resources:          "acm-resources-schedule",
		ResourcesGeneric:   "acm-resources-generic-schedule",
		ManagedClusters:    "acm-managed-clusters-schedule",
		ValidationSchedule: "acm-validation-policy-schedule",
	}
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

// set cumulative status of restores
func setRestorePhase(
	veleroRestoreList *veleroapi.RestoreList,
	restore *v1beta1.Restore,
) (v1beta1.RestorePhase, bool) {

	// returns true if the status has changed and resource delta need to be cleaned up
	cleanupOnEnabled := false

	if restore.Status.Phase == v1beta1.RestorePhaseEnabled &&
		restore.Spec.SyncRestoreWithNewBackups {
		return restore.Status.Phase, cleanupOnEnabled
	}

	if veleroRestoreList == nil || len(veleroRestoreList.Items) == 0 {
		if isSkipAllRestores(restore) {
			restore.Status.Phase = v1beta1.RestorePhaseFinished
			restore.Status.LastMessage = fmt.Sprintf(noopMsg, restore.Name)
			return restore.Status.Phase, cleanupOnEnabled
		}
		restore.Status.Phase = v1beta1.RestorePhaseStarted
		restore.Status.LastMessage = fmt.Sprintf("Restore %s started", restore.Name)
		return restore.Status.Phase, cleanupOnEnabled
	}

	// get all velero restores and check status for each
	partiallyFailed := false
	for i := range veleroRestoreList.Items {
		veleroRestore := veleroRestoreList.Items[i].DeepCopy()

		if veleroRestore.Status.Phase == "" {
			restore.Status.Phase = v1beta1.RestorePhaseUnknown
			restore.Status.LastMessage = fmt.Sprintf(
				"Unknown status for Velero restore %s",
				veleroRestore.Name,
			)
			return restore.Status.Phase, cleanupOnEnabled
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhaseNew {
			restore.Status.Phase = v1beta1.RestorePhaseStarted
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s has started",
				veleroRestore.Name,
			)
			return restore.Status.Phase, cleanupOnEnabled
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhaseInProgress {
			restore.Status.Phase = v1beta1.RestorePhaseRunning
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s is currently executing",
				veleroRestore.Name,
			)
			return restore.Status.Phase, cleanupOnEnabled
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhaseFailed ||
			veleroRestore.Status.Phase == veleroapi.RestorePhaseFailedValidation {
			restore.Status.Phase = v1beta1.RestorePhaseError
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s has failed validation or encountered errors",
				veleroRestore.Name,
			)
			return restore.Status.Phase, cleanupOnEnabled
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhasePartiallyFailed {
			partiallyFailed = true
			continue
		}
	}

	isValidSync, _ := isValidSyncOptions(restore)
	// sync is enabled only when the backup name for managed clusters is set to skip
	if isValidSync &&
		*restore.Spec.VeleroManagedClustersBackupName == skipRestoreStr {
		restore.Status.Phase = v1beta1.RestorePhaseEnabled
		restore.Status.LastMessage = "Velero restores have run to completion, " +
			"restore will continue to sync with new backups"

		// delta cleanup needed now
		cleanupOnEnabled = true
	} else if partiallyFailed {
		restore.Status.Phase = v1beta1.RestorePhaseFinishedWithErrors
		restore.Status.LastMessage = "Velero restores have run to completion but encountered 1+ errors"
	} else {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = "All Velero restores have run successfully"
	}

	return restore.Status.Phase, cleanupOnEnabled
}

// check if there is any active resource on this cluster
func isOtherResourcesRunning(
	ctx context.Context,
	c client.Client,
	restore *v1beta1.Restore,
) (string, error) {
	// don't create restore if an active schedule exists
	backupScheduleList := v1beta1.BackupScheduleList{}
	if err := c.List(
		ctx,
		&backupScheduleList,
		client.InNamespace(restore.Namespace),
	); err != nil {
		return "", err
	}
	backupScheduleName := isBackupScheduleRunning(backupScheduleList.Items)
	if backupScheduleName != "" {
		msg := "This resource is ignored because BackupSchedule resource " + backupScheduleName + " is currently active, " +
			"before creating another resource verify that any active resources are removed."
		return msg, nil
	}

	// don't create restore if an active restore exists
	restoreList := v1beta1.RestoreList{}
	if err := c.List(
		ctx,
		&restoreList,
		client.InNamespace(restore.Namespace),
	); err == nil {

		otherRestoreName := isOtherRestoresRunning(restoreList.Items, restore.Name)
		if otherRestoreName != "" {
			msg := "This resource is ignored because Restore resource " + otherRestoreName + " is currently active, " +
				"before creating another resource verify that any active resources are removed."
			return msg, nil
		}
	}

	return "", nil
}

// check if there is a backup schedule running on this cluster
func isBackupScheduleRunning(
	schedules []v1beta1.BackupSchedule,
) string {

	if len(schedules) == 0 {
		return ""
	}

	for i := range schedules {
		backupScheduleItem := schedules[i]
		if backupScheduleItem.Status.Phase != v1beta1.SchedulePhaseBackupCollision {
			return backupScheduleItem.Name
		}
	}

	return ""
}

// check if there are other restores that are not complete yet
func isOtherRestoresRunning(
	restores []v1beta1.Restore,
	restoreName string,
) string {

	if len(restores) == 0 {
		return ""
	}

	for i := range restores {
		restoreItem := restores[i]
		if restoreItem.Name == restoreName {
			continue
		}
		if restoreItem.Status.Phase != v1beta1.RestorePhaseFinished &&
			restoreItem.Status.Phase != v1beta1.RestorePhaseFinishedWithErrors {
			return restoreItem.Name
		}
	}

	return ""
}

func isNewBackupAvailable(
	ctx context.Context,
	c client.Client,
	restore *v1beta1.Restore,
	resourceType ResourceType) bool {
	logger := log.FromContext(ctx)

	// get the latest Velero backup for this resourceType
	// this backup might be newer than the backup which
	// was used in the latest Velero restore for this resourceType
	veleroBackups := &veleroapi.BackupList{}
	if err := c.List(ctx, veleroBackups, client.InNamespace(restore.Namespace)); err == nil {

		newVeleroBackupName, newVeleroBackup, err := getVeleroBackupName(
			ctx,
			c,
			restore.Namespace,
			resourceType,
			latestBackupStr,
			veleroBackups,
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
		if err = c.Get(
			ctx,
			types.NamespacedName{
				Name:      latestVeleroRestoreName,
				Namespace: restore.Namespace,
			},
			&latestVeleroRestore,
		); err != nil {
			return true // restore not found
		}

		// compare the backup name and timestamp of newVeleroBackupName
		// with the backup used in the latestVeleroRestore
		if latestVeleroRestore.Spec.BackupName != newVeleroBackupName {
			latestVeleroBackup := veleroapi.Backup{}
			if err := c.Get(
				ctx,
				types.NamespacedName{
					Name:      latestVeleroRestore.Spec.BackupName,
					Namespace: restore.Namespace,
				},
				&latestVeleroBackup,
			); err != nil {
				return true // backup not found
			}
			return latestVeleroBackup.Status.StartTimestamp.Before(
				newVeleroBackup.Status.StartTimestamp,
			)
		}
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
	isValidStorageLocation := isValidStorageLocationDefined(
		veleroStorageLocations.Items,
		namespace,
	)

	if !isValidStorageLocation {
		msg = "Backup storage location not available in namespace " + namespace +
			". Check velero.io.BackupStorageLocation and validate storage credentials."

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
	veleroBackups *veleroapi.BackupList,
) (string, *veleroapi.Backup, error) {

	if len(veleroBackups.Items) == 0 {
		return "", nil, fmt.Errorf("no velero backups found")
	}

	if backupName == latestBackupStr {
		// backup name not available, find a proper backup
		// filter available backups to get only the ones related to this resource type

		searchForBackupType := resourceType
		if resourceType == CredentialsHive ||
			resourceType == CredentialsCluster {
			// for creds hive and cluster backups get the latest Credentials backup
			// since this version of the controller no longer generates those types
			// we want to get the related hive and cluster backups based on the latest
			// controller generated cluster credentials backup - this is for backward compatibility,
			// when restoring backups which generated 3 backups for credentials
			searchForBackupType = Credentials
		}
		relatedBackups := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
			return strings.HasPrefix(bkp.Name, veleroBackupNames[searchForBackupType]) &&
				(bkp.Status.Phase == veleroapi.BackupPhaseCompleted ||
					bkp.Status.Phase == veleroapi.BackupPhasePartiallyFailed)
		})
		if len(relatedBackups) == 0 {
			return "", nil, fmt.Errorf("no backups found")
		}
		sort.Sort(mostRecent(relatedBackups))
		// return found backup if the same type as the requested type
		//or using the orSelector (credential backup)
		if resourceType == searchForBackupType ||
			len(relatedBackups[0].Spec.OrLabelSelectors) != 0 {
			return relatedBackups[0].Name, &relatedBackups[0], nil
		}
		// otherwise, this is a hive or cluster credentials backup,
		// find the most recent based on the credential backup name
		backupName = relatedBackups[0].Name
	}

	// get the backup name for this type of resource, based on the requested resource timestamp
	if resourceType == CredentialsHive ||
		resourceType == CredentialsCluster ||
		resourceType == ResourcesGeneric {
		// first try to find a backup for this resourceType with the exact timestamp
		var computedName string
		backupTimestamp := strings.LastIndex(backupName, "-")
		if backupTimestamp != -1 {
			computedName = veleroBackupNames[resourceType] + backupName[backupTimestamp:]
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
			if !strings.Contains(bkp.Name, veleroBackupNames[resourceType]) ||
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

// retrieve the backup details for this restore object
// based on the restore spec options
func retrieveRestoreDetails(
	ctx context.Context,
	c client.Client,
	s *runtime.Scheme,
	acmRestore *v1beta1.Restore,
	restoreOnlyManagedClusters bool,
) ([]ResourceType, map[ResourceType]*veleroapi.Restore,
	error) {

	restoreLength := len(veleroBackupNames) - 1 // ignore validation backup
	if restoreOnlyManagedClusters {
		restoreLength = 3 // will get only managed clusters, generic resources and credentials
	}
	restoreKeys := make([]ResourceType, 0, restoreLength)
	for key := range veleroBackupNames {
		if key == ValidationSchedule ||
			(restoreOnlyManagedClusters && !(key == ManagedClusters || key == ResourcesGeneric || key == Credentials)) {
			// ignore validation backup; this is used for the policy
			// to validate that there are backups schedules enabled
			// also ignore all but managed clusters when only this is restored
			continue
		}
		restoreKeys = append(restoreKeys, key)
	}
	// sort restores to restore first credentials, last resources
	sort.Slice(restoreKeys, func(i, j int) bool {
		return ResourceTypePriority[restoreKeys[i]] < ResourceTypePriority[restoreKeys[j]]
	})

	restoreMap, err := processRetrieveRestoreDetails(ctx, c, s, acmRestore, restoreKeys)
	return restoreKeys, restoreMap, err

}

func processRetrieveRestoreDetails(
	ctx context.Context,
	c client.Client,
	s *runtime.Scheme,
	acmRestore *v1beta1.Restore,
	restoreKeys []ResourceType,
) (map[ResourceType]*veleroapi.Restore,
	error) {

	restoreLogger := log.FromContext(ctx)

	veleroRestoresToCreate := make(map[ResourceType]*veleroapi.Restore, len(restoreKeys))

	veleroBackups := &veleroapi.BackupList{}
	if err := c.List(ctx, veleroBackups, client.InNamespace(acmRestore.Namespace)); err == nil {
		for i := range restoreKeys {
			backupName := latestBackupStr

			key := restoreKeys[i]
			switch key {
			case ManagedClusters:
				if acmRestore.Spec.VeleroManagedClustersBackupName != nil {
					backupName = *acmRestore.Spec.VeleroManagedClustersBackupName
				}
			case Credentials, CredentialsHive, CredentialsCluster:
				if acmRestore.Spec.VeleroCredentialsBackupName != nil {
					backupName = *acmRestore.Spec.VeleroCredentialsBackupName
				}
			case ResourcesGeneric, Resources:
				if acmRestore.Spec.VeleroResourcesBackupName != nil {
					backupName = *acmRestore.Spec.VeleroResourcesBackupName
				}

			}

			if (key == Credentials || key == ResourcesGeneric) && backupName == skipRestoreStr &&
				acmRestore.Spec.VeleroManagedClustersBackupName != nil {
				// if this is set to skip but managed clusters are restored
				// we still need the generic resources and credentials
				// for the resources with the label value 'cluster-activation'
				backupName = *acmRestore.Spec.VeleroManagedClustersBackupName
			}

			backupName = strings.ToLower(strings.TrimSpace(backupName))

			if backupName == "" {
				acmRestore.Status.LastMessage = fmt.Sprintf(
					"Backup name not found for resource type: %s",
					key,
				)
				return veleroRestoresToCreate, fmt.Errorf(
					"backup name not found",
				)
			}

			if backupName == skipRestoreStr {
				continue
			}

			veleroRestore := &veleroapi.Restore{}
			veleroBackupName, veleroBackup, err := getVeleroBackupName(
				ctx,
				c,
				acmRestore.Namespace,
				key,
				backupName,
				veleroBackups,
			)
			if err != nil {

				if key != CredentialsHive && key != CredentialsCluster && key != ResourcesGeneric {
					// ignore missing hive or cluster key backup files
					// for the case when the backups were created with an older controller version
					// or with oadp 1.1 when the OrSelector has being used
					restoreLogger.Info(
						"backup name not found, skipping restore for",
						"name", acmRestore.Name,
						"namespace", acmRestore.Namespace,
						"type", key,
					)
					acmRestore.Status.LastMessage = fmt.Sprintf(
						"Backup %s Not found for resource type: %s",
						backupName,
						key,
					)

					return veleroRestoresToCreate, err
				}
			} else {
				veleroRestore.Name = getValidKsRestoreName(acmRestore.Name, veleroBackupName)

				veleroRestore.Namespace = acmRestore.Namespace
				veleroRestore.Spec.BackupName = veleroBackupName

				// set backup label
				labels := veleroRestore.GetLabels()
				if labels == nil {
					labels = make(map[string]string)
				}
				labels[BackupScheduleClusterLabel] = veleroBackup.GetLabels()[BackupScheduleClusterLabel]
				veleroRestore.SetLabels(labels)

				setOptionalProperties(key, acmRestore, veleroRestore)

				if err := ctrl.SetControllerReference(acmRestore, veleroRestore, s); err != nil {
					acmRestore.Status.LastMessage = fmt.Sprintf(
						"Could not set controller reference for resource type: %s",
						key,
					)
					return veleroRestoresToCreate, err
				}
				veleroRestoresToCreate[key] = veleroRestore
			}
		}
	}
	return veleroRestoresToCreate, nil
}

func setOptionalProperties(
	key ResourceType,
	acmRestore *v1beta1.Restore,
	veleroRestore *veleroapi.Restore,
) {

	// set includeClusterResources for all restores except credentials
	if key == Resources || key == ManagedClusters || key == ResourcesGeneric {
		var clusterResource bool = true
		veleroRestore.Spec.IncludeClusterResources = &clusterResource
	}

	veleroRestore.Spec.ExcludedResources = append(veleroRestore.Spec.ExcludedResources, "CustomResourceDefinition")

	// update existing resources if part of the new backup
	veleroRestore.Spec.ExistingResourcePolicy = veleroapi.PolicyTypeUpdate

	// pass on velero optional properties
	if acmRestore.Spec.RestoreStatus != nil {
		veleroRestore.Spec.RestoreStatus = acmRestore.Spec.RestoreStatus
	}
	if acmRestore.Spec.PreserveNodePorts != nil {
		veleroRestore.Spec.PreserveNodePorts = acmRestore.Spec.PreserveNodePorts
	}
	if acmRestore.Spec.RestorePVs != nil {
		veleroRestore.Spec.RestorePVs = acmRestore.Spec.RestorePVs
	}
	if len(acmRestore.Spec.Hooks.Resources) > 0 {

		veleroRestore.Spec.Hooks.Resources = append(veleroRestore.Spec.Hooks.Resources,
			acmRestore.Spec.Hooks.Resources...,
		)
	}

	// set user options for resource filtering
	setUserRestoreFilters(acmRestore, veleroRestore)

	// allow namespace mapping
	if acmRestore.Spec.NamespaceMapping != nil {
		veleroRestore.Spec.NamespaceMapping = acmRestore.Spec.NamespaceMapping
	}
}

// set user options for resource filtering
func setUserRestoreFilters(
	acmRestore *v1beta1.Restore,
	veleroRestore *veleroapi.Restore,
) {

	// add any label selector set using the acm restore resource spec
	if acmRestore.Spec.LabelSelector != nil {

		if veleroRestore.Spec.LabelSelector == nil {
			labels := &v1.LabelSelector{}
			veleroRestore.Spec.LabelSelector = labels
		}

		// append LabelSelector resources since the acm restore uses the veleroRestore.Spec.LabelSelector
		// to filter out activation resources when restoring the data - as it does for credentials-active restore, using the cluster.open-cluster-management.io/backup=cluster-activation label
		// we want to keep any acm predefined veleroRestore.Spec.LabelSelector and add to those the user defined ones

		// set MatchExpressions
		if acmRestore.Spec.LabelSelector.MatchExpressions != nil {
			// create the MatchExpression for the velero resource, if not defined yet
			if veleroRestore.Spec.LabelSelector.MatchExpressions == nil {
				requirements := make([]v1.LabelSelectorRequirement, 0)
				veleroRestore.Spec.LabelSelector.MatchExpressions = requirements
			}

			veleroRestore.Spec.LabelSelector.MatchExpressions = append(veleroRestore.Spec.LabelSelector.MatchExpressions,
				acmRestore.Spec.LabelSelector.MatchExpressions...,
			)
		}

		// set MatchLabels
		if acmRestore.Spec.LabelSelector.MatchLabels != nil {
			// create the MatchLabels for the velero resource, if not defined yet
			if veleroRestore.Spec.LabelSelector.MatchLabels == nil {
				matchlabels := make(map[string]string, 0)
				veleroRestore.Spec.LabelSelector.MatchLabels = matchlabels
			}

			maps.Copy(veleroRestore.Spec.LabelSelector.MatchLabels, acmRestore.Spec.LabelSelector.MatchLabels)
		}
	}

	// add any or label selector set using the acm restore resource spec
	if acmRestore.Spec.OrLabelSelectors != nil {

		if veleroRestore.Spec.OrLabelSelectors == nil {
			labels := []*v1.LabelSelector{}
			veleroRestore.Spec.OrLabelSelectors = labels
		}

		// append user defined OrLabelSelector values to the restore OrLabelSelector
		// to keep any predefined OrLabelSelector values
		veleroRestore.Spec.OrLabelSelectors = append(veleroRestore.Spec.OrLabelSelectors, acmRestore.Spec.OrLabelSelectors...)

	}

	// allow excluding namespaces
	if acmRestore.Spec.ExcludedNamespaces != nil {

		for i := range acmRestore.Spec.ExcludedNamespaces {
			veleroRestore.Spec.ExcludedNamespaces = appendUnique(veleroRestore.Spec.ExcludedNamespaces,
				acmRestore.Spec.ExcludedNamespaces[i])
		}

	}

	// allow including namespaces
	if acmRestore.Spec.IncludedNamespaces != nil {

		for i := range acmRestore.Spec.IncludedNamespaces {
			veleroRestore.Spec.IncludedNamespaces = appendUnique(veleroRestore.Spec.IncludedNamespaces,
				acmRestore.Spec.IncludedNamespaces[i])
		}

	}

	// allow excluding resources
	if acmRestore.Spec.ExcludedResources != nil {

		for i := range acmRestore.Spec.ExcludedResources {
			veleroRestore.Spec.ExcludedResources = appendUnique(veleroRestore.Spec.ExcludedResources,
				acmRestore.Spec.ExcludedResources[i])
		}

	}

	// allow including resources
	if acmRestore.Spec.IncludedResources != nil {

		for i := range acmRestore.Spec.IncludedResources {
			veleroRestore.Spec.IncludedResources = appendUnique(veleroRestore.Spec.IncludedResources,
				acmRestore.Spec.IncludedResources[i])
		}

	}
}
