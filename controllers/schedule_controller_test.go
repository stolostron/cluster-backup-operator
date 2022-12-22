package controllers

import (
	"context"
	"time"

	"github.com/openshift/hive/apis/hive/v1/aws"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"

	ocinfrav1 "github.com/openshift/api/config/v1"
)

var _ = Describe("BackupSchedule controller", func() {

	var (
		ctx                     context.Context
		managedClusters         []clusterv1.ManagedCluster
		managedClustersAddons   []addonv1alpha1.ManagedClusterAddOn
		clusterDeployments      []hivev1.ClusterDeployment
		channels                []chnv1.Channel
		clusterPools            []hivev1.ClusterPool
		clusterVersions         []ocinfrav1.ClusterVersion
		backupStorageLocation   *veleroapi.BackupStorageLocation
		veleroBackups           []veleroapi.Backup
		veleroNamespaceName     string
		acmNamespaceName        string
		chartsv1NSName          string
		managedClusterNSName    string
		clusterPoolNSName       string
		veleroNamespace         *corev1.Namespace
		acmNamespace            *corev1.Namespace
		chartsv1NS              *corev1.Namespace
		clusterPoolNS           *corev1.Namespace
		aINS                    *corev1.Namespace
		managedClusterNS        *corev1.Namespace
		clusterPoolSecrets      []corev1.Secret
		clusterDeplSecrets      []corev1.Secret
		clusterDeploymentNSName string
		clusterDeploymentNS     *corev1.Namespace

		backupTimestamps = []string{
			"20210910181336",
			"20210910181337",
			"20210910181338",
		}

		backupScheduleName string = "the-backup-schedule-name"

		backupSchedule string = "0 */6 * * *"

		timeout  = time.Second * 9
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		veleroNamespaceName = "velero-ns"
		acmNamespaceName = "acm-ns"
		chartsv1NSName = "acm-channel-ns"
		managedClusterNSName = "managed1"
		clusterPoolNSName = "app"
		clusterDeploymentNSName = "vb-pool-fhbjs"

		clusterVersions = []ocinfrav1.ClusterVersion{
			*createClusterVersion("version", "aaa", nil),
		}
		managedClustersAddons = []addonv1alpha1.ManagedClusterAddOn{
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "addon.open-cluster-management.io/v1alpha1",
					Kind:       "ManagedClusterAddOn",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed1-addon",
					Namespace: managedClusterNSName,
				},
				Spec: addonv1alpha1.ManagedClusterAddOnSpec{
					InstallNamespace: "managed1",
				},
			},
		}

		managedClusters = []clusterv1.ManagedCluster{
			*createManagedCluster("local-cluster").object,
			*createManagedCluster(managedClusterNSName).object,
		}
		managedClusterNS = createNamespace(managedClusterNSName)
		chartsv1NS = createNamespace(chartsv1NSName)
		clusterPoolNS = createNamespace(clusterPoolNSName)
		aINS = createNamespace("openshift-machine-api")
		clusterDeploymentNS = createNamespace(clusterDeploymentNSName)
		veleroNamespace = createNamespace(veleroNamespaceName)
		acmNamespace = createNamespace(acmNamespaceName)

		clusterPoolSecrets = []corev1.Secret{
			*createSecret("app-prow-47-aws-creds", clusterPoolNSName,
				nil, nil, nil),
			*createSecret("auto-import", clusterPoolNSName,
				map[string]string{
					"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
				}, map[string]string{
					"expirationTimestamp":  "2024-08-05T15:25:34Z",
					"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
				}, nil),
			*createSecret("baremetal", clusterPoolNSName,
				map[string]string{
					"environment.metal3.io": "baremetal",
				}, nil, nil),
			*createSecret("ai-secret", clusterPoolNSName,
				map[string]string{
					"agent-install.openshift.io/watch": "true",
				}, nil, nil),
			*createSecret("baremetal-api-secret", "openshift-machine-api",
				map[string]string{
					"environment.metal3.io": "baremetal",
				}, nil, nil),
		}
		clusterDeplSecrets = []corev1.Secret{
			*createSecret(clusterDeploymentNSName+"-abcd", clusterDeploymentNSName,
				nil, nil, nil),
		}
		clusterPools = []hivev1.ClusterPool{
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "hive.openshift.io/v1",
					Kind:       "ClusterPool",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-prow-47",
					Namespace: clusterPoolNSName,
				},
				Spec: hivev1.ClusterPoolSpec{
					Platform: hivev1.Platform{
						AWS: &aws.Platform{
							Region: "us-east-2",
						},
					},
					Size:       4,
					BaseDomain: "dev06.red-chesterfield.com",
				},
			},
		}
		clusterDeployments = []hivev1.ClusterDeployment{
			{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "hive.openshift.io/v1",
					Kind:       "ClusterDeployment",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterDeploymentNSName,
					Namespace: clusterDeploymentNSName,
				},
				Spec: hivev1.ClusterDeploymentSpec{
					ClusterPoolRef: &hivev1.ClusterPoolReference{
						Namespace: clusterPoolNSName,
						PoolName:  clusterPoolNSName,
					},
					BaseDomain: "dev06.red-chesterfield.com",
				},
			},
		}
		channels = []chnv1.Channel{
			*createChannel("charts-v1", chartsv1NSName,
				chnv1.ChannelTypeHelmRepo, "http://test.svc.cluster.local:3000/charts").object,
			*createChannel("user-channel", "default",
				chnv1.ChannelTypeGit, "https://github.com/test/app-samples").object,
		}

		veleroBackups = []veleroapi.Backup{}
		oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
		aFewSecondsAgo := metav1.NewTime(time.Now().Add(-2 * time.Second))

		//create 3 sets of backups, for each timestamp
		for _, timestampStr := range backupTimestamps {

			for key, value := range veleroScheduleNames {

				backup := *createBackup(value+"-"+timestampStr, veleroNamespaceName).
					labels(map[string]string{
						"velero.io/schedule-name":  value,
						BackupScheduleClusterLabel: "abcd",
					}).
					phase(veleroapi.BackupPhaseCompleted).startTimestamp(aFewSecondsAgo).errors(0).
					object

				if key == ValidationSchedule {

					// mark it as expired
					backup.Spec.TTL = metav1.Duration{Duration: time.Second * 5}
					backup.Status.Expiration = &oneHourAgo

				}

				veleroBackups = append(veleroBackups, backup)
			}
		}

		// create some dummy backups
		veleroBackups = append(
			veleroBackups,
			*createBackup(veleroScheduleNames[Resources]+"-new", veleroNamespaceName).
				phase(veleroapi.BackupPhaseCompleted).startTimestamp(oneHourAgo).errors(0).
				object,
		)
	})

	AfterEach(func() {
		if managedClusterNS != nil {
			for i := range managedClustersAddons {
				Expect(k8sClient.Delete(ctx, &managedClustersAddons[i])).Should(Succeed())
			}
		}

		for i := range managedClusters {
			Expect(k8sClient.Delete(ctx, &managedClusters[i])).Should(Succeed())
		}
		for i := range channels {
			Expect(k8sClient.Delete(ctx, &channels[i])).Should(Succeed())
		}
		for i := range veleroBackups {
			Expect(k8sClient.Delete(ctx, &veleroBackups[i])).Should(Succeed())
		}
		Expect(k8sClient.Delete(ctx, backupStorageLocation)).Should(Succeed())

		var zero int64 = 0

		if clusterDeploymentNS != nil {

			for i := range clusterDeplSecrets {
				Expect(k8sClient.Delete(ctx, &clusterDeplSecrets[i])).Should(Succeed())
			}

			for i := range clusterDeployments {
				Expect(k8sClient.Delete(ctx, &clusterDeployments[i])).Should(Succeed())
			}

			Expect(
				k8sClient.Delete(
					ctx,
					clusterDeploymentNS,
					&client.DeleteOptions{GracePeriodSeconds: &zero},
				),
			).Should(Succeed())
		}

		if aINS != nil {
			Expect(k8sClient.Delete(ctx, aINS,
				&client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
		}

		if clusterPoolNS != nil {

			for i := range clusterPoolSecrets {
				if clusterPoolSecrets[i].Name == "auto-import" {
					// this should be already cleaned up by the MSA disabled function
					Expect(k8sClient.Delete(ctx, &clusterPoolSecrets[i])).ShouldNot(Succeed())
				} else {
					Expect(k8sClient.Delete(ctx, &clusterPoolSecrets[i])).Should(Succeed())
				}
			}
			for i := range clusterPools {
				Expect(k8sClient.Delete(ctx, &clusterPools[i])).Should(Succeed())
			}
			Expect(
				k8sClient.Delete(
					ctx,
					clusterPoolNS,
					&client.DeleteOptions{GracePeriodSeconds: &zero},
				),
			).Should(Succeed())
		}
		Expect(
			k8sClient.Delete(
				ctx,
				veleroNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())
		Expect(
			k8sClient.Delete(
				ctx,
				acmNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())
		Expect(
			k8sClient.Delete(
				ctx,
				chartsv1NS,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())
	})

	JustBeforeEach(func() {
		for i := range managedClusters {
			Expect(k8sClient.Create(ctx, &managedClusters[i])).Should(Succeed())
		}
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
		Expect(k8sClient.Create(ctx, acmNamespace)).Should(Succeed())
		Expect(k8sClient.Create(ctx, chartsv1NS)).Should(Succeed())

		if managedClusterNS != nil {
			Expect(k8sClient.Create(ctx, managedClusterNS)).Should(Succeed())

			for i := range managedClustersAddons {
				Expect(k8sClient.Create(ctx, &managedClustersAddons[i])).Should(Succeed())
			}
			for i := range clusterVersions {
				Expect(k8sClient.Create(ctx, &clusterVersions[i])).Should(Succeed())
			}

		}

		if clusterDeploymentNS != nil {
			Expect(k8sClient.Create(ctx, clusterDeploymentNS)).Should(Succeed())

			for i := range clusterDeplSecrets {
				Expect(k8sClient.Create(ctx, &clusterDeplSecrets[i])).Should(Succeed())
			}

			for i := range clusterDeployments {
				Expect(k8sClient.Create(ctx, &clusterDeployments[i])).Should(Succeed())
			}
		}

		if aINS != nil {
			Expect(k8sClient.Create(ctx, aINS)).Should(Succeed())

		}
		if clusterPoolNS != nil {
			Expect(k8sClient.Create(ctx, clusterPoolNS)).Should(Succeed())

			for i := range clusterPoolSecrets {
				Expect(k8sClient.Create(ctx, &clusterPoolSecrets[i])).Should(Succeed())
			}
			for i := range clusterPools {
				Expect(k8sClient.Create(ctx, &clusterPools[i])).Should(Succeed())
			}
		}

		for i := range channels {
			Expect(k8sClient.Create(ctx, &channels[i])).Should(Succeed())
		}

		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}

	})
	Context("When creating a BackupSchedule", func() {
		It("Should be creating a Velero Schedule updating the Status", func() {
			backupStorageLocation = createStorageLocation("default", veleroNamespaceName).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object
			Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
			storageLookupKey := types.NamespacedName{
				Name:      backupStorageLocation.Name,
				Namespace: backupStorageLocation.Namespace,
			}
			if err := k8sClient.Get(ctx, storageLookupKey, backupStorageLocation); err == nil {
				backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
				k8sClient.Status().Update(ctx, backupStorageLocation, &client.UpdateOptions{})
			}

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

			createdACMNamespace := corev1.Namespace{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: acmNamespaceName,
					Namespace: ""}, &createdACMNamespace); err != nil {
					return false
				}
				if createdACMNamespace.Status.Phase == "Active" {
					return true
				}
				return false
			}, timeout, interval).Should(BeTrue())

			rhacmBackupSchedule := *createBackupSchedule(backupScheduleName, veleroNamespaceName).
				schedule(backupSchedule).veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
				useManagedServiceAccount(true).
				managedServiceAccountTTL(metav1.Duration{Duration: time.Hour * 90}).
				object
			Expect(k8sClient.Create(ctx, &rhacmBackupSchedule)).Should(Succeed())

			backupLookupKey := types.NamespacedName{
				Name:      backupScheduleName,
				Namespace: veleroNamespaceName,
			}
			createdBackupSchedule := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// validate baremetal secret has backup annotation
			baremetalSecret := corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "baremetal",
					Namespace: clusterPoolNSName,
				}, &baremetalSecret)
				return err == nil &&
					baremetalSecret.GetLabels()["cluster.open-cluster-management.io/backup"] == "baremetal"
			}, timeout, interval).Should(BeTrue())

			// and the ones under openshift-machine-api dont
			baremetalSecretAPI := corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "baremetal-api-secret",
					Namespace: "openshift-machine-api",
				}, &baremetalSecretAPI)
				return err == nil &&
					baremetalSecretAPI.GetLabels()["cluster.open-cluster-management.io/backup"] == "baremetal"
			}, timeout, interval).Should(BeFalse())

			// validate auto-import secret secret has backup annotation
			// if the UseManagedServiceAccount is set to true
			autoImportSecret := corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "auto-import",
					Namespace: clusterPoolNSName,
				}, &autoImportSecret)
				return err == nil &&
					autoImportSecret.GetLabels()["cluster.open-cluster-management.io/backup"] == "msa"
			}, timeout, interval).Should(Equal(rhacmBackupSchedule.Spec.UseManagedServiceAccount))

			// validate AI secret has backup annotation
			secretAI := corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "ai-secret",
					Namespace: clusterPoolNSName,
				}, &secretAI)
				return err == nil &&
					secretAI.GetLabels()["cluster.open-cluster-management.io/backup"] == "agent-install"
			}, timeout, interval).Should(BeTrue())

			Expect(createdBackupSchedule.CreationTimestamp.Time).NotTo(BeNil())

			Expect(createdBackupSchedule.Spec.VeleroSchedule).Should(Equal(backupSchedule))

			Expect(
				createdBackupSchedule.Spec.VeleroTTL,
			).Should(Equal(metav1.Duration{Duration: time.Hour * 72}))

			By("created backup schedule should contain velero schedules in status")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return false
				}

				schedulesCreated := createdBackupSchedule.Status.VeleroScheduleCredentials != nil &&
					createdBackupSchedule.Status.VeleroScheduleManagedClusters != nil &&
					createdBackupSchedule.Status.VeleroScheduleResources != nil

				if schedulesCreated {
					// verify the acm charts channel ns is excluded
					_, chartsNSOK := find(
						createdBackupSchedule.Status.VeleroScheduleResources.Spec.Template.ExcludedNamespaces,
						chartsv1NSName,
					)
					return chartsNSOK
				}

				return schedulesCreated

			}, timeout, interval).Should(BeTrue())

			Expect(
				createdBackupSchedule.Status.VeleroScheduleResources.Spec.Schedule,
			).Should(Equal(backupSchedule))

			Expect(
				createdBackupSchedule.Status.VeleroScheduleResources.Spec.Template.TTL,
			).Should(Equal(metav1.Duration{Duration: time.Hour * 72}))

			// update schedule, it should NOT trigger velero schedules deletion
			createdBackupSchedule.Spec.VeleroTTL = metav1.Duration{Duration: time.Hour * 150}
			Expect(
				k8sClient.
					Update(context.Background(), &createdBackupSchedule, &client.UpdateOptions{}),
			).Should(Succeed())

			Eventually(func() metav1.Duration {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return metav1.Duration{Duration: time.Hour * 0}
				}
				return createdBackupSchedule.Spec.VeleroTTL
			}, timeout, interval).Should(BeIdenticalTo(metav1.Duration{Duration: time.Hour * 150}))

			// delete one schedule, it should trigger velero schedules recreation
			veleroSchedulesList := veleroapi.ScheduleList{}
			Eventually(func() bool {
				err := k8sClient.List(ctx, &veleroSchedulesList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			k8sClient.Delete(ctx, &veleroSchedulesList.Items[1])
			// count velero schedules, should be still len(veleroScheduleNames)
			Eventually(func() int {
				if err := k8sClient.List(ctx, &veleroSchedulesList, &client.ListOptions{}); err == nil {
					return len(veleroSchedulesList.Items)
				}
				return 0
			}, time.Second*65, interval).Should(BeNumerically("==", len(veleroScheduleNames)))

			// check that the velero schedules have now 150h for ttl
			Eventually(func() metav1.Duration {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return metav1.Duration{Duration: time.Hour * 0}
				}
				if createdBackupSchedule.Status.VeleroScheduleManagedClusters == nil {
					return metav1.Duration{Duration: time.Hour * 0}
				}
				return createdBackupSchedule.Status.VeleroScheduleManagedClusters.Spec.Template.TTL
			}, timeout, interval).Should(BeIdenticalTo(metav1.Duration{Duration: time.Hour * 150}))

			// count velero schedules, should be still len(veleroScheduleNames)
			Eventually(func() bool {
				err := k8sClient.List(ctx, &veleroSchedulesList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(veleroSchedulesList.Items)).To(BeNumerically("==", len(veleroScheduleNames)))
			//

			// new backup with no TTL
			backupScheduleNameNoTTL := backupScheduleName + "-nottl"
			rhacmBackupScheduleNoTTL := *createBackupSchedule(backupScheduleNameNoTTL, veleroNamespaceName).
				schedule(backupSchedule).
				object
			Expect(k8sClient.Create(ctx, &rhacmBackupScheduleNoTTL)).Should(Succeed())

			// execute a backup collission validation
			// first make sure the schedule is in enabled state
			if err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule); err == nil {
				createdBackupSchedule.Status.Phase = v1beta1.SchedulePhaseEnabled
				Eventually(func() bool {
					err := k8sClient.
						Status().Update(context.Background(), &createdBackupSchedule, &client.UpdateOptions{})
					return err == nil
				}, timeout, interval).Should(BeTrue())
			}
			Eventually(func() string {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return err.Error()
				}
				return string(createdBackupSchedule.Status.Phase)
			}, timeout, interval).Should(BeIdenticalTo(string(v1beta1.SchedulePhaseEnabled)))
			// then sleep 7 seconds to let the schedule timestamp be older then 5 sec from now
			// then update the schedule, which will try to create a new set of velero schedules
			// when the clusterID is checked, it is going to be (unkonwn) - since we have no cluster resource on test
			// and the previous schedules had used abcd as clusterId
			time.Sleep(time.Second * 7)
			// get the schedule again
			k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
			createdBackupSchedule.Spec.VeleroTTL = metav1.Duration{Duration: time.Hour * 50}
			Eventually(func() bool {
				err := k8sClient.Update(
					context.Background(),
					&createdBackupSchedule,
					&client.UpdateOptions{},
				)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			// schedule should be in backup collission because the latest schedules were using a
			// different clusterID, so they look as they are generated by another cluster
			Eventually(func() string {
				err := k8sClient.Get(ctx, backupLookupKey, &createdBackupSchedule)
				if err != nil {
					return "unknown"
				}
				return string(createdBackupSchedule.Status.Phase)
			}, time.Second*65, interval).Should(BeIdenticalTo(string(v1beta1.SchedulePhaseBackupCollision)))

			// new schedule backup
			backupScheduleName3 := backupScheduleName + "-3"
			backupSchedule3 := *createBackupSchedule(backupScheduleName3, veleroNamespaceName).
				schedule(backupSchedule).
				object
			Expect(k8sClient.Create(ctx, &backupSchedule3)).Should(Succeed())

			backupLookupKeyNoTTL := types.NamespacedName{
				Name:      backupScheduleNameNoTTL,
				Namespace: veleroNamespaceName,
			}
			createdBackupScheduleNoTTL := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyNoTTL, &createdBackupScheduleNoTTL)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(createdBackupScheduleNoTTL.CreationTimestamp.Time).NotTo(BeNil())

			Expect(
				createdBackupScheduleNoTTL.Spec.VeleroTTL,
			).Should(Equal(metav1.Duration{Duration: time.Second * 0}))

			Expect(createdBackupScheduleNoTTL.Spec.VeleroSchedule).Should(Equal(backupSchedule))

			// schedules cannot be created because there already some running from the above schedule
			By(
				"created backup schedule should NOT contain velero schedules, acm-credentials-schedule already exists error",
			)
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyNoTTL, &createdBackupScheduleNoTTL)
				if err != nil {
					return false
				}
				return createdBackupScheduleNoTTL.Status.VeleroScheduleCredentials != nil &&
					createdBackupScheduleNoTTL.Status.VeleroScheduleManagedClusters != nil &&
					createdBackupScheduleNoTTL.Status.VeleroScheduleResources != nil
			}, timeout, interval).ShouldNot(BeTrue())
			Eventually(func() v1beta1.SchedulePhase {
				err := k8sClient.Get(ctx, backupLookupKeyNoTTL, &createdBackupScheduleNoTTL)
				Expect(err).NotTo(HaveOccurred())
				return createdBackupScheduleNoTTL.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.SchedulePhaseFailed))
			Expect(
				createdBackupScheduleNoTTL.Status.LastMessage,
			).Should(ContainSubstring("already exists"))

			// backup not created in velero namespace, should fail validation
			acmBackupName := backupScheduleName
			rhacmBackupScheduleACM := *createBackupSchedule(acmBackupName, acmNamespaceName).
				schedule(backupSchedule).veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
				object
			Expect(k8sClient.Create(ctx, &rhacmBackupScheduleACM)).Should(Succeed())

			backupLookupKeyACM := types.NamespacedName{
				Name:      acmBackupName,
				Namespace: acmNamespaceName,
			}
			createdBackupScheduleACM := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyACM, &createdBackupScheduleACM)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By(
				"backup schedule in acm ns should be in failed validation status - since it must be in the velero ns",
			)
			Eventually(func() bool {
				err := k8sClient.Get(ctx, backupLookupKeyACM, &createdBackupScheduleACM)
				if err != nil {
					return false
				}
				return createdBackupScheduleACM.Status.Phase == v1beta1.SchedulePhaseFailedValidation
			}, timeout, interval).Should(BeTrue())
			Expect(
				createdBackupScheduleACM.Status.LastMessage,
			).Should(ContainSubstring("location is not available"))

			// backup with invalid cron job schedule, should fail validation
			invalidCronExpBackupName := backupScheduleName + "-invalid-cron-exp"
			invalidCronExpBackupScheduleACM := *createBackupSchedule(invalidCronExpBackupName, veleroNamespaceName).
				schedule("invalid-cron-exp").veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
				object
			Expect(k8sClient.Create(ctx, &invalidCronExpBackupScheduleACM)).Should(Succeed())

			backupLookupKeyInvalidCronExp := types.NamespacedName{
				Name:      invalidCronExpBackupName,
				Namespace: veleroNamespaceName,
			}
			createdBackupScheduleInvalidCronExp := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(
					ctx,
					backupLookupKeyInvalidCronExp,
					&createdBackupScheduleInvalidCronExp,
				)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By(
				"backup schedule with invalid cron exp should be in failed validation status",
			)
			Eventually(func() bool {
				err := k8sClient.Get(
					ctx,
					backupLookupKeyInvalidCronExp,
					&createdBackupScheduleInvalidCronExp,
				)
				if err != nil {
					return false
				}
				return createdBackupScheduleInvalidCronExp.Status.Phase == v1beta1.SchedulePhaseFailedValidation
			}, timeout, interval).Should(BeTrue())
			Expect(
				createdBackupScheduleInvalidCronExp.Status.LastMessage,
			).Should(ContainSubstring("invalid schedule: expected exactly 5 fields, found 1"))

			// update backup schedule with invalid exp to sth valid
			Eventually(func() bool {
				scheduleObj := createdBackupScheduleInvalidCronExp.DeepCopy()
				scheduleObj.Spec.VeleroSchedule = backupSchedule
				err := k8sClient.Update(ctx, scheduleObj, &client.UpdateOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())

			createdBackupScheduleValidCronExp := v1beta1.BackupSchedule{}
			Eventually(func() bool {
				err := k8sClient.Get(
					ctx,
					backupLookupKeyInvalidCronExp,
					&createdBackupScheduleValidCronExp,
				)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By(
				"backup schedule now with valid cron exp should pass cron exp validation",
			)
			Eventually(func() string {
				err := k8sClient.Get(
					ctx,
					backupLookupKeyInvalidCronExp,
					&createdBackupScheduleValidCronExp,
				)
				if err != nil {
					return ""
				}
				return createdBackupScheduleValidCronExp.Status.LastMessage
			}, timeout, interval).Should(ContainSubstring("already exists"))
			Expect(
				createdBackupScheduleValidCronExp.Spec.VeleroSchedule,
			).Should(BeIdenticalTo(backupSchedule))

			// count ACM ( NOT velero) schedules, should still be just 5
			// this nb is NOT the nb of velero backup schedules created from
			// the acm backupschedule object ( these velero schedules are len(veleroScheduleNames))
			// acmSchedulesList represents the ACM - BackupSchedule.cluster.open-cluster-management.io - schedules
			// created by the tests
			acmSchedulesList := v1beta1.BackupScheduleList{}
			Eventually(func() bool {
				err := k8sClient.List(ctx, &acmSchedulesList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(acmSchedulesList.Items)).To(BeNumerically(">=", 5))

			// count velero schedules
			veleroScheduleList := veleroapi.ScheduleList{}
			Eventually(func() bool {
				err := k8sClient.List(ctx, &veleroScheduleList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(veleroScheduleList.Items)).To(BeNumerically("==", len(veleroScheduleNames)))

			for i := range veleroScheduleList.Items {

				veleroSchedule := veleroScheduleList.Items[i]
				// validate resources schedule content
				if veleroSchedule.Name == "acm-resources-schedule" {
					Expect(findValue(veleroSchedule.Spec.Template.IncludedResources,
						"placement.cluster.open-cluster-management.io")).Should(BeTrue())
					Expect(findValue(veleroSchedule.Spec.Template.IncludedResources,
						"clusterdeployment.hive.openshift.io")).Should(BeFalse())
					Expect(findValue(
						veleroSchedule.Spec.Template.IncludedResources, //excludedGroup
						"managedclustermutators.admission.cluster.open-cluster-management.io",
					)).ShouldNot(BeTrue())
					Expect(findValue(veleroSchedule.Spec.Template.IncludedResources,
						"clusterpool.other.hive.openshift.io")).Should(BeFalse())

				} else
				// generic resources, using backup label
				if veleroSchedule.Name == "acm-resources-generic-schedule" {

					Expect(findValue(veleroSchedule.Spec.Template.ExcludedResources, //secrets are in the creds backup
						"secret")).Should(BeTrue())

					Expect(findValue(veleroSchedule.Spec.Template.ExcludedResources, // resources excluded from backup
						"clustermanagementaddon.addon.open-cluster-management.io")).Should(BeTrue())

					Expect(findValue(veleroSchedule.Spec.Template.ExcludedResources, //already in cluster resources backup
						"klusterletaddonconfig.agent.open-cluster-management.io")).Should(BeTrue())
					Expect(findValue(veleroSchedule.Spec.Template.ExcludedResources, // exclude this, part of mannged cluster
						"clusterpool.other.hive.openshift.io")).Should(BeTrue())

				} else
				// generic resources, using backup label
				if veleroSchedule.Name == "acm-managed-clusters-schedule" {

					Expect(findValue(veleroSchedule.Spec.Template.IncludedResources,
						"clusterdeployment.hive.openshift.io")).Should(BeTrue())
					//.other.hive.openshift.io included here
					Expect(findValue(veleroSchedule.Spec.Template.IncludedResources,
						"clusterpool.other.hive.openshift.io")).Should(BeTrue())

				}
			}

			// delete existing acm schedules
			for i := range acmSchedulesList.Items {
				Eventually(func() bool {
					scheduleObj := acmSchedulesList.Items[i].DeepCopy()
					err := k8sClient.Delete(ctx, scheduleObj)
					return err == nil
				}, timeout, interval).Should(BeTrue())
			}

			// acm schedules are 0 now
			Eventually(func() bool {
				err := k8sClient.List(ctx, &acmSchedulesList, &client.ListOptions{})
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(len(acmSchedulesList.Items)).To(BeNumerically("==", 0))

		})

	})

	Context("When BackupStorageLocation without OwnerReference is invalid", func() {
		var newVeleroNamespace = "velero-ns-new"
		var newAcmNamespace = "acm-ns-new"
		var newChartsv1NSName = "acm-channel-ns-new"

		oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))

		BeforeEach(func() {
			clusterPoolNS = nil
			aINS = nil
			clusterDeploymentNS = nil
			managedClusterNS = nil
			chartsv1NS = createNamespace(newChartsv1NSName)
			acmNamespace = createNamespace(newAcmNamespace)
			veleroNamespace = createNamespace(newVeleroNamespace)

			channels = []chnv1.Channel{
				*createChannel("charts-v1", newChartsv1NSName,
					chnv1.ChannelTypeHelmRepo, "http://test.svc.cluster.local:3000/charts").object,
			}
			veleroBackups = []veleroapi.Backup{
				*createBackup(veleroScheduleNames[Resources], newVeleroNamespace).
					phase(veleroapi.BackupPhaseCompleted).startTimestamp(oneHourAgo).errors(0).
					object,
			}
			backupStorageLocation = createStorageLocation("default-new", veleroNamespace.Name).
				phase(veleroapi.BackupStorageLocationPhaseUnavailable).object
		})
		It(
			"Should not create any velero schedule resources, BackupStorageLocation doesnt exist or is invalid",
			func() {
				rhacmBackupSchedule := *createBackupSchedule(backupScheduleName+"-new", newVeleroNamespace).
					schedule(backupSchedule).veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
					object

				Expect(k8sClient.Create(ctx, &rhacmBackupSchedule)).Should(Succeed())
				// there is no storage location object created
				veleroSchedules := veleroapi.ScheduleList{}
				Eventually(func() bool {
					if err := k8sClient.List(ctx, &veleroSchedules, client.InNamespace(newVeleroNamespace)); err != nil {
						return false
					}
					return len(veleroSchedules.Items) == 0
				}, timeout, interval).Should(BeTrue())
				createdSchedule := v1beta1.BackupSchedule{}
				Eventually(func() v1beta1.SchedulePhase {
					scheduleLookupKey := types.NamespacedName{
						Name:      backupScheduleName + "-new",
						Namespace: newVeleroNamespace,
					}
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					Expect(err).NotTo(HaveOccurred())
					return createdSchedule.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.SchedulePhaseFailedValidation))
				Expect(
					createdSchedule.Status.LastMessage,
				).Should(BeIdenticalTo("velero.io.BackupStorageLocation resources not found. " +
					"Verify you have created a konveyor.openshift.io.Velero or oadp.openshift.io.DataProtectionApplications resource."))

				// create the storage location now but in the wrong ns
				Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())

				storageLookupKey := types.NamespacedName{
					Name:      backupStorageLocation.Name,
					Namespace: backupStorageLocation.Namespace,
				}
				if err := k8sClient.Get(ctx, storageLookupKey, backupStorageLocation); err == nil {
					backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
					k8sClient.Status().Update(ctx, backupStorageLocation, &client.UpdateOptions{})

				}

				rhacmBackupScheduleNew := *createBackupSchedule(backupScheduleName+"-new-1", newVeleroNamespace).
					schedule(backupSchedule).veleroTTL(metav1.Duration{Duration: time.Hour * 72}).
					object
				Expect(k8sClient.Create(ctx, &rhacmBackupScheduleNew)).Should(Succeed())
				createdScheduleNew := v1beta1.BackupSchedule{}
				Eventually(func() v1beta1.SchedulePhase {
					scheduleLookupKey := types.NamespacedName{
						Name:      backupScheduleName + "-new-1",
						Namespace: newVeleroNamespace,
					}
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdScheduleNew)
					Expect(err).NotTo(HaveOccurred())
					return createdScheduleNew.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.SchedulePhaseFailedValidation))

				Expect(
					createdScheduleNew.Status.LastMessage,
				).Should(BeIdenticalTo("Backup storage location is not available. " +
					"Check velero.io.BackupStorageLocation and validate storage credentials."))
			},
		)
	})

})
