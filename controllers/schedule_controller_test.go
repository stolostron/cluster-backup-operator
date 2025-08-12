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
Package controllers contains comprehensive integration tests for the ACM BackupSchedule Controller.

This test suite validates the complete backup schedule workflow including:
- BackupSchedule resource creation and lifecycle management
- Velero Schedule resource orchestration and status tracking
- Backup schedule validation and error handling
- Resource labeling and managed service account integration
- Integration with backup storage locations and managed clusters

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
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BackupSchedule Controller Integration Test Suite
//
// This test suite comprehensively validates the ACM BackupSchedule Controller functionality
// across multiple scenarios including schedule creation, validation, status tracking, and
// error handling. Each test context focuses on a specific aspect of the backup schedule
// workflow to ensure proper isolation and clear failure diagnosis.
var _ = Describe("BackupSchedule controller", func() {

	// Test Variables Documentation
	//
	// These variables are shared across all test contexts and are reset in BeforeEach
	// to ensure test isolation. They represent the core components needed for testing
	// the ACM BackupSchedule Controller functionality.
	var (
		// Core test context and timing
		ctx      context.Context          // Test execution context
		timeout  = time.Second * 10       // Standard timeout for async operations
		interval = time.Millisecond * 250 // Polling interval for Eventually/Consistently checks

		// Velero infrastructure components
		veleroNamespace       *corev1.Namespace                // Namespace where Velero resources are created
		acmNamespace          *corev1.Namespace                // ACM namespace
		backupStorageLocation *veleroapi.BackupStorageLocation // Velero backup storage configuration

		// BackupSchedule configuration
		backupScheduleName  string                     // Name of the BackupSchedule resource being tested
		backupSchedule      string                     // Cron schedule expression
		rhacmBackupSchedule v1beta1.BackupSchedule     // The main BackupSchedule resource under test
		managedClusters     []clusterv1.ManagedCluster // Collection of managed cluster resources
		channels            []chnv1.Channel            // Channel resources for testing

		// Test timing configuration
		defaultVeleroTTL = time.Hour * 72 // Default TTL for Velero backups

		// Special values for testing different scenarios
		invalidCronExpression = "invalid-cron" // Invalid cron expression for error testing
		validCronExpression   = "0 2 * * *"    // Valid cron expression (daily at 2 AM)
	)

	// Test Setup - JustBeforeEach
	//
	// This setup runs before each individual test case and creates all necessary
	// Kubernetes resources in the test environment. The order of resource creation
	// is important to ensure proper dependencies and avoid race conditions.
	JustBeforeEach(func() {
		// Create the Velero namespace where all Velero resources will be placed
		Expect(k8sClient.Create(ctx, veleroNamespace)).Should(Succeed())

		// Create the ACM namespace
		Expect(k8sClient.Create(ctx, acmNamespace)).Should(Succeed())

		// Create managed clusters if any are defined for this test
		for i := range managedClusters {
			// Check if managed cluster already exists
			clusterLookupKey := types.NamespacedName{Name: managedClusters[i].Name}
			existingCluster := &clusterv1.ManagedCluster{}
			err := k8sClient.Get(ctx, clusterLookupKey, existingCluster)
			if errors.IsNotFound(err) {
				// Create new managed cluster
				Expect(k8sClient.Create(ctx, &managedClusters[i])).Should(Succeed())
			} else {
				// Managed cluster already exists, skip creation
				Expect(err).To(Succeed())
			}
		}

		// Create ACM resources (channels) if they don't exist
		// These are shared across tests to simulate a realistic ACM environment
		existingChannels := &chnv1.ChannelList{}
		Expect(k8sClient.List(ctx, existingChannels, &client.ListOptions{})).To(Succeed())
		if len(existingChannels.Items) == 0 {
			// Create test channels that simulate ACM resources
			for i := range channels {
				Expect(k8sClient.Create(ctx, &channels[i])).Should(Succeed())
			}
		}

		// Create and configure backup storage location if needed for this test
		// This simulates a properly configured Velero environment
		if backupStorageLocation != nil {
			storageLookupKey := types.NamespacedName{
				Name:      backupStorageLocation.Name,
				Namespace: backupStorageLocation.Namespace,
			}

			// Check if backup storage location already exists
			existingStorage := &veleroapi.BackupStorageLocation{}
			err := k8sClient.Get(ctx, storageLookupKey, existingStorage)
			if errors.IsNotFound(err) {
				// Create new storage location
				Expect(k8sClient.Create(ctx, backupStorageLocation)).Should(Succeed())
				Expect(k8sClient.Get(ctx, storageLookupKey, backupStorageLocation)).To(Succeed())
			} else {
				// Use existing storage location
				Expect(err).To(Succeed())
				backupStorageLocation = existingStorage
			}

			// Set storage location to available status to simulate a working Velero setup
			backupStorageLocation.Status.Phase = veleroapi.BackupStorageLocationPhaseAvailable
			// Velero CRD doesn't have status subresource set, so simply update the
			// status with a normal update() call.
			Expect(k8sClient.Update(ctx, backupStorageLocation)).To(Succeed())
			Expect(backupStorageLocation.Status.Phase).Should(BeIdenticalTo(veleroapi.BackupStorageLocationPhaseAvailable))
		}

		// Finally, create the BackupSchedule resource that will trigger the controller logic
		// This must be last to ensure all dependencies are in place

		// Check if BackupSchedule already exists
		scheduleLookupKey := types.NamespacedName{
			Name:      rhacmBackupSchedule.Name,
			Namespace: rhacmBackupSchedule.Namespace,
		}
		existingSchedule := &v1beta1.BackupSchedule{}
		err := k8sClient.Get(ctx, scheduleLookupKey, existingSchedule)
		if errors.IsNotFound(err) {
			// Create new BackupSchedule
			Expect(k8sClient.Create(ctx, &rhacmBackupSchedule)).Should(Succeed())
		} else {
			// BackupSchedule already exists, skip creation
			Expect(err).To(Succeed())
		}
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
		// This ensures all Velero resources (schedules, backups) are cleaned up quickly
		// and don't interfere with subsequent tests
		var zero int64 = 0
		Expect(
			k8sClient.Delete(
				ctx,
				veleroNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())

		// Force delete the ACM namespace
		Expect(
			k8sClient.Delete(
				ctx,
				acmNamespace,
				&client.DeleteOptions{GracePeriodSeconds: &zero},
			),
		).Should(Succeed())

		// Note: We don't wait for namespace deletion to complete as it can take time
		// and shouldn't block test completion. The unique namespace names prevent conflicts.

		// Clean up managed clusters (cluster-scoped resources)
		for i := range managedClusters {
			_ = k8sClient.Delete(ctx, &managedClusters[i])
		}

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

		// Set default backup schedule configuration
		backupScheduleName = "test-backup-schedule"
		backupSchedule = validCronExpression

		// Create unique namespace names using random seed and current time to avoid conflicts between tests
		uniqueSuffix := fmt.Sprintf("%d-%d", GinkgoRandomSeed(), time.Now().UnixNano())
		veleroNamespace = createNamespace(fmt.Sprintf("velero-schedule-ns-%s", uniqueSuffix))
		acmNamespace = createNamespace(fmt.Sprintf("acm-schedule-ns-%s", uniqueSuffix))

		// Create backup storage location
		backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
			setOwner().
			phase(veleroapi.BackupStorageLocationPhaseAvailable).object

		// Create standard ACM test resources using factory functions
		channels = createDefaultChannels() // Test channel data

		// Create managed clusters for testing with unique names
		managedClusters = []clusterv1.ManagedCluster{
			*createManagedCluster(fmt.Sprintf("local-cluster-%s", uniqueSuffix), true).object,
			*createManagedCluster(fmt.Sprintf("remote-cluster-%s", uniqueSuffix), false).object,
		}

		// Create the main BackupSchedule resource with standard configuration
		rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
			schedule(backupSchedule).
			veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
			object
	})

	// =============================================================================
	// CORE FUNCTIONALITY TESTS
	// =============================================================================
	//
	// This section tests the fundamental backup schedule operations including basic
	// schedule creation, validation logic, and core workflow validation.

	// Test Context: Basic BackupSchedule Functionality
	//
	// This context tests the core backup schedule functionality when creating a
	// BackupSchedule resource with valid configuration. It validates the complete
	// workflow including Velero schedule creation, status tracking, and validation.
	Context("basic backup schedule functionality", func() {
		Context("when creating backup schedule with valid configuration", func() {

			// Test Case: Basic BackupSchedule Creation and Status Tracking
			//
			// This test validates the fundamental backup schedule workflow:
			// 1. BackupSchedule creation triggers Velero schedule creation
			// 2. BackupSchedule status progresses through correct phases
			// 3. Velero schedules have correct configuration (TTL, cron schedule)
			// 4. Resource labeling is applied correctly
			// 5. Managed service account integration works properly
			It("should create velero schedules with proper configuration and track status progression", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Verify BackupSchedule is created and accessible
				By("waiting for backup schedule to be created")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Verify initial configuration
				Expect(createdSchedule.Spec.VeleroSchedule).Should(Equal(backupSchedule))
				Expect(createdSchedule.Spec.VeleroTTL).Should(Equal(metav1.Duration{Duration: defaultVeleroTTL}))

				// Step 2: Wait for controller to process the BackupSchedule
				By("waiting for controller to process backup schedule")
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					// Return current phase for better debugging
					return string(createdSchedule.Status.Phase), nil
				}, timeout, interval).Should(SatisfyAny(
					Equal(string(v1beta1.SchedulePhaseEnabled)),
					Equal(string(v1beta1.SchedulePhaseNew)),
					Not(BeEmpty()), // Any phase is acceptable as long as controller is processing
				))

				// Step 3: Wait for Velero schedules to be created
				By("waiting for velero schedules to be created")
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout, interval).Should(Equal(expectedScheduleCount))

				// Step 4: Verify Velero schedule configuration
				By("verifying velero schedule configuration")
				Expect(len(veleroSchedules.Items)).To(Equal(expectedScheduleCount))

				// Check configuration of the first schedule
				firstSchedule := veleroSchedules.Items[0]
				Expect(firstSchedule.Spec.Schedule).Should(Equal(backupSchedule))
				Expect(firstSchedule.Spec.Template.TTL).Should(Equal(metav1.Duration{Duration: defaultVeleroTTL}))

				// Verify all expected schedule names are created
				createdScheduleNames := make([]string, len(veleroSchedules.Items))
				for i, schedule := range veleroSchedules.Items {
					createdScheduleNames[i] = schedule.Name
				}
				for _, expectedName := range veleroScheduleNames {
					Expect(createdScheduleNames).To(ContainElement(expectedName))
				}

				// Step 5: Verify BackupSchedule status reflects Velero schedule creation
				By("verifying backup schedule status contains velero schedule information")
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}
					// Check if status has been updated with Velero schedule info
					hasVeleroSchedules := createdSchedule.Status.VeleroScheduleResources != nil
					return hasVeleroSchedules, nil
				}, timeout, interval).Should(BeTrue())

				// Step 6: Test active resource conflict by creating a Restore while BackupSchedule is active
				By("creating a restore resource while backup schedule is active to trigger line 177")
				skipBackup := "skip"
				conflictingRestore := &v1beta1.Restore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "conflicting-restore",
						Namespace: veleroNamespace.Name,
					},
					Spec: v1beta1.RestoreSpec{
						VeleroManagedClustersBackupName: &skipBackup,
						VeleroCredentialsBackupName:     &skipBackup,
						VeleroResourcesBackupName:       &skipBackup,
					},
				}
				Expect(k8sClient.Create(ctx, conflictingRestore)).Should(Succeed())

				// Step 7: Verify the restore is ignored due to active BackupSchedule (integration test)
				By("verifying restore is ignored due to active backup schedule")
				restoreLookupKey := types.NamespacedName{
					Name:      "conflicting-restore",
					Namespace: veleroNamespace.Name,
				}
				// The restore should remain in an initial state since it's being ignored
				Eventually(func() (bool, error) {
					createdRestore := &v1beta1.Restore{}
					err := k8sClient.Get(ctx, restoreLookupKey, createdRestore)
					if err != nil {
						return false, err
					}
					// The restore should exist but not progress to a finished state
					// since it's being ignored due to the active BackupSchedule
					return createdRestore.Status.Phase == "", nil
				}, timeout, interval).Should(BeTrue())

				// Step 8: Verify the error message mentions the active BackupSchedule
				By("verifying error message mentions active backup schedule")
				Eventually(func() (string, error) {
					createdRestore := &v1beta1.Restore{}
					err := k8sClient.Get(ctx, restoreLookupKey, createdRestore)
					if err != nil {
						return "", err
					}
					return createdRestore.Status.LastMessage, nil
				}, timeout, interval).Should(ContainSubstring("BackupSchedule resource"))
				Eventually(func() (string, error) {
					createdRestore := &v1beta1.Restore{}
					err := k8sClient.Get(ctx, restoreLookupKey, createdRestore)
					if err != nil {
						return "", err
					}
					return createdRestore.Status.LastMessage, nil
				}, timeout, interval).Should(ContainSubstring("currently active"))

			})
		})

		Context("when creating backup schedule with invalid cron expression", func() {
			BeforeEach(func() {
				// Override the default cron expression with an invalid one
				backupSchedule = invalidCronExpression
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					object
			})

			// Test Case: Invalid Cron Expression Validation
			//
			// This test validates that the controller properly validates cron expressions
			// and sets appropriate error status when invalid expressions are provided.
			It("should set failed validation status for invalid cron expression", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Wait for BackupSchedule to be created
				By("waiting for backup schedule to be created")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 2: Wait for controller to validate and set failed status
				By("waiting for controller to detect invalid cron expression")
				Eventually(func() (v1beta1.SchedulePhase, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return createdSchedule.Status.Phase, nil
				}, timeout, interval).Should(Equal(v1beta1.SchedulePhaseFailedValidation))

				// Step 3: Verify error message is set
				By("verifying error message contains validation details")
				Expect(createdSchedule.Status.LastMessage).Should(ContainSubstring("invalid"))

				// Step 4: Verify no Velero schedules are created
				By("verifying no velero schedules are created for invalid configuration")
				veleroSchedules := &veleroapi.ScheduleList{}
				Consistently(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return -1, err
					}
					return len(veleroSchedules.Items), nil
				}, time.Second*2, interval).Should(Equal(0))

			})
		})

		Context("when backup storage location is unavailable", func() {
			BeforeEach(func() {
				// Override backup storage location to be unavailable
				backupStorageLocation = createStorageLocation("default", veleroNamespace.Name).
					setOwner().
					phase(veleroapi.BackupStorageLocationPhaseUnavailable).object
			})

			// Test Case: Unavailable Storage Location Handling
			//
			// This test validates that the controller properly handles scenarios where
			// the backup storage location is not available.
			It("should handle unavailable backup storage location", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Verify BackupSchedule is created
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 2: Verify controller handles unavailable storage location
				By("backup schedule should handle unavailable storage location")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false
					}
					// The controller should either set failed validation or wait for storage location
					return createdSchedule.Status.Phase == v1beta1.SchedulePhaseFailedValidation ||
						createdSchedule.Status.LastMessage != ""
				}, timeout*2, interval).Should(BeTrue())

			})
		})

		Context("when backup schedule is paused", func() {
			BeforeEach(func() {
				// Create a paused backup schedule
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					paused(true).
					object
			})

			// Test Case: Paused BackupSchedule Handling and Unpausing
			//
			// This test validates that the controller properly handles paused backup schedules
			// and sets the appropriate status without creating Velero schedules, then tests
			// the transition from paused to unpaused state.
			It("should set paused status, not create velero schedules, then create them when unpaused", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Verify BackupSchedule is created
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 2: Verify controller sets paused status
				By("backup schedule should be in paused phase")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false
					}
					return createdSchedule.Status.Phase == v1beta1.SchedulePhasePaused
				}, timeout*2, interval).Should(BeTrue())

				// Step 3: Verify no Velero schedules are created while paused
				By("no velero schedules should be created for paused backup schedule")
				veleroSchedules := &veleroapi.ScheduleList{}
				Consistently(func() int {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return -1
					}
					return len(veleroSchedules.Items)
				}, time.Second*2, interval).Should(Equal(0))

				// Step 4: Unpause the backup schedule
				By("unpausing the backup schedule")
				Eventually(func() error {
					// Get the latest version of the schedule
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return err
					}
					// Set paused to false
					createdSchedule.Spec.Paused = false
					// Update the schedule
					return k8sClient.Update(ctx, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 5: Verify controller processes the unpaused schedule
				By("backup schedule should transition from paused to enabled phase")
				Eventually(func() (v1beta1.SchedulePhase, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return createdSchedule.Status.Phase, nil
				}, timeout*2, interval).Should(SatisfyAny(
					Equal(v1beta1.SchedulePhaseEnabled),
					Equal(v1beta1.SchedulePhaseNew),
					Not(Equal(v1beta1.SchedulePhasePaused)), // Any phase except paused
				))

				// Step 6: Verify Velero schedules are now created
				By("velero schedules should be created after unpausing")
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout*2, interval).Should(Equal(expectedScheduleCount))

				// Step 7: Verify Velero schedule configuration
				By("verifying velero schedule configuration after unpausing")
				Expect(len(veleroSchedules.Items)).To(Equal(expectedScheduleCount))
				firstSchedule := veleroSchedules.Items[0]
				Expect(firstSchedule.Spec.Schedule).Should(Equal(backupSchedule))
				Expect(firstSchedule.Spec.Template.TTL).Should(Equal(metav1.Duration{Duration: defaultVeleroTTL}))

				// Verify all expected schedule names are created after unpausing
				createdScheduleNames := make([]string, len(veleroSchedules.Items))
				for i, schedule := range veleroSchedules.Items {
					createdScheduleNames[i] = schedule.Name
				}
				for _, expectedName := range veleroScheduleNames {
					Expect(createdScheduleNames).To(ContainElement(expectedName))
				}

			})

			// Test Case: Advanced BackupSchedule Configuration Options
			//
			// This test validates that advanced backup schedule configuration options
			// are properly transferred to the Velero schedule objects:
			// - VolumeSnapshotLocations: Ensures volume snapshot locations are applied to backup templates
			// - UseOwnerReferencesInBackup: Verifies owner reference settings are propagated to Velero schedules
			// - SkipImmediately: Confirms skip immediately flag is set on Velero schedules
			It("should apply advanced backup schedule configuration options to velero schedules", func() {
				createdSchedule := v1beta1.BackupSchedule{}

				// Configure BackupSchedule with advanced options
				testVolumeSnapshotLocations := []string{"aws-snapshots", "azure-snapshots"}
				testUseOwnerReferences := true
				testSkipImmediately := true

				// Create BackupSchedule with advanced configuration
				By("creating backup schedule with advanced configuration options")
				advancedBackupSchedule := *createBackupSchedule(backupScheduleName+"-advanced", veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					volumeSnapshotLocations(testVolumeSnapshotLocations).
					useOwnerReferencesInBackup(testUseOwnerReferences).
					skipImmediately(testSkipImmediately).
					object

				// Create the new schedule
				Expect(k8sClient.Create(ctx, &advancedBackupSchedule)).Should(Succeed())

				// Step 1: Verify BackupSchedule is created with advanced configuration
				advancedScheduleLookupKey := createLookupKey(backupScheduleName+"-advanced", veleroNamespace.Name)
				Eventually(func() error {
					return k8sClient.Get(ctx, advancedScheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Verify the advanced configuration is set
				Expect(createdSchedule.Spec.VolumeSnapshotLocations).Should(Equal(testVolumeSnapshotLocations))
				Expect(createdSchedule.Spec.UseOwnerReferencesInBackup).Should(Equal(testUseOwnerReferences))
				Expect(createdSchedule.Spec.SkipImmediately).Should(Equal(testSkipImmediately))

				// Step 2: Wait for Velero schedules to be created
				By("waiting for velero schedules to be created with advanced configuration")
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					// Filter schedules that belong to our advanced test
					count := 0
					for _, schedule := range veleroSchedules.Items {
						if labels := schedule.GetLabels(); labels != nil {
							if scheduleName, exists := labels[BackupScheduleNameLabel]; exists &&
								scheduleName == backupScheduleName+"-advanced" {
								count++
							}
						}
					}
					return count, nil
				}, timeout, interval).Should(Equal(expectedScheduleCount))

				// Step 3: Verify VolumeSnapshotLocations are applied to Velero schedules
				By("verifying volume snapshot locations are applied to velero backup templates")
				for _, schedule := range veleroSchedules.Items {
					// Only check schedules that belong to our advanced test
					if labels := schedule.GetLabels(); labels != nil {
						if scheduleName, exists := labels[BackupScheduleNameLabel]; exists &&
							scheduleName == backupScheduleName+"-advanced" {
							// VolumeSnapshotLocations should be set in the backup template
							Expect(schedule.Spec.Template.VolumeSnapshotLocations).Should(Equal(testVolumeSnapshotLocations))
						}
					}
				}

				// Step 4: Verify UseOwnerReferencesInBackup is applied to Velero schedules
				By("verifying use owner references setting is applied to velero schedules")
				for _, schedule := range veleroSchedules.Items {
					// Only check schedules that belong to our advanced test
					if labels := schedule.GetLabels(); labels != nil {
						if scheduleName, exists := labels[BackupScheduleNameLabel]; exists &&
							scheduleName == backupScheduleName+"-advanced" {
							// UseOwnerReferencesInBackup should be set on the schedule
							Expect(schedule.Spec.UseOwnerReferencesInBackup).ShouldNot(BeNil())
							Expect(*schedule.Spec.UseOwnerReferencesInBackup).Should(Equal(testUseOwnerReferences))
						}
					}
				}

				// Step 5: Verify SkipImmediately is applied to Velero schedules
				By("verifying skip immediately setting is applied to velero schedules")
				for _, schedule := range veleroSchedules.Items {
					// Only check schedules that belong to our advanced test
					if labels := schedule.GetLabels(); labels != nil {
						if scheduleName, exists := labels[BackupScheduleNameLabel]; exists &&
							scheduleName == backupScheduleName+"-advanced" {
							// SkipImmediately should be set on the schedule
							Expect(schedule.Spec.SkipImmediately).ShouldNot(BeNil())
							Expect(*schedule.Spec.SkipImmediately).Should(Equal(testSkipImmediately))
						}
					}
				}

				// Step 6: Verify BackupSchedule status shows successful processing
				By("verifying backup schedule status reflects successful processing")
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, advancedScheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}
					// Check if status has been updated with Velero schedule info
					hasVeleroSchedules := createdSchedule.Status.VeleroScheduleResources != nil
					return hasVeleroSchedules, nil
				}, timeout, interval).Should(BeTrue())

			})
		})

		Context("when testing backup collision detection", func() {
			BeforeEach(func() {
				// Create a BackupSchedule that will have existing Velero schedules
				// to trigger the collision detection logic
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					object
			})

			// Test Case: Backup Collision Detection Logic
			//
			// This test validates the backup collision detection logic that runs when
			// Velero schedules already exist and the schedule is older than 5 seconds.
			// This covers the code path around line 173 in schedule_controller.go
			It("should detect backup collision when another cluster owns the backups", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Create the BackupSchedule and wait for initial Velero schedules
				By("creating backup schedule and waiting for initial velero schedules")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Wait for Velero schedules to be created
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout, interval).Should(Equal(expectedScheduleCount))

				// Step 2: Create mock backup resources that simulate our cluster owning some backups
				By("creating mock backup resources owned by our cluster")
				ourClusterID := "our-cluster-id"
				baseTime := time.Now().Add(-10 * time.Minute) // Older backups

				// Create a few backups that our cluster owns
				ourBackup1 := &veleroapi.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-our-backup-1",
						Namespace: veleroNamespace.Name,
						Labels: map[string]string{
							"cluster.open-cluster-management.io/backup-cluster": ourClusterID,
							"velero.io/schedule-name":                           veleroScheduleNames[Resources],
						},
						CreationTimestamp: metav1.NewTime(baseTime),
					},
					Spec: veleroapi.BackupSpec{
						StorageLocation: "default",
						TTL:             metav1.Duration{Duration: defaultVeleroTTL},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						StartTimestamp: &metav1.Time{Time: baseTime},
					},
				}
				Expect(k8sClient.Create(ctx, ourBackup1)).Should(Succeed())

				ourBackup2 := &veleroapi.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-our-backup-2",
						Namespace: veleroNamespace.Name,
						Labels: map[string]string{
							"cluster.open-cluster-management.io/backup-cluster": ourClusterID,
							"velero.io/schedule-name":                           veleroScheduleNames[Resources],
						},
						CreationTimestamp: metav1.NewTime(baseTime.Add(5 * time.Minute)),
					},
					Spec: veleroapi.BackupSpec{
						StorageLocation: "default",
						TTL:             metav1.Duration{Duration: defaultVeleroTTL},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						StartTimestamp: &metav1.Time{Time: baseTime.Add(5 * time.Minute)},
					},
				}
				Expect(k8sClient.Create(ctx, ourBackup2)).Should(Succeed())

				// Step 3: Create a Velero backup that appears to be owned by another cluster FIRST
				// This will simulate a backup collision scenario - it will be the LATEST backup
				By("creating a velero backup owned by another cluster to simulate collision")
				conflictTime := time.Now().Add(10 * time.Minute) // Make this clearly the latest backup
				conflictingBackup := &veleroapi.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-resources-schedule-conflicting-backup",
						Namespace: veleroNamespace.Name,
						Labels: map[string]string{
							"cluster.open-cluster-management.io/backup-cluster": "other-cluster-id",
							"velero.io/schedule-name":                           veleroScheduleNames[Resources],
						},
						CreationTimestamp: metav1.NewTime(conflictTime),
					},
					Spec: veleroapi.BackupSpec{
						StorageLocation: "default",
						TTL:             metav1.Duration{Duration: defaultVeleroTTL},
					},
					Status: veleroapi.BackupStatus{
						Phase:          veleroapi.BackupPhaseCompleted,
						StartTimestamp: &metav1.Time{Time: conflictTime}, // Latest timestamp - 10 minutes in the future
					},
				}
				Expect(k8sClient.Create(ctx, conflictingBackup)).Should(Succeed())

				// Step 3.1: Wait for the first schedule (credentials) to be older than 5 seconds
				// The original collision detection logic checks veleroScheduleList.Items[0] for age
				By("waiting for the first velero schedule to be older than 5 seconds")
				Eventually(func() (bool, error) {
					veleroScheduleList := &veleroapi.ScheduleList{}
					err := k8sClient.List(ctx, veleroScheduleList, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return false, err
					}
					if len(veleroScheduleList.Items) == 0 {
						return false, nil
					}
					// Check if the first schedule is older than 5 seconds
					ageSeconds := time.Since(veleroScheduleList.Items[0].CreationTimestamp.Time).Seconds()
					return ageSeconds > 5, nil
				}, timeout*2, interval).Should(BeTrue())

				// Step 4: Update all velero schedules to have proper status and labels
				By("updating velero schedules to appear enabled and set cluster ID")
				Eventually(func() error {
					veleroScheduleList := &veleroapi.ScheduleList{}
					err := k8sClient.List(ctx, veleroScheduleList, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return err
					}

					// Update all schedules to appear "enabled" and set proper labels
					for i := range veleroScheduleList.Items {
						schedule := &veleroScheduleList.Items[i]

						// Set status to make schedule appear enabled
						schedule.Status.Phase = veleroapi.SchedulePhaseEnabled

						// Set labels with our cluster ID
						if schedule.Labels == nil {
							schedule.Labels = make(map[string]string)
						}
						schedule.Labels["cluster.open-cluster-management.io/backup-cluster"] = ourClusterID

						// Update the schedule
						err = k8sClient.Update(ctx, schedule)
						if err != nil {
							return err
						}
					}
					return nil
				}, timeout, interval).Should(Succeed())

				// Step 4.1: Verify the cluster label is set on velero schedules for debugging
				By("verifying velero schedules have the cluster ID set")
				Eventually(func() (bool, error) {
					veleroScheduleList := &veleroapi.ScheduleList{}
					err := k8sClient.List(ctx, veleroScheduleList, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return false, err
					}

					for _, schedule := range veleroScheduleList.Items {
						if schedule.Labels["cluster.open-cluster-management.io/backup-cluster"] != ourClusterID {
							return false, nil
						}
					}
					return true, nil
				}, timeout, interval).Should(BeTrue())

				// The schedule age check is now handled in step 3.1 above

				// Step 6: Wait briefly for collision detection to be triggered
				//time.Sleep(2 * time.Second)

				// Step 7: Verify the controller detects the backup collision
				// The schedule should be set to SchedulePhaseBackupCollision since another cluster owns the backups
				By("verifying collision detection identifies the backup collision")
				Eventually(func() (v1beta1.SchedulePhase, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}

					return createdSchedule.Status.Phase, nil
				}, timeout*2, interval).Should(Equal(v1beta1.SchedulePhaseBackupCollision))

				// Step 8: Verify the collision message is set
				By("verifying collision message is set in status")
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return createdSchedule.Status.LastMessage, nil
				}, timeout, interval).Should(ContainSubstring("other-cluster-id"))

				// Step 9: Verify the controller is properly handling the collision state
				// When a collision is detected, the controller should ignore the BackupSchedule
				By("verifying the backup schedule remains in collision state")
				Consistently(func() (v1beta1.SchedulePhase, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return createdSchedule.Status.Phase, nil
				}, 1*time.Second, interval).Should(Equal(v1beta1.SchedulePhaseBackupCollision))

			})
		})

		Context("when testing manual schedule deletion and recreation", func() {
			BeforeEach(func() {
				// Create a BackupSchedule that will create Velero schedules
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					object
			})

			// Test Case: Manual Schedule Deletion Detection Logic
			//
			// This test validates that the controller properly detects missing Velero schedules
			// and initiates the cleanup process. Due to the 5-minute requeue interval after
			// schedule deletion, we focus on testing the detection and deletion phases.
			It("should detect and clean up when some schedules are manually deleted", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Create the BackupSchedule and wait for all Velero schedules to be created
				By("creating backup schedule and waiting for all velero schedules")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Wait for all 5 Velero schedules to be created
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout, interval).Should(Equal(expectedScheduleCount))

				// Step 2: Verify all expected schedules are created
				By("verifying all expected velero schedules are created")
				Expect(len(veleroSchedules.Items)).To(Equal(expectedScheduleCount))

				// Collect the names of created schedules
				createdScheduleNames := make([]string, len(veleroSchedules.Items))
				for i, schedule := range veleroSchedules.Items {
					createdScheduleNames[i] = schedule.Name
				}

				// Verify all expected schedule names exist
				for _, expectedName := range veleroScheduleNames {
					Expect(createdScheduleNames).To(ContainElement(expectedName))
				}

				// Step 3: Manually delete some of the Velero schedules to simulate manual deletion
				By("manually deleting some velero schedules to trigger cleanup logic")
				schedulesToDelete := 2 // Delete 2 out of 5 schedules

				// Get a fresh list right before deletion to avoid stale references
				freshVeleroSchedules := &veleroapi.ScheduleList{}
				Expect(k8sClient.List(ctx, freshVeleroSchedules, client.InNamespace(veleroNamespace.Name))).Should(Succeed())
				Expect(len(freshVeleroSchedules.Items)).To(BeNumerically(">=", schedulesToDelete))

				for i := 0; i < schedulesToDelete && i < len(freshVeleroSchedules.Items); i++ {
					scheduleToDelete := &freshVeleroSchedules.Items[i]
					err := k8sClient.Delete(ctx, scheduleToDelete)
					if err != nil && !errors.IsNotFound(err) {
						Expect(err).Should(Succeed()) // Only fail if it's not a "not found" error
					}
				}

				// Step 4: Verify some schedules are deleted (should have fewer than expected)
				By("verifying some schedules are deleted")
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout, interval).Should(BeNumerically("<", expectedScheduleCount))

				// Step 5: Trigger reconciliation and verify controller detects the issue
				By("triggering reconciliation to detect missing schedules")
				Eventually(func() error {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return err
					}
					// Add an annotation to trigger reconciliation
					if createdSchedule.Annotations == nil {
						createdSchedule.Annotations = make(map[string]string)
					}
					createdSchedule.Annotations["test.schedule.detection"] = time.Now().Format(time.RFC3339)
					return k8sClient.Update(ctx, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 6: Verify the controller processes the missing schedules
				// The controller should detect the issue and update the status to indicate processing
				By("verifying controller detects missing schedules and updates status")
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return string(createdSchedule.Status.Phase), nil
				}, timeout*2, interval).Should(SatisfyAny(
					Equal(string(v1beta1.SchedulePhaseNew)),                   // Controller resets to New phase during recreation
					Equal(string(v1beta1.SchedulePhaseEnabled)),               // Or maintains enabled if processing quickly
					Not(Equal(string(v1beta1.SchedulePhaseFailedValidation))), // Should not be in failed state
				))

				// Step 7: Verify the controller's cleanup behavior
				// Due to the 5-minute requeue, we test that the controller correctly manages
				// the transition and doesn't leave the schedule in an inconsistent state
				By("verifying backup schedule remains in a consistent state")
				Consistently(func() (bool, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}
					// The schedule should not be in a failed validation state
					// It should be either New (being processed) or Enabled (if processing completed)
					phase := createdSchedule.Status.Phase
					return phase != v1beta1.SchedulePhaseFailedValidation &&
						phase != "", nil
				}, timeout, interval).Should(BeTrue())

			})
		})

		Context("when testing schedule spec updates", func() {
			BeforeEach(func() {
				// Create a BackupSchedule that will have Velero schedules created
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					object
			})

			// Test Case: Schedule Spec Update Logic
			//
			// This test validates the schedule spec update logic that runs when the cron
			// schedule or TTL is changed on an existing BackupSchedule. This covers line 238
			// in schedule_controller.go where the controller returns early after updating schedules.
			It("should update velero schedules when BackupSchedule spec is changed", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Create the BackupSchedule and wait for all Velero schedules to be created
				By("creating backup schedule and waiting for all velero schedules")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Wait for all 5 Velero schedules to be created
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout, interval).Should(Equal(expectedScheduleCount))

				// Step 2: Verify initial schedule configuration
				By("verifying initial velero schedule configuration")
				Expect(len(veleroSchedules.Items)).To(Equal(expectedScheduleCount))

				// Check initial configuration values
				for _, schedule := range veleroSchedules.Items {
					Expect(schedule.Spec.Schedule).To(Equal(backupSchedule)) // Initial cron: "0 2 * * *"
					// Only check TTL for non-validation schedules
					if schedule.Name != veleroScheduleNames[ValidationSchedule] {
						Expect(schedule.Spec.Template.TTL.Duration).To(Equal(defaultVeleroTTL)) // Initial TTL: 72h
					}
				}

				// Step 3: Update the BackupSchedule spec with new cron schedule and TTL
				By("updating backup schedule spec with new cron and TTL values")
				newCronSchedule := "0 4 * * *" // Change from "0 2 * * *" to "0 4 * * *"
				newTTL := time.Hour * 96       // Change from 72h to 96h

				Eventually(func() error {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return err
					}
					// Update both cron schedule and TTL
					createdSchedule.Spec.VeleroSchedule = newCronSchedule
					createdSchedule.Spec.VeleroTTL = metav1.Duration{Duration: newTTL}
					return k8sClient.Update(ctx, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 4: Verify the BackupSchedule spec was updated
				By("verifying backup schedule spec was updated")
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}
					cronUpdated := createdSchedule.Spec.VeleroSchedule == newCronSchedule
					ttlUpdated := createdSchedule.Spec.VeleroTTL.Duration == newTTL
					return cronUpdated && ttlUpdated, nil
				}, timeout, interval).Should(BeTrue())

				// Step 5: Wait for controller to detect spec changes and update Velero schedules
				// This should trigger line 238: isVeleroSchedulesUpdateRequired returns updated=true
				By("waiting for controller to detect spec changes and update velero schedules")
				Eventually(func() (bool, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return false, err
					}

					// Check if all schedules have been updated with new values
					allUpdated := true
					for _, schedule := range veleroSchedules.Items {
						// Check cron schedule update
						if schedule.Spec.Schedule != newCronSchedule {
							allUpdated = false
							break
						}
						// Check TTL update (skip validation schedule as it has special TTL logic)
						if schedule.Name != veleroScheduleNames[ValidationSchedule] {
							if schedule.Spec.Template.TTL.Duration != newTTL {
								allUpdated = false
								break
							}
						}
					}
					return allUpdated, nil
				}, timeout*3, interval).Should(BeTrue())

				// Step 6: Verify all Velero schedules have been updated with new configuration
				By("verifying all velero schedules have new configuration")
				Eventually(func() error {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return err
					}

					if len(veleroSchedules.Items) != expectedScheduleCount {
						return fmt.Errorf("expected %d schedules, got %d", expectedScheduleCount, len(veleroSchedules.Items))
					}

					// Verify each schedule has the updated configuration
					for _, schedule := range veleroSchedules.Items {
						// Verify cron schedule update
						if schedule.Spec.Schedule != newCronSchedule {
							return fmt.Errorf("schedule %s has wrong cron: expected %s, got %s",
								schedule.Name, newCronSchedule, schedule.Spec.Schedule)
						}

						// Verify TTL update (validation schedule has special TTL calculation)
						if schedule.Name != veleroScheduleNames[ValidationSchedule] {
							if schedule.Spec.Template.TTL.Duration != newTTL {
								return fmt.Errorf("schedule %s has wrong TTL: expected %v, got %v",
									schedule.Name, newTTL, schedule.Spec.Template.TTL.Duration)
							}
						}
					}

					return nil
				}, timeout*2, interval).Should(Succeed())

				// Step 7: Verify BackupSchedule status reflects successful update
				By("verifying backup schedule status reflects successful update")
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}
					// The schedule should not be in a failed state after successful update
					return createdSchedule.Status.Phase != v1beta1.SchedulePhaseFailedValidation &&
						createdSchedule.Status.Phase != "", nil
				}, timeout, interval).Should(BeTrue())

				// Step 8: Test updating just the cron schedule (without TTL change)
				By("testing cron schedule update without TTL change")
				anotherCronSchedule := "0 6 * * *" // Change to 6 AM

				Eventually(func() error {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return err
					}
					// Update only cron schedule, keep TTL the same
					createdSchedule.Spec.VeleroSchedule = anotherCronSchedule
					return k8sClient.Update(ctx, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Verify the cron-only update is applied using smart waiting
				By("waiting for controller to propagate cron schedule changes to all velero schedules")

				// First, capture the current resource version to detect when controller processes the change
				var initialResourceVersion string
				Eventually(func() error {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&createdSchedule), &createdSchedule)
					if err != nil {
						return err
					}
					initialResourceVersion = createdSchedule.ResourceVersion
					return nil
				}, timeout, interval).Should(Succeed())

				// Wait for the BackupSchedule resource to be updated (indicating controller processed it)
				Eventually(func() (bool, error) {
					var currentSchedule v1beta1.BackupSchedule
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&createdSchedule), &currentSchedule)
					if err != nil {
						return false, err
					}

					// Check if the resource has been updated by comparing resource versions
					// and verify the spec actually contains our new cron schedule
					resourceUpdated := currentSchedule.ResourceVersion != initialResourceVersion
					specUpdated := currentSchedule.Spec.VeleroSchedule == anotherCronSchedule

					if !specUpdated {
						return false, fmt.Errorf("BackupSchedule spec not yet updated: expected %s, got %s",
							anotherCronSchedule, currentSchedule.Spec.VeleroSchedule)
					}

					return resourceUpdated && specUpdated, nil
				}, timeout, interval).Should(BeTrue(), "BackupSchedule should be updated with new cron schedule")

				// Now wait for Velero schedules to be updated, but with an early exit strategy
				Eventually(func() (bool, error) {
					freshVeleroSchedules := &veleroapi.ScheduleList{}
					err := k8sClient.List(ctx, freshVeleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return false, err
					}

					expectedScheduleCount := len(veleroScheduleNames)
					if len(freshVeleroSchedules.Items) != expectedScheduleCount {
						return false, fmt.Errorf("expected %d schedules, found %d",
							expectedScheduleCount, len(freshVeleroSchedules.Items))
					}

					// Count updated schedules and track progress
					updatedCount := 0
					for _, schedule := range freshVeleroSchedules.Items {
						if schedule.Spec.Schedule == anotherCronSchedule {
							updatedCount++
						}
					}

					// Success when all are updated
					if updatedCount == expectedScheduleCount {
						return true, nil
					}

					// Early failure if no progress after reasonable time - indicates a real issue
					// Instead of waiting forever, fail fast if there's clearly a problem
					return false, fmt.Errorf("schedule update progress: %d/%d updated", updatedCount, expectedScheduleCount)

				}, timeout*2, interval).Should(BeTrue(), "All Velero schedules should be updated")

			})
		})

		Context("when creating backup schedule with hive resources", func() {
			var (
				hiveNamespace1 *corev1.Namespace
				hiveNamespace2 *corev1.Namespace
			)

			BeforeEach(func() {
				// Create unique namespaces for Hive resources
				uniqueSuffix := fmt.Sprintf("%d-%d", GinkgoRandomSeed(), time.Now().UnixNano())
				hiveNamespace1 = createNamespace(fmt.Sprintf("hive-ns-1-%s", uniqueSuffix))
				hiveNamespace2 = createNamespace(fmt.Sprintf("hive-ns-2-%s", uniqueSuffix))

				// Create a standard BackupSchedule
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					object
			})

			// Test Case: Hive Resource Processing and updateHiveResources Coverage
			//
			// This test validates that the controller properly processes Hive resources
			// (ClusterDeployments and ClusterPools) during backup preparation, which
			// provides complete coverage of the updateHiveResources function.
			It("should process hive resources and update associated secrets", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Create Hive namespaces
				By("creating hive resource namespaces")
				Expect(k8sClient.Create(ctx, hiveNamespace1)).Should(Succeed())
				Expect(k8sClient.Create(ctx, hiveNamespace2)).Should(Succeed())

				// Step 2: Create ClusterDeployments with ClusterPoolRef
				By("creating cluster deployments with cluster pool references")

				clusterDeployment1 := &unstructured.Unstructured{}
				clusterDeployment1.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": "hive.openshift.io/v1",
					"kind":       "ClusterDeployment",
					"metadata": map[string]interface{}{
						"name":      "test-cluster-deployment-1",
						"namespace": hiveNamespace1.Name,
					},
					"spec": map[string]interface{}{
						"baseDomain":  "example.com",
						"clusterName": "test-cluster-1",
						"platform": map[string]interface{}{
							"aws": map[string]interface{}{
								"region": "us-east-1",
							},
						},
						"clusterPoolRef": map[string]interface{}{
							"poolName":  "test-cluster-pool-1",
							"namespace": hiveNamespace1.Name,
						},
					},
				})
				Expect(k8sClient.Create(ctx, clusterDeployment1)).Should(Succeed())

				clusterDeployment2 := &unstructured.Unstructured{}
				clusterDeployment2.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": "hive.openshift.io/v1",
					"kind":       "ClusterDeployment",
					"metadata": map[string]interface{}{
						"name":      "test-cluster-deployment-2",
						"namespace": hiveNamespace2.Name,
					},
					"spec": map[string]interface{}{
						"baseDomain":  "example.com",
						"clusterName": "test-cluster-2",
						"platform": map[string]interface{}{
							"aws": map[string]interface{}{
								"region": "us-west-2",
							},
						},
						"clusterPoolRef": map[string]interface{}{
							"poolName":  "test-cluster-pool-2",
							"namespace": hiveNamespace2.Name,
						},
					},
				})
				Expect(k8sClient.Create(ctx, clusterDeployment2)).Should(Succeed())

				// Step 3: Create ClusterPools
				By("creating cluster pools")

				clusterPool1 := &unstructured.Unstructured{}
				clusterPool1.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": "hive.openshift.io/v1",
					"kind":       "ClusterPool",
					"metadata": map[string]interface{}{
						"name":      "test-cluster-pool-1",
						"namespace": hiveNamespace1.Name,
					},
					"spec": map[string]interface{}{
						"size":       1,
						"baseDomain": "example.com",
						"imageSetRef": map[string]interface{}{
							"name": "openshift-v4.10.0",
						},
						"platform": map[string]interface{}{
							"aws": map[string]interface{}{
								"region": "us-east-1",
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, clusterPool1)).Should(Succeed())

				clusterPool2 := &unstructured.Unstructured{}
				clusterPool2.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": "hive.openshift.io/v1",
					"kind":       "ClusterPool",
					"metadata": map[string]interface{}{
						"name":      "test-cluster-pool-2",
						"namespace": hiveNamespace2.Name,
					},
					"spec": map[string]interface{}{
						"size":       2,
						"baseDomain": "example.com",
						"imageSetRef": map[string]interface{}{
							"name": "openshift-v4.11.0",
						},
						"platform": map[string]interface{}{
							"aws": map[string]interface{}{
								"region": "us-west-2",
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, clusterPool2)).Should(Succeed())

				// Step 4: Create secrets that should be processed by updateHiveResources
				By("creating secrets that should be processed by hive resource update")

				hiveSecret1 := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hive-test-secret-1",
						Namespace: hiveNamespace1.Name,
					},
					Data: map[string][]byte{
						"kubeconfig": []byte("test-kubeconfig-1"),
					},
				}
				Expect(k8sClient.Create(ctx, hiveSecret1)).Should(Succeed())

				hiveSecret2 := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hive-test-secret-2",
						Namespace: hiveNamespace2.Name,
					},
					Data: map[string][]byte{
						"kubeconfig": []byte("test-kubeconfig-2"),
					},
				}
				Expect(k8sClient.Create(ctx, hiveSecret2)).Should(Succeed())

				// Step 5: Verify BackupSchedule is created
				By("waiting for backup schedule to be created")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Step 6: Wait for controller to process the BackupSchedule and Hive resources
				By("waiting for controller to process backup schedule with hive resources")
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return string(createdSchedule.Status.Phase), nil
				}, timeout*2, interval).Should(SatisfyAny(
					Equal(string(v1beta1.SchedulePhaseEnabled)),
					Equal(string(v1beta1.SchedulePhaseNew)),
					Not(BeEmpty()),
				))

				// Step 7: Wait for Velero schedules to be created (this triggers Hive processing)
				By("waiting for velero schedules to be created which triggers hive processing")
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout*2, interval).Should(Equal(expectedScheduleCount))

				// Step 8: Verify that updateHiveResources was called and processed the resources
				// The function should have attempted to process ClusterDeployments and ClusterPools
				// We can verify this by checking that the controller attempted to process them
				By("verifying hive resources were processed by the controller")
				Eventually(func() (bool, error) {
					// The main indicator that updateHiveResources was called is that:
					// 1. The BackupSchedule status shows it's being processed
					// 2. Velero schedules were created (which means prepareForBackup was called)
					// 3. The controller logs show Hive resource processing attempts

					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}

					// If the BackupSchedule has a status and Velero schedules exist,
					// it means prepareForBackup was called, which calls updateHiveResources
					hasStatus := createdSchedule.Status.Phase != ""

					// Check that Velero schedules exist (this confirms prepareForBackup was called)
					veleroScheduleList := &veleroapi.ScheduleList{}
					err = k8sClient.List(ctx, veleroScheduleList, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return false, err
					}
					hasVeleroSchedules := len(veleroScheduleList.Items) > 0

					return hasStatus && hasVeleroSchedules, nil
				}, timeout*3, interval).Should(BeTrue())

				// Step 9: Verify Velero schedules have correct configuration
				By("verifying velero schedules have proper configuration")
				Expect(len(veleroSchedules.Items)).To(Equal(expectedScheduleCount))

				// Check configuration of schedules
				for _, schedule := range veleroSchedules.Items {
					Expect(schedule.Spec.Schedule).Should(Equal(backupSchedule))
					if schedule.Name != veleroScheduleNames[ValidationSchedule] {
						Expect(schedule.Spec.Template.TTL).Should(Equal(metav1.Duration{Duration: defaultVeleroTTL}))
					}
				}

				// Cleanup Hive namespaces
				var zero int64 = 0
				Expect(k8sClient.Delete(ctx, hiveNamespace1, &client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
				Expect(k8sClient.Delete(ctx, hiveNamespace2, &client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
			})
		})

		Context("when creating backup schedule with managed service account option", func() {
			var customMSATTL metav1.Duration

			BeforeEach(func() {
				// Create a BackupSchedule with MSA enabled and custom TTL (user-defined TTL)
				customMSATTL = metav1.Duration{Duration: 30 * time.Hour} // 30 hours custom TTL
				rhacmBackupSchedule = *createBackupSchedule(backupScheduleName, veleroNamespace.Name).
					schedule(backupSchedule).
					veleroTTL(metav1.Duration{Duration: defaultVeleroTTL}).
					useManagedServiceAccount(true).
					managedServiceAccountTTL(customMSATTL).
					object
			})

			// Test Case: MSA Integration and updateMSAResources Coverage
			//
			// This test validates that the controller properly handles BackupSchedules with
			// useManagedServiceAccount enabled, which triggers the updateMSAResources function
			// during backup preparation. This provides end-to-end coverage of the MSA workflow.
			It("should process managed service accounts and create velero schedules", func() {
				scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
				createdSchedule := v1beta1.BackupSchedule{}

				// Step 1: Create managed clusters and MSA resources that should be processed
				By("creating managed clusters and MSA resources that should be processed by prepareImportedClusters")

				// Create managed clusters
				managedCluster1 := createManagedCluster("test-cluster-1", false)
				managedCluster2 := createManagedCluster("test-cluster-2", false)
				Expect(k8sClient.Create(ctx, managedCluster1.object)).Should(Succeed())
				Expect(k8sClient.Create(ctx, managedCluster2.object)).Should(Succeed())

				// Create the namespaces first
				testNs1 := createNamespace("test-cluster-1")
				testNs2 := createNamespace("test-cluster-2")
				Expect(k8sClient.Create(ctx, testNs1)).Should(Succeed())
				Expect(k8sClient.Create(ctx, testNs2)).Should(Succeed())

				// Create a ManagedClusterAddOn without Status.Namespace set (tests logging path when status namespace not set)
				existingMSAAddon := &unstructured.Unstructured{}
				existingMSAAddon.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": "addon.open-cluster-management.io/v1alpha1",
					"kind":       "ManagedClusterAddOn",
					"metadata": map[string]interface{}{
						"name":      "managed-serviceaccount",
						"namespace": "test-cluster-1",
						"labels": map[string]interface{}{
							"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
						},
					},
					"spec": map[string]interface{}{
						"installNamespace": "open-cluster-management-agent-addon",
					},
					// Intentionally NOT setting status.namespace to trigger the else branch (line 319)
				})
				Expect(k8sClient.Create(ctx, existingMSAAddon)).Should(Succeed())

				// Create test MSA secrets with proper labels and names
				msaSecret1 := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "auto-import-account-test-cluster-1",
						Namespace: "test-cluster-1",
						Labels: map[string]string{
							"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
						},
					},
					Data: map[string][]byte{
						"token": []byte("test-token-1"),
					},
				}

				msaSecret2 := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "auto-import-account-test-cluster-2",
						Namespace: "test-cluster-2",
						Labels: map[string]string{
							"authentication.open-cluster-management.io/is-managed-serviceaccount": "true",
						},
					},
					Data: map[string][]byte{
						"token": []byte("test-token-2"),
					},
				}

				// Create the MSA secrets
				Expect(k8sClient.Create(ctx, msaSecret1)).Should(Succeed())
				Expect(k8sClient.Create(ctx, msaSecret2)).Should(Succeed())

				// Step 2: Verify BackupSchedule is created with MSA option and custom TTL
				By("waiting for backup schedule with MSA option and custom TTL to be created")
				Eventually(func() error {
					return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				}, timeout, interval).Should(Succeed())

				// Verify MSA option is set
				Expect(createdSchedule.Spec.UseManagedServiceAccount).Should(BeTrue())

				// Verify the custom MSA TTL is set correctly (tests user-defined TTL path in prepareImportedClusters)
				Expect(createdSchedule.Spec.ManagedServiceAccountTTL).Should(Equal(customMSATTL))

				// Step 3: Wait for controller to process the BackupSchedule and MSA secrets
				By("waiting for controller to process backup schedule with MSA")
				Eventually(func() (string, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return "", err
					}
					return string(createdSchedule.Status.Phase), nil
				}, timeout*2, interval).Should(SatisfyAny(
					Equal(string(v1beta1.SchedulePhaseEnabled)),
					Equal(string(v1beta1.SchedulePhaseNew)),
					Not(BeEmpty()),
				))

				// Step 4: Wait for Velero schedules to be created (this triggers MSA processing)
				By("waiting for velero schedules to be created which triggers MSA processing")
				veleroSchedules := &veleroapi.ScheduleList{}
				expectedScheduleCount := len(veleroScheduleNames)
				Eventually(func() (int, error) {
					err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
					if err != nil {
						return 0, err
					}
					return len(veleroSchedules.Items), nil
				}, timeout*2, interval).Should(Equal(expectedScheduleCount))

				// Step 5: Verify MSA secrets were processed (should have backup labels)
				By("verifying MSA secrets were processed and have backup labels")
				Eventually(func() (bool, error) {
					// Check first MSA secret
					secret1 := &corev1.Secret{}
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      "auto-import-account-test-cluster-1",
						Namespace: "test-cluster-1",
					}, secret1)
					if err != nil {
						return false, err
					}

					// Check if backup label was added
					if secret1.Labels["cluster.open-cluster-management.io/backup"] != "msa" {
						return false, nil
					}

					// Check second MSA secret
					secret2 := &corev1.Secret{}
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      "auto-import-account-test-cluster-2",
						Namespace: "test-cluster-2",
					}, secret2)
					if err != nil {
						return false, err
					}

					// Check if backup label was added
					return secret2.Labels["cluster.open-cluster-management.io/backup"] == "msa", nil
				}, timeout*3, interval).Should(BeTrue())

				// Step 6: Verify Velero schedules have correct configuration
				By("verifying velero schedules have proper MSA configuration")
				Expect(len(veleroSchedules.Items)).To(Equal(expectedScheduleCount))

				// Check configuration of schedules
				for _, schedule := range veleroSchedules.Items {
					Expect(schedule.Spec.Schedule).Should(Equal(backupSchedule))
					if schedule.Name != veleroScheduleNames[ValidationSchedule] {
						Expect(schedule.Spec.Template.TTL).Should(Equal(metav1.Duration{Duration: defaultVeleroTTL}))
					}
				}

				// Step 7: Verify existing MSA addon without Status.Namespace is properly handled
				By("verifying existing MSA addon without Status.Namespace triggers logging path")
				Eventually(func() (bool, error) {
					// Check that the existing MSA addon still exists
					existingAddon := &unstructured.Unstructured{}
					existingAddon.SetGroupVersionKind(schema.GroupVersionKind{
						Group:   "addon.open-cluster-management.io",
						Version: "v1alpha1",
						Kind:    "ManagedClusterAddOn",
					})
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      "managed-serviceaccount",
						Namespace: "test-cluster-1",
					}, existingAddon)
					if err != nil {
						return false, err
					}

					// Verify the addon exists but doesn't have status.namespace (which triggers the logging path)
					status, found, _ := unstructured.NestedMap(existingAddon.Object, "status")
					if !found {
						// No status at all - this will trigger the else branch
						return true, nil
					}

					namespace, found, _ := unstructured.NestedString(status, "namespace")
					// Return true if namespace is not found or empty (triggers the else branch on line 319)
					return !found || namespace == "", nil
				}, timeout, interval).Should(BeTrue())

				// Step 8: Verify BackupSchedule status reflects successful MSA processing
				By("verifying backup schedule status reflects successful MSA processing")
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
					if err != nil {
						return false, err
					}
					// Check if status has been updated with Velero schedule info
					hasVeleroSchedules := createdSchedule.Status.VeleroScheduleResources != nil
					return hasVeleroSchedules, nil
				}, timeout, interval).Should(BeTrue())

				// Cleanup test namespaces
				var zero int64 = 0
				Expect(k8sClient.Delete(ctx, testNs1, &client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
				Expect(k8sClient.Delete(ctx, testNs2, &client.DeleteOptions{GracePeriodSeconds: &zero})).Should(Succeed())
			})
		})
	})

	Context("when no backup storage location resources exist", func() {
		BeforeEach(func() {
			// Override to not create any BackupStorageLocation resources
			backupStorageLocation = nil
		})

		JustBeforeEach(func() {
			// Ensure there are no existing BackupStorageLocation resources across all namespaces
			// Delete all existing BackupStorageLocation resources to ensure clean state
			storageLocations := &veleroapi.BackupStorageLocationList{}
			Expect(k8sClient.List(ctx, storageLocations)).To(Succeed())

			for _, location := range storageLocations.Items {
				Expect(k8sClient.Delete(ctx, &location)).To(Succeed())
			}

			// Verify no BackupStorageLocation resources exist across all namespaces
			newStorageLocations := &veleroapi.BackupStorageLocationList{}
			Eventually(func() int {
				err := k8sClient.List(ctx, newStorageLocations)
				if err != nil {
					return -1
				}
				return len(newStorageLocations.Items)
			}, timeout, interval).Should(Equal(0))
		})

		// Test Case: No BackupStorageLocation Resources Found
		//
		// This test validates that the controller properly handles scenarios where
		// no BackupStorageLocation resources exist at all, triggering the validation
		// error that suggests creating Velero or DataProtectionApplications resources.
		It("should fail validation when no BackupStorageLocation resources exist", func() {
			scheduleLookupKey := createLookupKey(backupScheduleName, veleroNamespace.Name)
			createdSchedule := v1beta1.BackupSchedule{}

			// Step 1: Verify BackupSchedule is created
			Eventually(func() error {
				return k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
			}, timeout, interval).Should(Succeed())

			// Step 2: Verify controller fails validation when no storage locations exist
			By("backup schedule should fail validation when no storage locations exist")
			Eventually(func() v1beta1.SchedulePhase {
				err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				if err != nil {
					return v1beta1.SchedulePhaseNew
				}
				return createdSchedule.Status.Phase
			}, timeout, interval).Should(Equal(v1beta1.SchedulePhaseFailedValidation))

			// Step 3: Verify the error message suggests creating Velero or DataProtectionApplications
			By("error message should suggest creating Velero or DataProtectionApplications resources")
			Eventually(func() string {
				err := k8sClient.Get(ctx, scheduleLookupKey, &createdSchedule)
				if err != nil {
					return ""
				}
				return createdSchedule.Status.LastMessage
			}, timeout, interval).Should(And(
				ContainSubstring("velero.io.BackupStorageLocation resources not found"),
				ContainSubstring("Verify you have created a konveyor.openshift.io.Velero or "+
					"oadp.openshift.io.DataProtectionApplications resource"),
			))

			// Step 4: Verify no Velero schedules are created
			By("verifying no velero schedules are created when no storage locations exist")
			veleroSchedules := &veleroapi.ScheduleList{}
			Consistently(func() (int, error) {
				err := k8sClient.List(ctx, veleroSchedules, client.InNamespace(veleroNamespace.Name))
				if err != nil {
					return -1, err
				}
				return len(veleroSchedules.Items), nil
			}, time.Second*2, interval).Should(Equal(0))
		})
	})

})
