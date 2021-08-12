package controllers

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1beta1 "github.com/open-cluster-management-io/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func setVeleroPhase(restore *v1beta1.Restore, phase veleroapi.RestorePhase) {
	restore.Status.VeleroRestore.Status.Phase = phase
}

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
					VeleroRestore: &veleroapi.Restore{
						Status: veleroapi.RestoreStatus{
							Phase: veleroapi.RestorePhaseInProgress,
						},
					},
				},
			}

			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeFalse())

			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhaseCompleted)
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())
			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhaseFailed)
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())
			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhasePartiallyFailed)
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())
			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhaseFailedValidation)
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeTrue())

			Expect(isRestoreFinsihed(nil)).Should(BeFalse())

			rhacmRestore.Status.VeleroRestore = nil
			Expect(isRestoreFinsihed(&rhacmRestore)).Should(BeFalse())

		})
		/*
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
						VeleroBackupName: "the-latest-backup",
					},
				}

				veleroRestoreName := restoreName + "-" + "the-latest-backup"

				Expect(getVeleroRestoreName(&rhacmRestore)).Should(Equal(veleroRestoreName))
			})
		*/
	})
})
