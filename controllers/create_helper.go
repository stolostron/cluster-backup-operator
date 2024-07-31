package controllers

import (
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ocinfrav1 "github.com/openshift/api/config/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
)

const acmApiVersion = "cluster.open-cluster-management.io/v1beta1"
const veleroApiVersion = "velero.io/v1"

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
	data map[string][]byte) *corev1.Secret {
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

func createMWork(name string, ns string) *workv1.ManifestWork {
	mwork := &workv1.ManifestWork{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "work.open-cluster-management.io",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}

	return mwork

}

func createConfigMap(name string, ns string,
	labels map[string]string) *corev1.ConfigMap {
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
			Resources: corev1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse("10Gi"),
				},
			},
		},
	}
	return pvc

}

func createClusterVersion(name string, cid ocinfrav1.ClusterID,
	labels map[string]string) *ocinfrav1.ClusterVersion {
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

func (b *ACMRestoreHelper) preserveNodePorts(preserve bool) *ACMRestoreHelper {
	b.object.Spec.PreserveNodePorts = &preserve
	return b
}

func (b *ACMRestoreHelper) restoreLabelSelector(selector *metav1.LabelSelector) *ACMRestoreHelper {
	b.object.Spec.LabelSelector = selector
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

func (b *BackupScheduleHelper) schedule(sch string) *BackupScheduleHelper {
	b.object.Spec.VeleroSchedule = sch
	return b
}

func (b *BackupScheduleHelper) useManagedServiceAccount(usemsa bool) *BackupScheduleHelper {
	b.object.Spec.UseManagedServiceAccount = usemsa
	return b
}

func (b *BackupScheduleHelper) managedServiceAccountTTL(ttl metav1.Duration) *BackupScheduleHelper {
	b.object.Spec.ManagedServiceAccountTTL = ttl
	return b
}

func (b *BackupScheduleHelper) phase(ph v1beta1.SchedulePhase) *BackupScheduleHelper {
	b.object.Status.Phase = ph
	return b
}

func (b *BackupScheduleHelper) noBackupOnStart(stopBackupOnStart bool) *BackupScheduleHelper {
	b.object.Spec.NoBackupOnStart = stopBackupOnStart
	return b
}

func (b *BackupScheduleHelper) setVolumeSnapshotLocation(locations []string) *BackupScheduleHelper {
	b.object.Spec.VolumeSnapshotLocations = locations
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

func createManagedCluster(name string) *ManagedHelper {
	return &ManagedHelper{
		&clusterv1.ManagedCluster{
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
		},
	}
}

func (b *ManagedHelper) clusterUrl(curl string) *ManagedHelper {
	b.object.Spec.ManagedClusterClientConfigs = []clusterv1.ClientConfig{
		clusterv1.ClientConfig{
			URL: curl,
		},
	}
	return b
}

func (b *ManagedHelper) emptyClusterUrl() *ManagedHelper {
	b.object.Spec.ManagedClusterClientConfigs = []clusterv1.ClientConfig{
		clusterv1.ClientConfig{},
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

func createChannel(name string, ns string, ctype chnv1.ChannelType, path string) *ChannelHelper {
	return &ChannelHelper{
		object: &chnv1.Channel{
			TypeMeta: metav1.TypeMeta{
				APIVersion: acmApiVersion,
				Kind:       "Channel",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
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
