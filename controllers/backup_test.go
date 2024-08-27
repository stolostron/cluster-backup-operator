package controllers

import (
	"context"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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
		backupName                      string = "the-backup-name"
		veleroNamespaceName                    = "velero"
		veleroManagedClustersBackupName        = "acm-managed-clusters-schedule-20210910181336"
		veleroResourcesBackupName              = "acm-resources-schedule-20210910181336"
		veleroCredentialsBackupName            = "acm-credentials-schedule-20210910181336"

		labelsCls123 = map[string]string{
			"velero.io/schedule-name":  "acm-resources-schedule",
			BackupScheduleClusterLabel: "cls-123",
		}
	)

	Context("For utility functions of Backup", func() {
		It("isBackupFinished should return correct value based on the status", func() {
			// returns the concatenated strings, no trimming
			Expect(getValidKsRestoreName("a", "b")).Should(Equal("a-b"))

			// returns substring of length 252
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
					Labels:    labelsCls123,
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
			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			sameScheduleTime := metav1.NewTime(time.Now().Add(-3 * time.Second))

			twoHourAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))

			sliceBackups := []veleroapi.Backup{
				*createBackup(veleroManagedClustersBackupName, veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					startTimestamp(oneHourAgo).
					errors(0).object,
				*createBackup(veleroResourcesBackupName, veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					startTimestamp(sameScheduleTime).
					errors(0).object,
				*createBackup(veleroCredentialsBackupName, veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					startTimestamp(twoHourAgo).
					errors(0).object,
				*createBackup(veleroManagedClustersBackupName+"-new", veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).object,
				*createBackup(veleroResourcesBackupName+"-new", veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).object,
				*createBackup(veleroCredentialsBackupName+"-new", veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).object,
				*createBackup("some-other-new", veleroNamespaceName).
					labels(labelsCls123).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).object,
			}

			backupsInError := filterBackups(sliceBackups, func(bkp veleroapi.Backup) bool {
				return strings.HasPrefix(bkp.Name, veleroScheduleNames[Credentials]) ||
					strings.HasPrefix(bkp.Name, veleroScheduleNames[ManagedClusters]) ||
					strings.HasPrefix(bkp.Name, veleroScheduleNames[Resources])
			})
			Expect(backupsInError).Should(Equal(sliceBackups[:6])) // don't return last backup

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

			Expect(shouldBackupAPIGroup("security.openshift.io")).Should(BeFalse())
			Expect(
				shouldBackupAPIGroup("proxy.open-cluster-management.io"),
			).Should(BeFalse())
			Expect(shouldBackupAPIGroup("discovery.open-cluster-management.io")).Should(BeTrue())
			Expect(shouldBackupAPIGroup("argoproj.io")).Should(BeTrue())
		})
	})
})

func Test_deleteBackup(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}
	scheme1 := runtime.NewScheme()
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}

	backup := *createBackup("backup1", "ns1").object

	type args struct {
		ctx    context.Context
		c      client.Client
		backup veleroapi.Backup
	}
	tests := []struct {
		name    string
		args    args
		err_nil bool
	}{
		{
			name: "no kind is registered for the type v1.DeleteBackupRequest, return error when asking for deleterequests",
			args: args{
				ctx:    context.Background(),
				c:      k8sClient1,
				backup: backup,
			},
			err_nil: false,
		},
		{
			name: "create DeleteBackupRequest request error, because ns ns1 not found",
			args: args{
				ctx:    context.Background(),
				c:      k8sClient1,
				backup: backup,
			},
			err_nil: false,
		},
		{
			name: "create DeleteBackupRequest request success, ns ns1 was found",
			args: args{
				ctx:    context.Background(),
				c:      k8sClient1,
				backup: *createBackup("backup2", "ns1").object,
			},
			err_nil: true,
		},
		{
			name: "delete backup exists, has no errors",
			args: args{
				ctx:    context.Background(),
				c:      k8sClient1,
				backup: *createBackup("backup2", "ns1").object,
			},
			err_nil: true,
		},
		{
			name: "delete backup exists, backup also exists - has no errors",
			args: args{
				ctx:    context.Background(),
				c:      k8sClient1,
				backup: *createBackup("backup2", "ns1").object,
			},
			err_nil: true,
		},
		{
			name: "delete backup exists, backup does no exists but request does and it has errors",
			args: args{
				ctx:    context.Background(),
				c:      k8sClient1,
				backup: *createBackup("backup-does-not-exist", "ns3").object,
			},
			err_nil: false,
		},
	}

	for index, tt := range tests {
		if index == 1 {
			// create ns so create calls pass through
			if err := veleroapi.AddToScheme(scheme1); err != nil {
				t.Errorf("err adding veleroapis to scheme: %s", err.Error())
			}
		}
		if index == 2 {
			// create ns so create calls pass through
			if err := corev1.AddToScheme(scheme1); err != nil {
				t.Errorf("err adding core apis to scheme: %s", err.Error())
			}
			if err := k8sClient1.Create(tt.args.ctx, createNamespace("ns1"), &client.CreateOptions{}); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}
			if err := k8sClient1.Create(tt.args.ctx,
				createBackup("backup1", "ns1").object, &client.CreateOptions{}); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}
		}
		if index == len(tests)-2 {
			// create the delete request to find one already
			if err := k8sClient1.Create(tt.args.ctx, createNamespace("ns2"), &client.CreateOptions{}); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}
			if err := k8sClient1.Create(tt.args.ctx,
				createBackup("backup2", "ns1").object, &client.CreateOptions{}); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}
		}
		if index == len(tests)-1 {
			// create the delete request to find one already
			if err := k8sClient1.Create(tt.args.ctx, createNamespace("ns3"), &client.CreateOptions{}); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}
			if err := k8sClient1.Create(tt.args.ctx,
				createBackupDeleteRequest("backup-does-not-exist", "ns3", "backup-does-not-exist").
					errors([]string{"err1", "err2"}).
					object, &client.CreateOptions{}); err != nil {
				t.Errorf("failed to create %s", err.Error())
			}
			t.Run(tt.name, func(t *testing.T) {
				if err := deleteBackup(tt.args.ctx, &tt.args.backup, tt.args.c); (err == nil) != tt.err_nil {
					t.Errorf("deleteBackup() returns no error = %v, want %v", err == nil, tt.err_nil)
				}
			})
		}

	}

	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}
}
