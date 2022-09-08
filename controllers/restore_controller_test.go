package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocinfrav1 "github.com/openshift/api/config/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

var _ = Describe("Basic Restore controller", func() {
	var (
		ctx                                context.Context
		veleroNamespace                    *corev1.Namespace
		veleroManagedClustersBackupName    string
		veleroResourcesBackupName          string
		veleroResourcesGenericBackupName   string
		veleroCredentialsBackupName        string
		veleroCredentialsHiveBackupName    string
		veleroCredentialsClusterBackupName string
		channels                           []chnv1.Channel
		clusterVersions                    []ocinfrav1.ClusterVersion

		acmNamespaceName         string
		restoreName              string
		veleroBackups            []veleroapi.Backup
		rhacmRestore             v1beta1.Restore
		managedClusterNamespaces []corev1.Namespace
		backupStorageLocation    *veleroapi.BackupStorageLocation

		skipRestore   string
		latestBackup  string
		invalidBackup string

		timeout  = time.Second * 10
		interval = time.Millisecond * 250

		includedResources = []string{
			"clusterdeployment",
			"placementrule.apps.open-cluster-management.io",
			"multiclusterobservability.observability.open-cluster-management.io",
			"channel.apps.open-cluster-management.io",
			"channel.cluster.open-cluster-management.io",
		}
	)

	JustBeforeEach(func() {

		existingChannels := &chnv1.ChannelList{}
		k8sClient.List(ctx, existingChannels, &client.ListOptions{})
		if len(existingChannels.Items) == 0 {
			for i := range channels {
				Expect(k8sClient.Create(ctx, &channels[i])).Should(Succeed())
			}

			for i := range clusterVersions {
				Expect(k8sClient.Create(ctx, &clusterVersions[i])).Should(Succeed())
			}
		}

		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())
		for i := range managedClusterNamespaces {
			Expect(k8sClient.Create(ctx, &managedClusterNamespaces[i])).Should((Succeed()))
		}
		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}

		if backupStorageLocation != nil {
			Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
			storageLookupKey := types.NamespacedName{
				Name:      backupStorageLocation.Name,
				Namespace: backupStorageLocation.Namespace,
			}
			if err := k8sClient.Get(ctx, storageLookupKey, backupStorageLocation); err == nil {
				backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
				Eventually(func() bool {
					err := k8sClient.
						Status().Update(ctx, backupStorageLocation, &client.UpdateOptions{})
					return err == nil
				}, timeout, interval).Should(BeTrue())
			}
			Expect(backupStorageLocation.Status.Phase).Should(BeIdenticalTo(veleroapi.BackupStorageLocationPhaseAvailable))
		}

		Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())
	})

	JustAfterEach(func() {
		if backupStorageLocation != nil {
			Expect(k8sClient.Delete(ctx, backupStorageLocation)).Should(Succeed())
		}
		var zero int64 = 0
		Expect(
			k8sClient.Delete(
				ctx,
				veleroNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())

		backupStorageLocation = nil
	})

	BeforeEach(func() { // default values
		ctx = context.Background()
		veleroManagedClustersBackupName = "acm-managed-clusters-schedule-20210910181336"
		veleroResourcesBackupName = "acm-resources-schedule-20210910181336"
		veleroResourcesGenericBackupName = "acm-resources-generic-schedule-20210910181346"
		veleroCredentialsBackupName = "acm-credentials-schedule-20210910181336"
		veleroCredentialsHiveBackupName = "acm-credentials-hive-schedule-20210910181336"
		veleroCredentialsClusterBackupName = "acm-credentials-cluster-schedule-20210910181336"
		skipRestore = "skip"
		latestBackup = "latest"
		invalidBackup = "invalid-backup-name"
		restoreName = "rhacm-restore-1"
		resourcesTimestamp, _ := time.Parse("20060102150405", "20210910181336")
		resourcesGenericTimestamp, _ := time.Parse("20060102150405", "20210910181346")
		resourcesStartTime := metav1.NewTime(resourcesTimestamp)
		resourcesGenericStartTime := metav1.NewTime(resourcesGenericTimestamp)
		unrelatedResourcesGenericTimestamp, _ := time.Parse("20060102150405", "20210910181420")
		unrelatedResourcesGenericStartTime := metav1.NewTime(unrelatedResourcesGenericTimestamp)

		clusterVersions = []ocinfrav1.ClusterVersion{
			*createClusterVersion("version-new-one", "aaa", map[string]string{
				"velero.io/backup-name": "backup-123",
			}),
		}

		channels = []chnv1.Channel{
			*createChannel("channel-from-backup", "default",
				chnv1.ChannelTypeHelmRepo, "http://test.svc.cluster.local:3000/charts").
				channelLabels(map[string]string{
					"velero.io/backup-name": "backup-123",
				}).object,
			*createChannel("channel-from-backup-with-finalizers", "default",
				chnv1.ChannelTypeHelmRepo, "http://test.svc.cluster.local:3000/charts").
				channelLabels(map[string]string{
					"velero.io/backup-name": "backup-123",
				}).
				channelFinalizers([]string{"finalizer1"}).object,
			*createChannel("channel-not-from-backup", "default",
				chnv1.ChannelTypeGit, "https://github.com/test/app-samples").object,
		}

		veleroNamespace = createNamespace("velero-restore-ns-1")
		backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
			setOwner().
			phase(veleroapi.BackupStorageLocationPhaseAvailable).object

		veleroBackups = []veleroapi.Backup{
			*createBackup(veleroManagedClustersBackupName, veleroNamespace.Name).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(backupManagedClusterResources).
				object,
			*createBackup(veleroResourcesBackupName, veleroNamespace.Name).
				startTimestamp(resourcesStartTime).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(includedResources).
				object,
			*createBackup(veleroResourcesGenericBackupName, veleroNamespace.Name).
				startTimestamp(resourcesGenericStartTime).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(includedResources).
				object,
			*createBackup("acm-resources-generic-schedule-20210910181420", veleroNamespace.Name).
				startTimestamp(unrelatedResourcesGenericStartTime).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(includedResources).
				object,
			*createBackup(veleroCredentialsBackupName, veleroNamespace.Name).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(backupCredsResources).
				object,
			*createBackup(veleroCredentialsHiveBackupName, veleroNamespace.Name).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(backupCredsResources).
				object,
			*createBackup(veleroCredentialsClusterBackupName, veleroNamespace.Name).
				phase(veleroapi.BackupPhaseCompleted).
				errors(0).includedResources(backupCredsResources).
				object,
		}

		managedClusterNamespaces = []corev1.Namespace{}
		rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
			cleanupBeforeRestore(v1beta1.CleanupTypeAll).syncRestoreWithNewBackups(true).
			restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
			veleroManagedClustersBackupName(veleroManagedClustersBackupName).
			veleroCredentialsBackupName(veleroCredentialsBackupName).
			veleroResourcesBackupName(veleroResourcesBackupName).object
	})

	Context("When creating a Restore with backup name", func() {
		It("Should creating a Velero Restore having non empty status", func() {
			restoreLookupKey := types.NamespacedName{
				Name:      restoreName,
				Namespace: veleroNamespace.Name,
			}
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restores in status")
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() int {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
					return 0
				}
				return len(veleroRestores.Items)
			}, timeout, interval).Should(Equal(6))
			backupNames := []string{
				veleroManagedClustersBackupName,
				veleroResourcesBackupName,
				veleroResourcesGenericBackupName,
				veleroCredentialsBackupName,
				veleroCredentialsHiveBackupName,
				veleroCredentialsClusterBackupName,
			}
			_, found := find(backupNames, veleroRestores.Items[0].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[1].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[2].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[3].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[4].Spec.BackupName)
			Expect(found).Should(BeTrue())
			_, found = find(backupNames, veleroRestores.Items[5].Spec.BackupName)
			Expect(found).Should(BeTrue())
		})
	})

	Context("When creating a Restore with backup names set to latest", func() {
		BeforeEach(func() {
			veleroNamespace = createNamespace("velero-restore-ns-2")
			backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object

			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeAll).syncRestoreWithNewBackups(true).
				restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(latestBackup).
				veleroResourcesBackupName(latestBackup).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			twoHoursAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
			threeHoursAgo := metav1.NewTime(time.Now().Add(-3 * time.Hour))
			fourHoursAgo := metav1.NewTime(time.Now().Add(-4 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup("acm-managed-clusters-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-managed-clusters-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-managed-clusters-schedule-not-completed-recent-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseFailed).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-managed-clusters-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
				//acm-resources backups
				*createBackup("acm-resources-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-resources-generic-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-resources-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-resources-schedule-not-completed-recent-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseFailed).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-resources-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
				*createBackup("acm-resources-generic-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
				// acm-credentials-schedule backups
				*createBackup("acm-credentials-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-credentials-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-credentials-schedule-not-completed-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseInProgress).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-credentials-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
				*createBackup("acm-credentials-hive-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-credentials-cluster-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
			}
		})
		It("Should select the most recent backups without errors", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestore := veleroapi.Restore{}
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-credentials-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-resources-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
		})

	})

	Context("When creating a Restore with sync option enabled and new backups available", func() {
		BeforeEach(func() {
			veleroNamespace = createNamespace("velero-restore-ns-9")
			backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object
			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeRestored).syncRestoreWithNewBackups(true).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(latestBackup).
				veleroResourcesBackupName(latestBackup).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			threeHoursAgo := metav1.NewTime(time.Now().Add(-3 * time.Hour))
			fourHoursAgo := metav1.NewTime(time.Now().Add(-4 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup("acm-managed-clusters-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-managed-clusters-schedule-not-completed-recent-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseFailed).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-managed-clusters-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
				// acm-resources-schedule backups
				*createBackup("acm-resources-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-resources-schedule-not-completed-recent-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseFailed).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-resources-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
				// acm-credentials-schedule backups
				*createBackup("acm-credentials-schedule-good-old-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(threeHoursAgo).
					object,
				*createBackup("acm-credentials-schedule-not-completed-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseInProgress).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-credentials-schedule-bad-old-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(10).startTimestamp(fourHoursAgo).
					object,
			}
		})
		It("Should sync with the most recent backups without errors", func() {
			createdRestore := v1beta1.Restore{}
			restoreLookupKey := types.NamespacedName{
				Name:      restoreName,
				Namespace: veleroNamespace.Name,
			}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).Should(BeIdenticalTo("rhacm-restore-1-acm-credentials-schedule-good-old-backup"))
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).Should(BeIdenticalTo("rhacm-restore-1-acm-resources-schedule-good-old-backup"))

			Eventually(func() v1beta1.RestorePhase {
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				Expect(err).NotTo(HaveOccurred())
				return createdRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseUnknown))

			veleroRestore := veleroapi.Restore{}
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-credentials-schedule-good-old-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-resources-schedule-good-old-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())

			twoHoursAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
			newVeleroBackups := []veleroapi.Backup{
				*createBackup("acm-credentials-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-resources-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-managed-clusters-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
				*createBackup("acm-resources-generic-schedule-good-recent-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(twoHoursAgo).
					object,
			}

			// create new backups to sync with
			for i := range newVeleroBackups {
				Expect(k8sClient.Create(ctx, &newVeleroBackups[i])).Should(Succeed())
			}

			Eventually(func() string {
				if err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore); err == nil {
					// update createdRestore status to Enabled
					createdRestore.Status.Phase = v1beta1.RestorePhaseEnabled
					Expect(k8sClient.Status().Update(ctx, &createdRestore)).Should(Succeed())
					return string(createdRestore.Status.Phase)
				}
				return "notset"
			}, timeout, interval).Should(BeIdenticalTo(v1beta1.RestorePhaseEnabled))

			// now trigger a resource update by setting sync option to true
			if err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore); err == nil {
				createdRestore.Spec.SyncRestoreWithNewBackups = true
				Expect(k8sClient.Update(ctx, &createdRestore)).Should(Succeed())
			}

			By("created restore should now contain new velero backup names in status")
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).Should(BeIdenticalTo("rhacm-restore-1-acm-credentials-schedule-good-recent-backup"))
			Eventually(func() string {
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).Should(BeIdenticalTo("rhacm-restore-1-acm-resources-schedule-good-recent-backup"))
			// check if new velero restores are created
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-credentials-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())
			Expect(
				k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: veleroNamespace.Name,
						Name:      restoreName + "-acm-resources-schedule-good-recent-backup",
					},
					&veleroRestore,
				),
			).ShouldNot(HaveOccurred())

			// create a restore resource to test the collision path when trying to create the same restore
			restoreResourceCollision := *createRestore("rhacm-restore-1-acm-resources-generic-schedule-good-old-backup", veleroNamespace.Name).
				backupName("acm-resources-schedule-good-old-backup").
				phase("Completed").
				object

			Expect(k8sClient.Create(ctx, &restoreResourceCollision)).Should(Succeed())

			Expect(createdRestore.Spec.VeleroManagedClustersBackupName).Should(Equal(&skipRestore))

			Eventually(func() string {
				if err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore); err == nil {
					// update createdRestore status to Enabled
					createdRestore.Status.Phase = v1beta1.RestorePhaseEnabled
					Expect(k8sClient.Status().Update(ctx, &createdRestore)).Should(Succeed())
					return string(createdRestore.Status.Phase)
				}
				return "notset"
			}, timeout, interval).Should(BeIdenticalTo(v1beta1.RestorePhaseEnabled))

			// now trigger a resource update by setting VeleroManagedClustersBackupName to latest
			// it should only restore the managed clusters and generic resources since
			// there is no new backup for resources and credentials
			if err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore); err == nil {
				createdRestore.Spec.VeleroManagedClustersBackupName = &latestBackup
				Expect(k8sClient.Update(ctx, &createdRestore)).Should(Succeed())
			}

		})
	})

	Context("When creating a Restore with backup names set to skip", func() {
		BeforeEach(func() {
			veleroNamespace = createNamespace("velero-restore-ns-3")
			backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object
			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeNone).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			veleroBackups = []veleroapi.Backup{}
		})
		It("Should skip restoring backups without errors", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restore in status")
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroManagedClustersRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroCredentialsRestoreName
			}, timeout, interval).Should(BeEmpty())
			Eventually(func() string {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).Should(BeEmpty())

			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 0
			}, timeout, interval).Should(BeTrue())
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				Expect(err).NotTo(HaveOccurred())
				return createdRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinished))

			// createdRestore above has RestorePhaseFinished status
			// the following restore should not be ignored
			rhacmRestoreNotIgnoredButError := *createACMRestore(restoreName+"not-ignored-but-invalid", veleroNamespace.Name).
				cleanupBeforeRestore("someInvalidValue").
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(latestBackup).object

			Expect(k8sClient.Create(ctx, &rhacmRestoreNotIgnoredButError)).Should(Succeed())
			notIgnoredRestoreErr := v1beta1.Restore{}
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName + "not-ignored-but-invalid",
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &notIgnoredRestoreErr)
				Expect(err).NotTo(HaveOccurred())
				return notIgnoredRestoreErr.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinishedWithErrors))

			// createdRestore above has RestorePhaseFinished status
			// the following restore should not be ignored
			rhacmRestoreNotIgnored := *createACMRestore(restoreName+"not-ignored", veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeNone).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			Expect(k8sClient.Create(ctx, &rhacmRestoreNotIgnored)).Should(Succeed())
			notIgnoredRestore := v1beta1.Restore{}
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName + "not-ignored",
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &notIgnoredRestore)
				Expect(err).NotTo(HaveOccurred())
				return notIgnoredRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinished))
		})
	})

	Context("When creating a Restore with even one invalid backup name", func() {
		BeforeEach(func() {
			veleroNamespace = createNamespace("velero-restore-ns-4")

			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeRestored).
				veleroManagedClustersBackupName(latestBackup).
				veleroCredentialsBackupName(invalidBackup).
				veleroResourcesBackupName(latestBackup).object

			backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				// acm-managed-clusters-schedule backups
				*createBackup("acm-managed-clusters-schedule-gold-backup", veleroNamespace.Name).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-resources-schedule-gold-backup", veleroNamespace.Name).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
			}
		})
		It("Should not create any restore", func() {
			veleroRestores := veleroapi.RestoreList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
					return false
				}
				return len(veleroRestores.Items) == 0
			}, timeout, interval).Should(BeTrue())
			createdRestore := v1beta1.Restore{}
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				Expect(err).NotTo(HaveOccurred())
				return createdRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
			Expect(
				createdRestore.Status.LastMessage,
			).Should(BeIdenticalTo("cannot find invalid-backup-name Velero Backup: Backup.velero.io \"invalid-backup-name\" not found"))

			// createdRestore above is has RestorePhaseError status
			// the following restore should be ignored
			rhacmRestoreIgnored := *createACMRestore(restoreName+"ignored", veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeNone).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			Expect(k8sClient.Create(ctx, &rhacmRestoreIgnored)).Should(Succeed())
			ignoredRestore := v1beta1.Restore{}
			Eventually(func() v1beta1.RestorePhase {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName + "ignored",
					Namespace: veleroNamespace.Name,
				}
				err := k8sClient.Get(ctx, restoreLookupKey, &ignoredRestore)
				Expect(err).NotTo(HaveOccurred())
				return ignoredRestore.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinishedWithErrors))
		})
	})

	Context("When creating a Restore in a ns different then velero ns", func() {
		BeforeEach(func() {
			veleroNamespace = createNamespace("velero-restore-ns-5")
			backupStorageLocation = createStorageLocation("default-5", veleroNamespace.Name).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object

			acmNamespaceName = "acm-ns-1"
			acmNamespace := createNamespace(acmNamespaceName)
			Expect(k8sClient.Create(ctx, acmNamespace)).Should(Succeed())

			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = *createACMRestore(restoreName+"-new", acmNamespaceName).
				cleanupBeforeRestore(v1beta1.CleanupTypeNone).
				veleroManagedClustersBackupName(latestBackup).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup("acm-managed-clusters-schedule-recent-backup", veleroNamespace.Name).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
			}

		})
		It(
			"Should not create any velero restore resources, restore object created in the wrong ns",
			func() {
				veleroRestores := veleroapi.RestoreList{}
				Eventually(func() bool {
					if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
						return false
					}
					return len(veleroRestores.Items) == 0
				}, timeout, interval).Should(BeTrue())
				createdRestore := v1beta1.Restore{}
				Eventually(func() v1beta1.RestorePhase {
					restoreLookupKey := types.NamespacedName{
						Name:      restoreName + "-new",
						Namespace: acmNamespaceName,
					}
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					Expect(err).NotTo(HaveOccurred())
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("Restore resource [acm-ns-1/rhacm-restore-1-new] " +
					"must be created in the velero namespace [velero-restore-ns-5]"))
			},
		)
	})

	Context("When BackupStorageLocation without OwnerReference is invalid", func() {
		BeforeEach(func() {
			veleroNamespace = createNamespace("velero-restore-ns-6")
			backupStorageLocation = createStorageLocation("default-6", veleroNamespace.Name).
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object

			veleroBackups = []veleroapi.Backup{}
			rhacmRestore = *createACMRestore(restoreName+"-new", veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeNone).
				veleroManagedClustersBackupName(latestBackup).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup("acm-managed-clusters-schedule-recent-backup", veleroNamespace.Name).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
			}
		})
		It(
			"Should not create any velero restore resources, BackupStorageLocation is invalid",
			func() {
				veleroRestores := veleroapi.RestoreList{}
				Eventually(func() bool {
					if err := k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name)); err != nil {
						return false
					}
					return len(veleroRestores.Items) == 0
				}, timeout, interval).Should(BeTrue())
				createdRestore := v1beta1.Restore{}
				Eventually(func() v1beta1.RestorePhase {
					restoreLookupKey := types.NamespacedName{
						Name:      restoreName + "-new",
						Namespace: veleroNamespace.Name,
					}
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					Expect(err).NotTo(HaveOccurred())
					return createdRestore.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("Backup storage location not available in namespace velero-restore-ns-6. " +
					"Check velero.io.BackupStorageLocation and validate storage credentials."))
			},
		)
	})

	Context("When creating a valid Restore, track the ACM restore status phases", func() {
		BeforeEach(func() {
			restoreName = "my-restore"
			veleroNamespace = createNamespace("velero-restore-ns-7")
			backupStorageLocation = createStorageLocation("default-5", veleroNamespace.Name).
				phase(veleroapi.BackupStorageLocationPhaseAvailable).
				setOwner().object

			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeAll).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(latestBackup).
				veleroResourcesBackupName(latestBackup).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup("acm-resources-schedule-good-very-recent-backup", veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
				*createBackup("acm-credentials-schedule-good-very-recent-backup", veleroNamespace.Name).
					includedResources(backupCredsResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
			}
		})

		It("Should track the status evolution", func() {
			createdRestore := v1beta1.Restore{}
			By("created restore should contain velero restores in status")
			Eventually(func() string {
				k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      restoreName,
						Namespace: veleroNamespace.Name,
					}, &createdRestore)
				return createdRestore.Status.VeleroResourcesRestoreName
			}, timeout, interval).ShouldNot(BeEmpty())

			veleroRestores := veleroapi.RestoreList{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero/v1",
					Kind:       "RestoreList",
				},
				Items: []veleroapi.Restore{
					*createRestore("acm-credentials-restore", veleroNamespace.Name).
						backupName("acm-credentials-backup").
						phase("").
						object,
					*createRestore("acm-resources-restore", veleroNamespace.Name).
						backupName("acm-resources-backup").
						phase(veleroapi.RestorePhaseCompleted).
						object,
				},
			}

			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseUnknown))

			veleroRestores.Items[0].Status.Phase = veleroapi.RestorePhaseNew
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseStarted))

			veleroRestores.Items[0].Status.Phase = veleroapi.RestorePhaseFailedValidation
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseError))

			veleroRestores.Items[0].Status.Phase = veleroapi.RestorePhaseFailed
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseError))

			veleroRestores.Items[0].Status.Phase = veleroapi.RestorePhaseInProgress
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseRunning))

			veleroRestores.Items[0].Status.Phase = veleroapi.RestorePhasePartiallyFailed
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseFinishedWithErrors))

			veleroRestores.Items[0].Status.Phase = veleroapi.RestorePhaseCompleted
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseFinished))

			// failing to create schedule, restore is running
			rhacmBackupScheduleErr := *createBackupSchedule("backup-sch-to-error-restore", veleroNamespace.Name).
				schedule("backup-schedule").
				veleroTTL(metav1.Duration{Duration: time.Hour * 72}).object

			Expect(k8sClient.Create(ctx, &rhacmBackupScheduleErr)).Should(Succeed())
			Eventually(func() v1beta1.SchedulePhase {
				k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      rhacmBackupScheduleErr.Name,
						Namespace: veleroNamespace.Name,
					}, &rhacmBackupScheduleErr)
				return rhacmBackupScheduleErr.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.SchedulePhaseFailedValidation))

			createdRestore.Spec.SyncRestoreWithNewBackups = true
			createdRestore.Spec.RestoreSyncInterval = metav1.Duration{Duration: time.Minute * 20}
			setRestorePhase(&veleroRestores, &createdRestore)
			Expect(
				createdRestore.Status.Phase,
			).Should(BeEquivalentTo(v1beta1.RestorePhaseEnabled))

			// cannot create another restore, one is enabled
			restoreFailing := *createACMRestore(restoreName+"-fail", veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeAll).syncRestoreWithNewBackups(true).
				restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(veleroCredentialsBackupName).
				veleroResourcesBackupName(veleroResourcesBackupName).object

			Expect(k8sClient.Create(ctx, &restoreFailing)).Should(Succeed())
			// one is already enabled
			Eventually(func() v1beta1.RestorePhase {
				k8sClient.Get(ctx,
					types.NamespacedName{
						Name:      restoreFailing.Name,
						Namespace: veleroNamespace.Name,
					}, &restoreFailing)
				return restoreFailing.Status.Phase
			}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinishedWithErrors))
			Expect(k8sClient.Delete(ctx, &restoreFailing)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, &rhacmBackupScheduleErr)).Should(Succeed())

		})

	})

	Context("When creating a Restore and skip resources", func() {
		BeforeEach(func() {
			restoreName = "my-restore"
			veleroNamespace = createNamespace("velero-restore-ns-8")
			backupStorageLocation = nil

			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeNone).
				veleroManagedClustersBackupName(skipRestore).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup("acm-managed-clusters-schedule-good-very-recent-backup", veleroNamespace.Name).
					includedResources(backupManagedClusterResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
			}
		})

		It(
			"Should not create any velero restore resources, BackupStorageLocation is unavailable",
			func() {
				createdRestore := v1beta1.Restore{}
				By("created restore should not contain velero restores in status")
				Eventually(func() string {
					k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					return createdRestore.Status.VeleroManagedClustersRestoreName
				}, timeout, interval).Should(BeEmpty())

				By("Checking ACM restore phase when velero restore is in error", func() {
					Eventually(func() v1beta1.RestorePhase {
						k8sClient.Get(ctx,
							types.NamespacedName{
								Name:      restoreName,
								Namespace: veleroNamespace.Name,
							}, &createdRestore)
						return createdRestore.Status.Phase
					}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
				})

				By("Checking ACM restore message", func() {
					Eventually(func() string {
						k8sClient.Get(ctx,
							types.NamespacedName{
								Name:      restoreName,
								Namespace: veleroNamespace.Name,
							}, &createdRestore)
						return createdRestore.Status.LastMessage
					}, timeout, interval).Should(BeIdenticalTo("velero.io.BackupStorageLocation resources not found. " +
						"Verify you have created a konveyor.openshift.io.Velero or oadp.openshift.io.DataProtectionApplications resource."))
				})

			},
		)

	})

	Context("When creating a Restore and no storage location is available", func() {
		BeforeEach(func() {
			restoreName = "my-restore"
			veleroNamespace = createNamespace("velero-restore-ns-99")
			backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
				setOwner().
				phase(veleroapi.BackupStorageLocationPhaseAvailable).object

			backupName := "acm-managed-clusters-schedule-good-very-recent-backup"
			rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
				cleanupBeforeRestore(v1beta1.CleanupTypeAll).
				veleroManagedClustersBackupName(backupName).
				veleroCredentialsBackupName(skipRestore).
				veleroResourcesBackupName(skipRestore).object

			oneHourAgo := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			veleroBackups = []veleroapi.Backup{
				*createBackup(backupName, veleroNamespace.Name).
					includedResources(includedResources).
					phase(veleroapi.BackupPhaseCompleted).
					errors(0).startTimestamp(oneHourAgo).
					object,
			}
		})

		It(
			"Should create velero restore resource with only managec clusters",
			func() {
				createdRestore := v1beta1.Restore{}
				By("created restore should not contain velero restores in status")
				Eventually(func() string {
					k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					return createdRestore.Status.VeleroManagedClustersRestoreName
				}, timeout, interval).ShouldNot(BeEmpty())

				By("Checking ACM restore phase when velero restore is in error", func() {
					Eventually(func() v1beta1.RestorePhase {
						k8sClient.Get(ctx,
							types.NamespacedName{
								Name:      restoreName,
								Namespace: veleroNamespace.Name,
							}, &createdRestore)
						return createdRestore.Status.Phase
					}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseUnknown))
				})

			},
		)

	})
})
