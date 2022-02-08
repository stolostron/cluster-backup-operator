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

	"github.com/go-logr/logr"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	logger logr.Logger,
) {
	patch := `[ { "op": "remove", "path": "/metadata/finalizers" } ]`
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
			// delete finalizers
			dr.Namespace(resource.GetNamespace()).Patch(ctx, resource.GetName(), types.JSONPatchType, []byte(patch), v1.PatchOptions{})
		}
		// namespaced resources should specify the namespace
		err := dr.Namespace(resource.GetNamespace()).Delete(ctx, resource.GetName(), deleteOptions)
		if err != nil {
			logger.Info(err.Error())
		} else {
			logger.Info("deleted resource " + resource.GetKind() + "[" + resource.GetName() + "." + resource.GetNamespace() + "]")
		}

	} else {
		// for cluster-wide resources
		logger.Info("deleted resource " + resource.GetKind() + "[" + resource.GetName() + "." + resource.GetNamespace() + "]")
		if resource.GetFinalizers() != nil && len(resource.GetFinalizers()) > 0 {
			// delete finalizers
			dr.Patch(ctx, resource.GetName(), types.MergePatchType, []byte(patch), v1.PatchOptions{})
		}
		err := dr.Delete(ctx, resource.GetName(), deleteOptions)
		if err != nil {
			logger.Info(err.Error())
		} else {
			logger.Info("deleted resource " + resource.GetKind() + "[" + resource.GetName() + "." + resource.GetNamespace() + "]")
		}
	}
}

func prepareForRestore(
	ctx context.Context,
	c client.Client,
	dc discovery.DiscoveryInterface,
	dyn dynamic.Interface,
	logger logr.Logger,
	restoreType ResourceType,
	veleroBackup *veleroapi.Backup,
) {
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

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	deletePolicy := v1.DeletePropagationForeground
	deleteOptions := v1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}
	for i := range veleroBackup.Spec.IncludedResources {

		kind, groupName := getResourceDetails(veleroBackup.Spec.IncludedResources[i])

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
			logger.Info(err.Error())
		} else {
			var dr = dyn.Resource(mapping.Resource)
			if dr != nil {
				// get all resources of this type with the velero.io/backup-name set
				// we want to clean them up, they were created by a previous restore
				dynamiclist, err := dr.List(ctx, v1.ListOptions{LabelSelector: labelSelector})
				if err != nil {
					logger.Info(err.Error())
				} else {
					for i := range dynamiclist.Items {
						// delete them
						deleteDynamicResource(ctx, mapping, dr, dynamiclist.Items[i], deleteOptions, logger)
					}
				}
			}
		}
	}
	logger.Info("exit prepareForRestoreResources for " + string(restoreType))
}
