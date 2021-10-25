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

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	chnv1 "github.com/open-cluster-management/multicloud-operators-channel/pkg/apis/apps/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	backupNamespacesACM           = [...]string{"hive", "openshift-operator-lifecycle-manager"}
	backupManagedClusterResources = [...]string{"secret", "configmap",
		"ManagedCluster.cluster.open-cluster-management.io", "ManagedClusterAddon", "ServiceAccount",
		"ManagedClusterInfo", "ManagedClusterSet", "ManagedClusterSetBindings", "KlusterletAddonConfig",
		"ManagedClusterView", "ManagedCluster.clusterview.open-cluster-management.io",
		"ClusterPool", "ClusterDeployment", "MachinePool", "ClusterProvision",
		"ClusterState", "ClusterSyncLease", "ClusterSync", "ClusterCurator"}
	backupResources = [...]string{
		"applications.argoproj.io", "applicationset.argoproj.io",
		"appprojects.argoproj.io", "argocds.argoproj.io",
		"applications.app.k8s.io",
		"channel", "subscription",
		"deployable",
		"helmrelease",
		"placementrule", "placement", "placementdecisions",
		"PlacementBinding.policy.open-cluster-management.io",
		"policy",
	}
	backupCredsResources = [...]string{"secret"}
)

var (
	apiGVString = v1beta1.GroupVersion.String()
	// create credentials schedule first since this is the fastest one, followed by resources
	// mapping ResourceTypes to Velero schedule names
	veleroScheduleNames = map[ResourceType]string{
		Credentials:     "acm-credentials-schedule",
		Resources:       "acm-resources-schedule",
		ManagedClusters: "acm-managed-clusters-schedule",
	}
)

// clean up old backups if they exceed the maxCount number
func cleanupBackups(
	ctx context.Context,
	maxBackups int,
	c client.Client,
) {
	backupLogger := log.FromContext(ctx)

	backupLogger.Info(fmt.Sprintf("check if needed to remove backups maxBackups=%d", maxBackups))
	veleroBackupList := veleroapi.BackupList{}
	if err := c.List(ctx, &veleroBackupList, &client.ListOptions{}); err != nil {
		backupLogger.Error(err, "failed to get veleroapi.BackupList")
	} else {

		// get acm backups only when counting existing backups
		sliceBackups := filterBackups(veleroBackupList.Items[:], func(bkp veleroapi.Backup) bool {
			return strings.HasPrefix(bkp.Name, veleroScheduleNames[Credentials]) ||
				strings.HasPrefix(bkp.Name, veleroScheduleNames[ManagedClusters]) ||
				strings.HasPrefix(bkp.Name, veleroScheduleNames[Resources])
		})

		if maxBackups < len(sliceBackups) {
			// need to delete backups
			// sort backups by create time
			sort.Slice(sliceBackups, func(i, j int) bool {
				var timeA int64
				var timeB int64
				if sliceBackups[i].Status.StartTimestamp != nil {
					timeA = sliceBackups[i].Status.StartTimestamp.Time.Unix()
				}
				if sliceBackups[j].Status.StartTimestamp != nil {
					timeB = sliceBackups[j].Status.StartTimestamp.Time.Unix()
				}
				return timeA < timeB
			})

			for i := 0; i < len(sliceBackups)-maxBackups; i++ {
				deleteBackup(ctx, &sliceBackups[i], c)
			}
		}

	}
}

func deleteBackup(
	ctx context.Context,
	backup *veleroapi.Backup,
	c client.Client,
) {
	// delete backup now
	backupLogger := log.FromContext(ctx)
	backupName := backup.ObjectMeta.Name
	backupNamespace := backup.ObjectMeta.Namespace
	backupLogger.Info(fmt.Sprintf("delete backup %s", backupName))

	backupDeleteIdentity := types.NamespacedName{
		Name:      backupName,
		Namespace: backupNamespace,
	}

	// get the velero CR using the backupDeleteIdentity
	veleroDeleteBackup := &veleroapi.DeleteBackupRequest{}
	err := c.Get(ctx, backupDeleteIdentity, veleroDeleteBackup)
	if err != nil {
		// check if this is a  resource NotFound error, in which case create the resource
		if k8serr.IsNotFound(err) {

			veleroDeleteBackup.Spec.BackupName = backupName
			veleroDeleteBackup.Name = backupDeleteIdentity.Name
			veleroDeleteBackup.Namespace = backupDeleteIdentity.Namespace

			err = c.Create(ctx, veleroDeleteBackup, &client.CreateOptions{})
			if err != nil {
				backupLogger.Error(
					err,
					fmt.Sprintf("create  DeleteBackupRequest request error for %s", backupName),
				)
			}
		} else {
			backupLogger.Error(err, fmt.Sprintf("Failed to create DeleteBackupRequest for resource %s", backupName))
		}
	} else {
		backupLogger.Info(fmt.Sprintf("DeleteBackupRequest already exists, skip request creation %s", backupName))
	}
}

// set all acm resources backup info
func setResourcesBackupInfo(
	ctx context.Context,
	veleroBackupTemplate *veleroapi.BackupSpec,
	c client.Client,
) {

	backupLogger := log.FromContext(ctx)
	var clusterResource bool = false
	veleroBackupTemplate.IncludeClusterResources = &clusterResource
	veleroBackupTemplate.ExcludedNamespaces = appendUnique(
		veleroBackupTemplate.ExcludedNamespaces,
		"local-cluster",
	)

	for i := range backupResources { // acm resources
		veleroBackupTemplate.IncludedResources = appendUnique(
			veleroBackupTemplate.IncludedResources,
			backupResources[i],
		)
	}

	// exclude acm channel namespaces
	channels := chnv1.ChannelList{}
	if err := c.List(ctx, &channels, &client.ListOptions{}); err != nil {
		// if NotFound error
		if !k8serr.IsNotFound(err) {
			backupLogger.Info("channel resources NOT FOUND")
		} else {
			backupLogger.Error(err, "failed to get chnv1.ChannelList")
		}
	} else {
		for i := range channels.Items {
			if channels.Items[i].Name == "charts-v1" {
				veleroBackupTemplate.ExcludedNamespaces = appendUnique(
					veleroBackupTemplate.ExcludedNamespaces,
					channels.Items[i].Namespace,
				)
			}
		}
	}
}

// set credentials backup info
func setCredsBackupInfo(
	ctx context.Context,
	veleroBackupTemplate *veleroapi.BackupSpec,
	c client.Client,
) {

	var clusterResource bool = false
	veleroBackupTemplate.IncludeClusterResources = &clusterResource

	for i := range backupCredsResources { // acm secrets
		veleroBackupTemplate.IncludedResources = appendUnique(
			veleroBackupTemplate.IncludedResources,
			backupCredsResources[i],
		)
	}

	if veleroBackupTemplate.LabelSelector == nil {
		labels := &v1.LabelSelector{}
		veleroBackupTemplate.LabelSelector = labels

		requirements := make([]v1.LabelSelectorRequirement, 0)
		veleroBackupTemplate.LabelSelector.MatchExpressions = requirements
	}
	req := &v1.LabelSelectorRequirement{}
	req.Key = "cluster.open-cluster-management.io/type"
	req.Operator = "Exists"
	veleroBackupTemplate.LabelSelector.MatchExpressions = append(
		veleroBackupTemplate.LabelSelector.MatchExpressions,
		*req,
	)
}

// set managed clusters backup info
func setManagedClustersBackupInfo(
	ctx context.Context,
	veleroBackupTemplate *veleroapi.BackupSpec,
	c client.Client,
) {

	backupLogger := log.FromContext(ctx)
	var clusterResource bool = true // include cluster level managed cluster resources
	veleroBackupTemplate.IncludeClusterResources = &clusterResource

	for i := range backupManagedClusterResources { // managed clusters required resources, from namespace or cluster level
		veleroBackupTemplate.IncludedResources = appendUnique(
			veleroBackupTemplate.IncludedResources,
			backupManagedClusterResources[i],
		)
	}

	for i := range backupNamespacesACM { // acm ns
		veleroBackupTemplate.IncludedNamespaces = appendUnique(
			veleroBackupTemplate.IncludedNamespaces,
			backupNamespacesACM[i],
		)
	}

	// add cluster pool namespaces
	clusterPools := hivev1.ClusterPoolList{}
	if err := c.List(ctx, &clusterPools, &client.ListOptions{}); err != nil {
		backupLogger.Error(err, "failed to get hivev1.ClusterPoolList")
	} else {
		for i := range clusterPools.Items {
			veleroBackupTemplate.IncludedNamespaces = appendUnique(
				veleroBackupTemplate.IncludedNamespaces,
				clusterPools.Items[i].Namespace,
			)
		}
	}

	// get managed clusters namespaces
	managedClusterList := clusterv1.ManagedClusterList{}
	if err := c.List(ctx, &managedClusterList, &client.ListOptions{}); err != nil {
		backupLogger.Error(err, "failed to get clusterv1.ManagedClusterList")
	} else {
		for i := range managedClusterList.Items {
			if managedClusterList.Items[i].Name == "local-cluster" {
				continue
			}
			veleroBackupTemplate.IncludedNamespaces = appendUnique(
				veleroBackupTemplate.IncludedNamespaces,
				managedClusterList.Items[i].Name,
			)
		}
	}
}

func isBackupFinished(backups []*veleroapi.Backup) bool {

	if backups == nil || len(backups) <= 0 {
		return false
	}

	// get all backups and check status for each
	for i := 0; i < len(backups); i++ {
		if backups[i].Status.Phase != "Completed" &&
			backups[i].Status.Phase != "Failed" &&
			backups[i].Status.Phase != "PartiallyFailed" {
			return false // some backup is not ready
		}
	}

	return true
}

// filter backup list based on a boolean function
func filterBackups(vs []veleroapi.Backup, f func(veleroapi.Backup) bool) []veleroapi.Backup {
	filtered := make([]veleroapi.Backup, 0)
	for _, v := range vs {
		if f(v) {
			filtered = append(filtered, v)
		}
	}
	return filtered
}
