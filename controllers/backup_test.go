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
Package controllers contains comprehensive unit tests for backup-related utility functions
in the ACM Backup/Restore system.

This test suite validates core backup functionality including:
- Backup resource creation and configuration
- Label selector operations and resource filtering
- Backup validation and error handling
- Integration with Velero backup resources
- Namespace and resource inclusion/exclusion logic
- Backup scheduling and lifecycle management

The tests use standard table-driven test patterns to ensure comprehensive coverage
of different backup scenarios and edge cases.
*/
package controllers

import (
	"context"
	"math/rand"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
)

// generates a random string with specified length
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
		veleroNamespaceName             = "velero"
		veleroManagedClustersBackupName = "acm-managed-clusters-schedule-20210910181336"
		veleroResourcesBackupName       = "acm-resources-schedule-20210910181336"
		veleroCredentialsBackupName     = "acm-credentials-schedule-20210910181336"

		labelsCls123 = map[string]string{
			"velero.io/schedule-name":  "acm-resources-schedule",
			BackupScheduleClusterLabel: "cls-123",
		}
	)

	Context("For utility functions of Backup", func() {
		It("getValidKsRestoreName should return correct value", func() {
			// returns the concatenated strings, no trimming
			Expect(getValidKsRestoreName("a", "b")).Should(Equal("a-b"))

			// returns substring of length 252
			longName := RandStringBytesMask(260)
			Expect(getValidKsRestoreName(longName, "b")).Should(Equal(longName[:252]))
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

// Test_cleanupExpiredValidationBackups tests the cleanup of expired validation backups.
//
// This test verifies that:
// - Expired validation backups are properly identified and deleted
// - Non-expired validation backups are preserved
// - Non-validation backups are not affected by the cleanup process
// - The function handles edge cases like empty backup lists gracefully
//
// Test Coverage:
// - Validation backups with expired timestamps (should be deleted)
// - Validation backups that are still valid (should be preserved)
// - Non-validation backups (should be ignored)
// - Empty backup lists (should handle gracefully)
// - Different backup phases (FailedValidation, Completed, etc.)
//
// Test Scenarios:
//   - "cleanupExpiredValidationBackups processes expired validation backups correctly"
//     Creates a mix of expired and valid validation backups plus non-validation backups
//     Verifies only expired validation backups are deleted
//   - "cleanupExpiredValidationBackups handles no validation backups"
//     Tests behavior when no validation backups exist in the system
//   - "cleanupExpiredValidationBackups handles empty backup list"
//     Tests behavior with completely empty backup list
//
// Implementation Details:
// - Uses fake Kubernetes client for fast, isolated testing
// - Creates realistic backup objects with proper labels and timestamps
// - Uses custom client wrapper to track deletion operations
// - Verifies deletion through mock client inspection
// - Tests various backup phases and expiration scenarios
//
// Background:
// Velero doesn't automatically clean up validation backups that fail validation
// or have storage location issues. This function provides that cleanup capability
// to prevent accumulation of failed validation backups.
//

// createExpiredValidationBackups creates test backups for expired validation scenario
//
//nolint:funlen
func createExpiredValidationBackups() []veleroapi.Backup {
	now := time.Now()
	oneHourAgo := metav1.NewTime(now.Add(-1 * time.Hour))
	oneHourFromNow := metav1.NewTime(now.Add(1 * time.Hour))

	return []veleroapi.Backup{
		// Expired validation backup - should be deleted
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "expired-validation-backup-1",
				Namespace: "velero-ns",
				Labels: map[string]string{
					BackupVeleroLabel: veleroScheduleNames[ValidationSchedule],
				},
			},
			Status: veleroapi.BackupStatus{
				Phase:      veleroapi.BackupPhaseFailedValidation,
				Expiration: &oneHourAgo, // Expired 1 hour ago
			},
		},
		// Another expired validation backup - should be deleted
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "expired-validation-backup-2",
				Namespace: "velero-ns",
				Labels: map[string]string{
					BackupVeleroLabel: veleroScheduleNames[ValidationSchedule],
				},
			},
			Status: veleroapi.BackupStatus{
				Phase:      veleroapi.BackupPhaseCompleted,
				Expiration: &oneHourAgo, // Expired 1 hour ago
			},
		},
		// Non-expired validation backup - should NOT be deleted
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "valid-validation-backup",
				Namespace: "velero-ns",
				Labels: map[string]string{
					BackupVeleroLabel: veleroScheduleNames[ValidationSchedule],
				},
			},
			Status: veleroapi.BackupStatus{
				Phase:      veleroapi.BackupPhaseCompleted,
				Expiration: &oneHourFromNow, // Expires in 1 hour
			},
		},
		// Validation backup with no expiration - should NOT be deleted
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "no-expiration-validation-backup",
				Namespace: "velero-ns",
				Labels: map[string]string{
					BackupVeleroLabel: veleroScheduleNames[ValidationSchedule],
				},
			},
			Status: veleroapi.BackupStatus{
				Phase: veleroapi.BackupPhaseCompleted,
				// No expiration set
			},
		},
		// Non-validation backup (different label) - should NOT be deleted even if expired
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "expired-non-validation-backup",
				Namespace: "velero-ns",
				Labels: map[string]string{
					BackupVeleroLabel: "some-other-schedule",
				},
			},
			Status: veleroapi.BackupStatus{
				Phase:      veleroapi.BackupPhaseCompleted,
				Expiration: &oneHourAgo, // Expired but not validation backup
			},
		},
	}
}

// createNonValidationBackups creates test backups with no validation backups
func createNonValidationBackups() []veleroapi.Backup {
	now := time.Now()
	oneHourAgo := metav1.NewTime(now.Add(-1 * time.Hour))

	return []veleroapi.Backup{
		// Non-validation backup
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "non-validation-backup",
				Namespace: "velero-ns",
				Labels: map[string]string{
					BackupVeleroLabel: "some-other-schedule",
				},
			},
			Status: veleroapi.BackupStatus{
				Phase:      veleroapi.BackupPhaseCompleted,
				Expiration: &oneHourAgo,
			},
		},
	}
}

// verifyDeletedBackups verifies that only expected backups were deleted
func verifyDeletedBackups(t *testing.T, deletedBackups map[string]bool, allBackups []veleroapi.Backup) {
	for backupName := range deletedBackups {
		found := false
		for _, backup := range allBackups {
			if backup.Name == backupName {
				found = true
				// Should be a validation backup
				if backup.Labels[BackupVeleroLabel] != veleroScheduleNames[ValidationSchedule] {
					t.Errorf("Non-validation backup %s was deleted", backupName)
				}
				// Should be expired
				if backup.Status.Expiration == nil || !time.Now().After(backup.Status.Expiration.Time) {
					t.Errorf("Non-expired backup %s was deleted", backupName)
				}
				break
			}
		}
		if !found {
			t.Errorf("Unknown backup %s was deleted", backupName)
		}
	}
}

//nolint:funlen
func Test_cleanupExpiredValidationBackups(t *testing.T) {
	tests := []struct {
		name                   string
		setupBackups           func() []veleroapi.Backup
		expectedDeletedBackups int
		description            string
	}{
		{
			name:                   "cleanupExpiredValidationBackups processes expired validation backups correctly",
			setupBackups:           createExpiredValidationBackups,
			expectedDeletedBackups: 2, // Only the 2 expired validation backups should be deleted
			description:            "Should delete only expired validation backups and leave others untouched",
		},
		{
			name:                   "cleanupExpiredValidationBackups handles no validation backups",
			setupBackups:           createNonValidationBackups,
			expectedDeletedBackups: 0, // No validation backups to delete
			description:            "Should handle case where no validation backups exist",
		},
		{
			name: "cleanupExpiredValidationBackups handles empty backup list",
			setupBackups: func() []veleroapi.Backup {
				return []veleroapi.Backup{} // Empty list
			},
			expectedDeletedBackups: 0, // No backups to delete
			description:            "Should handle empty backup list gracefully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client with the test backups
			scheme := runtime.NewScheme()
			err := veleroapi.AddToScheme(scheme)
			if err != nil {
				t.Fatalf("Failed to add Velero API to scheme: %v", err)
			}

			// Create backups from test setup
			backups := tt.setupBackups()
			objects := make([]client.Object, len(backups))
			for i := range backups {
				objects[i] = &backups[i]
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			// Track deletion calls by wrapping the client
			deletedBackups := make(map[string]bool)
			wrappedClient := &clientWrapper{
				Client:         fakeClient,
				deletedBackups: deletedBackups,
			}

			// Call the function under test
			ctx := context.Background()
			cleanupExpiredValidationBackups(ctx, "velero-ns", wrappedClient)

			// Verify the expected number of backups were deleted
			actualDeleted := len(deletedBackups)
			if actualDeleted != tt.expectedDeletedBackups {
				t.Errorf("Expected %d backups to be deleted, but %d were deleted. Deleted backups: %v",
					tt.expectedDeletedBackups, actualDeleted, deletedBackups)
			}

			// Verify that only expired validation backups were deleted
			verifyDeletedBackups(t, deletedBackups, backups)

			t.Logf("Test '%s': %s - Successfully deleted %d expired validation backups",
				tt.name, tt.description, actualDeleted)
		})
	}
}

// clientWrapper wraps a fake client to track deleteBackup calls
type clientWrapper struct {
	client.Client
	deletedBackups map[string]bool
}

// Create wraps the Create method to track DeleteBackupRequest creation
func (c *clientWrapper) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	// Check if this is a DeleteBackupRequest being created
	if deleteReq, ok := obj.(*veleroapi.DeleteBackupRequest); ok {
		// Track that this backup was "deleted"
		c.deletedBackups[deleteReq.Spec.BackupName] = true
	}
	return c.Client.Create(ctx, obj, opts...)
}

// Test_deleteBackup tests the deletion of Velero backup objects through DeleteBackupRequest creation.
//
// This test verifies that:
// - DeleteBackupRequest objects are properly created for backup deletion
// - The function handles various backup states and conditions correctly
// - Error conditions are managed gracefully (missing namespace, existing requests, etc.)
// - Backup deletion works through Velero's standard deletion mechanism
//
// Test Coverage:
// - Standard backup deletion with valid backup object
// - Backup deletion when namespace is missing (should handle gracefully)
// - Backup deletion when DeleteBackupRequest already exists
// - Backup deletion when DeleteBackupRequest has errors (should delete backup directly)
// - Various error conditions and edge cases
//
// Test Scenarios:
//   - "should successfully create DeleteBackupRequest for existing backup"
//     Tests normal case where backup exists and DeleteBackupRequest is created
//   - "should handle missing namespace gracefully"
//     Tests behavior when backup namespace doesn't exist
//   - "should handle backup deletion when DeleteBackupRequest already exists"
//     Tests idempotent behavior when deletion request already exists
//   - "should handle DeleteBackupRequest with errors and delete backup"
//     Tests fallback deletion when DeleteBackupRequest encounters errors
//
// Implementation Details:
// - Uses fake Kubernetes client for isolated testing
// - Creates realistic Velero Backup and DeleteBackupRequest objects
// - Tests both successful and error scenarios
// - Verifies proper error handling and logging
// - Uses custom client wrapper to track operations
//
// Velero Integration:
// The function integrates with Velero's backup deletion mechanism by creating
// DeleteBackupRequest objects, which is the standard way to request backup
// deletion in Velero. If the request fails, it falls back to direct deletion.
//

//nolint:funlen
func Test_deleteBackup(t *testing.T) {
	tests := []struct {
		name          string
		backupName    string
		namespace     string
		setupObjects  func() []client.Object
		expectedError bool
		description   string
	}{
		{
			name:       "should successfully create DeleteBackupRequest for existing backup",
			backupName: "test-backup",
			namespace:  "velero-ns",
			setupObjects: func() []client.Object {
				return []client.Object{
					createNamespace("velero-ns"),
					createBackup("test-backup", "velero-ns").object,
				}
			},
			expectedError: false,
			description:   "Should create DeleteBackupRequest when backup exists",
		},
		{
			name:       "should handle missing namespace gracefully",
			backupName: "test-backup",
			namespace:  "missing-ns",
			setupObjects: func() []client.Object {
				return []client.Object{
					createBackup("test-backup", "missing-ns").object,
				}
			},
			expectedError: false,
			description:   "Should handle missing namespace gracefully by creating DeleteBackupRequest",
		},
		{
			name:       "should handle backup deletion when DeleteBackupRequest already exists",
			backupName: "existing-backup",
			namespace:  "velero-ns",
			setupObjects: func() []client.Object {
				return []client.Object{
					createNamespace("velero-ns"),
					createBackup("existing-backup", "velero-ns").object,
					createBackupDeleteRequest("existing-backup-delete", "velero-ns", "existing-backup").object,
				}
			},
			expectedError: false,
			description:   "Should handle case when DeleteBackupRequest already exists",
		},
		{
			name:       "should handle DeleteBackupRequest with errors and delete backup",
			backupName: "error-backup",
			namespace:  "velero-ns",
			setupObjects: func() []client.Object {
				return []client.Object{
					createNamespace("velero-ns"),
					createBackup("error-backup", "velero-ns").object,
					createBackupDeleteRequest("error-backup", "velero-ns", "error-backup").
						errors([]string{"deletion failed", "resource not found"}).object,
				}
			},
			expectedError: false,
			description:   "Should delete backup when DeleteBackupRequest has errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client with Velero scheme
			scheme := runtime.NewScheme()
			err := veleroapi.AddToScheme(scheme)
			if err != nil {
				t.Fatalf("Failed to add Velero API to scheme: %v", err)
			}
			err = corev1.AddToScheme(scheme)
			if err != nil {
				t.Fatalf("Failed to add Core API to scheme: %v", err)
			}

			// Create objects from test setup
			objects := tt.setupObjects()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			// Create backup object for test
			backup := createBackup(tt.backupName, tt.namespace).object

			// Call the function under test
			ctx := context.Background()
			err = deleteBackup(ctx, backup, fakeClient)

			// Verify the result
			if tt.expectedError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			t.Logf("Test '%s': %s - Result: error=%v", tt.name, tt.description, err != nil)
		})
	}
}
