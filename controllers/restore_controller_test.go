package controllers

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Backup controller", func() {

	var (
		ctx                 context.Context
		veleroNamespace     *corev1.Namespace
		veleroNamespaceName string
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
			// TODO: :)
		})
	})
})
