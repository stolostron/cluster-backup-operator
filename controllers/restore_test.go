package controllers

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1beta1 "github.com/open-cluster-management-io/cluster-backup-operator/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Restore", func() {

	var (
		restoreName string = "the-restore-name"
	)

	Context("For utility functions of Restore", func() {
		It("isRestoreFinsihed should return correct value based on the status", func() {
			rhacmRestore := v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: "",
				},
				Status: v1beta1.RestoreStatus{
					Phase: v1beta1.InProgressStatusPhase,
				},
			}

			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeFalse())

			rhacmRestore.Status.Phase = v1beta1.CompletedStatusPhase
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())
			rhacmRestore.Status.Phase = v1beta1.FailedStatusPhase
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())
			rhacmRestore.Status.Phase = v1beta1.PartiallyFailedStatusPhase
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())
			rhacmRestore.Status.Phase = "FailedValidation"
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())

			rhacmRestore.Status.Phase = "InvalidStatus"
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeFalse())
		})

		It("getVeleroRestoreName should return the expected name", func() {
			rhacmRestore := v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: "",
				},
				Spec: v1beta1.RestoreSpec{
					BackupName: "the-latest-backup",
				},
			}

			veleroRestoreName := restoreName + "-" + "the-latest-backup"

			Expect(getVeleroRestoreName(&rhacmRestore)).Should(Equal(veleroRestoreName))
		})
	})
})
