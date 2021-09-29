package controllers

import (
	"math/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
)

// gerenates a random string with specified length
func RandStringBytesMask(n int) string {
	b := make([]byte, n)
	for i := 0; i < n; {
		if idx := int(rand.Int63() & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i++
		}
	}
	return string(b)
}

var _ = Describe("Backup", func() {

	var (
		backupName string = "the-backup-name"
	)

	Context("For utility functions of Backup", func() {
		It("isBackupFinished should return correct value based on the status", func() {

			//returns the concatenated strings, no trimming
			Expect(getValidKsRestoreName("a", "b")).Should(Equal("a-b"))

			//returns substring of length 252
			longName := RandStringBytesMask(260)
			Expect(getValidKsRestoreName(longName, "b")).Should(Equal(longName[:252]))

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
