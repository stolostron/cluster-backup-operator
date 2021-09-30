package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	chnv1 "github.com/open-cluster-management/multicloud-operators-channel/pkg/apis/apps/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

var _ = Describe("BackupSchedule controller", func() {

	var (
		ctx                             context.Context
		managedClusters                 []clusterv1.ManagedCluster
		channels                        []chnv1.Channel
		backupStorageLocation           *veleroapi.BackupStorageLocation
		veleroBackups                   []veleroapi.Backup
		veleroNamespaceName             string
		acmNamespaceName                string
		chartsv1NSName                  string
		veleroNamespace                 *corev1.Namespace
		acmNamespace                    *corev1.Namespace
		chartsv1NS                      *corev1.Namespace
		backupScheduleName              string = "the-backup-schedule-name"
		veleroManagedClustersBackupName        = "acm-managed-clusters-schedule-20210910181336"
		veleroResourcesBackupName              = "acm-resources-schedule-20210910181336"
		veleroCredentialsBackupName            = "acm-credentials-schedule-20210910181336"

		backupSchedule string = "0 */6 * * *"

		timeout  = time.Second * 70
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		veleroNamespaceName = "velero-ns"
		acmNamespaceName = "acm-ns"
		chartsv1NSName = "acm-channel-ns"
		managedClusters = []clusterv1.ManagedCluster{
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1",
					Kind:       "ManagedCluster",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "local-cluster",
				},
				Spec: clusterv1.ManagedClusterSpec{
					HubAcceptsClient: true,
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1",
					Kind:       "ManagedCluster",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "managed1",
				},
				Spec: clusterv1.ManagedClusterSpec{
					HubAcceptsClient: true,
				},
			},
		}
		chartsv1NS = &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: chartsv1NSName,
			},
		}
		veleroNamespace = &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: veleroNamespaceName,
			},
		}
		acmNamespace = &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: acmNamespaceName,
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
		channels = []chnv1.Channel{
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apps.open-cluster-management.io/v1",
					Kind:       "Channel",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "charts-v1",
					Namespace: chartsv1NSName,
				},
				Spec: chnv1.ChannelSpec{
					Type:     chnv1.ChannelTypeHelmRepo,
					Pathname: "http://test.svc.cluster.local:3000/charts",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apps.open-cluster-management.io/v1",
					Kind:       "Channel",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-channel",
					Namespace: "default",
				},
				Spec: chnv1.ChannelSpec{
					Type:     chnv1.ChannelTypeGit,
					Pathname: "https://github.com/test/app-samples",
				},
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
			veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      veleroManagedClustersBackupName + "-new",
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
					Name:      veleroResourcesBackupName + "-new",
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
					Name:      veleroCredentialsBackupName + "-new",
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
					Name:      "some-other-new",
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

	})

	AfterEach(func() {
		for i := range managedClusters {
			Expect(k8sClient.Delete(ctx, &managedClusters[i])).Should(Succeed())
		}
		for i := range channels {
			Expect(k8sClient.Delete(ctx, &channels[i])).Should(Succeed())
		}
		Expect(k8sClient.Delete(ctx, veleroNamespace)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, acmNamespace)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, chartsv1NS)).Should(Succeed())

		Expect(k8sClient.Delete(ctx, backupStorageLocation)).Should(Succeed())

		for i := range veleroBackups {
			Expect(k8sClient.Delete(ctx, &veleroBackups[i])).Should(Succeed())
		}
	})

	JustBeforeEach(func() {
		for i := range managedClusters {
			Expect(k8sClient.Create(ctx, &managedClusters[i])).Should(Succeed())
		}
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
		Expect(k8sClient.Create(ctx, acmNamespace)).Should(Succeed())
		Expect(k8sClient.Create(ctx, chartsv1NS)).Should(Succeed())

		Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
		backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
		Expect(k8sClient.Status().Update(ctx, backupStorageLocation)).Should(Succeed())

		for i := range channels {
			Expect(k8sClient.Create(ctx, &channels[i])).Should(Succeed())
		}

		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}

	})
	Context("When creating a BackupSchedule", func() {
		It("Should be creating a Velero Schedule updating the Status", func() {
			managedClusterList := clusterv1.ManagedClusterList{}
			Eventually(func() bool {
				err := k8sClient.List(ctx, &managedClusterList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(managedClusterList.Items)).To(BeNumerically("==", 2))

			createdVeleroNamespace := corev1.Namespace{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: veleroNamespaceName,
					Namespace: ""}, &createdVeleroNamespace); err != nil {
					return false
				}
				if createdVeleroNamespace.Status.Phase == "Active" {
					return true
				}
				return false
			}, timeout, interval).Should(BeTrue())

			createdACMNamespace := corev1.Namespace{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: acmNamespaceName,
					Namespace: ""}, &createdACMNamespace); err != nil {
					return false
				}
				if createdACMNamespace.Status.Phase == "Active" {
					return true
				}
				return false
			}, timeout, interval).Should(BeTrue())

			rhacmBackupSchedule := v1beta1.BackupSchedule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "BackupSchedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupScheduleName,
					Namespace: veleroNamespaceName,
				},
				Spec: v1beta1.BackupScheduleSpec{
					MaxBackups:     1,
					VeleroSchedule: "0 */6 * * *",
					VeleroTTL:      metav1.Duration{Duration: time.Hour * 72},
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmBackupSchedule)).Should(Succeed())

			backupLookupKey := types.NamespacedName{
				Name:      backupScheduleName,
				Namespace: veleroNamespaceName,
			}
			createdBackupSchedule := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(createdBackupSchedule.CreationTimestamp.Time).NotTo(BeNil())

			Expect(createdBackupSchedule.Spec.VeleroSchedule).Should(Equal(backupSchedule))

			By("created backup schedule should contain velero schedules in status")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return false
				}

				schedulesCreated := createdBackupSchedule.Status.VeleroScheduleCredentials != nil &&
					createdBackupSchedule.Status.VeleroScheduleManagedClusters != nil &&
					createdBackupSchedule.Status.VeleroScheduleResources != nil

				if schedulesCreated {
					// verify the acm charts channel ns is excluded
					_, ok := find(createdBackupSchedule.Status.VeleroScheduleResources.Spec.Template.ExcludedNamespaces, chartsv1NSName)
					return ok

				}
				return schedulesCreated

			}, timeout, interval).Should(BeTrue())

			// new backup
			backupScheduleNameNoTTL := backupScheduleName + "-nottl"
			rhacmBackupScheduleNoTTL := v1beta1.BackupSchedule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "BackupSchedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupScheduleNameNoTTL,
					Namespace: veleroNamespaceName,
				},
				Spec: v1beta1.BackupScheduleSpec{
					VeleroSchedule: "0 */6 * * *",
					MaxBackups:     1,
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmBackupScheduleNoTTL)).Should(Succeed())

			// new schedule backup to trigger backup delete routine
			backupScheduleName3 := backupScheduleName + "-3"
			backupSchedule3 := v1beta1.BackupSchedule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "BackupSchedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupScheduleName3,
					Namespace: veleroNamespaceName,
				},
				Spec: v1beta1.BackupScheduleSpec{
					VeleroSchedule: "0 */6 * * *",
					MaxBackups:     1,
				},
			}
			Expect(k8sClient.Create(ctx, &backupSchedule3)).Should(Succeed())

			backupLookupKeyNoTTL := types.NamespacedName{
				Name:      backupScheduleNameNoTTL,
				Namespace: veleroNamespaceName,
			}
			createdBackupScheduleNoTTL := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyNoTTL, &createdBackupScheduleNoTTL)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(createdBackupScheduleNoTTL.CreationTimestamp.Time).NotTo(BeNil())

			Expect(createdBackupScheduleNoTTL.Spec.VeleroSchedule).Should(Equal(backupSchedule))

			// schedules cannot be created because there already some running from the above schedule
			By("created backup schedule should NOT contain velero schedules, acm-credentials-schedule already exists error")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyNoTTL, &createdBackupScheduleNoTTL)
				if err != nil {
					return false
				}
				return createdBackupScheduleNoTTL.Status.VeleroScheduleCredentials != nil &&
					createdBackupScheduleNoTTL.Status.VeleroScheduleManagedClusters != nil &&
					createdBackupScheduleNoTTL.Status.VeleroScheduleResources != nil
			}, timeout, interval).ShouldNot(BeTrue())

			//
			acmBackupName := backupScheduleName
			rhacmBackupScheduleACM := v1beta1.BackupSchedule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "BackupSchedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      acmBackupName,
					Namespace: acmNamespaceName,
				},
				Spec: v1beta1.BackupScheduleSpec{
					MaxBackups:     1,
					VeleroSchedule: "0 */6 * * *",
					VeleroTTL:      metav1.Duration{Duration: time.Hour * 72},
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmBackupScheduleACM)).Should(Succeed())

			backupLookupKeyACM := types.NamespacedName{
				Name:      acmBackupName,
				Namespace: acmNamespaceName,
			}
			createdBackupScheduleACM := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyACM, &createdBackupScheduleACM)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("backup schedule in acm ns should be in failed validation status - since it must be in the velero ns")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyACM, &createdBackupScheduleACM)
				if err != nil {
					return false
				}
				return createdBackupScheduleACM.Status.Phase == v1beta1.SchedulePhaseFailedValidation
			}, timeout, interval).Should(BeTrue())

			// count schedules
			acmSchedulesList := v1beta1.BackupScheduleList{}
			Eventually(func() bool {
				err := k8sClient.List(ctx, &acmSchedulesList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(acmSchedulesList.Items)).To(BeNumerically("==", 4))
		})

	})

})
