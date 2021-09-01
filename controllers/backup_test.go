package controllers

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Backup", func() {

	var (
		veleroNamespaceName string = "velero-backup-ns"
		backupName          string = "the-backup-name"
	)

	Context("For utility functions of Backup", func() {
		It("isBackupFinished should return correct value based on the status", func() {
			Expect(isBackupFinished(nil)).Should(BeFalse())

			veleroBackups := make([]*veleroapi.Backup, 0)
			Expect(isBackupFinished(veleroBackups)).Should(BeFalse())

			veleroBackup := veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: "",
				},
			}
			veleroBackups = append(veleroBackups, &veleroBackup)

			Expect(isBackupFinished(veleroBackups)).Should(BeFalse())

			veleroBackup.Status.Phase = "Completed"
			Expect(isBackupFinished(veleroBackups)).Should(BeTrue())
			veleroBackup.Status.Phase = "Failed"
			Expect(isBackupFinished(veleroBackups)).Should(BeTrue())
			veleroBackup.Status.Phase = "PartiallyFailed"
			Expect(isBackupFinished(veleroBackups)).Should(BeTrue())

			veleroBackup.Status.Phase = "InvalidStatus"
			Expect(isBackupFinished(veleroBackups)).Should(BeFalse())
		})

		It("getFormattedDuration should return the expected value", func() {
			duration := time.Minute * 10
			formatted := "10m0s"

			Expect(getFormattedDuration(duration)).Should(Equal(formatted))
		})

		It("canStartBackup should return the expected value", func() {
			veleroBackups := make([]*veleroapi.Backup, 0)
			veleroBackup := veleroapi.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: "",
				},
			}
			veleroBackups = append(veleroBackups, &veleroBackup)

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
					Interval:   60,
					MaxBackups: 2,
				},
				Status: v1beta1.BackupStatus{
					VeleroBackups: veleroBackups,
				},
			}

			Expect(canStartBackup(&rhacmBackup)).Should(BeTrue())

			t := metav1.Now()
			veleroBackup.Status.CompletionTimestamp = &t

			Expect(canStartBackup(&rhacmBackup)).Should(BeFalse())
		})

		It("min should return the expected value", func() {
			Expect(min(5, -1)).Should(Equal(-1))
			Expect(min(2, 3)).Should(Equal(2))
		})

		It("find should return the expected value", func() {
			slice := []string{}
			index, found := find(slice, "two")
			Expect(index).Should(Equal(-1))
			Expect(found).Should(BeFalse())

			slice = append(slice, "one")
			slice = append(slice, "two")
			slice = append(slice, "three")
			index, found = find(slice, "two")
			Expect(index).Should(Equal(1))
			Expect(found).Should(BeTrue())
		})

		It("filterBackups should work as expected", func() {
			sliceBackups := []veleroapi.Backup{}
			backupsInError := filterBackups(sliceBackups, func(bkp veleroapi.Backup) bool {
				return bkp.Status.Errors > 0
			})
			Expect(backupsInError).Should(Equal(sliceBackups))

			succeededBackup := veleroapi.Backup{
				Status: veleroapi.BackupStatus{
					Errors: 0,
				},
			}
			failedBackup := veleroapi.Backup{
				Status: veleroapi.BackupStatus{
					Errors: 1,
				},
			}
			sliceBackups = append(sliceBackups, succeededBackup)
			sliceBackups = append(sliceBackups, failedBackup)

			backupsInError = filterBackups(sliceBackups, func(bkp veleroapi.Backup) bool {
				return bkp.Status.Errors > 0
			})
			Expect(backupsInError).Should(Equal([]veleroapi.Backup{failedBackup}))
		})

	})
})
