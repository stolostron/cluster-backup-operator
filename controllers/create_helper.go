package controllers

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
