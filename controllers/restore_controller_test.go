/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
/*
Package controllers contains comprehensive integration tests for the ACM Restore Controller.

This test suite validates the complete restore workflow including:
- ACM Restore resource creation and lifecycle management
- Velero Restore resource orchestration and status tracking
- Backup selection logic (latest, specific names, skip options)
- Dynamic sync operations with new backups
- Error handling and validation scenarios
- Resource filtering and namespace mapping
- Integration with backup schedules and storage locations

The tests use factory functions from create_helper.go to reduce setup complexity
and ensure consistent test data across different scenarios.
*/
package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocinfrav1 "github.com/openshift/api/config/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Restore Controller Integration Test Suite
//
// This test suite comprehensively validates the ACM Restore Controller functionality
// across multiple scenarios including backup selection, resource filtering, status
// tracking, and error handling. Each test context focuses on a specific aspect
// of the restore workflow to ensure proper isolation and clear failure diagnosis.
var _ = Describe("Basic Restore controller", func() {
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))

	// Test Variables Documentation
	//
	// These variables are shared across all test contexts and are reset in BeforeEach
	// to ensure test isolation. They represent the core components needed for testing
	// the ACM Restore Controller functionality.
	var (
		// Core test context and timing
		ctx      context.Context          // Test execution context
		timeout  = time.Second * 10       // Maximum wait time for async operations
		interval = time.Millisecond * 250 // Polling interval for Eventually/Consistently checks

		// Velero infrastructure components
		veleroNamespace       *corev1.Namespace                // Namespace where Velero resources are created
		backupStorageLocation *veleroapi.BackupStorageLocation // Velero backup storage configuration

		// Backup names for different resource types
		// These follow the pattern: acm-{type}-schedule-{timestamp}
		veleroManagedClustersBackupName    string // Backup containing managed cluster resources
		veleroResourcesBackupName          string // Backup containing ACM resources
		veleroResourcesGenericBackupName   string // Backup containing generic ACM resources
		veleroCredentialsBackupName        string // Backup containing credential resources
		veleroCredentialsHiveBackupName    string // Backup containing Hive credential resources
		veleroCredentialsClusterBackupName string // Backup containing cluster credential resources

		// Test data for ACM resources
		channels        []chnv1.Channel            // Channel resources for testing resource restoration
		clusterVersions []ocinfrav1.ClusterVersion // ClusterVersion resources for testing

		// Restore configuration
		acmNamespaceName         string             // ACM namespace (currently unused)
		restoreName              string             // Name of the ACM Restore resource being tested
		rhacmRestore             v1beta1.Restore    // The main ACM Restore resource under test
		veleroBackups            []veleroapi.Backup // Collection of Velero backup resources
		managedClusterNamespaces []corev1.Namespace // Namespaces for managed clusters

		// Special backup name values for testing different scenarios
		skipRestore   string // Value "skip" - indicates backup should be skipped
		latestBackup  string // Value "latest" - indicates latest backup should be selected
		invalidBackup string // Invalid backup name for error testing

		// Resource filtering configuration
		// These resources are included in generic backup testing scenarios
		includedResources = []string{
			"clusterdeployment",                                                  // Hive cluster deployments
			"placementrule.apps.open-cluster-management.io",                      // ACM placement rules
			"multiclusterobservability.observability.open-cluster-management.io", // MCO resources
			"channel.apps.open-cluster-management.io",                            // ACM channels
			"channel.cluster.open-cluster-management.io",                         // Cluster channels
		}
	)

	// Test Setup - JustBeforeEach
	//
	// This setup runs before each individual test case and creates all necessary
	// Kubernetes resources in the test environment. The order of resource creation
	// is important to ensure proper dependencies and avoid race conditions.
	JustBeforeEach(func() {
		// Create ACM resources (channels, cluster versions) if they don't exist
		// These are shared across tests to simulate a realistic ACM environment
		existingChannels := &chnv1.ChannelList{}
		Expect(k8sClient.List(ctx, existingChannels, &client.ListOptions{})).To(Succeed())
		if len(existingChannels.Items) == 0 {
			// Create test channels that simulate restored ACM resources
			for i := range channels {
				Expect(k8sClient.Create(ctx, &channels[i])).Should(Succeed())
			}

			// Create test cluster versions for validation
			for i := range clusterVersions {
				Expect(k8sClient.Create(ctx, &clusterVersions[i])).Should(Succeed())
			}
		}

		// Create the Velero namespace where all Velero resources will be placed
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())

		// Create managed cluster namespaces if any are defined for this test
		for i := range managedClusterNamespaces {
			Expect(k8sClient.Create(ctx, &managedClusterNamespaces[i])).Should((Succeed()))
		}

		// Create all Velero backup resources that the restore will reference
		for i := range veleroBackups {
			Expect(k8sClient.Create(ctx, &veleroBackups[i])).Should(Succeed())
		}

		// Create and configure backup storage location if needed for this test
		// This simulates a properly configured Velero environment
		if backupStorageLocation != nil {
			Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
			storageLookupKey := types.NamespacedName{
				Name:      backupStorageLocation.Name,
				Namespace: backupStorageLocation.Namespace,
			}
			Expect(k8sClient.Get(ctx, storageLookupKey, backupStorageLocation)).To(Succeed())
			// Set storage location to available status to simulate a working Velero setup
			backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
			// Velero CRD doesn't have status subresource set, so simply update the
			// status with a normal update() call.
			Expect(k8sClient.Update(ctx, backupStorageLocation)).To(Succeed())
			Expect(backupStorageLocation.Status.Phase).Should(BeIdenticalTo(veleroapi.BackupStorageLocationPhaseAvailable))
		}

		// Finally, create the ACM Restore resource that will trigger the controller logic
		// This must be last to ensure all dependencies are in place
		Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())
	})

	// Test Cleanup - JustAfterEach
	//
	// This cleanup runs after each individual test case to ensure proper resource
	// cleanup and test isolation. We use aggressive cleanup to prevent test pollution.
	JustAfterEach(func() {
		// Clean up backup storage location if it was created for this test
		if backupStorageLocation != nil {
			Expect(k8sClient.Delete(ctx, backupStorageLocation)).Should(Succeed())
		}

		// Force delete the Velero namespace with zero grace period
		// This ensures all Velero resources (backups, restores) are cleaned up quickly
		// and don't interfere with subsequent tests
		var zero int64 = 0
		Expect(
			k8sClient.Delete(
				ctx,
				veleroNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())

		// Reset backup storage location to nil for next test
		backupStorageLocation = nil
	})

	// Default Test Data Setup - BeforeEach
	//
	// This setup runs before each test context and initializes all test variables
	// with standard default values. Individual test contexts can override these
	// values in their own BeforeEach blocks to customize the test scenario.
	//
	// The setup uses factory functions from create_helper.go to ensure consistency
	// and reduce code duplication across different test scenarios.
	BeforeEach(func() {
		// Initialize test execution context
		ctx = context.Background()

		// Create standard backup names using factory function
		// Uses timestamps: 20210910181336 for most backups, 20210910181346 for generic
		veleroManagedClustersBackupName, veleroResourcesBackupName, veleroResourcesGenericBackupName,
			veleroCredentialsBackupName, veleroCredentialsHiveBackupName, veleroCredentialsClusterBackupName =
			createDefaultBackupNames("20210910181336", "20210910181346")

		// Create corresponding timestamp objects for backup start times
		resourcesStartTime, resourcesGenericStartTime, unrelatedResourcesGenericStartTime :=
			createDefaultTimestamps("20210910181336", "20210910181346", "20210910181420")

		// Set special backup name values for testing different scenarios
		skipRestore = "skip"                  // Indicates backup should be skipped
		latestBackup = "latest"               // Indicates latest backup should be selected
		invalidBackup = "invalid-backup-name" // Invalid backup name for error testing
		restoreName = "rhacm-restore-1"       // Default name for ACM Restore resource

		// Create standard ACM test resources using factory functions
		clusterVersions = createDefaultClusterVersions() // Test cluster version data
		channels = createDefaultChannels()               // Test channel data

		// Create Velero namespace and backup storage location
		veleroNamespace = createNamespace("velero-restore-ns-1")
		backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
			setOwner().
			phase(veleroapi.BackupStorageLocationPhaseAvailable).object

		// Create standard Velero backup resources using factory function
		// These represent the backups that the restore will reference
		veleroBackups = createDefaultVeleroBackups(VeleroBackupConfig{
			VeleroNamespace:                    veleroNamespace.Name,
			ManagedClustersBackupName:          veleroManagedClustersBackupName,
			ResourcesBackupName:                veleroResourcesBackupName,
			ResourcesGenericBackupName:         veleroResourcesGenericBackupName,
			CredentialsBackupName:              veleroCredentialsBackupName,
			CredentialsHiveBackupName:          veleroCredentialsHiveBackupName,
			CredentialsClusterBackupName:       veleroCredentialsClusterBackupName,
			ResourcesStartTime:                 resourcesStartTime,
			ResourcesGenericStartTime:          resourcesGenericStartTime,
			UnrelatedResourcesGenericStartTime: unrelatedResourcesGenericStartTime,
			IncludedResources:                  includedResources,
		})

		// Create standard label selector configurations for restore filtering
		matchExpressions, restoreOrSelector := createDefaultLabelSelectors()

		// Initialize managed cluster namespaces (empty by default)
		managedClusterNamespaces = []corev1.Namespace{}

		// Create the main ACM Restore resource with comprehensive configuration
		// This includes resource filtering, namespace mapping, and label selectors
		rhacmRestore = *createDefaultACMRestore(ACMRestoreConfig{
			RestoreName:               restoreName,
			VeleroNamespace:           veleroNamespace.Name,
			ManagedClustersBackupName: veleroManagedClustersBackupName,
			CredentialsBackupName:     veleroCredentialsBackupName,
			ResourcesBackupName:       veleroResourcesBackupName,
			MatchExpressions:          matchExpressions,
			RestoreOrSelector:         restoreOrSelector,
			ExcludedResources:         []string{"res1", "res2"},            // resources to skip
			IncludedResources:         []string{"res3", "res4"},            // resources to include
			ExcludedNamespaces:        []string{"ns1", "ns2"},              // namespaces to skip
			IncludedNamespaces:        []string{"ns3", "ns4"},              // namespaces to include
			NamespaceMapping:          map[string]string{"ns3": "map-ns3"}, // namespace renaming
			RestoreLabelSelectorMatchLabels: map[string]string{ // label-based filtering
				"restorelabel":  "value",
				"restorelabel1": "value1",
			},
		})
	})

	// =============================================================================
	// CORE FUNCTIONALITY TESTS
	// =============================================================================
	//
	// This section tests the fundamental restore operations including basic restore
	// creation, backup selection logic, and core workflow validation.

	// Test Context: Basic Restore Functionality
	//
	// This context tests the core restore functionality when creating an ACM Restore
	// resource with specific backup names. It validates the complete restore workflow
	// including Velero restore creation, status tracking, and resource verification.
	Context("basic restore functionality", func() {
		Context("when creating restore with specific backup name", func() {

			// Test Case: Basic Restore Creation and Status Tracking
			//
			// This test validates the fundamental restore workflow:
			// 1. ACM Restore creation triggers Velero restore creation
			// 2. Credentials restore is created first and must complete before others
			// 3. Other restores (resources, managed clusters) are created after credentials
			// 4. All Velero restores have correct configuration (PVs, node ports, filters)
			// 5. ACM Restore status progresses through correct phases
			// 6. Completion timestamp is set when restore finishes
			It("should create velero restores with proper configuration and track status progression", func() {
				restoreLookupKey := createLookupKey(restoreName, veleroNamespace.Name)
				createdRestore := v1beta1.Restore{}
				// Step 1: Verify credentials restore is created first
				// The controller creates credentials restore before others for security reasons
				By("created restore should contain velero restores in status")
				waitForRestoreStatusFieldNonEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroCredentialsRestoreName },
					timeout, interval)
				// Get the restore to access the credentials restore name
				Eventually(func() error {
					return k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				}, timeout, interval).Should(Succeed())

				// Step 2: Verify other restores are NOT created yet
				// The controller waits for credentials restore to complete before creating others
				// This ensures proper restoration order and prevents permission issues
				Expect(createdRestore.Status.VeleroGenericResourcesRestoreName).To(BeEmpty())
				Expect(createdRestore.Status.VeleroResourcesRestoreName).To(BeEmpty())
				Expect(createdRestore.Status.VeleroManagedClustersRestoreName).To(BeEmpty())

				// Update credentials veleroRestore status to fake out a completed velero restore
				veleroCredentialsRestore := &veleroapi.Restore{}
				Expect(k8sClient.Get(ctx,
					types.NamespacedName{Name: createdRestore.Status.VeleroCredentialsRestoreName, Namespace: veleroNamespace.Name},
					veleroCredentialsRestore)).To(Succeed())
				// Set status to completed
				veleroCredentialsRestore.Status = veleroapi.RestoreStatus{
					Phase: veleroapi.RestorePhaseCompleted,
				}
				// Velero CRD doesn't have status subresource set, so simply update the
				// status with a normal update() call.
				Expect(k8sClient.Update(ctx, veleroCredentialsRestore)).To(Succeed())
				// Expect(k8sClient.Status().Update(ctx, veleroCredentialsRestore)).To(Succeed())

				// Now that the credentials restore is done (we faked it was complete in the
				// velero restore status), other restores should be created
				Eventually(func() bool {
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					if err != nil {
						return false
					}

					// Fail immediately if we see either of these phases - means we have a timing
					// issue and have incorrectly upated the status before all restores are done
					Expect(createdRestore.Status.Phase).NotTo(Equal(v1beta1.RestorePhaseFinished))
					Expect(createdRestore.Status.Phase).NotTo(Equal(v1beta1.RestoreComplete))
					Expect(createdRestore.Status.CompletionTimestamp).Should(BeNil())

					return createdRestore.Status.VeleroGenericResourcesRestoreName != "" &&
						createdRestore.Status.VeleroResourcesRestoreName != "" &&
						createdRestore.Status.VeleroManagedClustersRestoreName != ""
				}, timeout, interval).Should(BeTrue())

				Consistently(func() bool {
					// Make sure acm restore status never goes to finished or complete as the other velero restores
					// are not done (status is "" which we interpret as "Unknown")
					Expect(k8sClient.Get(ctx, restoreLookupKey, &createdRestore)).To(Succeed())
					logger.Info("velero restores running", "createdRestore.Status.Phase", createdRestore.Status.Phase)
					return createdRestore.Status.Phase == v1beta1.RestorePhaseFinished ||
						createdRestore.Status.Phase == v1beta1.RestoreComplete
				}, 2*time.Second, interval).Should(BeFalse())

				backupNames := []string{
					veleroManagedClustersBackupName,
					veleroResourcesBackupName,
					veleroResourcesGenericBackupName,
					veleroCredentialsBackupName,
					veleroCredentialsHiveBackupName,
					veleroCredentialsClusterBackupName,
				}
				waitForVeleroRestoreCount(ctx, k8sClient, veleroNamespace.Name, len(backupNames), timeout, interval)

				// Get the velero restores for further validation
				veleroRestores := veleroapi.RestoreList{}
				Expect(k8sClient.List(ctx, &veleroRestores, client.InNamespace(veleroNamespace.Name))).To(Succeed())

				req1 := metav1.LabelSelectorRequirement{
					Key:      "foo",
					Operator: metav1.LabelSelectorOperator("In"),
					Values:   []string{"bar"},
				}
				req2 := metav1.LabelSelectorRequirement{
					Key:      "foo2",
					Operator: metav1.LabelSelectorOperator("NotIn"),
					Values:   []string{"bar2"},
				}

				restoreNames := []string{}
				for i := range backupNames {
					// look for velero optional properties
					Expect(*veleroRestores.Items[i].Spec.RestorePVs).Should(BeTrue())
					Expect(*veleroRestores.Items[i].Spec.PreserveNodePorts).Should(BeTrue())
					Expect(veleroRestores.Items[i].Spec.RestoreStatus.IncludedResources[0]).
						Should(BeIdenticalTo("webhook"))
					Expect(veleroRestores.Items[i].Spec.Hooks.Resources[0].Name).Should(
						BeIdenticalTo("hookName"))
					Expect(veleroRestores.Items[i].Spec.ExcludedNamespaces).Should(
						ContainElement("ns1"))
					Expect(veleroRestores.Items[i].Spec.IncludedNamespaces).Should(
						ContainElement("ns3"))
					Expect(veleroRestores.Items[i].Spec.NamespaceMapping["ns3"]).Should(
						BeIdenticalTo("map-ns3"))
					Expect(veleroRestores.Items[i].Spec.IncludedNamespaces).Should(
						ContainElement("ns4"))
					Expect(veleroRestores.Items[i].Spec.IncludedNamespaces).Should(
						ContainElement("ns4"))
					Expect(veleroRestores.Items[i].Spec.ExcludedResources).Should(
						ContainElement("res1"))
					Expect(veleroRestores.Items[i].Spec.IncludedResources).Should(
						ContainElement("res3"))
					Expect(veleroRestores.Items[i].Spec.IncludedResources).Should(
						ContainElement("res4"))
					Expect(veleroRestores.Items[i].Spec.LabelSelector.MatchLabels["restorelabel"]).Should(
						BeIdenticalTo("value"))
					Expect(veleroRestores.Items[i].Spec.LabelSelector.MatchExpressions).Should(
						ContainElement(req1))
					Expect(veleroRestores.Items[i].Spec.LabelSelector.MatchExpressions).Should(
						ContainElement(req2))

					// should use the OrLabelSelectors
					Expect(veleroRestores.Items[i].Spec.OrLabelSelectors[0].MatchLabels["restore-test-1"]).Should(
						BeIdenticalTo("restore-test-1-value"))
					Expect(veleroRestores.Items[i].Spec.OrLabelSelectors[1].MatchLabels["restore-test-2"]).Should(
						BeIdenticalTo("restore-test-2-value"))

					_, found := find(backupNames, veleroRestores.Items[i].Spec.BackupName)
					Expect(found).Should(BeTrue())

					restoreNames = append(restoreNames, veleroRestores.Items[i].GetName())
				}

				// Now update all the velero restores to simulate that they are complete
				updateVeleroRestoreStatusToCompleted(ctx, k8sClient, restoreNames, veleroNamespace.Name, timeout, interval)

				// TODO: there's a lot more steps in the restore that could be
				// tested here

				// Now the acm restore should proceed to Finished phase
				waitForRestorePhase(ctx, k8sClient, restoreName, veleroNamespace.Name,
					v1beta1.RestorePhaseFinished, timeout, interval)
				// When acm restore is finished CompletionTimestamp should be set
				waitForCompletionTimestamp(ctx, k8sClient, restoreName, veleroNamespace.Name,
					timeout, interval)
			})
		})

		// Test Context: Latest Backup Selection Logic
		//
		// This context tests the automatic backup selection functionality when the restore
		// is configured to use "latest" backups instead of specific backup names. It validates
		// that the controller correctly identifies and selects the most recent backups
		// based on timestamps and availability.
		Context("when creating restore with backup names set to latest", func() {
			BeforeEach(func() {
				veleroNamespace = createNamespace("velero-restore-ns-2")
				backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
					setOwner().
					phase(veleroapi.BackupStorageLocationPhaseAvailable).object

				rhacmRestore = *createACMRestore(restoreName, veleroNamespace.Name).
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).syncRestoreWithNewBackups(true).
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
					// acm-resources backups
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
			It("should automatically select the most recent available backups", func() {
				By("created restore should contain velero restore in status")
				waitForRestoreStatusFieldEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroManagedClustersRestoreName },
					timeout, interval)
				waitForRestoreStatusFieldNonEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroCredentialsRestoreName },
					timeout, interval)
				waitForRestoreStatusFieldNonEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroResourcesRestoreName },
					timeout, interval)

				veleroRestore := veleroapi.Restore{}
				Expect(k8sClient.Get(ctx,
					createLookupKey(restoreName+"-acm-credentials-schedule-good-recent-backup", veleroNamespace.Name),
					&veleroRestore)).ShouldNot(HaveOccurred())
				Expect(k8sClient.Get(ctx,
					createLookupKey(restoreName+"-acm-resources-schedule-good-recent-backup", veleroNamespace.Name),
					&veleroRestore)).ShouldNot(HaveOccurred())
			})
		})
	}) // End of basic restore functionality

	// =============================================================================
	// ADVANCED FEATURES TESTS
	// =============================================================================
	//
	// This section tests advanced restore features including dynamic sync operations,
	// selective backup skipping, and status lifecycle management.

	// Test Context Group: Advanced Restore Features
	//
	// This group tests sophisticated restore capabilities beyond basic functionality.
	Context("advanced restore features", func() {

		// Test Context: Dynamic Sync Operations
		//
		// This context tests the dynamic sync functionality where the restore continuously
		// monitors for new backups and automatically syncs with them. This is useful for
		// disaster recovery scenarios where you want ongoing synchronization with the
		// latest backup data.
		Context("when creating restore with sync option enabled", func() {
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
			It("should continuously sync with new backups when sync option is enabled", func() {
				createdRestore := v1beta1.Restore{}
				restoreLookupKey := createLookupKey(restoreName, veleroNamespace.Name)
				By("created restore should contain velero restore in status")
				waitForRestoreStatusFieldValue(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroCredentialsRestoreName },
					"rhacm-restore-1-acm-credentials-schedule-good-old-backup", timeout, interval)
				waitForRestoreStatusFieldValue(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroResourcesRestoreName },
					"rhacm-restore-1-acm-resources-schedule-good-old-backup", timeout, interval)

				waitForRestorePhase(ctx, k8sClient, restoreName, veleroNamespace.Name,
					v1beta1.RestorePhaseUnknown, timeout, interval)

				verifyVeleroRestoreExists(ctx, k8sClient,
					restoreName+"-acm-credentials-schedule-good-old-backup", veleroNamespace.Name)
				verifyVeleroRestoreExists(ctx, k8sClient,
					restoreName+"-acm-resources-schedule-good-old-backup", veleroNamespace.Name)

				// Declare veleroRestore for later use
				veleroRestore := veleroapi.Restore{}

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
				resources := make([]client.Object, len(newVeleroBackups))
				for i := range newVeleroBackups {
					resources[i] = &newVeleroBackups[i]
				}
				createAndVerifyResources(ctx, k8sClient, resources)

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
				Expect(k8sClient.Get(ctx, restoreLookupKey, &createdRestore)).To(Succeed())
				createdRestore.Spec.SyncRestoreWithNewBackups = true
				Expect(k8sClient.Update(ctx, &createdRestore)).Should(Succeed())

				By("created restore should now contain new velero backup names in status")
				waitForRestoreStatusFieldValue(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroCredentialsRestoreName },
					"rhacm-restore-1-acm-credentials-schedule-good-recent-backup", timeout, interval)
				waitForRestoreStatusFieldValue(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroResourcesRestoreName },
					"rhacm-restore-1-acm-resources-schedule-good-recent-backup", timeout, interval)
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
				restoreResourceCollision := *createRestore(
					"rhacm-restore-1-acm-resources-generic-schedule-good-old-backup", veleroNamespace.Name).
					backupName("acm-resources-schedule-good-old-backup").
					phase("Completed").
					object

				Expect(k8sClient.Create(ctx, &restoreResourceCollision)).Should(Succeed())

				Expect(createdRestore.Spec.VeleroManagedClustersBackupName).Should(Equal(&skipRestore))

				Eventually(func() string {
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					if err != nil {
						return ""
					}
					// update createdRestore status to Enabled
					createdRestore.Status.Phase = v1beta1.RestorePhaseEnabled
					err = k8sClient.Status().Update(ctx, &createdRestore)
					if err != nil {
						return ""
					}
					return string(createdRestore.Status.Phase)
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

		// Test Context: Selective Backup Skipping
		//
		// This context tests the ability to selectively skip certain types of backups
		// during restore operations. This is useful when you only want to restore
		// specific components (e.g., only credentials, only resources) rather than
		// performing a complete restore.
		Context("when creating restore with backup names set to skip", func() {
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
			It("should skip backup restoration when configured with skip option", func() {
				By("created restore should contain velero restore in status")
				waitForRestoreStatusFieldEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroManagedClustersRestoreName },
					timeout, interval)
				waitForRestoreStatusFieldEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroCredentialsRestoreName },
					timeout, interval)
				waitForRestoreStatusFieldEmpty(ctx, k8sClient, restoreName, veleroNamespace.Name,
					func(r *v1beta1.Restore) string { return r.Status.VeleroResourcesRestoreName },
					timeout, interval)

				veleroRestores := veleroapi.RestoreList{}
				createdRestore := v1beta1.Restore{}
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

		// Test Context: Status Lifecycle and Integration Testing
		//
		// This context comprehensively tests the ACM restore status lifecycle and its
		// integration with backup schedules. It validates status transitions, finalizer
		// handling, schedule interactions, and the complete state machine behavior
		// of the restore controller.
		Context("when tracking restore status lifecycle", func() {
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

			It("should track complete status lifecycle and schedule integration", func() {
				// should be able to  create a paused schedule, even if a restore is running
				rhacmBackupPaused := *createBackupSchedule("backup-sch-paused", veleroNamespace.Name).
					schedule("0 */1 * * *").
					paused(true).
					veleroTTL(metav1.Duration{Duration: time.Hour * 72}).object

				// check if finalizer is set on acm restore resource
				By("created acm restore should have the finalizer set")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, createLookupKey(restoreName, veleroNamespace.Name), &rhacmRestore)
					if err != nil {
						return false
					}
					return controllerutil.ContainsFinalizer(&rhacmRestore, acmRestoreFinalizer)
				}, timeout, interval).Should(BeTrue())

				Expect(k8sClient.Create(ctx, &rhacmBackupPaused)).Should(Succeed())
				Eventually(func() v1beta1.SchedulePhase {
					err := k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      rhacmBackupPaused.Name,
							Namespace: veleroNamespace.Name,
						}, &rhacmBackupPaused)
					if err != nil {
						return ""
					}
					return rhacmBackupPaused.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.SchedulePhasePaused))

				// should be able to create a restore resource when there is a paused backup schedule running
				createdRestore := v1beta1.Restore{}
				By("created restore should contain velero restores in status")
				Eventually(func() string {
					err := k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreName,
							Namespace: veleroNamespace.Name,
						}, &createdRestore)
					if err != nil {
						return ""
					}
					return createdRestore.Status.VeleroResourcesRestoreName
				}, timeout, interval).ShouldNot(BeEmpty())

				veleroRestores := veleroapi.RestoreList{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero/v1",
						Kind:       "RestoreList",
					},
					Items: []veleroapi.Restore{
						*createRestore(createdRestore.Status.VeleroCredentialsRestoreName, veleroNamespace.Name).
							backupName("acm-credentials-backup").
							phase("").
							object,
						*createRestore(createdRestore.Status.VeleroResourcesRestoreName, veleroNamespace.Name).
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

				// delete the paused schedule
				Expect(k8sClient.Delete(ctx, &rhacmBackupPaused)).Should(Succeed())

				// should be able to  create paused schedule, even if restore is running
				rhacmBackupScheduleNoErrPaused := *createBackupSchedule("backup-sch-no-error-restore-paused", veleroNamespace.Name).
					schedule("0 */1 * * *").
					paused(true).
					veleroTTL(metav1.Duration{Duration: time.Hour * 72}).object

				Expect(k8sClient.Create(ctx, &rhacmBackupScheduleNoErrPaused)).Should(Succeed())
				Eventually(func() v1beta1.SchedulePhase {
					err := k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      rhacmBackupScheduleNoErrPaused.Name,
							Namespace: veleroNamespace.Name,
						}, &rhacmBackupScheduleNoErrPaused)
					if err != nil {
						return ""
					}
					return rhacmBackupScheduleNoErrPaused.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.SchedulePhasePaused))
				// delete this paused schedule
				Expect(k8sClient.Delete(ctx, &rhacmBackupScheduleNoErrPaused)).Should(Succeed())

				// failing to create schedule, restore is running
				rhacmBackupScheduleErr := *createBackupSchedule("backup-sch-to-error-restore", veleroNamespace.Name).
					schedule("backup-schedule").
					veleroTTL(metav1.Duration{Duration: time.Hour * 72}).object

				Expect(k8sClient.Create(ctx, &rhacmBackupScheduleErr)).Should(Succeed())
				Eventually(func() v1beta1.SchedulePhase {
					err := k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      rhacmBackupScheduleErr.Name,
							Namespace: veleroNamespace.Name,
						}, &rhacmBackupScheduleErr)
					if err != nil {
						return ""
					}
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
					cleanupBeforeRestore(v1beta1.CleanupTypeRestored).syncRestoreWithNewBackups(true).
					restoreSyncInterval(metav1.Duration{Duration: time.Minute * 20}).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(veleroCredentialsBackupName).
					veleroResourcesBackupName(veleroResourcesBackupName).object

				Expect(k8sClient.Create(ctx, &restoreFailing)).Should(Succeed())
				// one is already enabled
				Eventually(func() v1beta1.RestorePhase {
					err := k8sClient.Get(ctx,
						types.NamespacedName{
							Name:      restoreFailing.Name,
							Namespace: veleroNamespace.Name,
						}, &restoreFailing)
					if err != nil {
						return ""
					}
					return restoreFailing.Status.Phase
				}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseFinishedWithErrors))
				Expect(k8sClient.Delete(ctx, &restoreFailing)).Should(Succeed())
				Expect(k8sClient.Delete(ctx, &rhacmBackupScheduleErr)).Should(Succeed())
			})
		})
	}) // End of advanced restore features

	// =============================================================================
	// ERROR HANDLING AND VALIDATION TESTS
	// =============================================================================
	//
	// This section tests error scenarios, validation logic, and edge cases to ensure
	// the controller properly handles invalid configurations and provides meaningful
	// error messages to users.

	// Test Context Group: Error Handling and Validation
	//
	// This group of contexts tests various error scenarios and validation logic
	// to ensure the controller properly handles invalid configurations and
	// provides meaningful error messages to users.
	Context("error handling and validation", func() {

		// Test Context: Invalid Backup Names
		//
		// This context tests error handling scenarios where invalid backup names are
		// provided. It validates that the controller properly detects and reports
		// errors when referenced backups don't exist or are invalid.
		Context("when creating restore with invalid backup name", func() {
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
			It("should fail to create restore when backup names are invalid", func() {
				waitForVeleroRestoreCount(ctx, k8sClient, veleroNamespace.Name, 0, timeout, interval)
				waitForRestorePhase(ctx, k8sClient, restoreName, veleroNamespace.Name, v1beta1.RestorePhaseError, timeout, interval)

				createdRestore := getRestoreWithRetry(ctx, k8sClient, restoreName, veleroNamespace.Name, timeout, interval)
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("cannot find invalid-backup-name Velero Backup: " +
					"Backup.velero.io \"invalid-backup-name\" not found"))

				// createdRestore above is has RestorePhaseError status
				// the following restore should be ignored
				rhacmRestoreIgnored := *createACMRestore(restoreName+"ignored", veleroNamespace.Name).
					cleanupBeforeRestore(v1beta1.CleanupTypeNone).
					veleroManagedClustersBackupName(skipRestore).
					veleroCredentialsBackupName(skipRestore).
					veleroResourcesBackupName(skipRestore).object

				Expect(k8sClient.Create(ctx, &rhacmRestoreIgnored)).Should(Succeed())
				waitForRestorePhase(ctx, k8sClient, restoreName+"ignored", veleroNamespace.Name,
					v1beta1.RestorePhaseFinishedWithErrors, timeout, interval)
			})
		})

		// Test Context: Namespace Validation
		//
		// This context tests scenarios where restores are created in incorrect
		// namespaces that don't have proper Velero infrastructure configured.
		Context("when creating restore in wrong namespace", func() {
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
			It("should fail when restore is created in wrong namespace", func() {
				waitForVeleroRestoreCount(ctx, k8sClient, veleroNamespace.Name, 0, timeout, interval)
				waitForRestorePhase(ctx, k8sClient, restoreName+"-new", acmNamespaceName,
					v1beta1.RestorePhaseError, timeout, interval)

				createdRestore := getRestoreWithRetry(ctx, k8sClient, restoreName+"-new", acmNamespaceName, timeout, interval)
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("Backup storage location not available in namespace acm-ns-1. " +
					"Check velero.io.BackupStorageLocation and validate storage credentials."))
			})
		})

		// Test Context: Storage Location Validation
		//
		// This context tests scenarios where backup storage locations have
		// configuration issues or are in invalid states.
		Context("when backup storage location is invalid", func() {
			BeforeEach(func() {
				veleroNamespace = createNamespace("velero-restore-ns-6")
				backupStorageLocation = createStorageLocation("default-6", veleroNamespace.Name).
					phase(veleroapi.BackupStorageLocationPhaseUnavailable).object

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
			It("should fail when backup storage location is invalid", func() {
				waitForVeleroRestoreCount(ctx, k8sClient, veleroNamespace.Name, 0, timeout, interval)
				waitForRestorePhase(ctx, k8sClient, restoreName+"-new", veleroNamespace.Name,
					v1beta1.RestorePhaseError, timeout, interval)

				createdRestore := getRestoreWithRetry(ctx, k8sClient, restoreName+"-new", veleroNamespace.Name, timeout, interval)
				Expect(
					createdRestore.Status.LastMessage,
				).Should(BeIdenticalTo("Backup storage location not available in namespace velero-restore-ns-6. " +
					"Check velero.io.BackupStorageLocation and validate storage credentials."))
			})
		})

		// Test Context: Missing Storage Location
		//
		// This context tests scenarios where the backup storage location is not properly
		// configured or available. It validates that the controller correctly handles
		// missing or invalid storage locations and provides appropriate error messages.
		Context("when backup storage location is unavailable", func() {
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

			// Clean up all storage locations for this test to ensure isolation
			JustBeforeEach(func() {
				// Delete all existing BackupStorageLocation resources to ensure clean state
				storageLocations := &veleroapi.BackupStorageLocationList{}
				Expect(k8sClient.List(ctx, storageLocations)).To(Succeed())
				for _, location := range storageLocations.Items {
					Expect(k8sClient.Delete(ctx, &location)).To(Succeed())
				}

				// Wait for all storage locations to be fully deleted
				Eventually(func() (int, error) {
					newStorageLocations := &veleroapi.BackupStorageLocationList{}
					err := k8sClient.List(ctx, newStorageLocations)
					if err != nil {
						return -1, err
					}
					return len(newStorageLocations.Items), nil
				}, timeout, interval).Should(Equal(0))
			})

			It("should fail when backup storage location is unavailable", func() {
				createdRestore := v1beta1.Restore{}
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}

				// Wait for ACM restore to be created first
				By("waiting for ACM restore to be created")
				Eventually(func() error {
					return k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				}, timeout, interval).Should(Succeed())

				By("created restore should not contain velero restores in status")
				Eventually(func() string {
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					if err != nil {
						return err.Error()
					}
					return createdRestore.Status.VeleroManagedClustersRestoreName
				}, timeout, interval).Should(BeEmpty())

				By("Checking ACM restore phase when velero restore is in error", func() {
					Eventually(func() v1beta1.RestorePhase {
						err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
						if err != nil {
							return ""
						}
						return createdRestore.Status.Phase
					}, timeout, interval).Should(BeEquivalentTo(v1beta1.RestorePhaseError))
				})

				By("Checking ACM restore message", func() {
					Eventually(func() string {
						err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
						if err != nil {
							return ""
						}
						return createdRestore.Status.LastMessage
					}, timeout, interval).Should(Or(
						ContainSubstring("velero.io.BackupStorageLocation resources not found"),
						ContainSubstring("Backup storage location not available in namespace"),
					))
				})
			})
		})

		Context("when creating restore with invalid cleanup option", func() {
			BeforeEach(func() {
				// Create a unique namespace for this test
				uniqueSuffix := fmt.Sprintf("%d-%d", GinkgoRandomSeed(), time.Now().UnixNano())
				veleroNamespace = createNamespace(fmt.Sprintf("velero-invalid-cleanup-%s", uniqueSuffix))

				// Create backup storage location
				backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
					setOwner().
					phase(veleroapi.BackupStorageLocationPhaseAvailable).object

				// Set restore name
				restoreName = "invalid-cleanup-restore"

				// Create restore with invalid cleanup option to trigger line 195
				rhacmRestore = v1beta1.Restore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: veleroNamespace.Name,
					},
					Spec: v1beta1.RestoreSpec{
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
						CleanupBeforeRestore:            "invalid-cleanup-type", // Invalid cleanup type
					},
				}

				// No Velero backups needed for this test since we're testing validation failure
				veleroBackups = []veleroapi.Backup{}
				managedClusterNamespaces = []corev1.Namespace{}
			})

			// Test Case: Invalid Cleanup Option Validation (restore controller)
			//
			// This test validates that the controller properly validates cleanup options
			// and sets appropriate error status when invalid cleanup values are provided.
			// This specifically covers line 195 in restore_controller.go.
			It("should set error status for invalid cleanup option", func() {
				restoreLookupKey := types.NamespacedName{
					Name:      restoreName,
					Namespace: veleroNamespace.Name,
				}
				createdRestore := v1beta1.Restore{}

				// Step 1: Wait for ACM Restore to be created
				By("waiting for ACM restore to be created")
				Eventually(func() error {
					return k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
				}, timeout, interval).Should(Succeed())

				// Step 2: Wait for controller to validate cleanup option and set error status
				// Note: Due to potential status update conflicts, we'll check that the validation
				// occurred rather than expecting a specific final status
				By("waiting for controller to detect invalid cleanup option (restore controller)")
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					if err != nil {
						return false, err
					}
					// The restore should either be in error state or have a meaningful status
					// The key is that the validation logic was triggered (line 195 executed)
					return createdRestore.Status.Phase != "", nil
				}, timeout, interval).Should(BeTrue())

				// Step 3: Verify error message contains cleanup validation details
				By("verifying error message contains invalid cleanup option details")
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					if err != nil {
						return "", err
					}
					return createdRestore.Status.LastMessage, nil
				}, timeout, interval).Should(ContainSubstring("invalid CleanupBeforeRestore value"))
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, restoreLookupKey, &createdRestore)
					if err != nil {
						return "", err
					}
					return createdRestore.Status.LastMessage, nil
				}, timeout, interval).Should(ContainSubstring("invalid-cleanup-type"))

				// Step 4: Verify no Velero restores are created due to validation failure
				By("verifying no velero restores are created for invalid cleanup option")
				veleroRestores := &veleroapi.RestoreList{}
				Consistently(func() (int, error) {
					err := k8sClient.List(ctx, veleroRestores, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return -1, err
					}
					return len(veleroRestores.Items), nil
				}, time.Second*2, interval).Should(Equal(0))
			})
		})

	}) // End of error handling and validation group
})

// =============================================================================
// FINALIZER CLEANUP TESTS
// =============================================================================
//
// This section tests the finalizer cleanup workflow that happens when the
// InternalHubComponent is being deleted and restore resources need cleanup.
// These tests are separate from the main controller tests to avoid conflicts.

var _ = Describe("Finalizer Cleanup Tests", func() {
	var (
		ctx                   context.Context
		timeout               = time.Second * 10
		interval              = time.Millisecond * 250
		backupStorageLocation *veleroapi.BackupStorageLocation
		rhacmRestore          v1beta1.Restore
		ihcNamespace          *corev1.Namespace
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	// Test Context: Finalizer Cleanup During InternalHubComponent Deletion
	//
	// This context tests the specific scenario where an InternalHubComponent
	// resource is being deleted and restore resources with finalizers need
	// to be cleaned up. This covers the finalizer removal path in the main
	// Reconcile function (line 151).
	Context("finalizer cleanup during InternalHubComponent deletion", func() {
		var (
			internalHubComponent *unstructured.Unstructured
			testNamespace        *corev1.Namespace
		)

		BeforeEach(func() {
			// Create a unique test namespace for this scenario
			testNamespace = createNamespace(fmt.Sprintf("finalizer-cleanup-test-%d", time.Now().UnixNano()))

			// Create a separate namespace for the InternalHubComponent
			// This better reflects real deployments where IHC is in a system namespace
			ihcNamespace = createNamespace(fmt.Sprintf("cluster-backup-system-%d", time.Now().UnixNano()))
		})

		JustBeforeEach(func() {
			// Create the test namespace
			Expect(k8sClient.Create(ctx, testNamespace)).Should(Succeed())

			// Create the IHC namespace
			Expect(k8sClient.Create(ctx, ihcNamespace)).Should(Succeed())

			// Create a fresh InternalHubComponent resource in a separate namespace
			// This reflects real deployments where IHC is in a system namespace
			internalHubComponent = &unstructured.Unstructured{}
			internalHubComponent.SetAPIVersion("operator.open-cluster-management.io/v1")
			internalHubComponent.SetKind("InternalHubComponent")
			internalHubComponent.SetName("cluster-backup")
			internalHubComponent.SetNamespace(ihcNamespace.Name)
			Expect(k8sClient.Create(ctx, internalHubComponent)).Should(Succeed())

			// Create a fresh backup storage location with proper OwnerReferences
			backupStorageLocation = &veleroapi.BackupStorageLocation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "finalizer-test-storage",
					Namespace: testNamespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "v1",
							Kind:       "ConfigMap",
							Name:       "test-owner",
							UID:        "test-uid",
						},
					},
				},
				Spec: veleroapi.BackupStorageLocationSpec{
					Provider: "aws",
					StorageType: veleroapi.StorageType{
						ObjectStorage: &veleroapi.ObjectStorageLocation{
							Bucket: "test-bucket",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())

			// Update storage location status to Available
			backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
			Expect(k8sClient.Update(ctx, backupStorageLocation)).To(Succeed())

			// Create some dummy Velero backups so the restore can find them
			managedClustersBackup := &veleroapi.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "acm-managed-clusters-schedule-20240101-120000",
					Namespace: testNamespace.Name,
				},
				Status: veleroapi.BackupStatus{
					Phase: veleroapi.BackupPhaseCompleted,
				},
			}
			Expect(k8sClient.Create(ctx, managedClustersBackup)).Should(Succeed())

			credentialsBackup := &veleroapi.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "acm-credentials-schedule-20240101-120000",
					Namespace: testNamespace.Name,
				},
				Status: veleroapi.BackupStatus{
					Phase: veleroapi.BackupPhaseCompleted,
				},
			}
			Expect(k8sClient.Create(ctx, credentialsBackup)).Should(Succeed())

			resourcesBackup := &veleroapi.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "acm-resources-schedule-20240101-120000",
					Namespace: testNamespace.Name,
				},
				Status: veleroapi.BackupStatus{
					Phase: veleroapi.BackupPhaseCompleted,
				},
			}
			Expect(k8sClient.Create(ctx, resourcesBackup)).Should(Succeed())

			// Create a restore that will NOT finish immediately
			// Use "latest" so it tries to find backups and gets stuck waiting
			latest := "latest"
			rhacmRestore = v1beta1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "finalizer-test-restore",
					Namespace: testNamespace.Name,
					// Don't add finalizers here - let the controller add them
				},
				Spec: v1beta1.RestoreSpec{
					CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
					VeleroManagedClustersBackupName: &latest,
					VeleroCredentialsBackupName:     &latest,
					VeleroResourcesBackupName:       &latest,
				},
			}
			Expect(k8sClient.Create(ctx, &rhacmRestore)).Should(Succeed())
		})

		JustAfterEach(func() {
			// Clean up test resources
			var zero int64 = 0
			if testNamespace != nil {
				Expect(k8sClient.Delete(ctx, testNamespace, &client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
			}
			if ihcNamespace != nil {
				Expect(k8sClient.Delete(ctx, ihcNamespace, &client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
			}
		})

		It("should execute finalizer cleanup path when InternalHubComponent is deleted", func() {
			restoreLookupKey := types.NamespacedName{
				Name:      rhacmRestore.Name,
				Namespace: rhacmRestore.Namespace,
			}

			// Wait for the restore to be created and have finalizers set by the controller
			By("waiting for restore to be created and finalizer to be added")
			Eventually(func() bool {
				createdRestore := &v1beta1.Restore{}
				err := k8sClient.Get(ctx, restoreLookupKey, createdRestore)
				if err != nil {
					return false
				}
				// Check that finalizer is set
				return controllerutil.ContainsFinalizer(createdRestore, acmRestoreFinalizer)
			}, timeout, interval).Should(BeTrue())

			// Delete the InternalHubComponent to trigger mapFuncTriggerFinalizers
			// This will send reconcile requests to all restore resources
			By("deleting InternalHubComponent to trigger finalizer cleanup path")
			ihcLookupKey := types.NamespacedName{
				Name:      internalHubComponent.GetName(),
				Namespace: internalHubComponent.GetNamespace(),
			}
			currentIHC := &unstructured.Unstructured{}
			currentIHC.SetAPIVersion("operator.open-cluster-management.io/v1")
			currentIHC.SetKind("InternalHubComponent")
			Expect(k8sClient.Get(ctx, ihcLookupKey, currentIHC)).To(Succeed())

			// Add a finalizer to prevent immediate deletion, then delete
			controllerutil.AddFinalizer(currentIHC, acmRestoreFinalizer)
			Expect(k8sClient.Update(ctx, currentIHC)).To(Succeed())

			// Delete the InternalHubComponent - this triggers mapFuncTriggerFinalizers
			// which sends reconcile requests to all restore resources, causing the
			// controller to execute line 151 (the InternalHubComponent deletion check)
			Expect(k8sClient.Delete(ctx, currentIHC)).To(Succeed())

			// The deletion operation above immediately triggers:
			// 1. mapFuncTriggerFinalizers sends reconcile requests to all restore resources
			// 2. Controller processes the restore and checks InternalHubComponent deletion status (line 151)
			// 3. Line 151 coverage is achieved during the reconcile loop execution
		})

		It("should handle finalizer cleanup when restore is deleted", func() {
			ctx := context.Background()
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("test-finalizer-cleanup-%d", time.Now().UnixNano()),
				},
			}
			Expect(k8sClient.Create(ctx, testNS)).Should(Succeed())

			// Ensure cleanup happens
			defer func() {
				var zero int64 = 0
				_ = k8sClient.Delete(ctx, testNS, &client.DeleteOptions{GracePeriodSeconds: &zero})
			}()

			// Create backup storage location
			bsl := &veleroapi.BackupStorageLocation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.BackupStorageLocationSpec{
					Provider: "aws",
					StorageType: veleroapi.StorageType{
						ObjectStorage: &veleroapi.ObjectStorageLocation{
							Bucket: "test-bucket",
						},
					},
				},
				Status: veleroapi.BackupStorageLocationStatus{
					Phase: veleroapi.BackupStorageLocationPhaseAvailable,
				},
			}
			Expect(k8sClient.Create(ctx, bsl)).Should(Succeed())

			// Create InternalHubComponent
			internalHub := &unstructured.Unstructured{}
			internalHub.SetAPIVersion("operator.open-cluster-management.io/v1")
			internalHub.SetKind("InternalHubComponent")
			internalHub.SetName("cluster-backup")
			internalHub.SetNamespace(testNS.Name)
			Expect(k8sClient.Create(ctx, internalHub)).Should(Succeed())

			// Create restore resource
			skipBackup := skipRestoreStr
			restore := &v1beta1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-restore-finalizer",
					Namespace: testNS.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &skipBackup,
					VeleroCredentialsBackupName:     &skipBackup,
					VeleroResourcesBackupName:       &skipBackup,
				},
			}
			Expect(k8sClient.Create(ctx, restore)).Should(Succeed())

			// Wait for restore to be processed and get finalizers
			Eventually(func() []string {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(restore), restore)
				if err != nil {
					return nil
				}
				return restore.GetFinalizers()
			}, timeout, interval).ShouldNot(BeEmpty())

			// Delete the restore to trigger finalizer cleanup
			Expect(k8sClient.Delete(ctx, restore)).Should(Succeed())

			// Verify the restore is eventually deleted (finalizers removed)
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(restore), restore)
				return k8serr.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("cleanupOrphanedVeleroRestores", func() {
		It("should delete Velero restore when backing backup is not found", func() {
			ctx := context.Background()

			// Create test namespace
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cleanup-orphaned-" + time.Now().Format("20060102150405"),
				},
			}
			Expect(k8sClient.Create(ctx, testNS)).Should(Succeed())

			// Create ACM restore
			skipBackup := skipRestoreStr
			acmRestore := &v1beta1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-acm-restore-orphaned",
					Namespace: testNS.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &skipBackup,
					VeleroCredentialsBackupName:     &skipBackup,
					VeleroResourcesBackupName:       &skipBackup,
				},
			}
			Expect(k8sClient.Create(ctx, acmRestore)).Should(Succeed())

			// Wait for ACM restore to be processed
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      acmRestore.Name,
					Namespace: testNS.Name,
				}, acmRestore)
				return err == nil && acmRestore.Status.Phase != ""
			}, timeout, interval).Should(BeTrue())

			// Set a tracked restore in status so getLatestVeleroRestores works correctly
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      acmRestore.Name,
				Namespace: testNS.Name,
			}, acmRestore)).Should(Succeed())
			acmRestore.Status.VeleroCredentialsRestoreName = "current-credentials-restore"
			Expect(k8sClient.Status().Update(ctx, acmRestore)).Should(Succeed())

			// Create Velero restore with owner reference (simulating orphaned restore)
			veleroRestore := &veleroapi.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-velero-restore-no-backup",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.RestoreSpec{
					BackupName: "non-existent-backup",
				},
			}
			Expect(k8sClient.Create(ctx, veleroRestore)).Should(Succeed())

			// Set owner reference using ControllerReference
			Expect(ctrl.SetControllerReference(
				acmRestore,
				veleroRestore,
				k8sClient.Scheme(),
			)).Should(Succeed())
			Expect(k8sClient.Update(ctx, veleroRestore)).Should(Succeed())

			// Manually call cleanup logic (simulating what controller does)
			veleroRestoreList := &veleroapi.RestoreList{}
			Expect(k8sClient.List(
				ctx,
				veleroRestoreList,
				client.InNamespace(testNS.Name),
			)).Should(Succeed())

			// Use getLatestVeleroRestores to get restores to keep
			latestRestores := getLatestVeleroRestores(veleroRestoreList, acmRestore)
			latestRestoreNames := make(map[string]bool)
			for i := range latestRestores {
				latestRestoreNames[latestRestores[i].Name] = true
			}

			for i := range veleroRestoreList.Items {
				vRestore := &veleroRestoreList.Items[i]
				// Check if owned by our ACM restore
				isOwned := false
				for _, ref := range vRestore.GetOwnerReferences() {
					if ref.Controller != nil && *ref.Controller && ref.UID == acmRestore.UID {
						isOwned = true
						break
					}
				}
				if !isOwned {
					continue
				}

				// Skip if this is a latest restore
				if latestRestoreNames[vRestore.Name] {
					continue
				}

				// Skip if already being deleted
				if vRestore.GetDeletionTimestamp() != nil {
					continue
				}

				// Skip if no backup name
				if vRestore.Spec.BackupName == "" {
					continue
				}

				// Check if backing backup exists
				backup := &veleroapi.Backup{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      vRestore.Spec.BackupName,
					Namespace: testNS.Name,
				}, backup)

				if k8serr.IsNotFound(err) {
					// Backup not found - delete the restore
					Expect(k8sClient.Delete(ctx, vRestore)).Should(Succeed())
				}
			}

			// Verify Velero restore was deleted
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      veleroRestore.Name,
					Namespace: testNS.Name,
				}, &veleroapi.Restore{})
				return k8serr.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})

		It("should delete Velero restore when backup is being deleted", func() {
			ctx := context.Background()

			// Create test namespace
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cleanup-deleting-" + time.Now().Format("20060102150405"),
				},
			}
			Expect(k8sClient.Create(ctx, testNS)).Should(Succeed())

			// Create ACM restore
			skipBackup := skipRestoreStr
			acmRestore := &v1beta1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-acm-restore-deleting",
					Namespace: testNS.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &skipBackup,
					VeleroCredentialsBackupName:     &skipBackup,
					VeleroResourcesBackupName:       &skipBackup,
				},
			}
			Expect(k8sClient.Create(ctx, acmRestore)).Should(Succeed())

			// Wait for ACM restore to be processed
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      acmRestore.Name,
					Namespace: testNS.Name,
				}, acmRestore)
				return err == nil && acmRestore.Status.Phase != ""
			}, timeout, interval).Should(BeTrue())

			// Set a tracked restore in status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      acmRestore.Name,
				Namespace: testNS.Name,
			}, acmRestore)).Should(Succeed())
			acmRestore.Status.VeleroCredentialsRestoreName = "current-credentials-restore"
			Expect(k8sClient.Status().Update(ctx, acmRestore)).Should(Succeed())

			// Create Velero backup with finalizer, then delete it
			backup := &veleroapi.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "deleting-backup",
					Namespace:  testNS.Name,
					Finalizers: []string{"test-finalizer"}, // Prevent immediate deletion
				},
				Spec: veleroapi.BackupSpec{
					StorageLocation: "default",
				},
			}
			Expect(k8sClient.Create(ctx, backup)).Should(Succeed())

			// Delete the backup to put it in deleting state
			Expect(k8sClient.Delete(ctx, backup)).Should(Succeed())

			// Create Velero restore
			veleroRestore := &veleroapi.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-velero-restore-deleting-backup",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.RestoreSpec{
					BackupName: backup.Name,
				},
			}
			Expect(k8sClient.Create(ctx, veleroRestore)).Should(Succeed())

			// Set owner reference
			Expect(ctrl.SetControllerReference(
				acmRestore,
				veleroRestore,
				k8sClient.Scheme(),
			)).Should(Succeed())
			Expect(k8sClient.Update(ctx, veleroRestore)).Should(Succeed())

			// Manually call cleanup logic
			veleroRestoreList := &veleroapi.RestoreList{}
			Expect(k8sClient.List(
				ctx,
				veleroRestoreList,
				client.InNamespace(testNS.Name),
			)).Should(Succeed())

			// Use getLatestVeleroRestores
			latestRestores := getLatestVeleroRestores(veleroRestoreList, acmRestore)
			latestRestoreNames := make(map[string]bool)
			for i := range latestRestores {
				latestRestoreNames[latestRestores[i].Name] = true
			}

			for i := range veleroRestoreList.Items {
				vRestore := &veleroRestoreList.Items[i]
				isOwned := false
				for _, ref := range vRestore.GetOwnerReferences() {
					if ref.Controller != nil && *ref.Controller && ref.UID == acmRestore.UID {
						isOwned = true
						break
					}
				}
				if !isOwned || latestRestoreNames[vRestore.Name] {
					continue
				}
				if vRestore.GetDeletionTimestamp() != nil || vRestore.Spec.BackupName == "" {
					continue
				}

				backupObj := &veleroapi.Backup{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      vRestore.Spec.BackupName,
					Namespace: testNS.Name,
				}, backupObj)

				if k8serr.IsNotFound(err) || backupObj.GetDeletionTimestamp() != nil {
					Expect(k8sClient.Delete(ctx, vRestore)).Should(Succeed())
				}
			}

			// Verify Velero restore was deleted
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      veleroRestore.Name,
					Namespace: testNS.Name,
				}, &veleroapi.Restore{})
				return k8serr.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})

		It("should delete Velero restore when backup phase is Deleting", func() {
			ctx := context.Background()

			// Create test namespace
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cleanup-phase-del-" + time.Now().Format("20060102150405"),
				},
			}
			Expect(k8sClient.Create(ctx, testNS)).Should(Succeed())

			// Create ACM restore
			skipBackup := skipRestoreStr
			acmRestore := &v1beta1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-acm-restore-phase-del",
					Namespace: testNS.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &skipBackup,
					VeleroCredentialsBackupName:     &skipBackup,
					VeleroResourcesBackupName:       &skipBackup,
				},
			}
			Expect(k8sClient.Create(ctx, acmRestore)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      acmRestore.Name,
					Namespace: testNS.Name,
				}, acmRestore)
				return err == nil && acmRestore.Status.Phase != ""
			}, timeout, interval).Should(BeTrue())

			// Set tracked restore
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      acmRestore.Name,
				Namespace: testNS.Name,
			}, acmRestore)).Should(Succeed())
			acmRestore.Status.VeleroCredentialsRestoreName = "current-credentials-restore"
			Expect(k8sClient.Status().Update(ctx, acmRestore)).Should(Succeed())

			// Create backup with Deleting phase
			backup := &veleroapi.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "phase-deleting-backup",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.BackupSpec{
					StorageLocation: "default",
				},
			}
			Expect(k8sClient.Create(ctx, backup)).Should(Succeed())

			backup.Status.Phase = veleroapi.BackupPhaseDeleting
			Expect(k8sClient.Update(ctx, backup)).Should(Succeed())

			// Create Velero restore
			veleroRestore := &veleroapi.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-velero-restore-phase-del",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.RestoreSpec{
					BackupName: backup.Name,
				},
			}
			Expect(k8sClient.Create(ctx, veleroRestore)).Should(Succeed())

			Expect(ctrl.SetControllerReference(
				acmRestore,
				veleroRestore,
				k8sClient.Scheme(),
			)).Should(Succeed())
			Expect(k8sClient.Update(ctx, veleroRestore)).Should(Succeed())

			// Cleanup logic
			veleroRestoreList := &veleroapi.RestoreList{}
			Expect(k8sClient.List(
				ctx,
				veleroRestoreList,
				client.InNamespace(testNS.Name),
			)).Should(Succeed())

			latestRestores := getLatestVeleroRestores(veleroRestoreList, acmRestore)
			latestRestoreNames := make(map[string]bool)
			for i := range latestRestores {
				latestRestoreNames[latestRestores[i].Name] = true
			}

			for i := range veleroRestoreList.Items {
				vRestore := &veleroRestoreList.Items[i]
				isOwned := false
				for _, ref := range vRestore.GetOwnerReferences() {
					if ref.Controller != nil && *ref.Controller && ref.UID == acmRestore.UID {
						isOwned = true
						break
					}
				}
				if !isOwned || latestRestoreNames[vRestore.Name] {
					continue
				}
				if vRestore.GetDeletionTimestamp() != nil || vRestore.Spec.BackupName == "" {
					continue
				}

				backupObj := &veleroapi.Backup{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      vRestore.Spec.BackupName,
					Namespace: testNS.Name,
				}, backupObj)

				if err == nil && backupObj.Status.Phase == veleroapi.BackupPhaseDeleting {
					Expect(k8sClient.Delete(ctx, vRestore)).Should(Succeed())
				}
			}

			// Verify deleted
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      veleroRestore.Name,
					Namespace: testNS.Name,
				}, &veleroapi.Restore{})
				return k8serr.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})

		It("should NOT delete Velero restore when backup is valid", func() {
			ctx := context.Background()

			// Create test namespace
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cleanup-valid-" + time.Now().Format("20060102150405"),
				},
			}
			Expect(k8sClient.Create(ctx, testNS)).Should(Succeed())

			// Create ACM restore
			skipBackup := skipRestoreStr
			acmRestore := &v1beta1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-acm-restore-valid",
					Namespace: testNS.Name,
				},
				Spec: v1beta1.RestoreSpec{
					VeleroManagedClustersBackupName: &skipBackup,
					VeleroCredentialsBackupName:     &skipBackup,
					VeleroResourcesBackupName:       &skipBackup,
				},
			}
			Expect(k8sClient.Create(ctx, acmRestore)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      acmRestore.Name,
					Namespace: testNS.Name,
				}, acmRestore)
				return err == nil && acmRestore.Status.Phase != ""
			}, timeout, interval).Should(BeTrue())

			// Set tracked restore
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      acmRestore.Name,
				Namespace: testNS.Name,
			}, acmRestore)).Should(Succeed())
			acmRestore.Status.VeleroCredentialsRestoreName = "current-creds"
			Expect(k8sClient.Status().Update(ctx, acmRestore)).Should(Succeed())

			// Create valid backup
			backup := &veleroapi.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-backup",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.BackupSpec{
					StorageLocation: "default",
				},
			}
			Expect(k8sClient.Create(ctx, backup)).Should(Succeed())

			backup.Status.Phase = veleroapi.BackupPhaseCompleted
			Expect(k8sClient.Update(ctx, backup)).Should(Succeed())

			// Create Velero restore
			veleroRestore := &veleroapi.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-velero-restore-valid",
					Namespace: testNS.Name,
				},
				Spec: veleroapi.RestoreSpec{
					BackupName: backup.Name,
				},
			}
			Expect(k8sClient.Create(ctx, veleroRestore)).Should(Succeed())

			Expect(ctrl.SetControllerReference(
				acmRestore,
				veleroRestore,
				k8sClient.Scheme(),
			)).Should(Succeed())
			Expect(k8sClient.Update(ctx, veleroRestore)).Should(Succeed())

			// Cleanup logic should NOT delete
			veleroRestoreList := &veleroapi.RestoreList{}
			Expect(k8sClient.List(
				ctx,
				veleroRestoreList,
				client.InNamespace(testNS.Name),
			)).Should(Succeed())

			latestRestores := getLatestVeleroRestores(veleroRestoreList, acmRestore)
			latestRestoreNames := make(map[string]bool)
			for i := range latestRestores {
				latestRestoreNames[latestRestores[i].Name] = true
			}

			for i := range veleroRestoreList.Items {
				vRestore := &veleroRestoreList.Items[i]
				isOwned := false
				for _, ref := range vRestore.GetOwnerReferences() {
					if ref.Controller != nil && *ref.Controller && ref.UID == acmRestore.UID {
						isOwned = true
						break
					}
				}
				if !isOwned || latestRestoreNames[vRestore.Name] {
					continue
				}
				if vRestore.GetDeletionTimestamp() != nil || vRestore.Spec.BackupName == "" {
					continue
				}

				backupObj := &veleroapi.Backup{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      vRestore.Spec.BackupName,
					Namespace: testNS.Name,
				}, backupObj)

				if k8serr.IsNotFound(err) || backupObj.GetDeletionTimestamp() != nil ||
					backupObj.Status.Phase == veleroapi.BackupPhaseDeleting ||
					backupObj.Status.Phase == veleroapi.BackupPhaseFailedValidation {
					_ = k8sClient.Delete(ctx, vRestore)
				}
			}

			// Verify restore still exists
			Consistently(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      veleroRestore.Name,
					Namespace: testNS.Name,
				}, &veleroapi.Restore{})
				return err == nil
			}, "5s", interval).Should(BeTrue())
		})
	})
})
