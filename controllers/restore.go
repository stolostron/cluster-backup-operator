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
	"strings"

	"github.com/go-logr/logr"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
) {
	logger := log.FromContext(ctx)
	nsScopedMsg := fmt.Sprintf(
		"Deleted resource %s [%s.%s]",
		resource.GetKind(),
		resource.GetName(),
		resource.GetNamespace())

	globalResourceMsg := fmt.Sprintf(
		"Deleted resource %s [%s]",
		resource.GetKind(),
		resource.GetName())

	patch := `[ { "op": "remove", "path": "/metadata/finalizers" } ]`
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if err := dr.Namespace(resource.GetNamespace()).Delete(ctx, resource.GetName(), deleteOptions); err != nil {
			logger.Info(err.Error())
		} else {
			logger.Info(nsScopedMsg)
		}
		// namespaced resources should specify the namespace
		if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
			// delete finalizers and delete resource in this way
			if _, err := dr.Namespace(resource.GetNamespace()).Patch(ctx, resource.GetName(),
				types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
				logger.Info(err.Error())
			} else {
				logger.Info(nsScopedMsg)
			}
		}
	} else {
		// for cluster-wide resources
		if err := dr.Delete(ctx, resource.GetName(), deleteOptions); err != nil {
			logger.Info(err.Error())
		} else {
			logger.Info(globalResourceMsg)
		}
		if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
			// delete finalizers and delete resource in this way
			if _, err := dr.Patch(ctx, resource.GetName(),
				types.MergePatchType, []byte(patch), v1.PatchOptions{}); err != nil {
				logger.Info(err.Error())
			} else {
				logger.Info(globalResourceMsg)
			}
		}
	}
}

func prepareForRestore(
	ctx context.Context,
	c client.Client,
	dc discovery.DiscoveryInterface,
	dyn dynamic.Interface,
	restoreType ResourceType,
	veleroBackup *veleroapi.Backup,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	deleteOptions v1.DeleteOptions,
) {
	logger := log.FromContext(ctx)
	logger.Info("enter prepareForRestoreResources for " + string(restoreType))
	// delete each resource from included resources, if it has a velero annotation
	// meaning that the resource was created by another restore

	labelSelector := "velero.io/backup-name"
	switch restoreType {
	case ResourcesGeneric:
		labelSelector = labelSelector + ",cluster.open-cluster-management.io/backup"
	case Credentials:
		labelSelector = labelSelector + "," + backupCredsUserLabel
	case CredentialsHive:
		labelSelector = labelSelector + "," + backupCredsHiveLabel
	case CredentialsCluster:
		labelSelector = labelSelector + "," + backupCredsClusterLabel
	}

	var resources []string
	if restoreType != ResourcesGeneric {
		resources = veleroBackup.Spec.IncludedResources
	} else {
		// for generic resources get all CRDs and exclude the ones in the veleroBackup.Spec.ExcludedResources
		resources, _ = getGenericCRDFromAPIGroups(ctx, dc, veleroBackup)
	}

	for i := range resources {

		kind, groupName := getResourceDetails(resources[i])

		if kind == "clusterdeployment" || kind == "machinepool" {
			// old backups have a short version for these resource
			groupName = "hive.openshift.io"
		}

		groupKind := schema.GroupKind{
			Group: groupName,
			Kind:  kind,
		}
		mapping, err := mapper.RESTMapping(groupKind, "")
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get dynamic mapper for group=%s, error : %s",
				groupKind, err.Error()))
			continue
		}
		var dr = dyn.Resource(mapping.Resource)
		if dr == nil {
			continue
		}
		// get all resources of this type with the velero.io/backup-name set
		// we want to clean them up, they were created by a previous restore
		dynamiclist, err := dr.List(ctx, v1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			// ignore error
			continue
		}
		// get all items and delete them
		for i := range dynamiclist.Items {
			deleteDynamicResource(ctx, mapping, dr, dynamiclist.Items[i], deleteOptions)
		}

	}
	logger.Info("exit prepareForRestoreResources for " + string(restoreType))
}

func (r *RestoreReconciler) becomeActiveCluster(ctx context.Context,
	restore v1beta1.Restore) {
	logger := log.FromContext(ctx)

	logger.Info("create backup schedule")
	backupSchedule := v1beta1.BackupSchedule{}
	backupSchedule.Namespace = restore.Namespace
	backupSchedule.Name = restore.Name + "-backup"

	backupSchedule.Spec.VeleroSchedule = "0 */1 * * *"

	// set restore name as label annotation
	// so we know from what activation restore this backup was initiated
	labels := backupSchedule.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[BackupScheduleActivationLabel] = restore.Name

	backupSchedule.SetLabels(labels)

	if err := r.Create(ctx, &backupSchedule, &client.CreateOptions{}); err != nil {
		logger.Error(err, "Failed to create schedule")
	}

}
