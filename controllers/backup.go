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
	"time"

	v1beta1 "github.com/open-cluster-management-io/cluster-backup-operator/api/v1beta1"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	chnv1 "github.com/open-cluster-management/multicloud-operators-channel/pkg/apis/apps/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	backupNamespacesACM = [...]string{"hive", "openshift-operator-lifecycle-manager"}
	backupResources     = [...]string{
		"applications.app.k8s.io",
		"channel", "subscription",
		"application", "deployable",
		"placementrule", "placement", "placementdecisions",
		"PlacementBinding.policy.open-cluster-management.io",
		"policy",
		//"multiclusterobservability"
	}
	backupCredsResources = [...]string{"secret"}
)

// returns then name of the last backup resource, or,
// if this is the first time to run the backup or current backup is not found, a newly generated name
func (r *BackupReconciler) getActiveBackupName(backup *v1beta1.Backup, c client.Client, ctx context.Context) string {

	if backup.Status.CurrentBackup == "" || isBackupPhaseFinished(backup.Status.Phase) {

		// no active backup, return newyly generated backup name
		return getVeleroBackupName(backup.Name, backup.Spec.VeleroConfig.Namespace)
	} else {
		// check if current backup resource exists

		veleroIdentity := types.NamespacedName{
			Namespace: backup.Spec.VeleroConfig.Namespace,
			Name:      backup.Status.CurrentBackup,
		}
		// get the velero CR using the veleroIdentity
		veleroBackup := &veleroapi.Backup{}
		err := r.Get(ctx, veleroIdentity, veleroBackup)
		if err != nil {
			// unable to get backup resource, create a new one
			backup.Status.CurrentBackup = ""
			return getVeleroBackupName(backup.Name, backup.Spec.VeleroConfig.Namespace)
		}
	}

	if !isBackupFinished(backup.Status.VeleroBackups) {
		//if an active backup, return the CurrentBackup value
		return backup.Status.CurrentBackup
	}

	// no active backup, return newyly generated backup name
	return getVeleroBackupName(backup.Name, backup.Spec.VeleroConfig.Namespace)

}

// clean up old backups if they exceed the maxCount number
func (r *BackupReconciler) cleanupBackups(ctx context.Context, backup *v1beta1.Backup, c client.Client) {
	maxBackups := backup.Spec.MaxBackups * 3 // each backup contains 3 backup files ( for clusters, resources and secrets )
	backupLogger := log.FromContext(ctx)

	backupLogger.Info(fmt.Sprintf("check if needed to remove backups maxBackups=%d", maxBackups))
	veleroBackupList := veleroapi.BackupList{}
	if err := c.List(ctx, &veleroBackupList, &client.ListOptions{}); err != nil {

		// this is a NotFound error
		if !k8serr.IsNotFound(err) {
			backupLogger.Info("no backups found")
		} else {
			backupLogger.Error(err, "failed to get veleroapi.BackupList")
		}
	} else {

		sliceBackups := veleroBackupList.Items[:]
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

			backupsInError := filterBackups(sliceBackups, func(bkp veleroapi.Backup) bool {
				return bkp.Status.Errors > 0
			})

			// delete backup in error first
			for i := 0; i < min(len(backupsInError), maxBackups); i++ {
				r.deleteBackup(&backupsInError[i], ctx, c)
			}

			for i := 0; i < len(sliceBackups)-maxBackups; i++ {
				// delete extra backups now
				if sliceBackups[i].Status.Errors > 0 {
					continue // ignore error status backups, they were processed in the step above
				}
				r.deleteBackup(&sliceBackups[i], ctx, c)
			}
		}

	}
}

func (r *BackupReconciler) deleteBackup(backup *veleroapi.Backup, ctx context.Context, c client.Client) {
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
	err := r.Get(ctx, backupDeleteIdentity, veleroDeleteBackup)
	if err != nil {
		// check if this is a  resource NotFound error, in which case create the resource
		if k8serr.IsNotFound(err) {

			veleroDeleteBackup.Spec.BackupName = backupName
			veleroDeleteBackup.Name = backupDeleteIdentity.Name
			veleroDeleteBackup.Namespace = backupDeleteIdentity.Namespace

			err = c.Create(ctx, veleroDeleteBackup, &client.CreateOptions{})
			if err != nil {
				backupLogger.Error(err, fmt.Sprintf("create  DeleteBackupRequest request error for %s", backupName))
			}
		} else {
			backupLogger.Error(err, fmt.Sprintf("Failed to create DeleteBackupRequest for resource %s", backupName))
		}
	} else {
		backupLogger.Info(fmt.Sprintf("DeleteBackupRequest already exists, skip request creation %s", backupName))
	}
}

// create velero.io.Backup resource for the specified resourceType types
func (r *BackupReconciler) createBackupForResource(resourceType string, backup *v1beta1.Backup, veleroBackup *veleroapi.Backup, ctx context.Context, c client.Client) {

	if resourceType != "creds" && resourceType != "resource" {
		// skip if unknown backup type
		return
	}

	backupLogger := log.FromContext(ctx)
	// create resources backup
	veleroResIdentity := types.NamespacedName{
		Namespace: veleroBackup.Namespace,
		Name:      fmt.Sprintf("%s-%s", veleroBackup.Name, resourceType),
	}

	veleroResBackup := &veleroapi.Backup{}
	veleroResBackup.Name = veleroResIdentity.Name
	veleroResBackup.Namespace = veleroResIdentity.Namespace

	err := r.Get(ctx, veleroResIdentity, veleroResBackup)
	if err != nil {
		backupLogger.Info("velero.io.Backup [name=%s, namespace=%s] returned error, checking if the resource was not yet created", veleroResIdentity.Name, veleroResIdentity.Namespace)
		// check if this is a  resource NotFound error, in which case create the resource
		if k8serr.IsNotFound(err) {
			// create backup based on resource type
			switch resourceType {
			case "resource":
				setResourcesBackupInfo(ctx, veleroResBackup, c)
			case "creds":
				setCredsBackupInfo(ctx, veleroResBackup, c)
			}

			err = c.Create(ctx, veleroResBackup, &client.CreateOptions{})
			if veleroResBackup != nil {
				// add backup to list of backups
				backup.Status.VeleroBackups = append(backup.Status.VeleroBackups, veleroResBackup)
			}

			if err != nil {
				backupLogger.Error(err, "create backup error")
				return
			}
		}
	} else {
		if resourceType == "creds" {
			if len(backup.Status.VeleroBackups) <= 1 {
				backup.Status.VeleroBackups = append(backup.Status.VeleroBackups, veleroResBackup)
			} else {
				backup.Status.VeleroBackups[1] = veleroResBackup
			}
		} else {
			if len(backup.Status.VeleroBackups) <= 2 {
				backup.Status.VeleroBackups = append(backup.Status.VeleroBackups, veleroResBackup)
			} else {
				backup.Status.VeleroBackups[2] = veleroResBackup
			}
		}
	}

	if veleroResBackup.Status.Progress != nil {
		msg := fmt.Sprintf("%s ; [%s: ItemsBackedUp[%d], TotalItems[%d]]", backup.Status.LastMessage, resourceType, veleroResBackup.Status.Progress.ItemsBackedUp, veleroResBackup.Status.Progress.TotalItems)
		backup.Status.LastMessage = msg
	}
}

// set all acm resources backup info
func setResourcesBackupInfo(ctx context.Context, veleroBackup *veleroapi.Backup, c client.Client) {

	backupLogger := log.FromContext(ctx)
	var clusterResource bool = false
	veleroBackup.Spec.IncludeClusterResources = &clusterResource
	veleroBackup.Spec.ExcludedNamespaces = appendUnique(veleroBackup.Spec.ExcludedNamespaces, "local-cluster")

	for i := range backupResources { // acm resources
		veleroBackup.Spec.IncludedResources = appendUnique(veleroBackup.Spec.IncludedResources, backupResources[i])
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
				veleroBackup.Spec.ExcludedNamespaces = appendUnique(veleroBackup.Spec.ExcludedNamespaces, channels.Items[i].Namespace)
			}
		}
	}
}

// set credentials backup info
func setCredsBackupInfo(ctx context.Context, veleroBackup *veleroapi.Backup, c client.Client) {

	var clusterResource bool = false
	veleroBackup.Spec.IncludeClusterResources = &clusterResource

	for i := range backupCredsResources { // acm secrets
		veleroBackup.Spec.IncludedResources = appendUnique(veleroBackup.Spec.IncludedResources, backupCredsResources[i])
	}

	if veleroBackup.Spec.LabelSelector == nil {
		labels := &v1.LabelSelector{}
		veleroBackup.Spec.LabelSelector = labels

		requirements := make([]v1.LabelSelectorRequirement, 0)
		veleroBackup.Spec.LabelSelector.MatchExpressions = requirements
	}
	req := &v1.LabelSelectorRequirement{}
	req.Key = "cluster.open-cluster-management.io/type"
	req.Operator = "Exists"
	veleroBackup.Spec.LabelSelector.MatchExpressions = append(veleroBackup.Spec.LabelSelector.MatchExpressions, *req)
}

// set managed clusters backup info
func setManagedClustersBackupInfo(ctx context.Context, veleroBackup *veleroapi.Backup, c client.Client) {

	backupLogger := log.FromContext(ctx)
	var clusterResource bool = false
	veleroBackup.Spec.IncludeClusterResources = &clusterResource

	for i := range backupNamespacesACM { // acm ns
		veleroBackup.Spec.IncludedNamespaces = appendUnique(veleroBackup.Spec.IncludedNamespaces, backupNamespacesACM[i])
	}

	// get managed clusters namespaces
	managedClusterList := clusterv1.ManagedClusterList{}
	if err := c.List(ctx, &managedClusterList, &client.ListOptions{}); err != nil {
		// if NotFound error
		if !k8serr.IsNotFound(err) {
			backupLogger.Info("managed clusters resources NOT FOUND")
		} else {
			backupLogger.Error(err, "failed to get clusterv1.ManagedClusterList")
		}
	} else {
		for i := range managedClusterList.Items {
			if managedClusterList.Items[i].Name == "local-cluster" {
				continue
			}
			veleroBackup.Spec.IncludedNamespaces = appendUnique(veleroBackup.Spec.IncludedNamespaces, managedClusterList.Items[i].Name)
		}
	}
}

// update status for the last executed backup
func updateLastBackupStatus(backup *v1beta1.Backup) {

	if backup.Status.VeleroBackups != nil && len(backup.Status.VeleroBackups) > 0 {

		backup.Status.Phase = v1beta1.StatusPhase(getBackupPhase(backup.Status.VeleroBackups))
		if isBackupFinished(backup.Status.VeleroBackups) {
			backup.Status.LastBackup = backup.Status.CurrentBackup

			completedTime := getLastBackupCompletionTime(backup)
			if completedTime != nil {
				backup.Status.CompletionTimestamp = completedTime

				startTime := backup.Status.VeleroBackups[0].Status.StartTimestamp
				if startTime != nil {
					duration := completedTime.Time.Sub(startTime.Time)
					backup.Status.LastBackupDuration = getFormattedDuration(duration)

				}
			}
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

// return cumulative status of backups
func getBackupPhase(backups []*veleroapi.Backup) veleroapi.BackupPhase {

	if backups == nil || len(backups) <= 0 {
		return ""
	}

	// get all backups and check status for each
	for i := 0; i < len(backups); i++ {

		if backups[i].Status.Phase == veleroapi.BackupPhase(v1beta1.InProgressStatusPhase) {
			return backups[i].Status.Phase // some backup is not ready, show that
		}
		if backups[i].Status.Phase == veleroapi.BackupPhase(v1beta1.FailedStatusPhase) {
			return backups[i].Status.Phase // show failed first
		}
		if backups[i].Status.Phase == veleroapi.BackupPhase(v1beta1.PartiallyFailedStatusPhase) {
			return backups[i].Status.Phase // show PartiallyFailed second
		}
	}

	//if here, return the state of the first backup
	return backups[0].Status.Phase
}

// return the completion time for the last backup in this list of backups ( creds, res, cluster)
func getLastBackupCompletionTime(backup *v1beta1.Backup) *v1.Time {

	if backup.Status.VeleroBackups == nil || len(backup.Status.VeleroBackups) <= 0 {
		// no previous completed backup, can start one now
		return nil
	}

	lastBackup := backup.Status.VeleroBackups[len(backup.Status.VeleroBackups)-1]
	if lastBackup.Status.CompletionTimestamp != nil {
		return lastBackup.Status.CompletionTimestamp
	}

	return nil
}

// returns true if the interval required to wait for a backup has passed since the last backup execution
// or if there is no previous backup execution
func canStartBackup(backup *v1beta1.Backup) bool {

	if backup.Status.VeleroBackups == nil || len(backup.Status.VeleroBackups) <= 0 {
		// no previous completed backup, can start one now
		return true
	}

	var completedTime int64
	lastBackup := backup.Status.VeleroBackups[len(backup.Status.VeleroBackups)-1]

	if lastBackup.Status.CompletionTimestamp != nil {
		completedTime = lastBackup.Status.CompletionTimestamp.Time.Unix()
	}

	if completedTime < 0 {
		// completion time not set, wait for it to be set
		return false
	}

	// interval in minutes, between backups
	interval := backup.Spec.Interval
	currentTime := time.Now().Unix()

	//can run another backup if current time - completed backup time is bigger then the interval in seconds
	return currentTime-completedTime >= int64(interval*60)

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
