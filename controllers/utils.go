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

	ocinfrav1 "github.com/openshift/api/config/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/discovery"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func findSuffix(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if strings.HasSuffix(val, item) {
			return i, true
		}
	}
	return -1, false
}

// find takes a slice and looks for an element in it. If found it will
// return it's key, otherwise it will return -1 and a bool of false.
func find(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if item == val {
			return i, true
		}
	}
	return -1, false
}

func findValue(slice []string, val string) bool {

	_, ok := find(slice, val)

	return ok
}

// append unique value to a list
func appendUnique(slice []string, value string) []string {
	// check if the NS exists
	_, ok := find(slice, value)
	if !ok {
		slice = append(slice, value)
	}
	return slice
}

// min returns the smallest of x or y.
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// SortCompare checks for equality on slices without order, returns true if they contain the same members
func sortCompare(a, b []string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}

	sort.Strings(a)
	sort.Strings(b)

	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// returns a valid name for the velero restore kubernetes resource
// by trimming the concatenated cluster restore and backup names
func getValidKsRestoreName(clusterRestoreName string, backupName string) string {
	//max name for ns or resources is 253 chars
	fullName := clusterRestoreName + "-" + backupName

	if len(fullName) > 252 {
		return fullName[:252]
	}
	return fullName
}

// Velero uses TimestampedName for backups using the follwoing format
// by setting the default backup name format based on the schedule
// fmt.Sprintf("%s-%s", s.Name, timestamp.Format("20060102150405"))
// this function parses Velero backupName and returns the timestamp
func getBackupTimestamp(backupName string) (time.Time, error) {
	timestampIndex := strings.LastIndex(backupName, "-")
	if timestampIndex != -1 {
		timestampStr := strings.Trim(backupName[timestampIndex:], "-")
		return time.Parse("20060102150405", timestampStr)
	}
	return time.Time{}, nil
}

// SortResourceType implements sort.Interface
type SortResourceType []ResourceType

func (a SortResourceType) Len() int           { return len(a) }
func (a SortResourceType) Less(i, j int) bool { return a[i] < a[j] }
func (a SortResourceType) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

// check if we have a valid storage location object
func isValidStorageLocationDefined(
	veleroStorageLocations []veleroapi.BackupStorageLocation,
	preferredNs string,
) bool {
	isValidStorageLocation := false
	for i := range veleroStorageLocations {
		if veleroStorageLocations[i].Namespace == preferredNs &&
			veleroStorageLocations[i].OwnerReferences != nil &&
			veleroStorageLocations[i].Status.Phase == veleroapi.BackupStorageLocationPhaseAvailable {
			for _, ref := range veleroStorageLocations[i].OwnerReferences {
				if ref.Kind != "" {
					isValidStorageLocation = true
					break
				}
			}
		}
		if isValidStorageLocation {
			break
		}
	}
	return isValidStorageLocation
}

// having a resourceKind.resourceGroup string, return (resourceKind, resourceGroup)
func getResourceDetails(resourceName string) (string, string) {

	indexOfName := strings.Index(resourceName, ".")
	if indexOfName > -1 {
		return resourceName[:indexOfName], resourceName[indexOfName+1:]
	}

	return resourceName, ""
}

// retrurn the set of CRDs for a potential generic resource,
// backed up by acm-resources-generic-schedule
// labeled by cluster.open-cluster-management.io/backup
func getGenericCRDFromAPIGroups(
	ctx context.Context,
	dc discovery.DiscoveryInterface,
	veleroBackup *veleroapi.Backup,
) ([]string, error) {

	resources := []string{}
	if groupList, err := dc.ServerGroups(); err == nil && groupList != nil {
		resources = processGenericCRDFromAPIGroups(ctx, dc, veleroBackup, *groupList)
	}

	return resources, nil
}

func processGenericCRDFromAPIGroups(
	ctx context.Context,
	dc discovery.DiscoveryInterface,
	veleroBackup *veleroapi.Backup,
	groupList v1.APIGroupList,
) []string {

	logger := log.FromContext(ctx)

	resources := []string{}
	for _, group := range groupList.Groups {
		for _, version := range group.Versions {
			//get all resources for each group version
			resourceList, err := dc.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				logger.Error(err, "failed to get server resources")
				continue
			}
			if resourceList == nil || group.Name == "" {
				// don't want any resource with no apigroup
				continue
			}
			for _, resource := range resourceList.APIResources {

				resourceKind := strings.ToLower(resource.Kind)
				resourceName := resourceKind + "." + group.Name

				if !findValue(veleroBackup.Spec.ExcludedResources, resourceName) &&
					!findValue(veleroBackup.Spec.ExcludedResources, resourceKind) {
					resources = appendUnique(resources, resourceName)
				}
			}
		}
	}

	return resources
}

// return hub uid, used to annotate backup schedules
// to know what hub is pushing the backups to the storage location
// info used when switching active - passive clusters
func getHubIdentification(
	ctx context.Context,
	c client.Client,
) (string, error) {

	clusterId := "unknown"
	clusterVersions := &ocinfrav1.ClusterVersionList{}
	if err := c.List(ctx, clusterVersions, &client.ListOptions{}); err != nil {
		return clusterId, err
	}

	if len(clusterVersions.Items) > 0 {
		clusterId = string(clusterVersions.Items[0].Spec.ClusterID)
	}
	return clusterId, nil
}

// returns true if this clusterName namespace has any secrets labeled with the hive
// hive.openshift.io/secret-type label
// this identifies hive clusters
func isHiveCreatedCluster(
	ctx context.Context,
	c client.Client,
	clusterName string) bool {

	nbOfSecrets := 0
	hiveSecrets := &corev1.SecretList{}
	if hiveLabel, err := labels.NewRequirement(backupCredsHiveLabel,
		selection.Exists, []string{}); err == nil {
		selector := labels.NewSelector()
		selector = selector.Add(*hiveLabel)
		if err := c.List(ctx, hiveSecrets, &client.ListOptions{
			Namespace:     clusterName,
			LabelSelector: selector}); err == nil {

			nbOfSecrets = len(hiveSecrets.Items)
		}
	}
	return nbOfSecrets > 0
}

func findValidMSAToken(
	secrets []corev1.Secret,
	currentTime time.Time) string {

	accessToken := ""

	// find MSA secrets in this namespace
	if len(secrets) == 0 {
		return accessToken
	}
	// go through MSA secrets and try to find one having a valid token
	for s := range secrets {
		secret := secrets[s]
		annotations := secret.GetAnnotations()
		if annotations == nil {
			continue
		}
		tokenExpiry := annotations["expirationTimestamp"]
		if tokenExpiry == "" {
			continue
		}
		expiryTime, err := time.Parse(time.RFC3339, tokenExpiry)
		if err != nil || expiryTime.IsZero() {
			continue
		}
		if expiryTime.After(currentTime) {
			if err = yaml.Unmarshal(secret.Data["token"], &accessToken); err == nil {
				if accessToken != "" {
					// secret has token value
					break
				}
			}
		}
	}

	return accessToken
}

// returns true if the managed cluster
// needs to be reimported
func managedClusterShouldReimport(
	ctx context.Context,
	managedClusters []clusterv1.ManagedCluster,
	clusterName string,
) (bool, string, string) {

	logger := log.FromContext(ctx)

	url := ""
	if clusterName == "local-cluster" {
		// skip local-cluster
		return false, url, ""
	}

	for i := range managedClusters {

		managedCluster := managedClusters[i]

		if clusterName != managedCluster.Name {
			// find the cluster by name
			continue
		}

		// skip available managed clusters
		isManagedClusterAvailable := false
		for _, condition := range managedCluster.Status.Conditions {
			if condition.Type == "ManagedClusterConditionAvailable" &&
				condition.Status == v1.ConditionTrue {
				isManagedClusterAvailable = true
				break
			}
		}
		if isManagedClusterAvailable {
			msg := fmt.Sprintf("managed cluster %s already available",
				managedCluster.Name)

			logger.Info(msg)
			return false, url, msg
		}

		// if empty, the managed cluster has no accessible address for the hub to connect with it
		if len(managedCluster.Spec.ManagedClusterClientConfigs) == 0 ||
			managedCluster.Spec.ManagedClusterClientConfigs[0].URL == "" {
			msg := fmt.Sprintf("Cannot reimport cluster %s, no serverUrl property",
				managedCluster.Name)

			logger.Info(msg)

			return false, url, msg
		}

		url = managedCluster.Spec.ManagedClusterClientConfigs[0].URL
		return true, url, ""
	}

	return false, url, ""
}

func VeleroCRDsPresent(
	ctx context.Context,
	c client.Client) (bool, error) {

	veleroScheduleList := veleroapi.ScheduleList{}
	veleroScheduleCRDPresent, err := isCRDPresent(ctx, c, &veleroScheduleList)
	if err != nil {
		return false, err
	}

	veleroRestoreList := veleroapi.RestoreList{}
	veleroRestoreCRDPresent, err := isCRDPresent(ctx, c, &veleroRestoreList)
	if err != nil {
		return false, err
	}

	return veleroScheduleCRDPresent && veleroRestoreCRDPresent, nil
}

func isCRDPresent(ctx context.Context, k8sClient client.Client, objList client.ObjectList) (bool, error) {
	err := k8sClient.List(ctx, objList)
	if err != nil {
		if isCRDNotPresentError(err) {
			// This api Kind is not present
			return false, nil
		}
		// Some other error querying
		return false, err
	}
	// API is present
	return true, nil
}

func isCRDNotPresentError(err error) bool {
	if apimeta.IsNoMatchError(err) || kerrors.IsNotFound(err) ||
		strings.Contains(err.Error(), "failed to get API group resources") {
		return true
	}
	return false
}

// add restore label selector requirement - like cluster activation label requirement, for credentials and generis resources restore files
// This will be added to the user defined restore filters with the following rule:
// 1. if the user defines a set of OrLabelSelectors, the req LabelSelectorRequirement will be injected
// to each OrLabelSelectors MatchExpression ( will run as an AND rule on each MatchExpression).
// 2. if the user defines a LabelSelector, the req LabelSelectorRequirement will be appeded to each of the OR MatchExpressions ( ANDed)
// 3. If the user doesn't define a LabelSelector or a OrLabelSelectors, the req Requirement will be created as a LabelSelector option
func addRestoreLabelSelector(
	restoreObj *veleroapi.Restore,
	req metav1.LabelSelectorRequirement,
) {

	if restoreObj.Spec.OrLabelSelectors != nil && len(restoreObj.Spec.OrLabelSelectors) > 0 {
		// LabelSelector and OrLabelSelectors are mutually exclusive
		// if restoreObj.Spec.OrLabelSelectors is not null, add LabelSelectors match expressions ( which are ANDed ) to each of the
		// OrLabelSelectors expressions ( which are also ANDed )
		for i := range restoreObj.Spec.OrLabelSelectors {
			restoreObj.Spec.OrLabelSelectors[i].MatchExpressions = append(restoreObj.Spec.OrLabelSelectors[i].MatchExpressions,
				req)
		}
	} else {
		// if no OrLabelSelector, add the MatchExpression to the LabelSelector
		if restoreObj.Spec.LabelSelector == nil {
			labels := &metav1.LabelSelector{}
			restoreObj.Spec.LabelSelector = labels

			requirements := make([]metav1.LabelSelectorRequirement, 0)
			restoreObj.Spec.LabelSelector.MatchExpressions = requirements
		}
		restoreObj.Spec.LabelSelector.MatchExpressions = append(
			restoreObj.Spec.LabelSelector.MatchExpressions,
			req,
		)
	}
}
