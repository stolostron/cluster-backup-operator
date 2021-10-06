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

var _ = Describe("Basic Restore controller", func() {
	var (
		ctx                             context.Context
		veleroNamespace                 *corev1.Namespace
		veleroManagedClustersBackupName string
		veleroResourcesBackupName       string
		veleroCredentialsBackupName     string
		veleroNamespaceName             string
		acmNamespaceName                string
		restoreName                     string
		veleroBackups                   []veleroapi.Backup
		rhacmRestore                    v1beta1.Restore
		managedClusterNamespaces        []corev1.Namespace
		backupStorageLocation           *veleroapi.BackupStorageLocation

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

		Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
		backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
		Expect(k8sClient.Status().Update(ctx, backupStorageLocation)).Should(Succeed())

		Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())

	})

	JustAfterEach(func() {
		Expect(k8sClient.Delete(ctx, backupStorageLocation)).Should(Succeed())
	})

	BeforeEach(func() { // default values
		ctx = context.Background()
		veleroNamespaceName = "velero-restore-ns-1"
		veleroManagedClustersBackupName = "acm-managed-clusters-schedule-20210910181336"
		veleroResourcesBackupName = "acm-resources-schedule-20210910181336"
		veleroCredentialsBackupName = "acm-credentials-schedule-20210910181336"
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
				Name: veleroNamespaceName,
			},
		}

		backupStorageLocation = &veleroapi.BackupStorageLocation{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "velero/v1",
				Kind:       "BackupStorageLocation",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
				Namespace: veleroNamespaceName,
			},
			Spec: v1beta1.RestoreSpec{
				VeleroManagedClustersBackupName: &veleroManagedClustersBackupName,
				VeleroCredentialsBackupName:     &veleroCredentialsBackupName,
				VeleroResourcesBackupName:       &veleroResourcesBackupName,
			},
		}
	})

	Context("When creating a Restore with backup name", func() {
		It("Should creating a Velero Restore having non empty status", func() {
			restoreLookupKey := types.NamespacedName{
				Name:      restoreName,
				Namespace: veleroNamespaceName,
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
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespaceName)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 3
			}, timeout, interval).Should(BeTrue())
			backupNames := []string{
				veleroManagedClustersBackupName,
				veleroResourcesBackupName,
				veleroCredentialsBackupName,
			}
			_, found := find(backupNames, veleroRestores.Items[0].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[1].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[2].Spec.BackupName)
			Expect(found).Should(BeTrue())
		})
	})

	Context("When creating a Restore with backup names set to latest", func() {
		BeforeEach(func() {
			veleroNamespaceName = "velero-restore-ns-2"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: veleroNamespaceName,
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
				// acm-managed-clusters-schedule backups
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-managed-clusters-schedule-bad-very-recent-backup",
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Name:      "acm-managed-clusters-schedule-bad-old-backup",
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Name:      "acm-resources-schedule-bad-old-backup",
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Name:      "acm-credentials-schedule-bad-old-backup",
						Namespace: veleroNamespaceName,
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
			}
		})
		It("Should select the most recent backups without errors", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestore := veleroapi.Restore{}
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespaceName,
						Name:      restoreName + "-acm-managed-clusters-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespaceName,
						Name:      restoreName + "-acm-credentials-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespaceName,
						Name:      restoreName + "-acm-resources-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
		})
	})

	Context("When creating a Restore with backup names set to skip", func() {
		BeforeEach(func() {
			veleroNamespaceName = "velero-restore-ns-3"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).Should(BeEmpty())

			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespaceName)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 0
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("When creating a Restore with even one invalid backup name", func() {
		BeforeEach(func() {
			veleroNamespaceName = "velero-restore-ns-4"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespaceName)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 0
			}, timeout, interval).Should(BeTrue())
			createdRestore := v1beta1.Restore{}
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
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

			veleroNamespaceName = "velero-restore-ns-5"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: veleroNamespaceName,
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default-5",
					Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
					if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespaceName)); err != nil {
						return false
					}
					return len(veleroRestores.Items) == 0
				}, timeout, interval).Should(BeTrue())
			},
		)
	})

	Context("When BackupStorageLocation without OwnerReference is invalid", func() {
		BeforeEach(func() {

			veleroNamespaceName = "velero-restore-ns-6"
			veleroNamespace = &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: veleroNamespaceName,
				},
			}
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "BackupStorageLocation",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default-6",
					Namespace: veleroNamespaceName,
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
					Namespace: veleroNamespaceName,
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
						Namespace: veleroNamespaceName,
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
					if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespaceName)); err != nil {
						return false
					}
					return len(veleroRestores.Items) == 0
				}, timeout, interval).Should(BeTrue())
			},
		)
	})
})
