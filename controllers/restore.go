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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func prepareForRestore(
	ctx context.Context,
	c client.Client,
	dc discovery.DiscoveryInterface,
	dyn dynamic.Interface,
	logger logr.Logger,
	restoreType ResourceType,
	veleroBackup *veleroapi.Backup,
) {

	switch restoreType {
	case Credentials, CredentialsHive, CredentialsCluster:
		prepareForRestoreCredentials(ctx, c, logger, restoreType)
	case Resources: //, ResourcesGeneric:
		prepareForRestoreResources(ctx, c, dc, dyn, logger, restoreType, veleroBackup)
	}
}

func deleteResource(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	name string,
	namespace string,
	logger logr.Logger,
) {
	// TODO if finalizers, call c.Patch() to remove them
	//c.Patch()
	err := c.Delete(ctx, obj)
	if err != nil {
		logger.Info(err.Error())
	} else {
		logger.Info("deleted resource" + namespace + "-" + name)
	}

}

func prepareForRestoreCredentials(
	ctx context.Context,
	c client.Client,
	logger logr.Logger,
	restoreType ResourceType,
) {

	logger.Info("enter prepareForRestoreCredentials for " + string(restoreType))
	var labelKey string
	switch string(restoreType) {
	case string(CredentialsHive):
		labelKey = backupCredsHiveLabel
	case string(CredentialsCluster):
		labelKey = backupCredsClusterLabel
	default:
		labelKey = backupCredsUserLabel
	}

	labelSelector := &v1.LabelSelector{}

	req := &v1.LabelSelectorRequirement{}
	req.Key = "velero.io/backup-name"
	req.Operator = "Exists"
	labelSelector.MatchExpressions = append(
		labelSelector.MatchExpressions,
		*req,
	)
	reqCredential := &v1.LabelSelectorRequirement{}
	reqCredential.Key = labelKey
	reqCredential.Operator = "Exists"
	labelSelector.MatchExpressions = append(
		labelSelector.MatchExpressions,
		*reqCredential,
	)

	var labelSelectors labels.Selector
	labelSelectors, _ = v1.LabelSelectorAsSelector(labelSelector)

	secrets := corev1.SecretList{}
	if err := c.List(ctx, &secrets, &client.ListOptions{LabelSelector: labelSelectors}); err != nil {
		logger.Info(err.Error())
	} else {
		for i := range secrets.Items {
			deleteResource(ctx, c, &secrets.Items[i], secrets.Items[i].Name, secrets.Items[i].Namespace, logger)
		}
	}
	configmaps := corev1.ConfigMapList{}
	if err := c.List(ctx, &configmaps, &client.ListOptions{LabelSelector: labelSelectors}); err != nil {
		logger.Info(err.Error())
	} else {
		for i := range configmaps.Items {
			deleteResource(ctx, c, &configmaps.Items[i], configmaps.Items[i].Name, configmaps.Items[i].Namespace, logger)
		}
	}
	logger.Info("exit prepareForRestoreCredentials for " + string(restoreType))

}

func deleteDynamicResource(
	ctx context.Context,
	mapping *meta.RESTMapping,
	dr dynamic.NamespaceableResourceInterface,
	resource unstructured.Unstructured,
	deleteOptions v1.DeleteOptions,
	logger logr.Logger,
) {
	// TODO if finalizers, call c.Patch() to remove them
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		// namespaced resources should specify the namespace
		err := dr.Namespace(resource.GetNamespace()).Delete(ctx, resource.GetName(), deleteOptions)
		if err != nil {
			logger.Info(err.Error())
		} else {
			logger.Info("deleted resource " + resource.GetKind() + "[" + resource.GetName() + "." + resource.GetNamespace() + "]")
		}
	} else {
		// for cluster-wide resources
		//dr.Patch()
		err := dr.Delete(ctx, resource.GetName(), deleteOptions)
		if err != nil {
			logger.Info(err.Error())
		} else {
			logger.Info("deleted resource " + resource.GetKind() + "[" + resource.GetName() + "." + resource.GetNamespace() + "]")
		}
	}

}

func prepareForRestoreResources(
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

	labelSelector := ""
	if restoreType == ResourcesGeneric {
		// add an extra label here, which is the cluster.open-cluster-management.io/backup
		labelSelector = "velero.io/backup-name, cluster.open-cluster-management.io/backup"
	} else {
		labelSelector = "velero.io/backup-name"
		// otherwise exclude all resources with a cluster.open-cluster-management.io/backup label
		//labelSelector = "velero.io/backup-name, !cluster.open-cluster-management.io/backup"
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	deletePolicy := v1.DeletePropagationForeground
	deleteOptions := v1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}
	for i := range veleroBackup.Spec.IncludedResources {

		kind, groupName := getResourceDetails(veleroBackup.Spec.IncludedResources[i])
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
