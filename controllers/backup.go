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
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	// BackupScheduleNameLabel is the label key used to identify the backup schedule by name.
	BackupScheduleNameLabel string = "cluster.open-cluster-management.io/backup-schedule-name"
	// BackupScheduleTypeLabel is the label key used to identify the type of the backup schedule
	BackupScheduleTypeLabel string = "cluster.open-cluster-management.io/backup-schedule-type"
	// BackupScheduleClusterUIDLabel is the label key used to identify the cluster id that generated the backup
	BackupScheduleClusterLabel string = "cluster.open-cluster-management.io/backup-cluster"
	// BackupScheduleActivationLabel stores the name of the restore resources that resulted in creating this backup
	BackupScheduleActivationLabel string = "cluster.open-cluster-management.io/backup-activation-restore"
	// label for backups generated from velero schedules
	BackupVeleroLabel string = "velero.io/schedule-name"
	// label for backups generated from velero schedules
	BackupNameVeleroLabel string = "velero.io/backup-name"

	ClusterActivationLabel string = "cluster-activation"

	ExcludeBackupLabel string = "velero.io/exclude-from-backup"
)
var (
	hiveSuffix = ".hive.openshift.io"
	// include resources from these api groups
	includedAPIGroupsSuffix = []string{
		".open-cluster-management.io",
		".hive.openshift.io",
	}
	includedAPIGroupsByName = []string{
		"argoproj.io",
		"app.k8s.io",
		"core.observatorium.io",
	}
	includedActivationAPIGroupsByName = []string{
		"agent-install.openshift.io",
	}

	// exclude resources from these api groups
	// search.open-cluster-management.io is required to be backed up
	// but since the CRs are created in the MCH NS which is excluded by the resources backup
	// we want those CRs to be labeled with cluster.open-cluster-management.io/backup
	// so they are picked up by the resources-generic backup
	excludedAPIGroups = []string{
		"internal.open-cluster-management.io",
		"operator.open-cluster-management.io",
		"work.open-cluster-management.io",
		"search.open-cluster-management.io",
		"admission.hive.openshift.io",
		"proxy.open-cluster-management.io",
		"action.open-cluster-management.io",
		"view.open-cluster-management.io",
		"clusterview.open-cluster-management.io",
		"velero.io",
	}
	// exclude these CRDs
	// they are part of the included api groups but are either not needed
	// or they are being recreated by owner resources, which are also backed up
	excludedCRDs = []string{
		"clustermanagementaddon.addon.open-cluster-management.io",
		"backupschedule.cluster.open-cluster-management.io",
		"restore.cluster.open-cluster-management.io",
		"clusterclaim.cluster.open-cluster-management.io",
		"discoveredcluster.discovery.open-cluster-management.io",
	}

	// resources used to activate the connection between hub and managed clusters - activation resources
	backupManagedClusterResources = []string{
		"clusterdeployment.hive.openshift.io",               // restore these first
		"machinepool.hive.openshift.io",                     // restore these first
		"managedcluster.cluster.open-cluster-management.io", //global
		"managedcluster.clusterview.open-cluster-management.io",
		"klusterletaddonconfig.agent.open-cluster-management.io",
		"managedclusteraddon.addon.open-cluster-management.io",
		"clusterpool.hive.openshift.io",
		"clusterclaim.hive.openshift.io",
		"clustercurator.cluster.open-cluster-management.io",
		"bmceventsubscription.metal3.io",
		"hostfirmwaresettings.metal3.io",
		"clustersync.hiveinternal.openshift.io",
		"clusterimageset.hive.openshift.io",
		"multiclusterobservability.observability.open-cluster-management.io",
	}

	// all backup resources, except secrets, configmaps and managed cluster activation resources
	// backup resources will be generated from the api groups CRDs
	// the two resources below should already be picked up by the api group selection
	// they are used here for testing purpose
	backupResources = []string{}

	backupCredsResources = []string{
		"secret",
		"configmap",
	}

	// secrets and configmaps labels
	backupCredsUserLabel    = "cluster.open-cluster-management.io/type"   // #nosec G101 -- This is a false positive
	backupCredsHiveLabel    = "hive.openshift.io/secret-type"             // hive
	backupCredsClusterLabel = "cluster.open-cluster-management.io/backup" // #nosec G101 -- This is a false positive
	policyRootLabel         = "policy.open-cluster-management.io/root-policy"
)

var (
	apiGVString = v1beta1.GroupVersion.String()
	// create credentials schedule first since this is the fastest one, followed by resources
	// mapping ResourceTypes to Velero schedule names
	veleroScheduleNames = map[ResourceType]string{
		Credentials:        "acm-credentials-schedule",
		Resources:          "acm-resources-schedule",
		ResourcesGeneric:   "acm-resources-generic-schedule",
		ManagedClusters:    "acm-managed-clusters-schedule",
		ValidationSchedule: "acm-validation-policy-schedule",
	}
)

// set all acm resources backup info
func setResourcesBackupInfo(
	ctx context.Context,
	veleroBackupTemplate *veleroapi.BackupSpec,
	resourcesToBackup []string,
	backupNS string,
	c client.Client,
) {
	var clusterResource bool = true
	veleroBackupTemplate.IncludeClusterResources = &clusterResource
	veleroBackupTemplate.ExcludedNamespaces = appendUnique(
		veleroBackupTemplate.ExcludedNamespaces,
		"local-cluster",
	)

	// exclude backup chart NS
	veleroBackupTemplate.ExcludedNamespaces = appendUnique(
		veleroBackupTemplate.ExcludedNamespaces,
		backupNS,
	)

	veleroBackupTemplate.IncludedResources = getResourcesByBackupType(
		resourcesToBackup,
		Resources,
	)

	// exclude acm channel namespaces
	channels := chnv1.ChannelList{}
	if err := c.List(ctx, &channels, &client.ListOptions{}); err == nil {
		for i := range channels.Items {
			if channels.Items[i].Name == "charts-v1" {
				veleroBackupTemplate.ExcludedNamespaces = appendUnique(
					veleroBackupTemplate.ExcludedNamespaces,
					channels.Items[i].Namespace,
				)
			}
		}
	}

	// exclude child policies
	if veleroBackupTemplate.LabelSelector == nil {
		labels := &v1.LabelSelector{}
		veleroBackupTemplate.LabelSelector = labels

		requirements := make([]v1.LabelSelectorRequirement, 0)
		veleroBackupTemplate.LabelSelector.MatchExpressions = requirements
	}
	req := &v1.LabelSelectorRequirement{}
	req.Key = policyRootLabel
	req.Operator = "DoesNotExist"
	veleroBackupTemplate.LabelSelector.MatchExpressions = append(
		veleroBackupTemplate.LabelSelector.MatchExpressions,
		*req,
	)
	// exclude resources backed up by the generic resources backup
	req = &v1.LabelSelectorRequirement{}
	req.Key = backupCredsClusterLabel
	req.Operator = "DoesNotExist"
	veleroBackupTemplate.LabelSelector.MatchExpressions = append(
		veleroBackupTemplate.LabelSelector.MatchExpressions,
		*req,
	)

}

// set generic backup info
func setGenericResourcesBackupInfo(
	veleroBackupTemplate *veleroapi.BackupSpec,
	resourcesToBackup []string,
) {

	var clusterResource bool = true // check global resources
	veleroBackupTemplate.IncludeClusterResources = &clusterResource

	veleroBackupTemplate.ExcludedResources = getResourcesByBackupType(
		resourcesToBackup,
		ResourcesGeneric,
	)

	if veleroBackupTemplate.LabelSelector == nil {
		labels := &v1.LabelSelector{}
		veleroBackupTemplate.LabelSelector = labels

		requirements := make([]v1.LabelSelectorRequirement, 0)
		veleroBackupTemplate.LabelSelector.MatchExpressions = requirements
	}
	req := &v1.LabelSelectorRequirement{}
	req.Key = backupCredsClusterLabel
	req.Operator = "Exists"
	veleroBackupTemplate.LabelSelector.MatchExpressions = append(
		veleroBackupTemplate.LabelSelector.MatchExpressions,
		*req,
	)
}

// set credentials backup info
func setCredsBackupInfo(
	veleroBackupTemplate *veleroapi.BackupSpec,
) {
	var clusterResource bool = false
	veleroBackupTemplate.IncludeClusterResources = &clusterResource

	for i := range backupCredsResources { // secrets and configmaps
		veleroBackupTemplate.IncludedResources = appendUnique(
			veleroBackupTemplate.IncludedResources,
			backupCredsResources[i],
		)
	}

	OrSelectors := []*v1.LabelSelector{}

	// hive label selector
	reqHive := &v1.LabelSelectorRequirement{}
	reqHive.Key = backupCredsHiveLabel
	reqHive.Operator = "Exists"
	OrSelectors = append(
		OrSelectors,
		&v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{*reqHive}},
	)
	// generic backup selector
	reqUser := &v1.LabelSelectorRequirement{}
	reqUser.Key = backupCredsUserLabel
	reqUser.Operator = "Exists"
	OrSelectors = append(
		OrSelectors,
		&v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{*reqUser}},
	)

	// cluster backup selector
	reqCls := &v1.LabelSelectorRequirement{}
	reqCls.Key = backupCredsClusterLabel
	reqCls.Operator = "Exists"
	OrSelectors = append(
		OrSelectors,
		&v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{*reqCls}},
	)

	veleroBackupTemplate.OrLabelSelectors = OrSelectors
}

// set managed clusters backup info
func setManagedClustersBackupInfo(
	veleroBackupTemplate *veleroapi.BackupSpec,
	resourcesToBackup []string,
) {
	var clusterResource bool = true // include cluster level resources
	veleroBackupTemplate.IncludeClusterResources = &clusterResource

	veleroBackupTemplate.ExcludedNamespaces = appendUnique(
		veleroBackupTemplate.ExcludedNamespaces,
		"local-cluster",
	)

	veleroBackupTemplate.ExcludedNamespaces = appendUnique(
		veleroBackupTemplate.ExcludedNamespaces,
		"openshift-machine-api",
	)

	veleroBackupTemplate.IncludedResources = getResourcesByBackupType(
		resourcesToBackup,
		ManagedClusters,
	)

}

// set validation backup information
// this is a dummy backup, only purpose being to verify if there are
// new backups stored since the last cron job was invoked
// the backup will delete itself using ttl = owning schedule cron job + 5 min
// So, if no other backups are scheduled later on, there will be
// no backups created from the acm-validation-policy-schedule schedule
// and the policy will alert if no acm-validation-policy-schedule are found
func setValidationBackupInfo(
	veleroBackupTemplate *veleroapi.BackupSpec,
	backupSchedule *v1beta1.BackupSchedule,
) *veleroapi.BackupSpec {

	veleroBackupTemplate.IncludedNamespaces = appendUnique(
		veleroBackupTemplate.IncludedNamespaces,
		backupSchedule.Namespace,
	)

	veleroBackupTemplate.IncludedResources = appendUnique(
		veleroBackupTemplate.IncludedResources,
		backupSchedule.Kind,
	)

	veleroBackupTemplate.TTL = getValidationBackupTTL(backupSchedule.Spec.VeleroSchedule)

	return veleroBackupTemplate
}

func getValidationBackupTTL(VeleroSchedule string) v1.Duration {
	validationTTL := v1.Duration{Duration: time.Hour * 1}

	// the backup should be deleted after ttl = schedule cron job time passed
	// adding extra 5min so that an old validation backup overlaps for a few minutes
	// with the new validation backup; doing that so that policy doesn't report an error
	// in this short interval when the old validation backup is deleted
	// and the old one is recreated by the cron job validation schedule
	if cronSchedule, err := cron.ParseStandard(VeleroSchedule); err == nil {
		currentTime := v1.Now().Time
		nextRunTime := cronSchedule.Next(currentTime)
		// add extra 5 minutes to the cron job time before deleting this backup
		secondNextRunTime := cronSchedule.Next(nextRunTime).Add(time.Minute * 5)

		validationTTL = v1.Duration{
			Duration: secondNextRunTime.Sub(nextRunTime),
		}
	}
	return validationTTL
}

// creates the list of backup resources based on backup type
func getResourcesByBackupType(
	resourcesToBackup []string,
	resourceType ResourceType,
) []string {

	filteredResourceNames := []string{}

	switch resourceType {
	case Resources:
		for i := range resourcesToBackup { // exclude hive resources
			if !strings.HasSuffix(resourcesToBackup[i], hiveSuffix) {
				filteredResourceNames = appendUnique(
					filteredResourceNames,
					resourcesToBackup[i],
				)
			}
		}
	case ResourcesGeneric:
		for i := range excludedCRDs { // exclude resources not backed up
			filteredResourceNames = appendUnique(
				filteredResourceNames,
				excludedCRDs[i],
			)
		}
		for i := range backupCredsResources { // exclude resources already backed up by creds
			filteredResourceNames = appendUnique(
				filteredResourceNames,
				backupCredsResources[i],
			)
		}
		for i := range backupManagedClusterResources { // exclude resources in managed clusters
			filteredResourceNames = appendUnique(
				filteredResourceNames,
				backupManagedClusterResources[i],
			)
		}
		for i := range resourcesToBackup { // exclude hive resources
			if strings.HasSuffix(resourcesToBackup[i], hiveSuffix) {
				filteredResourceNames = appendUnique(
					filteredResourceNames,
					resourcesToBackup[i],
				)
			}
		}
		// a temporary workaround for NS not filtered by the label selector in OADP 1.3
		filteredResourceNames = appendUnique(filteredResourceNames, "namespace")
	case ManagedClusters:
		for i := range backupManagedClusterResources {
			// managed clusters required resources, from namespace or cluster level
			filteredResourceNames = appendUnique(
				filteredResourceNames,
				backupManagedClusterResources[i],
			)
		}
		for i := range resourcesToBackup { // include hive resources
			if strings.HasSuffix(resourcesToBackup[i], hiveSuffix) {
				filteredResourceNames = appendUnique(
					filteredResourceNames,
					resourcesToBackup[i],
				)
			}
		}
	}

	return filteredResourceNames
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

// get server resources that needs backup
func getResourcesToBackup(
	ctx context.Context,
	dc discovery.DiscoveryInterface,
) []string {

	backupResourceNames := []string{}
	if groupList, err := dc.ServerGroups(); err == nil && groupList != nil {
		backupResourceNames = processResourcesToBackup(ctx, dc, *groupList)
	}

	return backupResourceNames
}

func processResourcesToBackup(
	ctx context.Context,
	dc discovery.DiscoveryInterface,
	groupList v1.APIGroupList,
) []string {
	backupLogger := log.FromContext(ctx)

	backupResourceNames := backupResources
	// build the list of excluded resources
	ignoreCRDs := excludedCRDs

	for _, group := range groupList.Groups {

		if !shouldBackupAPIGroup(group.Name) {
			// ignore excluded api groups
			continue
		}

		for _, version := range group.Versions {
			//get all resources for each group version
			resourceList, err := dc.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				backupLogger.Info(
					fmt.Sprintf("Failed to get server resources for group=%s, version=%s, error:%s",
						group.Name, version.GroupVersion,
						err.Error()),
				)
				continue
			}
			for _, resource := range resourceList.APIResources {
				resourceKind := strings.ToLower(resource.Kind)
				resourceName := resourceKind + "." + group.Name

				if findValue(includedActivationAPIGroupsByName, group.Name) &&
					!findValue(ignoreCRDs, resourceKind) &&
					!findValue(ignoreCRDs, resourceName) {
					// if resource kind is not ignored
					// and this is an activation group
					// then add the resource to the activation list
					backupManagedClusterResources = appendUnique(backupManagedClusterResources, resourceName)
				}

				// if resource kind is not ignored
				// and the resource is not in cluster activation backup group
				// then add it to the backup resources
				if !findValue(includedActivationAPIGroupsByName, group.Name) &&
					!findValue(ignoreCRDs, resourceKind) &&
					!findValue(ignoreCRDs, resourceName) &&
					!findValue(backupManagedClusterResources, resourceKind) &&
					!findValue(backupManagedClusterResources, resourceName) {
					backupResourceNames = appendUnique(backupResourceNames, resourceName)
				}
			}
		}
	}
	return backupResourceNames
}

// returns true if this api group needs to be backed up
func shouldBackupAPIGroup(groupStr string) bool {

	_, ok := find(excludedAPIGroups, groupStr)
	if ok {
		// this has to be excluded
		return false
	}

	_, ok = find(includedAPIGroupsByName, groupStr)
	// if not in the included api groups
	if !ok {
		// check if is in the activation api groups list
		_, ok = find(includedActivationAPIGroupsByName, groupStr)

	}
	// if not in the activation api groups
	if !ok {
		// check if is in the included api groups by suffix
		_, ok = findSuffix(includedAPIGroupsSuffix, groupStr)

	}

	return ok
}

// clean up expired validation backups, workaround for
// velero doesn't delete expired backups if they are in FailedValidation
// or storage location is no longer valid
func cleanupExpiredValidationBackups(
	ctx context.Context,
	veleroNS string,
	c client.Client,
) {
	backupLogger := log.FromContext(ctx)
	validationLabel := labels.SelectorFromSet(
		map[string]string{BackupVeleroLabel: veleroScheduleNames[ValidationSchedule]})
	veleroBackupList := veleroapi.BackupList{}
	if err := c.List(ctx, &veleroBackupList, &client.ListOptions{
		Namespace:     veleroNS,
		LabelSelector: validationLabel,
	}); err == nil {

		for i := range veleroBackupList.Items {
			backup := veleroBackupList.Items[i]
			if backup.Status.Expiration != nil &&
				v1.Now().Time.After(backup.Status.Expiration.Time) {
				backupLogger.Info(fmt.Sprintf("validation backup %s expired, attepmpt to delete it",
					backup.Name))
				if err = deleteBackup(ctx, &backup, c); err == nil {
					backupLogger.Info(fmt.Sprintf("Delete completed for %s",
						backup.Name))
				}
			}
		}
	}
}

// delete backup using a deletebackuprequest
func deleteBackup(
	ctx context.Context,
	backup *veleroapi.Backup,
	c client.Client,
) error {
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
	if err := c.Get(ctx, backupDeleteIdentity, veleroDeleteBackup); err != nil {

		// this is a  resource NotFound error, create the resource
		veleroDeleteBackup.Spec.BackupName = backupName
		veleroDeleteBackup.Name = backupDeleteIdentity.Name
		veleroDeleteBackup.Namespace = backupDeleteIdentity.Namespace

		backupLogger.Info(
			fmt.Sprintf("Attempt to create DeleteBackupRequest %s", veleroDeleteBackup.Name),
		)
		if err := c.Create(ctx, veleroDeleteBackup, &client.CreateOptions{}); err == nil {
			backupLogger.Info(
				fmt.Sprintf("Created DeleteBackupRequest %s", veleroDeleteBackup.Name),
			)
		}

		return nil
	}
	backupLogger.Info(
		fmt.Sprintf("DeleteBackupRequest already exists, skip request creation %s", backupName),
	)
	if veleroDeleteBackup.Status.Errors != nil {
		// delete the backup now
		if err := c.Delete(ctx, backup); err != nil {
			backupLogger.Error(
				err,
				fmt.Sprintf("failed to delete the backup %s", backupName),
			)
			return err
		}
	}

	return nil
}
