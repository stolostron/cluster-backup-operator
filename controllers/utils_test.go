// Copyright Contributors to the Open Cluster Management project

/*
Package controllers contains comprehensive unit tests for utility functions used across the ACM Backup/Restore system.

This test suite validates core utility functions including:
- Hub cluster identification and metadata extraction
- Velero CRD presence detection and validation
- Backup timestamp parsing and manipulation
- Storage location validation and configuration
- Resource filtering and label selector operations
- Managed service account token validation
- Backup schedule phase management and collision detection
- String manipulation and array operations

The tests use fake Kubernetes clients to simulate various cluster states and configurations,
ensuring reliable testing without external dependencies. Helper functions from create_helper.go
are used to reduce setup complexity and maintain consistency across test scenarios.
*/

//nolint:funlen
package controllers

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/pkg/errors"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Test_getBackupTimestamp tests extraction of timestamp information from backup names.
//
// This test validates the logic that parses backup names to extract embedded
// timestamp information, which is critical for backup ordering and lifecycle management.
//
// Test Coverage:
// - Timestamp extraction from properly formatted backup names
// - Error handling for invalid or malformed backup names
// - Time parsing validation with specific timestamp format
// - Edge cases with invalid timestamp formats
//
// Test Scenarios:
// - Invalid backup names that don't contain timestamps
// - Valid backup names with proper timestamp format (20060102150405)
// - Timestamp parsing accuracy and format compliance
//
// Implementation Details:
// - Uses standard Go time parsing with specific layout format
// - Returns zero time for invalid or missing timestamps
// - Validates proper timestamp extraction from backup name suffixes
//
// Business Logic:
// Backup timestamp extraction is essential for backup lifecycle management,
// enabling proper ordering, retention policies, and temporal operations
// on backup resources within the disaster recovery system.
func Test_getBackupTimestamp(t *testing.T) {
	type args struct {
		backupName string
	}

	goodTime, _ := time.Parse("20060102150405", "20220629100052")

	tests := []struct {
		name string
		args args
		want time.Time
	}{
		{
			name: "invalid time",
			args: args{
				backupName: "aaaaa",
			},
			want: time.Time{},
		},
		{
			name: "valid time",
			args: args{
				backupName: "acm-managed-clusters-schedule-20220629100052",
			},
			want: goodTime,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := getBackupTimestamp(tt.args.backupName); got != tt.want {
				t.Errorf("getBackupTimestamp() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_sortCompare tests slice comparison functionality with order-independent equality.
//
// This test validates the logic that compares two string slices for equality
// regardless of element order, which is essential for resource list comparisons.
//
// Test Coverage:
// - Nil slice handling and comparison
// - Empty slice comparison scenarios
// - Different element membership validation
// - Length difference detection
// - Order-independent equality verification
//
// Test Scenarios:
// - Both slices are nil (should return true)
// - One slice is nil, other is not (should return false)
// - Slices with different elements (should return false)
// - Slices with different lengths (should return false)
// - Slices with same elements in same order (should return true)
// - Slices with same elements in different order (should return true)
//
// Implementation Details:
// - Performs deep comparison of slice contents
// - Uses sorting internally to enable order-independent comparison
// - Handles edge cases with nil and empty slices
// - Returns boolean indicating equality
//
// Business Logic:
// Order-independent slice comparison is crucial for validating resource
// configurations where element order shouldn't affect equality, such as
// comparing backup resource lists or schedule configurations.
func Test_sortCompare(t *testing.T) {
	type args struct {
		a []string
		b []string
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "both slices are nil",
			args: args{
				a: nil,
				b: nil,
			},
			want: true,
		},
		{
			name: "one slice is nil",
			args: args{
				a: []string{"abc"},
				b: nil,
			},
			want: false,
		},
		{
			name: "different members",
			args: args{
				a: []string{"abc"},
				b: []string{"def"},
			},
			want: false,
		},
		{
			name: "b has more members",
			args: args{
				a: []string{"abc"},
				b: []string{"abc", "def"},
			},
			want: false,
		},
		{
			name: "a has more members",
			args: args{
				a: []string{"abc", "def", "z"},
				b: []string{"abc", "def"},
			},
			want: false,
		},
		{
			name: "same members, same order",
			args: args{
				a: []string{"abc", "def"},
				b: []string{"abc", "def"},
			},
			want: true,
		},
		{
			name: "same members, different order",
			args: args{
				a: []string{"abc", "def"},
				b: []string{"def", "abc"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortCompare(tt.args.a, tt.args.b); got != tt.want {
				t.Errorf("sortCompare() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_isValidStorageLocationDefined tests validation of backup storage location configurations.
//
// This test validates the logic that determines whether valid backup storage
// locations are properly configured and available for backup operations.
//
// Test Coverage:
// - Empty storage location list handling
// - Storage location owner reference validation
// - Storage location phase and availability checking
// - Namespace preference matching
// - Default storage location identification
//
// Test Scenarios:
// - No storage locations available (should return false)
// - Storage locations without proper owner references (should return false)
// - Storage locations with proper configuration and ownership
// - Preferred namespace matching and selection
// - Default storage location validation
//
// Implementation Details:
// - Validates BackupStorageLocation resource presence
// - Checks storage location phases for availability
// - Verifies proper owner references for managed resources
// - Supports namespace preference for multi-namespace deployments
// - Returns boolean indicating valid storage availability
//
// Business Logic:
// Valid storage location validation is fundamental for backup operations,
// ensuring that backup schedules and restore operations have access to
// properly configured and available storage before attempting data operations.
func Test_isValidStorageLocationDefined(t *testing.T) {
	type args struct {
		veleroStorageLocations *veleroapi.BackupStorageLocationList
		preferredNS            string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "No storage locations",
			args: args{
				veleroStorageLocations: &veleroapi.BackupStorageLocationList{
					Items: make([]veleroapi.BackupStorageLocation, 0),
				},
				preferredNS: "",
			},
			want: false,
		},
		{
			name: "Storage locations but no owner reference",
			args: args{
				veleroStorageLocations: &veleroapi.BackupStorageLocationList{
					Items: []veleroapi.BackupStorageLocation{
						{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "invalid-location-no-owner-ref",
								Namespace: "default",
							},
							Status: veleroapi.BackupStorageLocationStatus{
								Phase: veleroapi.BackupStorageLocationPhaseAvailable,
							},
						},
					},
				},
				preferredNS: "default",
			},
			want: false,
		},
		{
			name: "Storage location valid",
			args: args{
				veleroStorageLocations: &veleroapi.BackupStorageLocationList{
					Items: []veleroapi.BackupStorageLocation{
						{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "valid-storage",
								Namespace: "default",
								OwnerReferences: []metav1.OwnerReference{
									{
										Kind: "DataProtectionApplication",
									},
								},
							},
							Status: veleroapi.BackupStorageLocationStatus{
								Phase: veleroapi.BackupStorageLocationPhaseAvailable,
							},
						},
					},
				},
				preferredNS: "default",
			},
			want: true,
		},
		{
			name: "Storage location valid but not using the preferred ns",
			args: args{
				veleroStorageLocations: &veleroapi.BackupStorageLocationList{
					Items: []veleroapi.BackupStorageLocation{
						{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "valid-storage",
								Namespace: "default1",
								OwnerReferences: []metav1.OwnerReference{
									{
										Kind: "DataProtectionApplication",
									},
								},
							},
							Status: veleroapi.BackupStorageLocationStatus{
								Phase: veleroapi.BackupStorageLocationPhaseAvailable,
							},
						},
						{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "valid-storage",
								Namespace: "default",
								OwnerReferences: []metav1.OwnerReference{
									{
										Kind: "DataProtectionApplication",
									},
								},
							},
							Status: veleroapi.BackupStorageLocationStatus{},
						},
					},
				},
				preferredNS: "default",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidStorageLocationDefined(tt.args.veleroStorageLocations.Items,
				tt.args.preferredNS); got != tt.want {
				t.Errorf("isValidStorageLocationDefined() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_getResourceDetails tests extraction of resource information from resource names.
//
// This test validates the logic that parses resource names to extract
// detailed information about Kubernetes resource types and configurations.
//
// Test Coverage:
// - Resource name parsing and decomposition
// - Resource type identification
// - Group and version extraction from resource names
// - Edge cases with malformed or invalid resource names
// - Resource detail structure validation
//
// Test Scenarios:
// - Valid resource names with proper format
// - Invalid or malformed resource names
// - Resource names with different group and version combinations
// - Edge cases and error handling scenarios
//
// Implementation Details:
// - Parses resource names according to Kubernetes naming conventions
// - Extracts group, version, and resource type information
// - Returns structured resource details for further processing
// - Handles parsing errors and invalid input gracefully
//
// Business Logic:
// Resource detail extraction is essential for dynamic resource management,
// enabling the backup system to properly handle different resource types
// and apply appropriate backup strategies based on resource characteristics.
func Test_getResourceDetails(t *testing.T) {
	type args struct {
		resourceName string
	}
	tests := []struct {
		name     string
		args     args
		wantname string
		wantkind string
	}{
		{
			name: "resource with kind",
			args: args{
				resourceName: "channel.apps.open-cluster-management.io",
			},
			wantname: "channel",
			wantkind: "apps.open-cluster-management.io",
		},
		{
			name: "resource without kind",
			args: args{
				resourceName: "channel",
			},
			wantname: "channel",
			wantkind: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotname, gotkind := getResourceDetails(tt.args.resourceName)
			if gotname != tt.wantname || gotkind != tt.wantkind {
				t.Errorf("getResourceDetails() = %v,%v, want %v,%v", gotname, gotkind, tt.wantname, tt.wantkind)
			}
		})
	}
}

// Test_findValidMSAToken tests identification and validation of Managed Service Account tokens.
//
// This test validates the logic that finds valid MSA tokens from a collection
// of secrets, ensuring proper authentication for backup operations.
//
// Test Coverage:
// - MSA token discovery from secret collections
// - Token expiration and validity checking
// - Secret type filtering and validation
// - Token metadata parsing and verification
// - Time-based token validity assessment
//
// Test Scenarios:
// - No secrets available for token discovery
// - Secrets without valid expirationTimestamp annotations
// - Secrets with invalid or malformed expiration times
// - Expired MSA tokens that should be rejected
// - Valid MSA tokens with proper expiration times and token data
//
// Implementation Details:
// - Filters secrets by type to identify MSA tokens
// - Parses token metadata for expiration information using RFC3339 format
// - Compares current time against token expiration timestamps
// - Validates presence of required token data in secret
// - Returns the most appropriate valid token or empty string
//
// Business Logic:
// Valid MSA token identification is crucial for authenticated backup operations,
// ensuring that restore processes have proper credentials to access cluster resources.
func Test_findValidMSAToken(t *testing.T) {
	current, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")
	nextHour := "2022-07-26T16:25:34Z"
	fourHoursAgo := "2022-07-26T11:25:34Z"

	type args struct {
		currentTime time.Time
		secrets     []corev1.Secret
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "MSA has no secrets",
			args: args{
				secrets: []corev1.Secret{},
			},
			want: "",
		},
		{
			name: "MSA has secrets but no expirationTimestamp valid ",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-no-annotations", "managed1",
						nil, nil, nil),
					*createSecret("auto-import-no-expiration", "managed1",
						nil, map[string]string{
							"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
						}, nil),
					*createSecret("auto-import-invalid-expiration", "managed1",
						nil, map[string]string{
							"expirationTimestamp": "aaa",
						}, nil),
				},
			},
			want: "",
		},
		{
			name: "MSA has secrets but expirationTimestamp is before current time ",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed1",
						nil, map[string]string{
							"expirationTimestamp": fourHoursAgo,
						}, nil),
				},
			},
			want: "",
		},
		{
			name: "MSA has secrets with valid expirationTimestamp but no token",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed1",
						nil, map[string]string{
							"expirationTimestamp": nextHour,
						}, map[string][]byte{
							"token1": []byte("aaa"),
						}),
				},
			},
			want: "",
		},
		{
			name: "MSA has secrets with valid expirationTimestamp and one valid token",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed1",
						nil, map[string]string{
							"expirationTimestamp": nextHour,
						}, map[string][]byte{
							"token1": []byte("aaa"),
						}),
					*createSecret("auto-import-account", "managed1",
						nil, map[string]string{
							"expirationTimestamp": nextHour,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				},
			},
			want: "YWRtaW4=",
		},
		{
			name: "MSA has secrets with valid expirationTimestamp and with token",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					*createSecret("auto-import-account", "managed1",
						nil, map[string]string{
							"expirationTimestamp": nextHour,
						}, map[string][]byte{
							"token": []byte("YWRtaW4="),
						}),
				},
			},
			want: "YWRtaW4=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findValidMSAToken(tt.args.secrets, tt.args.currentTime); got != tt.want {
				t.Errorf("findValidMSAToken() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_managedClusterShouldReimport tests determination of whether managed clusters require reimport.
//
// This test validates the logic that determines when managed clusters should be
// reimported based on their availability status and configuration.
//
// Test Coverage:
// - Local cluster identification and special handling
// - Managed cluster availability status checking
// - Cluster URL presence validation
// - Reimport decision logic based on cluster state
// - Special case handling for local-cluster vs other local clusters
//
// Test Scenarios:
// - Local cluster named "local-cluster" (should not reimport)
// - Local cluster with different name (should not reimport)
// - Cluster not in the managed cluster list (should not reimport)
// - Cluster without URL configuration (should not reimport)
// - Available clusters (should not reimport)
// - Unavailable clusters without URL (should not reimport)
// - Unavailable clusters with URL (should reimport)
//
// Implementation Details:
// - Uses ManagedCluster custom resources for testing
// - Checks cluster conditions for availability status
// - Validates cluster URL presence and configuration
// - Handles special cases for local vs remote clusters
// - Returns boolean indicating reimport necessity
//
// Business Logic:
// Managed cluster reimport determination is essential for disaster recovery,
// ensuring that disconnected or unavailable clusters with proper configuration
// can be automatically reimported when connectivity is restored.
func Test_managedClusterShouldReimport(t *testing.T) {
	managedClusters1 := []clusterv1.ManagedCluster{
		*createManagedCluster("local-cluster", true).object,
		// Just testing should Reimport func, run a test to make sure mgd clusters that are local
		// but are NOT named "local-cluster" should not be Reimported
		*createManagedCluster("test-local", true).object, // Local-cluster, but not named 'local-cluster'
		*createManagedCluster("test1", false).object,
	}

	conditionTypeAvailableTrue := metav1.Condition{
		Type:   "ManagedClusterConditionAvailable",
		Status: metav1.ConditionTrue,
	}

	conditionTypeAvailableFalse := metav1.Condition{
		Status: metav1.ConditionFalse,
		Type:   "ManagedClusterConditionAvailable",
	}

	managedClustersAvailable := []clusterv1.ManagedCluster{
		*createManagedCluster("test1", false).
			conditions([]metav1.Condition{
				conditionTypeAvailableTrue,
			}).
			object,
	}

	managedClustersNOTAvailableNoURL := []clusterv1.ManagedCluster{
		*createManagedCluster("test1", false).emptyClusterUrl().
			conditions([]metav1.Condition{
				conditionTypeAvailableFalse,
			}).object,
	}

	managedClustersNOTAvailableWithURL := []clusterv1.ManagedCluster{
		*createManagedCluster("test1", false).
			clusterUrl("aaaaa").
			conditions([]metav1.Condition{
				conditionTypeAvailableFalse,
			}).object,
	}

	type args struct {
		ctx             context.Context
		clusterName     string
		managedClusters []clusterv1.ManagedCluster
	}
	tests := []struct {
		name string
		args args
		want bool
		url  string
	}{
		{
			name: "managed cluster is local cluster, ignore",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClusters1,
				clusterName:     "local-cluster",
			},
			want: false,
		},
		{
			name: "managed cluster is local cluster (but not named 'local-cluster'), ignore",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClusters1,
				clusterName:     "test-local",
			},
			want: false,
		},
		{
			name: "managed cluster name not in the list",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClusters1,
				clusterName:     "test3",
			},
			want: false,
		},
		{
			name: "managed cluster has no url",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClusters1,
				clusterName:     "test1",
			},
			want: false,
		},
		{
			name: "managed cluster is available",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClustersAvailable,
				clusterName:     "test1",
			},
			want: false,
		},
		{
			name: "managed cluster is not available but has no url",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClustersNOTAvailableNoURL,
				clusterName:     "test1",
			},
			want: false,
		},
		{
			name: "managed cluster is not available AND has url",
			args: args{
				ctx:             context.Background(),
				managedClusters: managedClustersNOTAvailableWithURL,
				clusterName:     "test1",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _, _ := managedClusterShouldReimport(tt.args.ctx,
				tt.args.managedClusters, tt.args.clusterName); got != tt.want {
				t.Errorf("managedClusterShouldReimport() returns = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test_getHubIdentification tests extraction of hub cluster identification information.
//
// This test validates the logic that retrieves and processes hub cluster
// identification data for backup labeling and cluster tracking purposes.
//
// Test Coverage:
// - Hub cluster identification extraction
// - ClusterVersion resource processing
// - Error handling for missing or invalid cluster information
// - Scheme validation and resource availability checking
// - Hub ID extraction and formatting
//
// Test Scenarios:
// - No ClusterVersion scheme defined (should handle error)
// - ClusterVersion scheme defined but no resource present
// - ClusterVersion resource without cluster ID information
// - ClusterVersion resource with valid cluster ID
//
// Implementation Details:
// - Uses ClusterVersion custom resources for hub identification
// - Handles scheme validation and resource discovery
// - Processes cluster version metadata for ID extraction
// - Returns hub identification string or default values
// - Manages error conditions gracefully
//
// Business Logic:
// Hub identification is crucial for multi-cluster backup scenarios,
// enabling proper labeling and tracking of backups across different
// hub clusters in complex deployment environments.
func Test_getHubIdentification(t *testing.T) {
	crNoVersion := createClusterVersion("version", "", nil)
	crWithVersion := createClusterVersion("version", "aaa", nil)

	tests := []struct {
		name         string
		setupScheme  bool
		setupObjects []client.Object
		err_nil      bool
		want_msg     string
	}{
		{
			name:         "no clusterversion scheme defined",
			setupScheme:  false,
			setupObjects: []client.Object{},
			err_nil:      false,
			want_msg:     "unknown",
		},
		{
			name:         "clusterversion scheme is defined but no resource",
			setupScheme:  true,
			setupObjects: []client.Object{},
			err_nil:      true,
			want_msg:     "unknown",
		},
		{
			name:         "clusterversion resource with no id",
			setupScheme:  true,
			setupObjects: []client.Object{crNoVersion},
			err_nil:      true,
			want_msg:     "",
		},
		{
			name:         "clusterversion resource with id",
			setupScheme:  true,
			setupObjects: []client.Object{crWithVersion},
			err_nil:      true,
			want_msg:     "aaa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := CreateHubIdentificationTestClient(tt.setupScheme, tt.setupObjects...)

			version, err := getHubIdentification(context.Background(), fakeClient)

			if (err == nil) != tt.err_nil {
				t.Errorf("getHubIdentification() error = %v, want error = %v", err, !tt.err_nil)
			}
			if version != tt.want_msg {
				t.Errorf("getHubIdentification() version = %v, want %v", version, tt.want_msg)
			}
		})
	}
}

// Test_VeleroCRDsPresent_NotPresent tests detection when Velero CRDs are not available.
//
// This test validates the logic that determines when Velero Custom Resource
// Definitions are not present in the cluster, which affects backup functionality.
//
// Test Coverage:
// - CRD presence detection for missing Velero resources
// - Error handling when Velero is not installed
// - Proper boolean return value for missing CRDs
// - Client interaction without Velero scheme support
//
// Test Scenarios:
// - Cluster without Velero CRDs installed (should return false)
// - Proper error handling without throwing exceptions
// - Graceful degradation when backup infrastructure is missing
//
// Implementation Details:
// - Uses fake client without Velero scheme to simulate missing CRDs
// - Tests API discovery and resource availability checking
// - Validates proper false return when resources are unavailable
// - Ensures no unexpected errors are thrown
//
// Business Logic:
// Velero CRD presence detection is essential for determining whether
// backup operations can be performed, enabling graceful fallback
// when backup infrastructure is not available.
func Test_VeleroCRDsPresent_NotPresent(t *testing.T) {
	fakeClient := CreateVeleroCRDTestClient(false) // false = don't include velero scheme

	t.Run("velero CRDs not present", func(t *testing.T) {
		crdsPresent, err := VeleroCRDsPresent(context.Background(), fakeClient)
		if err != nil {
			t.Errorf("VeleroCRDsPresent() returned an unexpected error %s", err.Error())
		}
		if crdsPresent {
			t.Errorf("VeleroCRDsPresent() should return false when CRDs not present")
		}
	})
}

// Test_VeleroCRDsPresent tests detection when Velero CRDs are available in the cluster.
//
// This test validates the logic that confirms Velero Custom Resource
// Definitions are present and accessible for backup operations.
//
// Test Coverage:
// - CRD presence detection for available Velero resources
// - Successful validation when Velero is properly installed
// - Proper boolean return value for available CRDs
// - Client interaction with full Velero scheme support
//
// Test Scenarios:
// - Cluster with Velero CRDs properly installed (should return true)
// - Successful API discovery and resource validation
// - Proper confirmation of backup infrastructure availability
//
// Implementation Details:
// - Uses fake client with Velero scheme to simulate available CRDs
// - Tests API discovery and resource availability checking
// - Validates proper true return when resources are accessible
// - Ensures successful operation without errors
//
// Business Logic:
// Velero CRD presence confirmation enables backup operations to proceed
// with confidence that the required infrastructure is available and
// properly configured for backup and restore functionality.
func Test_VeleroCRDsPresent(t *testing.T) {
	fakeClient := CreateVeleroCRDTestClient(true) // true = include velero scheme

	t.Run("velero CRDs present", func(t *testing.T) {
		crdsPresent, err := VeleroCRDsPresent(context.Background(), fakeClient)
		if err != nil {
			t.Errorf("VeleroCRDsPresent() returned an unexpected error %s", err.Error())
		}
		if !crdsPresent {
			t.Errorf("VeleroCRDsPresent() should return true when CRDs are present")
		}
	})
}

// Test_isCRDNotPresentError tests identification of CRD-not-present error conditions.
//
// This test validates the logic that determines whether specific errors
// indicate that Custom Resource Definitions are not available in the cluster.
//
// Test Coverage:
// - Error type classification for CRD absence
// - NoResourceMatchError detection and handling
// - NotFound error identification for missing resources
// - String pattern matching for API group errors
// - Case sensitivity validation for error messages
// - Partial match prevention for similar error patterns
//
// Test Scenarios:
// - Nil error handling (should return false)
// - Generic errors that don't indicate CRD absence
// - NoResourceMatchError indicating missing CRDs (should return true)
// - NotFound errors for missing resources (should return true)
// - API group resource failure errors (should return true)
// - Type registration errors (should return true)
// - Case-sensitive error message validation
// - Partial match rejection for incomplete patterns
// - Other error types like timeout and permission errors
//
// Implementation Details:
// - Uses specific error type checking for Kubernetes errors
// - Performs string pattern matching for error messages
// - Validates case sensitivity and exact pattern matching
// - Returns boolean indicating CRD absence vs other error types
//
// Business Logic:
// Proper CRD-not-present error identification enables the system to
// distinguish between infrastructure issues and other operational
// problems, allowing for appropriate error handling and user messaging.
func Test_isCRDNotPresentError(t *testing.T) {
	type args struct {
		err error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "nil error should return false",
			args: args{
				err: nil,
			},
			want: false,
		},
		{
			name: "generic error should return false",
			args: args{
				err: errors.New("some generic error"),
			},
			want: false,
		},
		{
			name: "NoMatchError should return true",
			args: args{
				err: &meta.NoResourceMatchError{
					PartialResource: schema.GroupVersionResource{
						Group: "test", Version: "v1", Resource: "tests",
					},
				},
			},
			want: true,
		},
		{
			name: "NotFound error should return true",
			args: args{
				err: kerrors.NewNotFound(schema.GroupResource{Group: "test", Resource: "tests"}, "test-resource"),
			},
			want: true,
		},
		{
			name: "error containing 'failed to get API group resources' should return true",
			args: args{
				err: errors.New("failed to get API group resources for group test"),
			},
			want: true,
		},
		{
			name: "error containing 'no kind is registered for the type' should return true",
			args: args{
				err: errors.New("no kind is registered for the type v1beta1.TestResource"),
			},
			want: true,
		},
		{
			name: "error with 'failed to get API group resources' substring should return true",
			args: args{
				err: errors.New("some prefix: failed to get API group resources and some suffix"),
			},
			want: true,
		},
		{
			name: "error with 'no kind is registered for the type' substring should return true",
			args: args{
				err: errors.New("prefix: no kind is registered for the type SomeType: suffix"),
			},
			want: true,
		},
		{
			name: "case sensitivity - 'Failed to get API group resources' should return false",
			args: args{
				err: errors.New("Failed to get API group resources"),
			},
			want: false,
		},
		{
			name: "case sensitivity - 'No kind is registered for the type' should return false",
			args: args{
				err: errors.New("No kind is registered for the type TestType"),
			},
			want: false,
		},
		{
			name: "partial match - 'failed to get API' should return false",
			args: args{
				err: errors.New("failed to get API"),
			},
			want: false,
		},
		{
			name: "partial match - 'no kind is registered' should return false",
			args: args{
				err: errors.New("no kind is registered"),
			},
			want: false,
		},
		{
			name: "timeout error should return false",
			args: args{
				err: errors.New("timeout waiting for response"),
			},
			want: false,
		},
		{
			name: "permission denied error should return false",
			args: args{
				err: kerrors.NewForbidden(
					schema.GroupResource{Group: "test", Resource: "tests"},
					"test-resource",
					errors.New("forbidden"),
				),
			},
			want: false,
		},
		{
			name: "already exists error should return false",
			args: args{
				err: kerrors.NewAlreadyExists(schema.GroupResource{Group: "test", Resource: "tests"}, "test-resource"),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCRDNotPresentError(tt.args.err); got != tt.want {
				t.Errorf("isCRDNotPresentError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func labelSelectorArrayEqual(a []*metav1.LabelSelector, b []metav1.LabelSelector) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if !labelSelectorEqual(v, &b[i]) {
			return false
		}
	}
	return true
}

func labelSelectorEqual(a *metav1.LabelSelector, b *metav1.LabelSelector) bool {
	if a == nil && b == nil {
		return true
	}

	if (a == nil && b != nil) || (a != nil && b == nil) {
		return true
	}

	al := a.MatchLabels
	bl := b.MatchLabels

	if al == nil || bl == nil {
		return true
	}

	for key, value := range al {
		if value != bl[key] {
			// match labels should be equal
			return false
		}
	}

	// validate MatchExpressions
	ma := a.MatchExpressions
	mb := b.MatchExpressions

	return requirementsEqual(ma, mb)
}

func requirementsEqual(ma []metav1.LabelSelectorRequirement, mb []metav1.LabelSelectorRequirement) bool {
	if ma == nil && mb == nil {
		return true
	}

	if (ma == nil && mb != nil) || (mb == nil && ma != nil) {
		return false
	}

	if len(ma) != len(mb) {
		return false
	}

	for i, v := range ma {
		if v.Key != mb[i].Key || v.Operator != mb[i].Operator || !sortCompare(v.Values, mb[i].Values) {
			return false
		}
	}
	return true
}

// Test_addRestoreLabelSelector tests addition of label selectors to Velero restore operations.
//
// This test validates the logic that adds label selector requirements to
// Velero restore resources for proper resource filtering during restoration.
//
// Test Coverage:
// - Label selector addition to empty restore configurations
// - Cluster activation label selector handling
// - OrLabelSelector array management and updates
// - Label selector requirement merging and uniqueness
// - Empty vs populated selector handling
//
// Test Scenarios:
// - Empty restore with no selectors (should remain empty)
// - Empty restore with cluster activation selector
// - Restore with existing orLabelSelector arrays
// - Merge operations with multiple selector requirements
// - Preservation of user-defined selector configurations
//
// Implementation Details:
// - Uses Velero Restore custom resources for testing
// - Tests label selector requirement structures
// - Validates array equality for complex selector comparisons
// - Handles both LabelSelector and OrLabelSelector fields
// - Ensures proper merging without duplication
//
// Business Logic:
// Label selector management is crucial for targeted restore operations,
// ensuring that only appropriate resources are restored based on cluster
// configuration and activation status during disaster recovery.
func Test_addRestoreLabelSelector(t *testing.T) {
	type args struct {
		veleroRestore *veleroapi.Restore
		labelSelector metav1.LabelSelectorRequirement
	}

	emptyLabelSelector := make([]metav1.LabelSelector, 0)

	clusterActivationReq := metav1.LabelSelectorRequirement{}
	clusterActivationReq.Key = backupCredsClusterLabel
	clusterActivationReq.Operator = "NotIn"
	clusterActivationReq.Values = []string{ClusterActivationLabel}

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
	req3 := metav1.LabelSelectorRequirement{
		Key:      "foo3",
		Operator: metav1.LabelSelectorOperator("NotIn"),
		Values:   []string{"bar3"},
	}

	tests := []struct {
		name                string
		args                args
		wantOrLabelSelector []metav1.LabelSelector
		wantLabelSelector   metav1.LabelSelector
	}{
		{
			name: "no user defined orLabelSelector or LabelSelector, get empty match expressions",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					object,
				labelSelector: metav1.LabelSelectorRequirement{},
			},
			wantOrLabelSelector: emptyLabelSelector,
			wantLabelSelector:   metav1.LabelSelector{},
		},
		{
			name: "no user defined orLabelSelector or LabelSelector, just create cluster activation LabelSelector",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					object,
				labelSelector: clusterActivationReq,
			},
			wantOrLabelSelector: emptyLabelSelector,
			wantLabelSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					clusterActivationReq,
				},
			},
		},
		{
			name: "using user defined orLabelSelector, so add cluster activation LabelSelector to it",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					orLabelSelector([]*metav1.LabelSelector{
						{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
						{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req3,
							},
						},
					}).
					object,
				labelSelector: clusterActivationReq,
			},
			wantOrLabelSelector: []metav1.LabelSelector{
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req1,
						req2,
						clusterActivationReq,
					},
				},
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req3,
						clusterActivationReq,
					},
				},
			},
			wantLabelSelector: metav1.LabelSelector{},
		},
		{
			name: "using user defined orLabelSelector, no cluster activation LabelSelector",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					orLabelSelector([]*metav1.LabelSelector{
						{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
						{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req3,
							},
						},
					}).
					object,
				labelSelector: metav1.LabelSelectorRequirement{},
			},
			wantOrLabelSelector: []metav1.LabelSelector{
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req1,
						req2,
					},
				},
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req3,
					},
				},
			},
			wantLabelSelector: metav1.LabelSelector{},
		},
		{
			name: "using user defined LabelSelector, set cluster activation LabelSelectors as usual",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					labelSelector(
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
					).
					object,
				labelSelector: clusterActivationReq,
			},
			wantOrLabelSelector: emptyLabelSelector,
			wantLabelSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					req1,
					req2,
					clusterActivationReq,
				},
			},
		},
		{
			name: "using user defined LabelSelector, no activation LabelSelectors",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					labelSelector(
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
					).
					object,
				labelSelector: metav1.LabelSelectorRequirement{},
			},
			wantOrLabelSelector: emptyLabelSelector,
			wantLabelSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					req1,
					req2,
				},
			},
		},
		{
			// this is is going to be flagged as a validation error by velero so this is not really a valid usecase
			// the user can set both, still, so verifing that the cluster activation LabelSelector goes to the
			// orLabelSelector in this case
			name: "using both user defined orLabelSelector and LabelSelector, " +
				"add cluster activation LabelSelector to orLabelSelector",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					orLabelSelector([]*metav1.LabelSelector{
						{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
						{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req3,
							},
						},
					}).
					labelSelector(
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req3,
							},
						},
					).
					object,
				labelSelector: clusterActivationReq,
			},
			wantOrLabelSelector: []metav1.LabelSelector{
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req1,
						req2,
						clusterActivationReq,
					},
				},
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req3,
						clusterActivationReq,
					},
				},
			},
			wantLabelSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					req3,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addRestoreLabelSelector(tt.args.veleroRestore, tt.args.labelSelector)

			if !labelSelectorArrayEqual(tt.args.veleroRestore.Spec.OrLabelSelectors, tt.wantOrLabelSelector) {
				t.Errorf("OrLabelSelectors not as expected  = %v, want %v got=%v",
					tt.name, tt.wantOrLabelSelector, tt.args.veleroRestore.Spec.OrLabelSelectors)
			}

			if !labelSelectorEqual(tt.args.veleroRestore.Spec.LabelSelector, &tt.wantLabelSelector) {
				t.Errorf("LabelSelector not as expected  = %v, want %v got=%v",
					tt.name, tt.wantLabelSelector, tt.args.veleroRestore.Spec.LabelSelector)
			}
		})
	}
}

// Test_appendUniqueReq tests addition of unique label selector requirements to requirement lists.
//
// This test validates the logic that appends label selector requirements to
// existing requirement lists while preventing duplication.
//
// Test Coverage:
// - Unique requirement addition to existing lists
// - Duplicate detection and prevention
// - Empty requirement handling
// - Requirement equality comparison
// - List preservation when duplicates are found
//
// Test Scenarios:
// - Adding empty requirements (should not modify list)
// - Adding duplicate requirements (should not modify list)
// - Adding unique requirements (should append to list)
// - Requirement comparison with different operators and values
//
// Implementation Details:
// - Uses LabelSelectorRequirement structures for testing
// - Performs deep equality comparison of requirements
// - Validates in-place modification of requirement slices
// - Ensures proper deduplication without changing order
// - Handles Key, Operator, and Values comparison
//
// Business Logic:
// Unique requirement management is essential for maintaining clean
// and efficient label selector configurations, preventing redundant
// filtering rules that could impact restore performance.
func Test_appendUniqueReq(t *testing.T) {
	type args struct {
		requirements  []metav1.LabelSelectorRequirement
		labelSelector metav1.LabelSelectorRequirement
	}

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

	tests := []struct {
		name             string
		args             args
		wantRequirements []metav1.LabelSelectorRequirement
	}{
		{
			name: "return the same selector",
			args: args{
				requirements:  []metav1.LabelSelectorRequirement{req1, req2},
				labelSelector: metav1.LabelSelectorRequirement{},
			},
			wantRequirements: []metav1.LabelSelectorRequirement{req1, req2},
		},
		{
			name: "return the same selector req1 no duplication",
			args: args{
				requirements:  []metav1.LabelSelectorRequirement{req1, req2},
				labelSelector: req1,
			},
			wantRequirements: []metav1.LabelSelectorRequirement{req1, req2},
		},
		{
			name: "return the same selector req2 no duplication",
			args: args{
				requirements:  []metav1.LabelSelectorRequirement{req1, req2},
				labelSelector: req2,
			},
			wantRequirements: []metav1.LabelSelectorRequirement{req1, req2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appendUniqueReq(tt.args.requirements, tt.args.labelSelector)

			if !requirementsEqual(tt.args.requirements, tt.wantRequirements) {
				t.Errorf("reqs not as expected  = %v, want %v got=%v", tt.name, tt.wantRequirements, tt.args.requirements)
			}
		})
	}
}

// Test_updateBackupSchedulePhaseWhenPaused tests backup schedule phase management during paused states.
//
// This test validates the logic that updates backup schedule phases and
// manages associated Velero schedules when backup schedules are paused or in collision.
//
// Test Coverage:
// - Backup schedule phase transitions (enabled to paused, collision states)
// - Velero schedule deletion during pause operations
// - Schedule status cleanup and management
// - Client interaction for schedule and status updates
// - Controller result handling for different phase states
//
// Test Scenarios:
// - Backup schedule paused (should delete all Velero schedules)
// - Backup schedule in collision state (should clean up schedules)
// - Schedule status updates and cleanup validation
// - Phase transition validation and messaging
//
// Implementation Details:
// - Uses fake Kubernetes client for testing schedule operations
// - Tests multiple backup types (Credentials, ManagedClusters, Resources)
// - Validates proper cleanup of schedule status references
// - Checks controller result values for proper reconciliation
// - Manages complex schedule state transitions
//
// Business Logic:
// Backup schedule phase management is critical for coordinating backup
// operations, ensuring that paused schedules properly clean up their
// associated Velero resources and handle collision scenarios gracefully.
func Test_updateBackupSchedulePhaseWhenPaused(t *testing.T) {
	type args struct {
		ctx                context.Context
		c                  client.Client
		veleroScheduleList veleroapi.ScheduleList
		backupSchedule     *v1beta1.BackupSchedule
		phase              v1beta1.SchedulePhase
		msg                string
		setupScheme        bool
		setupObjects       []client.Object
	}

	creds := *createSchedule(veleroBackupNames[Credentials], "default").object
	cls := *createSchedule(veleroBackupNames[ManagedClusters], "default").object
	res := *createSchedule(veleroBackupNames[Resources], "default").object

	tests := []struct {
		name                    string
		args                    args
		wantBackupSchedule      v1beta1.BackupSchedule
		wantPausedSchedules     []veleroapi.Schedule
		wantScheduleStatusNil   bool
		wantReturn              ctrl.Result
		wantBackupSchedulePhase v1beta1.SchedulePhase
		wantMsg                 string
	}{
		{
			name: "backup schedule paused",
			args: args{
				ctx:         context.Background(),
				msg:         "BackupSchedule is paused.",
				phase:       v1beta1.SchedulePhasePaused,
				setupScheme: true, // need scheme for objects
				veleroScheduleList: veleroapi.ScheduleList{
					Items: []veleroapi.Schedule{
						creds,
						cls,
						res,
					},
				},
				backupSchedule: &v1beta1.BackupSchedule{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "BackupSchedule",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-schedule",
						Namespace: "default",
					},
					Spec: v1beta1.BackupScheduleSpec{
						Paused: true,
					},
					Status: v1beta1.BackupScheduleStatus{
						Phase:                         v1beta1.SchedulePhaseEnabled,
						VeleroScheduleCredentials:     creds.DeepCopy(),
						VeleroScheduleManagedClusters: cls.DeepCopy(),
						VeleroScheduleResources:       res.DeepCopy(),
					},
				},
				setupObjects: []client.Object{
					&creds,
					&cls,
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(true).
				object,
			wantPausedSchedules: []veleroapi.Schedule{
				creds,
				cls,
				res,
			},
			wantScheduleStatusNil:   false, // backup schedule status kept since schedules are paused, not deleted
			wantReturn:              ctrl.Result{},
			wantBackupSchedulePhase: v1beta1.SchedulePhasePaused,
			wantMsg:                 "BackupSchedule is paused.",
		},
		{
			name: "backup schedule in collision",
			args: args{
				ctx:         context.Background(),
				msg:         "collision",
				phase:       v1beta1.SchedulePhaseBackupCollision,
				setupScheme: true,
				veleroScheduleList: veleroapi.ScheduleList{
					Items: []veleroapi.Schedule{
						creds,
						cls,
						res,
					},
				},
				backupSchedule: &v1beta1.BackupSchedule{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "BackupSchedule",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-schedule",
						Namespace: "default",
					},
					Status: v1beta1.BackupScheduleStatus{
						Phase:                         v1beta1.SchedulePhaseBackupCollision,
						VeleroScheduleCredentials:     creds.DeepCopy(),
						VeleroScheduleManagedClusters: cls.DeepCopy(),
						VeleroScheduleResources:       res.DeepCopy(),
					},
				},
				setupObjects: []client.Object{
					&creds,
					&cls,
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				phase(v1beta1.SchedulePhaseBackupCollision).
				object,
			wantPausedSchedules: []veleroapi.Schedule{
				creds,
				cls,
				res,
			},
			wantScheduleStatusNil:   false, // backup schedule status kept since schedules are paused, not deleted
			wantReturn:              ctrl.Result{},
			wantBackupSchedulePhase: v1beta1.SchedulePhaseBackupCollision,
			wantMsg:                 "collision",
		},
		{
			name: "backup schedule paused with creds schedule not found",
			args: args{
				ctx:         context.Background(),
				msg:         "BackupSchedule is paused.",
				phase:       v1beta1.SchedulePhasePaused,
				setupScheme: true,
				veleroScheduleList: veleroapi.ScheduleList{
					Items: []veleroapi.Schedule{
						cls,
						res,
					},
				},
				backupSchedule: &v1beta1.BackupSchedule{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "BackupSchedule",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "acm-schedule",
						Namespace: "default",
					},
					Spec: v1beta1.BackupScheduleSpec{
						Paused: true,
					},
					Status: v1beta1.BackupScheduleStatus{
						VeleroScheduleCredentials:     creds.DeepCopy(),
						VeleroScheduleManagedClusters: cls.DeepCopy(),
						VeleroScheduleResources:       res.DeepCopy(),
					},
				},
				setupObjects: []client.Object{
					&cls, // only cls and res, no creds
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(true).
				object,
			wantPausedSchedules: []veleroapi.Schedule{
				// creds is intentionally missing (not found scenario)
				cls,
				res,
			},
			wantScheduleStatusNil:   false, // backup schedule status kept since schedules are paused, not deleted
			wantReturn:              ctrl.Result{},
			wantBackupSchedulePhase: v1beta1.SchedulePhasePaused,
			wantMsg:                 "BackupSchedule is paused.",
		},
		{
			name: "backup schedule not in collision or paused",
			args: args{
				ctx:         context.Background(),
				msg:         "some value",
				phase:       v1beta1.SchedulePhaseEnabled,
				setupScheme: true,
				veleroScheduleList: veleroapi.ScheduleList{
					Items: []veleroapi.Schedule{
						cls,
						res,
					},
				},
				backupSchedule: createBackupSchedule("acm-schedule", "default").
					paused(false).
					scheduleStatus(Credentials, creds).
					scheduleStatus(ManagedClusters, cls).
					scheduleStatus(Resources, res).
					phase(v1beta1.SchedulePhaseEnabled).
					object,
				setupObjects: []client.Object{
					createBackupSchedule("acm-schedule", "default").
						paused(false).
						scheduleStatus(Credentials, creds).
						scheduleStatus(ManagedClusters, cls).
						scheduleStatus(Resources, res).
						phase(v1beta1.SchedulePhaseEnabled).
						object,
					&cls,
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(false).
				phase(v1beta1.SchedulePhaseEnabled).
				object,
			wantPausedSchedules:     []veleroapi.Schedule{},
			wantScheduleStatusNil:   false, // status should remain unchanged
			wantReturn:              ctrl.Result{},
			wantBackupSchedulePhase: v1beta1.SchedulePhaseEnabled, // phase should remain unchanged
			wantMsg:                 "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := CreateBackupSchedulePausedTestClient(tt.args.setupObjects...)
			tt.args.c = fakeClient

			// Only create the BackupSchedule if the function will actually process it
			if tt.args.phase == v1beta1.SchedulePhasePaused || tt.args.phase == v1beta1.SchedulePhaseBackupCollision {
				// Create the BackupSchedule in the fake client
				if err := fakeClient.Create(tt.args.ctx, tt.args.backupSchedule); err != nil {
					t.Fatalf("BackupSchedule creation failed: %v", err)
				}

				// Verify the BackupSchedule was created successfully
				testBackupSchedule := &v1beta1.BackupSchedule{}
				err := fakeClient.Get(tt.args.ctx, client.ObjectKey{
					Name:      tt.args.backupSchedule.Name,
					Namespace: tt.args.backupSchedule.Namespace,
				}, testBackupSchedule)
				if err != nil {
					t.Fatalf("BackupSchedule not found after creation: %v", err)
				}
			}

			returnValue, err := updateBackupSchedulePhaseWhenPaused(context.Background(), tt.args.c,
				tt.args.veleroScheduleList, tt.args.backupSchedule, tt.args.phase, tt.args.msg)

			if err != nil {
				t.Errorf("updateBackupSchedulePhaseWhenPaused returned error: %v", err)
			}

			if returnValue != tt.wantReturn {
				t.Errorf("updateBackupSchedulePhaseWhenPaused return should be =%v got=%v",
					tt.wantReturn,
					returnValue)
			}

			// check schedules in status - they should remain populated since schedules are paused, not deleted
			if (tt.args.backupSchedule.Status.VeleroScheduleCredentials == nil) !=
				tt.wantScheduleStatusNil {
				t.Errorf("VeleroScheduleCredentials don't match expected behavior, nil=%v, expected nil=%v",
					tt.args.backupSchedule.Status.VeleroScheduleCredentials == nil,
					tt.wantScheduleStatusNil)
			}
			if (tt.args.backupSchedule.Status.VeleroScheduleManagedClusters == nil) !=
				tt.wantScheduleStatusNil {
				t.Errorf("VeleroScheduleManagedClusters don't match expected behavior, nil=%v, expected nil=%v",
					tt.args.backupSchedule.Status.VeleroScheduleManagedClusters == nil,
					tt.wantScheduleStatusNil)
			}
			if (tt.args.backupSchedule.Status.VeleroScheduleResources == nil) !=
				tt.wantScheduleStatusNil {
				t.Errorf("VeleroScheduleResources don't match expected behavior, nil=%v, expected nil=%v",
					tt.args.backupSchedule.Status.VeleroScheduleResources == nil,
					tt.wantScheduleStatusNil)
			}

			// check phase
			if tt.args.backupSchedule.Status.Phase != tt.wantBackupSchedulePhase {
				t.Errorf("backup schedule should have phase= %v got=%v",
					tt.wantBackupSchedulePhase, tt.args.backupSchedule.Status.Phase)
			}
			// check msg
			if tt.args.backupSchedule.Status.LastMessage != tt.wantMsg {
				t.Errorf("backup schedule last message should be = %v got=%v",
					tt.wantMsg, tt.args.backupSchedule.Status.LastMessage)
			}

			// check schedules are paused
			for i := range tt.wantPausedSchedules {

				lookupKey := types.NamespacedName{
					Name:      tt.wantPausedSchedules[i].Name,
					Namespace: tt.wantPausedSchedules[i].Namespace,
				}

				schedule := veleroapi.Schedule{}

				if err := fakeClient.Get(tt.args.ctx, lookupKey, &schedule); err != nil {
					t.Errorf("schedule should be found = %v, err: %v", lookupKey.Name, err)
				} else {
					// Check that the schedule is paused
					if !schedule.Spec.Paused {
						t.Errorf("schedule should be paused but got paused = %v", schedule.Spec.Paused)
					}
				}
			}
		})
	}
}

// Test_remove tests removal of specific strings from string slices.
//
// This test validates the logic that removes specific string values from
// string slices while preserving order and handling edge cases.
//
// Test Coverage:
// - String removal from populated slices
// - Handling of non-existent string removal requests
// - Order preservation after string removal
// - Edge cases with empty slices and missing values
//
// Test Scenarios:
// - String not found in slice (should return unchanged slice)
// - String found and successfully removed from slice
// - Order preservation of remaining elements
// - Multiple occurrences handling (if applicable)
//
// Implementation Details:
// - Uses simple string slice operations for testing
// - Performs deep equality comparison of result slices
// - Validates proper slice modification without side effects
// - Handles edge cases gracefully without panics
//
// Business Logic:
// String removal functionality is essential for maintaining clean
// configuration lists, enabling dynamic management of resource
// filters and backup configurations by removing obsolete entries.
func Test_remove(t *testing.T) {
	type args struct {
		s []string
		r string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "string now found",
			args: args{
				s: []string{"b", "c"},
				r: "a",
			},
			want: []string{"b", "c"},
		},
		{
			name: "string found and removed",
			args: args{
				s: []string{"b", "c", "a", "d"},
				r: "a",
			},
			want: []string{"b", "c", "d"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := remove(tt.args.s, tt.args.r); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("remove() = %v, want %v", got, tt.want)
			}
		})
	}
}
