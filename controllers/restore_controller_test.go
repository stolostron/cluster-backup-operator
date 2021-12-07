package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func initNamespace(name string) corev1.Namespace {
	return corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
		},
	}
}

// a managed cluster namespace has label: cluster.open-cluster-management.io/managedCluster=<cluster name>
func initManagedClusterNamespace(name string) corev1.Namespace {
	ns := initNamespace(name)
	ns.Labels = map[string]string{"cluster.open-cluster-management.io/managedCluster": name}
	return ns

}

func updateVeleroRestoreStatus(
	name, namespace string,
	status veleroapi.RestorePhase,
	timeout, interval time.Duration,
) {
	createdVeleroRestore := &veleroapi.Restore{}
	Eventually(func() error {
		return k8sClient.Get(context.Background(),
			types.NamespacedName{
				Name:      name,
				Namespace: namespace}, createdVeleroRestore)
	}, timeout, interval).Should(Succeed())

	createdVeleroRestore.Status.Phase = status
	Expect(
		k8sClient.Status().
			Update(context.Background(), createdVeleroRestore, &client.UpdateOptions{}),
	).Should(Succeed())
}

var _ = Describe("Basic Restore controller", func() {
	var (
		ctx                                context.Context
		veleroNamespace                    *corev1.Namespace
		veleroManagedClustersBackupName    string
		veleroResourcesBackupName          string
		veleroCredentialsBackupName        string
		veleroCredentialsHiveBackupName    string
		veleroCredentialsClusterBackupName string

		acmNamespaceName         string
		restoreName              string
		veleroBackups            []veleroapi.Backup
		rhacmRestore             v1beta1.Restore
		managedClusterNamespaces []corev1.Namespace
		backupStorageLocation    *veleroapi.BackupStorageLocation

		skipRestore   string
		latestBackup  string
		invalidBackup string

		timeout  = time.Second * 60
		interval = time.Millisecond * 250
	)

	JustBeforeEach(func() {
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
		for i := range managedClusterNamespaces {
			Expect(k8sClient.Create(ctx, &managedClusterNamespaces[i])).Should((Succeed()))
		}
		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}

		if backupStorageLocation != nil {
			Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
			backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
			Expect(k8sClient.Status().Update(ctx, backupStorageLocation)).Should(Succeed())
		}

		Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())

	})

	JustAfterEach(func() {

		if backupStorageLocation != nil {
			Expect(k8sClient.Delete(ctx, backupStorageLocation)).Should(Succeed())
		}
		var zero int64 = 0
		Expect(
			k8sClient.Delete(
				ctx,
				veleroNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())

		backupStorageLocation = nil
	})

	BeforeEach(func() { // default values
		ctx = context.Background()
		veleroManagedClustersBackupName = "acm-managed-clusters-schedule-20210910181336"
		veleroResourcesBackupName = "acm-resources-schedule-20210910181336"
		veleroCredentialsBackupName = "acm-credentials-schedule-20210910181336"
		veleroCredentialsHiveBackupName = "acm-credentials-hive-schedule-20210910181336"
		veleroCredentialsClusterBackupName = "acm-credentials-cluster-schedule-20210910181336"
		skipRestore = "skip"
		latestBackup = "latest"
		invalidBackup = "invalid-backup-name"
		restoreName = "rhacm-restore-1"

		veleroNamespace = &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "velero-restore-ns-1",
			},
		}

		backupStorageLocation = &veleroapi.BackupStorageLocation{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "velero/v1",
				Kind:       "BackupStorageLocation",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: veleroNamespace.Name,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "oadp.openshift.io/v1alpha1",
						Kind:       "Velero",
						Name:       "velero-instnace",
						UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
					},
				},
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
		}

		veleroBackups = []veleroapi.Backup{
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroManagedClustersBackupName,
					Namespace: veleroNamespace.Name,
				},
				Spec: veleroapi.BackupSpec{
					IncludedNamespaces: []string{"please-keep-this-one"},
				},
				Status: veleroapi.BackupStatus{
					Phase:  veleroapi.BackupPhaseCompleted,
					Errors: 0,
				},
			},
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroResourcesBackupName,
					Namespace: veleroNamespace.Name,
				},
				Spec: veleroapi.BackupSpec{
					IncludedNamespaces: []string{"please-keep-this-one"},
				},
				Status: veleroapi.BackupStatus{
					Phase:  veleroapi.BackupPhaseCompleted,
					Errors: 0,
				},
			},
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroCredentialsBackupName,
					Namespace: veleroNamespace.Name,
				},
				Spec: veleroapi.BackupSpec{
					IncludedNamespaces: []string{"please-keep-this-one"},
				},
				Status: veleroapi.BackupStatus{
					Phase:  veleroapi.BackupPhaseCompleted,
					Errors: 0,
				},
			},
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroCredentialsHiveBackupName,
					Namespace: veleroNamespace.Name,
				},
				Spec: veleroapi.BackupSpec{
					IncludedNamespaces: []string{"please-keep-this-one"},
				},
				Status: veleroapi.BackupStatus{
					Phase:  veleroapi.BackupPhaseCompleted,
					Errors: 0,
				},
			},
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroCredentialsClusterBackupName,
					Namespace: veleroNamespace.Name,
				},
				Spec: veleroapi.BackupSpec{
					IncludedNamespaces: []string{"please-keep-this-one"},
				},
				Status: veleroapi.BackupStatus{
					Phase:  veleroapi.BackupPhaseCompleted,
					Errors: 0,
				},
			},
		}

		managedClusterNamespaces = []corev1.Namespace{}

		rhacmRestore = v1beta1.Restore{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "cluster.open-cluster-management.io/v1beta1",
				Kind:       "Restore",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      restoreName,
				Namespace: veleroNamespace.Name,
			},
			Spec: v1beta1.RestoreSpec{
				VeleroManagedClustersBackupName: &veleroManagedClustersBackupName,
				VeleroCredentialsBackupName:     &latestBackup,
				VeleroResourcesBackupName:       &veleroResourcesBackupName,
			},
		}
	})

	Context("When creating a Restore with backup name", func() {
		It("Should creating a Velero Restore having non empty status", func() {
			restoreLookupKey := types.NamespacedName{
				Name:      restoreName,
				Namespace: veleroNamespace.Name,
			}
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restores in status")
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 5
			}, timeout, interval).Should(BeTrue())
			backupNames := []string{
				veleroManagedClustersBackupName,
				veleroResourcesBackupName,
				veleroCredentialsBackupName,
				veleroCredentialsHiveBackupName,
				veleroCredentialsClusterBackupName,
			}
			_, found := find(backupNames, veleroRestores.Items[0].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[1].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[2].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[3].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[4].Spec.BackupName)
			Expect(found).Should(BeTrue())
		})
	})

	Context("When creating a Restore with backup names set to latest", func() {
		BeforeEach(func() {
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-2",
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: veleroNamespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "oadp.openshift.io/v1alpha1",
							Kind:       "Velero",
							Name:       "velero-instnace",
							UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
						},
					},
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
			}
			backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable

			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &latestBackup,
					VeleroCredentialsBackupName:     &latestBackup,
					VeleroResourcesBackupName:       &latestBackup,
				},
			}
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			twoHoursAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
			threeHoursAgo := metav1.NewTime(time.Now().Add(-3 * time.Hour))
			fourHoursAgo := metav1.NewTime(time.Now().Add(-4 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-bad-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-good-old-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &threeHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-good-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &twoHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-not-completed-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseFailed,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-bad-old-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &fourHoursAgo,
					},
				},
				// acm-resources-schedule backups
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-bad-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-good-old-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &threeHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-good-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &twoHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-not-completed-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhasePartiallyFailed,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-bad-old-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &fourHoursAgo,
					},
				},
				// acm-credentials-schedule backups
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-schedule-bad-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-schedule-good-old-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &threeHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-schedule-good-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &twoHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-schedule-not-completed-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseInProgress,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-schedule-bad-old-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &fourHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-hive-schedule-good-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &twoHoursAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-credentials-cluster-schedule-good-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &twoHoursAgo,
					},
				},
			}
		})
		It("Should select the most recent backups without errors", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestore := veleroapi.Restore{}
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-managed-clusters-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-credentials-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-resources-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
		})
	})

	Context("When creating a Restore with backup names set to skip", func() {
		BeforeEach(func() {
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-3",
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: veleroNamespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "oadp.openshift.io/v1alpha1",
							Kind:       "Velero",
							Name:       "velero-instnace",
							UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
						},
					},
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
			}
			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &skipRestore,
					VeleroCredentialsBackupName:     &skipRestore,
					VeleroResourcesBackupName:       &skipRestore,
				},
			}
		})
		It("Should skip restoring backups without errors", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).Should(BeEmpty())

			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 0
			}, timeout, interval).Should(BeTrue())
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				Expect(err).NotTo(HaveOccurred())
				return createdRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinished))
		})
	})

	Context("When creating a Restore with even one invalid backup name", func() {
		BeforeEach(func() {
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-4",
				},
			}
			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &latestBackup,
					VeleroCredentialsBackupName:     &invalidBackup,
					VeleroResourcesBackupName:       &latestBackup,
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: veleroNamespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "oadp.openshift.io/v1alpha1",
							Kind:       "Velero",
							Name:       "velero-instnace",
							UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
						},
					},
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
			}
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				// acm-managed-clusters-schedule backups
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-gold-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-gold-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
			}
		})
		It("Should not create any restore", func() {
			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 0
			}, timeout, interval).Should(BeTrue())
			createdRestore := v1beta1.Restore{}
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				Expect(err).NotTo(HaveOccurred())
				return createdRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
			Expect(
				createdRestore.Status.LastMessage,
			).Should(BeIdenticalTo("Backup invalid-backup-name Not found"))
		})
	})

	Context("When creating a Restore in a ns different then velero ns", func() {
		BeforeEach(func() {
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-5",
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default-5",
					Namespace: veleroNamespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "oadp.openshift.io/v1alpha1",
							Kind:       "Velero",
							Name:       "velero-instnace",
							UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
						},
					},
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
			}
			acmNamespaceName = "acm-ns-1"
			acmNamespace := &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: acmNamespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, acmNamespace)).Should(Succeed())

			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName + "-new",
					Namespace: acmNamespaceName,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &latestBackup,
					VeleroCredentialsBackupName:     &skipRestore,
					VeleroResourcesBackupName:       &skipRestore,
				},
			}
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-bad-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &oneHourAgo,
					},
				},
			}

		})
		It(
			"Should not create any velero restore resources, restore object created in the wrong ns",
			func() {
				veleroRestores := veleroapi.RestoreList{}
				Eventually(func() bool {
					if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
						return false
					}
					return len(veleroRestores.Items) == 0
				}, timeout, interval).Should(BeTrue())
				createdRestore := v1beta1.Restore{}
				Eventually(func() v1beta1.RestorePhase {
					restoreLookupKey := types.NamespacedName{
						Name:      restoreName + "-new",
						Namespace: acmNamespaceName,
					}
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					Expect(err).NotTo(HaveOccurred())
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("Restore resource [acm-ns-1/rhacm-restore-1-new] " +
					"must be created in the velero namespace [velero-restore-ns-5]"))
			},
		)
	})

	Context("When BackupStorageLocation without OwnerReference is invalid", func() {
		BeforeEach(func() {
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-6",
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default-6",
					Namespace: veleroNamespace.Name,
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
			}

			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName + "-new",
					Namespace: veleroNamespace.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &latestBackup,
					VeleroCredentialsBackupName:     &skipRestore,
					VeleroResourcesBackupName:       &skipRestore,
				},
			}
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-bad-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         10,
						StartTimestamp: &oneHourAgo,
					},
				},
			}
		})
		It(
			"Should not create any velero restore resources, BackupStorageLocation is invalid",
			func() {
				veleroRestores := veleroapi.RestoreList{}
				Eventually(func() bool {
					if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
						return false
					}
					return len(veleroRestores.Items) == 0
				}, timeout, interval).Should(BeTrue())
				createdRestore := v1beta1.Restore{}
				Eventually(func() v1beta1.RestorePhase {
					restoreLookupKey := types.NamespacedName{
						Name:      restoreName + "-new",
						Namespace: veleroNamespace.Name,
					}
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					Expect(err).NotTo(HaveOccurred())
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("Backup storage location not available in namespace velero-restore-ns-6. " +
					"Check velero.io.BackupStorageLocation and validate storage credentials."))
			},
		)
	})

	Context("When creating a Restore with backup name", func() {
		BeforeEach(func() {
			restoreName = "my-restore"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-7",
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default-5",
					Namespace: veleroNamespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "oadp.openshift.io/v1alpha1",
							Kind:       "Velero",
							Name:       "velero-instance",
							UID:        "fed287da-02ea-4c83-a7f8-906ce662451a",
						},
					},
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
			}
			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &latestBackup,
					VeleroCredentialsBackupName:     &skipRestore,
					VeleroResourcesBackupName:       &skipRestore,
				},
			}
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-good-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
			}
		})

		It("Should track the status evolution", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restores in status")
			Eventually(func() string {
				k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      restoreName,
						Namespace: veleroNamespace.Name,
					}, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			By(
				"Setting "+createdRestore.Status.VeleroManagedClustersRestoreName+" in progress",
				func() {
					updateVeleroRestoreStatus(
						createdRestore.Status.VeleroManagedClustersRestoreName,
						createdRestore.Namespace,
						veleroapi.RestorePhaseInProgress,
						timeout,
						interval,
					)
				},
			)

			By("Checking ACM restore phase when velero restore is in progress", func() {
				Eventually(func() v1beta1.RestorePhase {
					k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseRunning))
			})

			By(
				"Setting "+createdRestore.Status.VeleroManagedClustersRestoreName+" completed",
				func() {
					updateVeleroRestoreStatus(
						createdRestore.Status.VeleroManagedClustersRestoreName,
						createdRestore.Namespace,
						veleroapi.RestorePhaseCompleted,
						timeout,
						interval,
					)
				},
			)

			By("Checking ACM restore phase when velero restore finished", func() {
				Eventually(func() v1beta1.RestorePhase {
					k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinished))
			})
		})

	})

	Context("When creating a Restore and no storage location is available", func() {
		BeforeEach(func() {
			restoreName = "my-restore"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "velero-restore-ns-8",
				},
			}
			backupStorageLocation = nil

			rhacmRestore = v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &latestBackup,
					VeleroCredentialsBackupName:     &skipRestore,
					VeleroResourcesBackupName:       &skipRestore,
				},
			}
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-good-very-recent-backup",
						Namespace: veleroNamespace.Name,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						Errors:         0,
						StartTimestamp: &oneHourAgo,
					},
				},
			}
		})

		It("Should track the status evolution", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should does not contain velero restores in status")
			Eventually(func() string {
				k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      restoreName,
						Namespace: veleroNamespace.Name,
					}, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).Should(BeEmpty())

			By("Checking ACM restore phase when velero restore is in error", func() {
				Eventually(func() v1beta1.RestorePhase {
					k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
			})

			By("Checking ACM restore message", func() {
				Eventually(func() string {
					k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					return createdRestore.Status.LastMessage
				}, timeout, interval).Should(BeIdenticalTo("velero.io.BackupStorageLocation resources not found. " +
					"Verify you have created a konveyor.openshift.io.Velero resource."))
			})
		})

	})
})
