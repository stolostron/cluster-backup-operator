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

var _ = Describe("Backup controller", func() {

	var (
		ctx                 context.Context
		managedClusters     []clusterv1.ManagedCluster
		veleroNamespaceName string
		veleroNamespace     *corev1.Namespace
		backupName          string = "the-backup-name"

		timeout = time.Second * 70
		//duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		veleroNamespaceName = "velero-backup-ns"
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

	JustBeforeEach(func() {
		for i := range managedClusters {
			Expect(k8sClient.Create(ctx, &managedClusters[i])).Should(Succeed())
		}
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
	})
	Context("When creating a Backup", func() {
		It("Should creating a Velero Backup updating the Status", func() {
			managedClusterList := clusterv1.ManagedClusterList{}
			Eventually(func() bool {
				err := k8sClient.List(ctx, &managedClusterList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(managedClusterList.Items)).To(BeNumerically("==", 2))

			createdVeleroNamespace := corev1.Namespace{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: veleroNamespaceName, Namespace: ""}, &createdVeleroNamespace); err != nil {
					return false
				}
				if createdVeleroNamespace.Status.Phase == "Active" {
					return true
				}
				return false
			}, timeout, interval).Should(BeTrue())

			rhacmBackup := v1beta1.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: veleroNamespaceName,
				},
				Spec: v1beta1.BackupSpec{
					VeleroConfig: &v1beta1.VeleroConfigBackupProxy{
						Namespace: veleroNamespaceName,
					},
					Interval:   60,
					MaxBackups: 2,
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmBackup)).Should(Succeed())
			backupLookupKey := types.NamespacedName{Name: backupName, Namespace: veleroNamespaceName}
			createdBackup := v1beta1.Backup{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackup)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(createdBackup.CreationTimestamp.Time).NotTo(BeNil())

			//By("created backup should contain velero backup in status")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackup)
				if err != nil {
					return false
				}
				return createdBackup.Status.VeleroBackups != nil
			}, timeout, interval).ShouldNot(BeNil())

		})

	})
})
