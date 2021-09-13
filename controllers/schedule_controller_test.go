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
)

var _ = Describe("BackupSchedule controller", func() {

	var (
		ctx                 context.Context
		managedClusters     []clusterv1.ManagedCluster
		veleroNamespaceName string
		veleroNamespace     *corev1.Namespace
		backupScheduleName  string = "the-backup-schedule-name"
		backupSchedule      string = "0 */6 * * *"

		timeout  = time.Second * 70
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		veleroNamespaceName = "velero-ns"
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
		veleroNamespace = &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: veleroNamespaceName,
			},
		}

	})

	AfterEach(func() {
		for i := range managedClusters {
			Expect(k8sClient.Delete(ctx, &managedClusters[i])).Should(Succeed())
		}
		Expect(k8sClient.Delete(ctx, veleroNamespace)).Should(Succeed())
	})

	JustBeforeEach(func() {
		for i := range managedClusters {
			Expect(k8sClient.Create(ctx, &managedClusters[i])).Should(Succeed())
		}
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
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
					MaxBackups:     2,
					VeleroSchedule: "0 */6 * * *",
					VeleroTTL:      metav1.Duration{Duration: time.Hour * 72},
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmBackupSchedule)).Should(Succeed())

			rhacmBackupScheduleNoTTL := v1beta1.BackupSchedule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "BackupSchedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupScheduleName + "-nottl",
					Namespace: veleroNamespaceName,
				},
				Spec: v1beta1.BackupScheduleSpec{
					VeleroSchedule: "0 */6 * * *",
					MaxBackups:     2,
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmBackupScheduleNoTTL)).Should(Succeed())

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

			//By("created backup schedule should contain velero schedules in status")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return false
				}
				return createdBackupSchedule.Status.VeleroScheduleCredentials != nil &&
					createdBackupSchedule.Status.VeleroScheduleManagedClusters != nil &&
					createdBackupSchedule.Status.VeleroScheduleResources != nil
			}, timeout, interval).ShouldNot(BeNil())
		})

	})
})
