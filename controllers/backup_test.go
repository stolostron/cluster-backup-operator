package controllers

import (
	"context"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	corev1 "k8s.io/api/core/v1"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
)

func createNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func createSecret(name string, ns string,
	labels map[string]string,
	annotations map[string]string,
	data map[string][]byte) *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	if labels != nil {
		secret.Labels = labels
	}
	if annotations != nil {
		secret.Annotations = annotations
	}
	if data != nil {
		secret.Data = data
	}

	return secret

}

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
				veleroapi.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      veleroManagedClustersBackupName,
						Namespace: veleroNamespaceName,
						Labels:    labelsCls123,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						StartTimestamp: &oneHourAgo,
						Errors:         0,
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
						Labels:    labelsCls123,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						StartTimestamp: &sameScheduleTime,
						Errors:         0,
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
						Labels:    labelsCls123,
					},
					Spec: veleroapi.BackupSpec{
						IncludedNamespaces: []string{"please-keep-this-one"},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						StartTimestamp: &twoHourAgo,
						Errors:         0,
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
						Labels:    labelsCls123,
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
						Labels:    labelsCls123,
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
						Labels:    labelsCls123,
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
						Labels:    labelsCls123,
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
				shouldBackupAPIGroup("admission.cluster.open-cluster-management.io"),
			).Should(BeFalse())
			Expect(shouldBackupAPIGroup("discovery.open-cluster-management.io")).Should(BeTrue())
			Expect(shouldBackupAPIGroup("argoproj.io")).Should(BeTrue())
		})

	})
})

func Test_updateSecretsLabels(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	scheme1 := runtime.NewScheme()

	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})
	clusterv1.AddToScheme(scheme1)
	corev1.AddToScheme(scheme1)

	labelName := backupCredsClusterLabel
	labelValue := "clusterpool"
	clsName := "managed1"

	if err := k8sClient1.Create(context.Background(), createNamespace(clsName)); err != nil {
		t.Errorf("cannot create ns %s ", err.Error())
	}

	hiveSecrets := corev1.SecretList{
		Items: []corev1.Secret{
			*createSecret(clsName+"-import", clsName, map[string]string{
				labelName: labelValue,
			}, nil, nil), // do not back up, name is cls-import
			*createSecret(clsName+"-import-1", clsName, map[string]string{
				labelName: labelValue,
			}, nil, nil), // back it up
			*createSecret(clsName+"-import-2", clsName, nil, nil, nil),               // back it up
			*createSecret(clsName+"-bootstrap-test", clsName, nil, nil, nil),         // do not backup
			*createSecret(clsName+"-some-other-secret-test", clsName, nil, nil, nil), // backup
		},
	}

	type args struct {
		ctx     context.Context
		c       client.Client
		secrets corev1.SecretList
		prefix  string
		lName   string
		lValue  string
	}
	tests := []struct {
		name          string
		args          args
		backupSecrets []string // what should be backed up
	}{
		{
			name: "hive secrets 1",
			args: args{
				ctx:     context.Background(),
				c:       k8sClient1,
				secrets: hiveSecrets,
				lName:   labelName,
				lValue:  labelValue,
				prefix:  clsName,
			},
			backupSecrets: []string{"managed1-import-1", "managed1-import-2", "managed1-some-other-secret-test"},
		},
	}
	for _, tt := range tests {
		for index := range hiveSecrets.Items {
			if err := k8sClient1.Create(context.Background(), &hiveSecrets.Items[index]); err != nil {
				t.Errorf("cannot create %s ", err.Error())
			}
		}

		t.Run(tt.name, func(t *testing.T) {
			updateSecretsLabels(tt.args.ctx, k8sClient1,
				tt.args.secrets,
				tt.args.prefix,
				tt.args.lName,
				tt.args.lValue,
			)
		})

		result := []string{}
		for index := range hiveSecrets.Items {
			secret := hiveSecrets.Items[index]
			if err := k8sClient1.Get(context.Background(), types.NamespacedName{Name: secret.Name,
				Namespace: secret.Namespace}, &secret); err != nil {
				t.Errorf("cannot get secret %s ", err.Error())
			}
			if secret.GetLabels()[labelName] == labelValue {
				// it was backed up
				result = append(result, secret.Name)
			}
		}

		if !reflect.DeepEqual(result, tt.backupSecrets) {
			t.Errorf("updateSecretsLabels() = %v want %v", result, tt.backupSecrets)
		}
	}
	testEnv.Stop()

}
