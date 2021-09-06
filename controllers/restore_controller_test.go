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
		ctx                      context.Context
		veleroNamespace          *corev1.Namespace
		veleroBackupName         string
		veleroNamespaceName      string
		restoreName              string
		veleroBackups            []veleroapi.Backup
		rhacmRestore             v1beta1.Restore
		managedClusterNamespaces []corev1.Namespace

		timeout  = time.Second * 60
		interval = time.Millisecond * 250
	)

	BeforeEach(func() { // default values
		ctx = context.Background()
		veleroNamespaceName = "velero-restore-ns-1"
		veleroBackupName = "velero-backup"
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

		veleroBackups = []veleroapi.Backup{
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroBackupName,
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
				VeleroBackupName: &veleroBackupName,
			},
		}
	})
	JustBeforeEach(func() {
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
		for i := range managedClusterNamespaces {
			Expect(k8sClient.Create(ctx, &managedClusterNamespaces[i])).Should((Succeed()))
		}
		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}
		Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())
	})

	Context("When creating a Restore with backup name", func() {
		It("Should creating a Velero Restore having non empty status", func() {
			restoreLookupKey := types.NamespacedName{Name: restoreName, Namespace: veleroNamespaceName}
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespaceName)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 1
			}, timeout, interval).Should(BeTrue())
			Expect(veleroRestores.Items[0].Spec.BackupName == veleroBackupName).Should(BeTrue())
		})
	})

	Context("When creating a Restore without backup name", func() {
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
					//VeleroBackupName <NO BACKUP NAME>
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
						Name:      "bad-very-recent-backup",
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
						Name:      "good-old-backup",
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
						Name:      "good-recent-backup",
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
						Name:      "bad-old-backup",
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
				restoreLookupKey := types.NamespacedName{Name: restoreName, Namespace: veleroNamespaceName}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestore := veleroapi.Restore{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: veleroNamespaceName, Name: restoreName + "-good-recent-backup"}, &veleroRestore)).ShouldNot(HaveOccurred())
		})
	})

	// TODO: Context("When creating a Restore with wrong backup name ", func() {})

})

var _ = Describe("Advanced Restore controller", func() {
	var (
		ctx                                 context.Context
		managedClusterName                  string
		veleroNamespaceName                 string
		veleroNamespace                     corev1.Namespace
		managedClusterNamespace             corev1.Namespace
		veleroBackups                       []veleroapi.Backup
		openClusterManagementAgentNamespace corev1.Namespace
		restoreName                         string
		veleroBackupName                    string
		rhacmRestore                        v1beta1.Restore

		timeout  = time.Second * 60
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		managedClusterName = "managed-cluster"
		managedClusterNamespace = initManagedClusterNamespace(managedClusterName) // Hub
		Expect(k8sClient.Create(ctx, &managedClusterNamespace)).Should(Succeed())
		veleroNamespaceName = "velero-restore-ns-4"
		restoreName = "the-restore-4"
		veleroNamespace = initManagedClusterNamespace(veleroNamespaceName)
		Expect(k8sClient.Create(ctx, &veleroNamespace)).Should(Succeed())
		veleroBackupName = "the-backup"

		veleroBackups = []veleroapi.Backup{
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroBackupName,
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

		openClusterManagementAgentNamespace = initManagedClusterNamespace("open-cluster-management-agent") // managed
		Expect(managedClusterK8sClient.Create(ctx, &openClusterManagementAgentNamespace)).Should(Succeed())

		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}

	})

	Context("When Velero Restore is finished", func() {
		BeforeEach(func() {
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
					VeleroBackupName: &veleroBackupName,
				},
			}
		})

		It("Replace managed cluster boostrap-hub-kubeconfig", func() {
			Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())

			veleroRestore := veleroapi.Restore{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Namespace: veleroNamespaceName, Name: restoreName + "-" + veleroBackupName}, &veleroRestore)
			}, timeout, interval).ShouldNot(HaveOccurred())

			restore := v1beta1.Restore{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Namespace: veleroNamespaceName, Name: restoreName}, &restore)
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(restore.Status.VeleroRestoreName == restoreName+"-"+veleroBackupName).To(BeTrue())
			Expect(restore.Status.Conditions).ToNot((BeEmpty()))
		})
	})
})
