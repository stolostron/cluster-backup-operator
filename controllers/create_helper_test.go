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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/gomega" //nolint:staticcheck
	ocinfrav1 "github.com/openshift/api/config/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
)

const (
	acmApiVersion         = "cluster.open-cluster-management.io/v1beta1"
	veleroApiVersion      = "velero.io/v1"
	localClusterName      = "local-cluster"
	veleroBackupNameLabel = "velero.io/backup-name"
	testBackupLabelValue  = "backup-123"
)

func newUnstructured(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"namespace": namespace,
				"name":      name,
			},
		},
	}
}

func createNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func createSecret(name string, ns string,
	labels map[string]string,
	annotations map[string]string,
	data map[string][]byte,
) *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	if labels != nil {
		secret.Labels = labels
	}
	if annotations != nil {
		secret.Annotations = annotations
	}
	if data != nil {
		secret.Data = data
	}

	return secret
}

func createConfigMap(name string, ns string,
	labels map[string]string,
) *corev1.ConfigMap {
	cmap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	if labels != nil {
		cmap.Labels = labels
	}

	return cmap
}

//nolint:unparam
func createPVC(name string, ns string) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("10Gi"),
				},
			},
		},
	}
	return pvc
}

func createClusterVersion(name string, cid ocinfrav1.ClusterID,
	labels map[string]string,
) *ocinfrav1.ClusterVersion {
	clusterVersion := &ocinfrav1.ClusterVersion{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "config.openshift.io/v1",
			Kind:       "ClusterVersion",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if string(cid) != "" {
		clusterVersion.Spec.ClusterID = cid
	}
	if labels != nil {
		clusterVersion.Labels = labels
	}

	return clusterVersion
}

type BackupDeleteRequest struct {
	object *veleroapi.DeleteBackupRequest
}

func createBackupDeleteRequest(name string, ns string, backup string) *BackupDeleteRequest {
	return &BackupDeleteRequest{
		object: &veleroapi.DeleteBackupRequest{
			TypeMeta: metav1.TypeMeta{
				APIVersion: veleroApiVersion,
				Kind:       "DeleteBackupRequest",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: veleroapi.DeleteBackupRequestSpec{
				BackupName: backup,
			},
		},
	}
}

func (b *BackupDeleteRequest) errors(errs []string) *BackupDeleteRequest {
	b.object.Status.Errors = errs
	return b
}

// backup helper
type BackupHelper struct {
	object *veleroapi.Backup
}

func createBackup(name string, ns string) *BackupHelper {
	return &BackupHelper{
		object: &veleroapi.Backup{
			TypeMeta: metav1.TypeMeta{
				APIVersion: veleroApiVersion,
				Kind:       "Backup",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Status: veleroapi.BackupStatus{
				Phase:  veleroapi.BackupPhaseCompleted,
				Errors: 0,
			},
		},
	}
}

func (b *BackupHelper) orLabelSelectors(selectors []*metav1.LabelSelector) *BackupHelper {
	b.object.Spec.OrLabelSelectors = selectors
	return b
}

func (b *BackupHelper) startTimestamp(timestamp metav1.Time) *BackupHelper {
	b.object.Status.StartTimestamp = &timestamp
	return b
}

func (b *BackupHelper) phase(phase veleroapi.BackupPhase) *BackupHelper {
	b.object.Status.Phase = phase
	return b
}

func (b *BackupHelper) errors(error int) *BackupHelper {
	b.object.Status.Errors = error
	return b
}

func (b *BackupHelper) includedResources(resources []string) *BackupHelper {
	b.object.Spec.IncludedResources = resources
	return b
}

func (b *BackupHelper) excludedResources(resources []string) *BackupHelper {
	b.object.Spec.ExcludedResources = resources
	return b
}

func (b *BackupHelper) excludedNamespaces(nspaces []string) *BackupHelper {
	b.object.Spec.ExcludedNamespaces = nspaces
	return b
}

func (b *BackupHelper) labels(list map[string]string) *BackupHelper {
	b.object.Labels = list
	return b
}

// velero schedule helper
type ScheduleHelper struct {
	object *veleroapi.Schedule
}

func createSchedule(name string, ns string) *ScheduleHelper {
	return &ScheduleHelper{
		object: &veleroapi.Schedule{
			TypeMeta: metav1.TypeMeta{
				APIVersion: veleroApiVersion,
				Kind:       "Schedule",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		},
	}
}

func (b *ScheduleHelper) scheduleLabels(labels map[string]string) *ScheduleHelper {
	b.object.Labels = labels
	return b
}

func (b *ScheduleHelper) schedule(cronjob string) *ScheduleHelper {
	b.object.Spec.Schedule = cronjob
	return b
}

func (b *ScheduleHelper) phase(ph veleroapi.SchedulePhase) *ScheduleHelper {
	b.object.Status.Phase = ph
	return b
}

func (b *ScheduleHelper) ttl(duration metav1.Duration) *ScheduleHelper {
	b.object.Spec.Template.TTL = duration
	return b
}

// velero restore
type RestoreHelper struct {
	object *veleroapi.Restore
}

func createRestore(name string, ns string) *RestoreHelper {
	return &RestoreHelper{
		object: &veleroapi.Restore{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "velero/v1",
				Kind:       "Restore",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		},
	}
}

func (b *RestoreHelper) labelSelector(selector *metav1.LabelSelector) *RestoreHelper {
	b.object.Spec.LabelSelector = selector
	return b
}

func (b *RestoreHelper) orLabelSelector(selectors []*metav1.LabelSelector) *RestoreHelper {
	b.object.Spec.OrLabelSelectors = selectors
	return b
}

func (b *RestoreHelper) backupName(name string) *RestoreHelper {
	b.object.Spec.BackupName = name
	return b
}

func (b *RestoreHelper) scheduleName(name string) *RestoreHelper {
	b.object.Spec.ScheduleName = name
	return b
}

func (b *RestoreHelper) phase(phase veleroapi.RestorePhase) *RestoreHelper {
	b.object.Status.Phase = phase
	return b
}

func (b *RestoreHelper) setDeleteTimestamp(deletionTimestamp metav1.Time) *RestoreHelper {
	b.object.SetDeletionTimestamp(&deletionTimestamp)
	return b
}

func (b *RestoreHelper) setFinalizer(values []string) *RestoreHelper {
	b.object.SetFinalizers(values)
	return b
}

// acm restore
type ACMRestoreHelper struct {
	object *v1beta1.Restore
}

func createACMRestore(name string, ns string) *ACMRestoreHelper {
	return &ACMRestoreHelper{
		object: &v1beta1.Restore{
			TypeMeta: metav1.TypeMeta{
				APIVersion: acmApiVersion,
				Kind:       "Restore",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		},
	}
}

func (b *ACMRestoreHelper) setFinalizer(values []string) *ACMRestoreHelper {
	b.object.SetFinalizers(values)
	return b
}

func (b *ACMRestoreHelper) cleanupBeforeRestore(cleanup v1beta1.CleanupType) *ACMRestoreHelper {
	b.object.Spec.CleanupBeforeRestore = cleanup
	return b
}

func (b *ACMRestoreHelper) syncRestoreWithNewBackups(syncb bool) *ACMRestoreHelper {
	b.object.Spec.SyncRestoreWithNewBackups = syncb
	return b
}

func (b *ACMRestoreHelper) restoreSyncInterval(dur metav1.Duration) *ACMRestoreHelper {
	b.object.Spec.RestoreSyncInterval = dur
	return b
}

func (b *ACMRestoreHelper) veleroManagedClustersBackupName(name string) *ACMRestoreHelper {
	b.object.Spec.VeleroManagedClustersBackupName = &name
	return b
}

func (b *ACMRestoreHelper) veleroCredentialsBackupName(name string) *ACMRestoreHelper {
	b.object.Spec.VeleroCredentialsBackupName = &name
	return b
}

func (b *ACMRestoreHelper) veleroResourcesBackupName(name string) *ACMRestoreHelper {
	b.object.Spec.VeleroResourcesBackupName = &name
	return b
}

func (b *ACMRestoreHelper) phase(phase v1beta1.RestorePhase) *ACMRestoreHelper {
	b.object.Status.Phase = phase
	return b
}

func (b *ACMRestoreHelper) veleroCredentialsRestoreName(name string) *ACMRestoreHelper {
	b.object.Status.VeleroCredentialsRestoreName = name
	return b
}

func (b *ACMRestoreHelper) veleroResourcesRestoreName(name string) *ACMRestoreHelper {
	b.object.Status.VeleroResourcesRestoreName = name
	return b
}

func (b *ACMRestoreHelper) veleroGenericResourcesRestoreName(name string) *ACMRestoreHelper {
	b.object.Status.VeleroGenericResourcesRestoreName = name
	return b
}

func (b *ACMRestoreHelper) veleroManagedClustersRestoreName(name string) *ACMRestoreHelper {
	b.object.Status.VeleroManagedClustersRestoreName = name
	return b
}

func (b *ACMRestoreHelper) preserveNodePorts(preserve bool) *ACMRestoreHelper {
	b.object.Spec.PreserveNodePorts = &preserve
	return b
}

func (b *ACMRestoreHelper) restoreLabelSelector(selector *metav1.LabelSelector) *ACMRestoreHelper {
	b.object.Spec.LabelSelector = selector
	return b
}

func (b *ACMRestoreHelper) restoreORLabelSelector(selectors []*metav1.LabelSelector) *ACMRestoreHelper {
	b.object.Spec.OrLabelSelectors = selectors
	return b
}

func (b *ACMRestoreHelper) restorePVs(restorePV bool) *ACMRestoreHelper {
	b.object.Spec.RestorePVs = &restorePV
	return b
}

func (b *ACMRestoreHelper) restoreStatus(stat *veleroapi.RestoreStatusSpec) *ACMRestoreHelper {
	b.object.Spec.RestoreStatus = stat
	return b
}

func (b *ACMRestoreHelper) restoreACMStatus(stat v1beta1.RestoreStatus) *ACMRestoreHelper {
	b.object.Status = stat
	return b
}

func (b *ACMRestoreHelper) hookResources(res []veleroapi.RestoreResourceHookSpec) *ACMRestoreHelper {
	b.object.Spec.Hooks.Resources = append(b.object.Spec.Hooks.Resources,
		res...)
	return b
}

func (b *ACMRestoreHelper) excludedResources(resources []string) *ACMRestoreHelper {
	b.object.Spec.ExcludedResources = resources
	return b
}

func (b *ACMRestoreHelper) includedResources(resources []string) *ACMRestoreHelper {
	b.object.Spec.IncludedResources = resources
	return b
}

func (b *ACMRestoreHelper) excludedNamespaces(nspaces []string) *ACMRestoreHelper {
	b.object.Spec.ExcludedNamespaces = nspaces
	return b
}

func (b *ACMRestoreHelper) includedNamespaces(nspaces []string) *ACMRestoreHelper {
	b.object.Spec.IncludedNamespaces = nspaces
	return b
}

func (b *ACMRestoreHelper) namespaceMapping(nspaces map[string]string) *ACMRestoreHelper {
	b.object.Spec.NamespaceMapping = nspaces
	return b
}

// backup schedule
type BackupScheduleHelper struct {
	object *v1beta1.BackupSchedule
}

func createBackupSchedule(name string, ns string) *BackupScheduleHelper {
	return &BackupScheduleHelper{
		object: &v1beta1.BackupSchedule{
			TypeMeta: metav1.TypeMeta{
				APIVersion: acmApiVersion,
				Kind:       "BackupSchedule",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		},
	}
}

func (b *BackupScheduleHelper) veleroTTL(ttl metav1.Duration) *BackupScheduleHelper {
	b.object.Spec.VeleroTTL = ttl
	return b
}

func (b *BackupScheduleHelper) paused(isPaused bool) *BackupScheduleHelper {
	b.object.Spec.Paused = isPaused
	return b
}

func (b *BackupScheduleHelper) schedule(sch string) *BackupScheduleHelper {
	b.object.Spec.VeleroSchedule = sch
	return b
}

func (b *BackupScheduleHelper) useManagedServiceAccount(usemsa bool) *BackupScheduleHelper {
	b.object.Spec.UseManagedServiceAccount = usemsa
	return b
}

func (b *BackupScheduleHelper) phase(ph v1beta1.SchedulePhase) *BackupScheduleHelper {
	b.object.Status.Phase = ph
	return b
}

func (b *BackupScheduleHelper) scheduleStatus(scheduleType ResourceType, sch veleroapi.Schedule) *BackupScheduleHelper {
	if scheduleType == Credentials {
		b.object.Status.VeleroScheduleCredentials = sch.DeepCopy()
	}
	if scheduleType == ManagedClusters {
		b.object.Status.VeleroScheduleManagedClusters = sch.DeepCopy()
	}
	if scheduleType == Resources {
		b.object.Status.VeleroScheduleResources = sch.DeepCopy()
	}

	return b
}

func (b *BackupScheduleHelper) noBackupOnStart(stopBackupOnStart bool) *BackupScheduleHelper {
	b.object.Spec.NoBackupOnStart = stopBackupOnStart
	return b
}

func (b *BackupScheduleHelper) managedServiceAccountTTL(ttl metav1.Duration) *BackupScheduleHelper {
	b.object.Spec.ManagedServiceAccountTTL = ttl
	return b
}

func (b *BackupScheduleHelper) volumeSnapshotLocations(locations []string) *BackupScheduleHelper {
	b.object.Spec.VolumeSnapshotLocations = locations
	return b
}

func (b *BackupScheduleHelper) useOwnerReferencesInBackup(useOwnerRefs bool) *BackupScheduleHelper {
	b.object.Spec.UseOwnerReferencesInBackup = useOwnerRefs
	return b
}

func (b *BackupScheduleHelper) skipImmediately(skip bool) *BackupScheduleHelper {
	b.object.Spec.SkipImmediately = skip
	return b
}

// storage location
type StorageLocationHelper struct {
	object *veleroapi.BackupStorageLocation
}

func createStorageLocation(name string, ns string) *StorageLocationHelper {
	return &StorageLocationHelper{
		object: &veleroapi.BackupStorageLocation{
			TypeMeta: metav1.TypeMeta{
				APIVersion: veleroApiVersion,
				Kind:       "BackupStorageLocation",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: veleroapi.BackupStorageLocationSpec{
				AccessMode: "ReadWrite",
				StorageType: veleroapi.StorageType{
					ObjectStorage: &veleroapi.ObjectStorageLocation{
						Bucket: "velero-backup-acm-dr",
						Prefix: "velero",
					},
				},
				Provider: "aws",
			},
		},
	}
}

func (b *StorageLocationHelper) phase(phase veleroapi.BackupStorageLocationPhase) *StorageLocationHelper {
	b.object.Status.Phase = phase
	return b
}

func (b *StorageLocationHelper) setOwner() *StorageLocationHelper {
	b.object.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "oadp.openshift.io/v1alpha1",
			Kind:       "Velero",
			Name:       "velero-instance",
			UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
		},
	}
	return b
}

// managed cluster helper
type ManagedHelper struct {
	object *clusterv1.ManagedCluster
}

func createManagedCluster(name string, isLocalCluster bool) *ManagedHelper {
	mgdCluster := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.open-cluster-management.io/v1",
			Kind:       "ManagedCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient: true,
		},
	}

	if isLocalCluster {
		// Add local-cluster label
		mgdCluster.Labels = map[string]string{
			localClusterName: "true",
		}
	}

	return &ManagedHelper{mgdCluster}
}

func (b *ManagedHelper) clusterUrl(curl string) *ManagedHelper {
	b.object.Spec.ManagedClusterClientConfigs = []clusterv1.ClientConfig{
		{
			URL: curl,
		},
	}
	return b
}

func (b *ManagedHelper) emptyClusterUrl() *ManagedHelper {
	b.object.Spec.ManagedClusterClientConfigs = []clusterv1.ClientConfig{
		{},
	}
	return b
}

func (b *ManagedHelper) conditions(conditions []metav1.Condition) *ManagedHelper {
	b.object.Status.Conditions = conditions
	return b
}

// channel helper
type ChannelHelper struct {
	object *chnv1.Channel
}

func createChannel(name string, ctype chnv1.ChannelType, path string) *ChannelHelper {
	return &ChannelHelper{
		object: &chnv1.Channel{
			TypeMeta: metav1.TypeMeta{
				APIVersion: acmApiVersion,
				Kind:       "Channel",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: chnv1.ChannelSpec{
				Type:     ctype,
				Pathname: path,
			},
		},
	}
}

func (b *ChannelHelper) channelLabels(labels map[string]string) *ChannelHelper {
	b.object.Labels = labels
	return b
}

func (b *ChannelHelper) channelFinalizers(finalizers []string) *ChannelHelper {
	b.object.Finalizers = finalizers
	return b
}

// Factory functions for restore controller test data

// createDefaultBackupNames creates standard backup names for restore testing with specified timestamps
func createDefaultBackupNames(timestamp, genericTimestamp string) (string, string, string, string, string, string) {
	return fmt.Sprintf("acm-managed-clusters-schedule-%s", timestamp),
		fmt.Sprintf("acm-resources-schedule-%s", timestamp),
		fmt.Sprintf("acm-resources-generic-schedule-%s", genericTimestamp),
		fmt.Sprintf("acm-credentials-schedule-%s", timestamp),
		fmt.Sprintf("acm-credentials-hive-schedule-%s", timestamp),
		fmt.Sprintf("acm-credentials-cluster-schedule-%s", timestamp)
}

// createDefaultTimestamps creates standard timestamp objects for restore testing with specified timestamp strings
func createDefaultTimestamps(
	resourcesTime, resourcesGenericTime, unrelatedResourcesGenericTime string,
) (metav1.Time, metav1.Time, metav1.Time) {
	resourcesTimestamp, _ := time.Parse("20060102150405", resourcesTime)
	resourcesGenericTimestamp, _ := time.Parse("20060102150405", resourcesGenericTime)
	unrelatedResourcesGenericTimestamp, _ := time.Parse("20060102150405", unrelatedResourcesGenericTime)

	return metav1.NewTime(resourcesTimestamp),
		metav1.NewTime(resourcesGenericTimestamp),
		metav1.NewTime(unrelatedResourcesGenericTimestamp)
}

// createDefaultClusterVersions creates standard cluster version test data
func createDefaultClusterVersions() []ocinfrav1.ClusterVersion {
	return []ocinfrav1.ClusterVersion{
		*createClusterVersion("version-new-one", "aaa", map[string]string{
			veleroBackupNameLabel: testBackupLabelValue,
		}),
	}
}

// createDefaultChannels creates standard channel test data
func createDefaultChannels() []chnv1.Channel {
	return []chnv1.Channel{
		*createChannel("channel-from-backup",
			chnv1.ChannelTypeHelmRepo, "http://test.svc.cluster.local:3000/charts").
			channelLabels(map[string]string{
				veleroBackupNameLabel: testBackupLabelValue,
			}).object,
		*createChannel("channel-from-backup-with-finalizers",
			chnv1.ChannelTypeHelmRepo, "http://test.svc.cluster.local:3000/charts").
			channelLabels(map[string]string{
				veleroBackupNameLabel: testBackupLabelValue,
			}).
			channelFinalizers([]string{"finalizer1"}).object,
		*createChannel("channel-not-from-backup",
			chnv1.ChannelTypeGit, "https://github.com/test/app-samples").object,
		// Add charts-v1 channel to test the backup exclusion logic
		*createChannel("charts-v1",
			chnv1.ChannelTypeHelmRepo, "http://charts.svc.cluster.local:3000/charts").object,
	}
}

// VeleroBackupConfig holds configuration for creating default Velero backups
type VeleroBackupConfig struct {
	VeleroNamespace                    string
	ManagedClustersBackupName          string
	ResourcesBackupName                string
	ResourcesGenericBackupName         string
	CredentialsBackupName              string
	CredentialsHiveBackupName          string
	CredentialsClusterBackupName       string
	ResourcesStartTime                 metav1.Time
	ResourcesGenericStartTime          metav1.Time
	UnrelatedResourcesGenericStartTime metav1.Time
	IncludedResources                  []string
}

// createDefaultVeleroBackups creates standard velero backup test data
func createDefaultVeleroBackups(config VeleroBackupConfig) []veleroapi.Backup {
	return []veleroapi.Backup{
		*createBackup(config.ManagedClustersBackupName, config.VeleroNamespace).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(backupManagedClusterResources).
			object,
		*createBackup(config.ResourcesBackupName, config.VeleroNamespace).
			startTimestamp(config.ResourcesStartTime).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(config.IncludedResources).
			object,
		*createBackup(config.ResourcesGenericBackupName, config.VeleroNamespace).
			startTimestamp(config.ResourcesGenericStartTime).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(config.IncludedResources).
			object,
		*createBackup("acm-resources-generic-schedule-20210910181420", config.VeleroNamespace).
			startTimestamp(config.UnrelatedResourcesGenericStartTime).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(config.IncludedResources).
			object,
		*createBackup(config.CredentialsBackupName, config.VeleroNamespace).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(backupCredsResources).
			object,
		*createBackup(config.CredentialsHiveBackupName, config.VeleroNamespace).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(backupCredsResources).
			object,
		*createBackup(config.CredentialsClusterBackupName, config.VeleroNamespace).
			phase(veleroapi.BackupPhaseCompleted).
			errors(0).includedResources(backupCredsResources).
			object,
	}
}

// createDefaultLabelSelectors creates standard label selector test data
func createDefaultLabelSelectors() ([]metav1.LabelSelectorRequirement, []*metav1.LabelSelector) {
	req1 := metav1.LabelSelectorRequirement{
		Key:      "foo",
		Operator: metav1.LabelSelectorOperator("In"),
		Values:   []string{"bar"},
	}
	req2 := metav1.LabelSelectorRequirement{
		Key:      "foo2",
		Operator: metav1.LabelSelectorOperator("NotIn"),
		Values:   []string{"bar2"},
	}

	matchExpressions := []metav1.LabelSelectorRequirement{req1, req2}

	restoreOrSelector := []*metav1.LabelSelector{
		{
			MatchLabels: map[string]string{
				"restore-test-1": "restore-test-1-value",
			},
		},
		{
			MatchLabels: map[string]string{
				"restore-test-2": "restore-test-2-value",
			},
		},
	}

	return matchExpressions, restoreOrSelector
}

// ACMRestoreConfig holds configuration for creating default ACM restores
type ACMRestoreConfig struct {
	RestoreName                     string
	VeleroNamespace                 string
	ManagedClustersBackupName       string
	CredentialsBackupName           string
	ResourcesBackupName             string
	MatchExpressions                []metav1.LabelSelectorRequirement
	RestoreOrSelector               []*metav1.LabelSelector
	ExcludedResources               []string
	IncludedResources               []string
	ExcludedNamespaces              []string
	IncludedNamespaces              []string
	NamespaceMapping                map[string]string
	RestoreLabelSelectorMatchLabels map[string]string
}

// createDefaultACMRestore creates a fully configured ACM restore for testing with customizable resource filters
func createDefaultACMRestore(config ACMRestoreConfig) *v1beta1.Restore {
	return createACMRestore(config.RestoreName, config.VeleroNamespace).
		cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
		syncRestoreWithNewBackups(true).
		restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
		veleroManagedClustersBackupName(config.ManagedClustersBackupName).
		veleroCredentialsBackupName(config.CredentialsBackupName).
		restorePVs(true).
		preserveNodePorts(true).
		restoreStatus(&veleroapi.RestoreStatusSpec{
			IncludedResources: []string{"webhook"},
		}).
		hookResources([]veleroapi.RestoreResourceHookSpec{
			{Name: "hookName"},
		}).
		excludedResources(config.ExcludedResources).
		includedResources(config.IncludedResources).
		excludedNamespaces(config.ExcludedNamespaces).
		namespaceMapping(config.NamespaceMapping).
		includedNamespaces(config.IncludedNamespaces).
		restoreLabelSelector(&metav1.LabelSelector{
			MatchLabels:      config.RestoreLabelSelectorMatchLabels,
			MatchExpressions: config.MatchExpressions,
		}).
		restoreORLabelSelector(config.RestoreOrSelector).
		veleroResourcesBackupName(config.ResourcesBackupName).object
}

// Test Helper Functions for Common Patterns
//
// These functions extract common Eventually/Consistently patterns used across
// multiple test cases to reduce code duplication and improve maintainability.

// waitForRestorePhase waits for a restore to reach the specified phase
func waitForRestorePhase(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
	expectedPhase v1beta1.RestorePhase,
	timeout, interval time.Duration,
) {
	Eventually(func() v1beta1.RestorePhase {
		restore := v1beta1.Restore{}
		restoreLookupKey := types.NamespacedName{
			Name:      restoreName,
			Namespace: namespace,
		}
		err := k8sClient.Get(ctx, restoreLookupKey, &restore)
		if err != nil {
			return ""
		}
		return restore.Status.Phase
	}, timeout, interval).Should(BeEquivalentTo(expectedPhase))
}

// waitForVeleroRestoreCount waits for the specified number of velero restores in namespace
func waitForVeleroRestoreCount(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	expectedCount int,
	timeout, interval time.Duration,
) {
	veleroRestores := veleroapi.RestoreList{}
	Eventually(func() int {
		if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(namespace)); err != nil {
			return -1
		}
		return len(veleroRestores.Items)
	}, timeout, interval).Should(Equal(expectedCount))
}

// getRestoreWithRetry gets a restore resource with retry logic
func getRestoreWithRetry(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
	timeout, interval time.Duration,
) *v1beta1.Restore {
	restore := &v1beta1.Restore{}
	Eventually(func() error {
		restoreLookupKey := types.NamespacedName{
			Name:      restoreName,
			Namespace: namespace,
		}
		return k8sClient.Get(ctx, restoreLookupKey, restore)
	}, timeout, interval).Should(Succeed())
	return restore
}

// waitForRestoreStatusFieldEmpty waits for a specific restore status field to be empty
func waitForRestoreStatusFieldEmpty(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
	fieldExtractor func(*v1beta1.Restore) string,
	timeout, interval time.Duration,
) {
	Eventually(func() string {
		restore := v1beta1.Restore{}
		restoreLookupKey := types.NamespacedName{
			Name:      restoreName,
			Namespace: namespace,
		}
		err := k8sClient.Get(ctx, restoreLookupKey, &restore)
		if err != nil {
			return err.Error()
		}
		return fieldExtractor(&restore)
	}, timeout, interval).Should(BeEmpty())
}

// waitForCompletionTimestamp waits for restore completion timestamp to be set
func waitForCompletionTimestamp(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
	timeout, interval time.Duration,
) {
	Eventually(func() *metav1.Time {
		restore := v1beta1.Restore{}
		restoreLookupKey := types.NamespacedName{
			Name:      restoreName,
			Namespace: namespace,
		}
		err := k8sClient.Get(ctx, restoreLookupKey, &restore)
		if err != nil {
			return nil
		}
		return restore.Status.CompletionTimestamp
	}, timeout, interval).ShouldNot(BeNil())
}

// createLookupKey creates a NamespacedName for any resource lookup
func createLookupKey(name, namespace string) types.NamespacedName {
	return types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
}

// waitForRestoreStatusFieldNonEmpty waits for a specific restore status field to become non-empty
func waitForRestoreStatusFieldNonEmpty(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
	fieldExtractor func(*v1beta1.Restore) string,
	timeout, interval time.Duration,
) {
	Eventually(func() string {
		restore := v1beta1.Restore{}
		restoreLookupKey := types.NamespacedName{
			Name:      restoreName,
			Namespace: namespace,
		}
		err := k8sClient.Get(ctx, restoreLookupKey, &restore)
		if err != nil {
			return ""
		}
		return fieldExtractor(&restore)
	}, timeout, interval).ShouldNot(BeEmpty())
}

// waitForRestoreStatusFieldValue waits for a restore status field to equal a specific value
func waitForRestoreStatusFieldValue(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
	fieldExtractor func(*v1beta1.Restore) string,
	expectedValue string,
	timeout, interval time.Duration,
) {
	Eventually(func() string {
		restore := v1beta1.Restore{}
		restoreLookupKey := types.NamespacedName{
			Name:      restoreName,
			Namespace: namespace,
		}
		err := k8sClient.Get(ctx, restoreLookupKey, &restore)
		if err != nil {
			return ""
		}
		return fieldExtractor(&restore)
	}, timeout, interval).Should(BeIdenticalTo(expectedValue))
}

// updateVeleroRestoreStatusToCompleted updates all velero restores in the list to completed status
func updateVeleroRestoreStatusToCompleted(
	ctx context.Context,
	k8sClient client.Client,
	restoreNames []string,
	namespace string,
	timeout, interval time.Duration,
) {
	Eventually(func() error {
		for _, restoreName := range restoreNames {
			veleroRestore := &veleroapi.Restore{}
			err := k8sClient.Get(ctx, createLookupKey(restoreName, namespace), veleroRestore)
			if err != nil {
				return err
			}
			if veleroRestore.Status.Phase != veleroapi.RestorePhaseCompleted {
				veleroRestore.Status.Phase = veleroapi.RestorePhaseCompleted
				err = k8sClient.Update(ctx, veleroRestore)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}, timeout, interval).Should(Succeed())
}

// verifyVeleroRestoreExists verifies that a velero restore resource exists
func verifyVeleroRestoreExists(
	ctx context.Context,
	k8sClient client.Client,
	restoreName, namespace string,
) {
	veleroRestore := veleroapi.Restore{}
	Expect(k8sClient.Get(ctx, createLookupKey(restoreName, namespace), &veleroRestore)).ShouldNot(HaveOccurred())
}

// createAndVerifyResources creates a slice of resources and verifies creation success
func createAndVerifyResources(ctx context.Context, k8sClient client.Client, resources []client.Object) {
	for i := range resources {
		Expect(k8sClient.Create(ctx, resources[i])).Should(Succeed())
	}
}

// ChannelUnstructuredHelper creates unstructured Channel objects for testing
func createChannelUnstructured(name, namespace string) *unstructured.Unstructured {
	channel := &unstructured.Unstructured{}
	channel.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})
	return channel
}

func withBackupLabel(obj *unstructured.Unstructured, backupName string) *unstructured.Unstructured {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[BackupNameVeleroLabel] = backupName
	obj.SetLabels(labels)
	return obj
}

func withFinalizers(obj *unstructured.Unstructured, finalizers []string) *unstructured.Unstructured {
	obj.SetFinalizers(finalizers)
	return obj
}

func withGenericLabel(obj *unstructured.Unstructured) *unstructured.Unstructured {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[backupCredsClusterLabel] = "i-am-a-generic-resource"
	obj.SetLabels(labels)
	return obj
}

// ManagedClusterUnstructuredHelper creates unstructured ManagedCluster objects for testing
func createManagedClusterUnstructured(name, namespace string) *unstructured.Unstructured {
	cluster := &unstructured.Unstructured{}
	cluster.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "cluster.open-cluster-management.io/v1beta1",
		"kind":       "ManagedCluster",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	})
	return cluster
}

// MSAUnstructuredHelper creates unstructured ManagedServiceAccount objects for testing
func createMSAUnstructured(name, namespace string) *unstructured.Unstructured {
	msa := &unstructured.Unstructured{}
	msa.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				msa_label: msa_service_name,
			},
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
	})
	return msa
}

// TestSchemeSetup creates a common scheme with all necessary APIs for testing
func setupTestScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	if err := veleroapi.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := chnv1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return scheme, nil
}

// TestBackupSetup creates common backup objects for testing
type TestBackupSetup struct {
	ResourcesBackup               veleroapi.Backup
	GenericBackup                 veleroapi.Backup
	GenericBackupOld              veleroapi.Backup
	ClustersBackup                veleroapi.Backup
	ClustersBackupOld             veleroapi.Backup
	NamespaceName                 string
	ClusterNamespace              string
	CurrentTime                   string
	TenHourAgoTime                string
	AFewSecondsAgoTime            string
	VeleroResourcesBackupName     string
	VeleroGenericBackupName       string
	VeleroGenericBackupNameOlder  string
	VeleroClustersBackupName      string
	VeleroClustersBackupNameOlder string
}

//nolint:funlen
func createTestBackupSetup() *TestBackupSetup {
	namespaceName := "open-cluster-management-backup"
	clusterNamespace := "managed1"

	timeNow, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")
	rightNow := metav1.NewTime(timeNow)
	tenHourAgo := rightNow.Add(-10 * time.Hour)
	aFewSecondsAgo := rightNow.Add(-2 * time.Second)

	currentTime := rightNow.Format("20060102150405")
	tenHourAgoTime := tenHourAgo.Format("20060102150405")
	aFewSecondsAgoTime := aFewSecondsAgo.Format("20060102150405")

	veleroResourcesBackupName := veleroScheduleNames[Resources] + "-" + currentTime
	veleroGenericBackupName := veleroScheduleNames[ResourcesGeneric] + "-" + aFewSecondsAgoTime
	veleroGenericBackupNameOlder := veleroScheduleNames[ResourcesGeneric] + "-" + tenHourAgoTime
	veleroClustersBackupName := veleroScheduleNames[ManagedClusters] + "-" + aFewSecondsAgoTime
	veleroClustersBackupNameOlder := veleroScheduleNames[ManagedClusters] + "-" + tenHourAgoTime

	resources := []string{
		"crd-not-found.apps.open-cluster-management.io",
		"channel.apps.open-cluster-management.io",
	}
	resources = append(resources, backupResources...)

	return &TestBackupSetup{
		ResourcesBackup: *createBackup(veleroResourcesBackupName, namespaceName).
			includedResources(resources).
			startTimestamp(rightNow).
			excludedNamespaces([]string{localClusterName, "open-cluster-management-backup"}).
			labels(map[string]string{BackupScheduleTypeLabel: string(Resources)}).
			phase(veleroapi.BackupPhaseCompleted).object,

		GenericBackup: *createBackup(veleroGenericBackupName, namespaceName).
			excludedResources(backupManagedClusterResources).
			startTimestamp(metav1.NewTime(aFewSecondsAgo)).
			labels(map[string]string{BackupScheduleTypeLabel: string(ResourcesGeneric)}).
			phase(veleroapi.BackupPhaseCompleted).object,

		GenericBackupOld: *createBackup(veleroGenericBackupNameOlder, namespaceName).
			excludedResources(backupManagedClusterResources).
			startTimestamp(metav1.NewTime(tenHourAgo)).
			labels(map[string]string{BackupScheduleTypeLabel: string(ResourcesGeneric)}).object,

		ClustersBackup: *createBackup(veleroClustersBackupName, namespaceName).
			includedResources(backupManagedClusterResources).
			excludedNamespaces([]string{localClusterName}).
			startTimestamp(metav1.NewTime(aFewSecondsAgo)).
			labels(map[string]string{BackupScheduleTypeLabel: string(ManagedClusters)}).
			phase(veleroapi.BackupPhaseCompleted).object,

		ClustersBackupOld: *createBackup(veleroClustersBackupNameOlder, namespaceName).
			includedResources(backupManagedClusterResources).
			excludedNamespaces([]string{localClusterName}).
			startTimestamp(metav1.NewTime(tenHourAgo)).
			labels(map[string]string{BackupScheduleTypeLabel: string(ManagedClusters)}).object,

		NamespaceName:                 namespaceName,
		ClusterNamespace:              clusterNamespace,
		CurrentTime:                   currentTime,
		TenHourAgoTime:                tenHourAgoTime,
		AFewSecondsAgoTime:            aFewSecondsAgoTime,
		VeleroResourcesBackupName:     veleroResourcesBackupName,
		VeleroGenericBackupName:       veleroGenericBackupName,
		VeleroGenericBackupNameOlder:  veleroGenericBackupNameOlder,
		VeleroClustersBackupName:      veleroClustersBackupName,
		VeleroClustersBackupNameOlder: veleroClustersBackupNameOlder,
	}
}

// Test helper functions to reduce repetitive patterns

// AssertNoError is a helper to reduce repetitive error checking in tests
func AssertNoError(t *testing.T, err error, message string) {
	if err != nil {
		t.Fatalf("%s: %s", message, err.Error())
	}
}

// CreateTestClient creates a fake client with common scheme setup
func CreateTestClient(objects ...client.Object) (client.Client, error) {
	scheme, err := setupTestScheme()
	if err != nil {
		return nil, err
	}

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build(), nil
}

// CreateTestClientOrFail creates a test client and fails the test if there's an error
func CreateTestClientOrFail(t *testing.T, objects ...client.Object) client.Client {
	client, err := CreateTestClient(objects...)
	AssertNoError(t, err, "Error creating test client")
	return client
}

// Schedule Test Helper Functions

// ScheduleTestScheme creates a scheme with all APIs needed for schedule tests
func createScheduleTestScheme() *runtime.Scheme {
	testScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(testScheme)
	_ = veleroapi.AddToScheme(testScheme)
	_ = v1beta1.AddToScheme(testScheme)
	_ = ocinfrav1.AddToScheme(testScheme)
	return testScheme
}

// CreateScheduleTestClient creates a fake client with schedule test scheme and objects
func CreateScheduleTestClient(objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(createScheduleTestScheme()).
		WithObjects(objects...).
		Build()
}

// CreateScheduleTestClientWithScheme creates a fake client with conditional scheme setup
func CreateScheduleTestClientWithScheme(setupScheme bool, objects ...client.Object) client.Client {
	var testScheme *runtime.Scheme
	if setupScheme {
		testScheme = createScheduleTestScheme()
	} else {
		testScheme = runtime.NewScheme()
	}

	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objects...).
		Build()
}

// CreateDeleteVeleroSchedulesTestClient creates a client for deleteVeleroSchedules tests with conditional setup
func CreateDeleteVeleroSchedulesTestClient(
	testName string,
	veleroNamespace *corev1.Namespace,
	veleroSchedules *veleroapi.ScheduleList,
) client.Client {
	testObjects := []client.Object{veleroNamespace}

	// Add schedules for specific test cases
	if testName == "velero schedules is not empty, schedules are updated" ||
		testName == "velero schedules is not empty, schedules are updated and NO CRDs found" ||
		testName == "velero schedules is not empty, schedules are NOT updated but new CRDs found" ||
		testName == "velero schedules is not empty, schedules are NOT updated but new CRDs found error on update" {

		for i := range veleroSchedules.Items {
			veleroSchedule := &veleroSchedules.Items[i]
			veleroSchedule.Namespace = veleroNamespace.Name
			testObjects = append(testObjects, veleroSchedule)
		}
	}

	return CreateScheduleTestClient(testObjects...)
}

// CreateTestBackupWithLabels creates a backup with common test labels
func CreateTestBackupWithLabels(name, namespace, clusterLabel, restoreLabel string) *veleroapi.Backup {
	return createBackup(name, namespace).
		labels(map[string]string{
			BackupScheduleClusterLabel: clusterLabel,
			RestoreClusterLabel:        restoreLabel,
		}).
		phase(veleroapi.BackupPhaseCompleted).
		object
}

// Utils Test Helper Functions

// createUtilsTestScheme creates a scheme for utils tests with conditional API registration
func createUtilsTestScheme(includeVelero, includeOCInfra, includeV1Beta1 bool) *runtime.Scheme {
	testScheme := runtime.NewScheme()

	// Always add core v1
	_ = corev1.AddToScheme(testScheme)

	if includeVelero {
		_ = veleroapi.AddToScheme(testScheme)
	}

	if includeOCInfra {
		_ = ocinfrav1.AddToScheme(testScheme)
	}

	if includeV1Beta1 {
		_ = v1beta1.AddToScheme(testScheme)
	}

	return testScheme
}

// CreateUtilsTestClient creates a fake client for utils tests with conditional scheme setup
func CreateUtilsTestClient(includeVelero, includeOCInfra, includeV1Beta1 bool, objects ...client.Object) client.Client {
	testScheme := createUtilsTestScheme(includeVelero, includeOCInfra, includeV1Beta1)

	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objects...).
		Build()
}

// CreateHubIdentificationTestClient creates a fake client specifically for hub identification tests
func CreateHubIdentificationTestClient(setupScheme bool, objects ...client.Object) client.Client {
	return CreateUtilsTestClient(false, setupScheme, false, objects...)
}

// CreateVeleroCRDTestClient creates a fake client for VeleroCRDs tests
func CreateVeleroCRDTestClient(includeVeleroScheme bool, objects ...client.Object) client.Client {
	return CreateUtilsTestClient(includeVeleroScheme, false, false, objects...)
}

// CreateBackupSchedulePausedTestClient creates a fake client for backup schedule paused tests
func CreateBackupSchedulePausedTestClient(objects ...client.Object) client.Client {
	return CreateUtilsTestClient(true, false, true, objects...)
}
