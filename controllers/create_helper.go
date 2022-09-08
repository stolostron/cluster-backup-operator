package controllers

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

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

//backup helper
type BackupHelper struct {
	object *veleroapi.Backup
}

func createBackup(name string, ns string) *BackupHelper {
	return &BackupHelper{
		object: &veleroapi.Backup{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "velero.io/v1",
				Kind:       "Backup",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: veleroapi.BackupSpec{
				IncludedNamespaces: []string{"please-keep-this-one"},
				IncludedResources:  backupManagedClusterResources,
			},
			Status: veleroapi.BackupStatus{
				Phase:  veleroapi.BackupPhaseCompleted,
				Errors: 0,
			},
		},
	}
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
				APIVersion: "cluster.open-cluster-management.io/v1beta1",
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

// backup schedule
type BackupScheduleHelper struct {
	object *v1beta1.BackupSchedule
}

func createBackupSchedule(name string, ns string) *BackupScheduleHelper {
	return &BackupScheduleHelper{
		object: &v1beta1.BackupSchedule{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "cluster.open-cluster-management.io/v1beta1",
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

// storage location
type StorageLocationHelper struct {
	object *veleroapi.BackupStorageLocation
}

func createStorageLocation(name string, ns string) *StorageLocationHelper {
	return &StorageLocationHelper{
		object: &veleroapi.BackupStorageLocation{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "velero.io/v1",
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
