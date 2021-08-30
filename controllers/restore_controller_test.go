package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Restore controller", func() {

	var (
		ctx                 context.Context
		veleroNamespace     *corev1.Namespace
		veleroNamespaceName string
		restoreName         string = "the-restore-name"

		timeout  = time.Second * 70
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		veleroNamespaceName = "velero-restore-ns"

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
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
	})
	Context("When creating a Restore", func() {
		It("Should creating a Velero Restore updating the Status", func() {
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

			rhacmRestore := v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: veleroNamespaceName,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroConfig: &v1beta1.VeleroConfigRestoreProxy{
						Namespace: veleroNamespaceName,
					},
				},
			}

			Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())
			restoreLookupKey := types.NamespacedName{Name: restoreName, Namespace: veleroNamespaceName}
			createdRestore := v1beta1.Restore{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(createdRestore.CreationTimestamp.Time).NotTo(BeNil())

			//By("created restore should contain velero restore in status")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				if err != nil {
					return false
				}
				return createdRestore.Status.VeleroRestore != nil
			}, timeout, interval).ShouldNot(BeNil())
		})
	})
})
