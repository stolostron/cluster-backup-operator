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
		wantDeletedSchedules    []veleroapi.Schedule
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
				backupSchedule: createBackupSchedule("acm-schedule", "default").
					paused(true).
					phase(v1beta1.SchedulePhaseEnabled).
					scheduleStatus(Credentials, creds).
					scheduleStatus(ManagedClusters, cls).
					scheduleStatus(Resources, res).
					object,
				setupObjects: []client.Object{
					createBackupSchedule("acm-schedule", "default").
						paused(true).
						phase(v1beta1.SchedulePhaseEnabled).
						scheduleStatus(Credentials, creds).
						scheduleStatus(ManagedClusters, cls).
						scheduleStatus(Resources, res).
						object,
					&creds,
					&cls,
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(true).
				object,
			wantDeletedSchedules: []veleroapi.Schedule{
				creds,
				cls,
				res,
			},
			wantScheduleStatusNil:   true, // backup schedule status gets updated with fake client
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
				backupSchedule: createBackupSchedule("acm-schedule", "default").
					scheduleStatus(Credentials, creds).
					scheduleStatus(ManagedClusters, cls).
					scheduleStatus(Resources, res).
					phase(v1beta1.SchedulePhaseBackupCollision).
					object,
				setupObjects: []client.Object{
					createBackupSchedule("acm-schedule", "default").
						scheduleStatus(Credentials, creds).
						scheduleStatus(ManagedClusters, cls).
						scheduleStatus(Resources, res).
						phase(v1beta1.SchedulePhaseBackupCollision).
						object,
					&creds,
					&cls,
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				phase(v1beta1.SchedulePhaseBackupCollision).
				object,
			wantDeletedSchedules: []veleroapi.Schedule{
				creds,
				cls,
				res,
			},
			wantScheduleStatusNil:   true,
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
				backupSchedule: createBackupSchedule("acm-schedule", "default").
					paused(true).
					scheduleStatus(Credentials, creds).
					scheduleStatus(ManagedClusters, cls).
					scheduleStatus(Resources, res).
					object,
				setupObjects: []client.Object{
					createBackupSchedule("acm-schedule", "default").
						paused(true).
						scheduleStatus(Credentials, creds).
						scheduleStatus(ManagedClusters, cls).
						scheduleStatus(Resources, res).
						object,
					&cls, // only cls and res, no creds
					&res,
				},
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(true).
				object,
			wantDeletedSchedules: []veleroapi.Schedule{
				creds,
				cls,
				res,
			},
			wantScheduleStatusNil:   true,
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
			wantDeletedSchedules:    []veleroapi.Schedule{},
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

			returnValue, _ := updateBackupSchedulePhaseWhenPaused(tt.args.ctx, tt.args.c,
				tt.args.veleroScheduleList, tt.args.backupSchedule, tt.args.phase, tt.args.msg)

			if returnValue != tt.wantReturn {
				t.Errorf("updateBackupSchedulePhaseWhenPaused return should be =%v got=%v",
					tt.wantReturn,
					returnValue)
			}

			// check schedules in status
			if (tt.args.backupSchedule.Status.VeleroScheduleCredentials == nil) !=
				tt.wantScheduleStatusNil {
				t.Errorf("VeleroScheduleCredentials don't match , should be nil =%v got should be nil =%v",
					tt.args.backupSchedule.Status.VeleroScheduleCredentials,
					tt.wantScheduleStatusNil)
			}
			if (tt.args.backupSchedule.Status.VeleroScheduleManagedClusters == nil) !=
				tt.wantScheduleStatusNil {
				t.Errorf("VeleroScheduleManagedClusters don't match , should be  =%v got=%v",
					tt.args.backupSchedule.Status.VeleroScheduleManagedClusters,
					tt.wantScheduleStatusNil)
			}
			if (tt.args.backupSchedule.Status.VeleroScheduleResources == nil) !=
				tt.wantScheduleStatusNil {
				t.Errorf("VeleroScheduleResources don't match , should be  =%v got=%v",
					tt.args.backupSchedule.Status.VeleroScheduleResources,
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

			// check schedules are deleted
			for i := range tt.wantDeletedSchedules {

				lookupKey := types.NamespacedName{
					Name:      tt.wantDeletedSchedules[i].Name,
					Namespace: tt.wantDeletedSchedules[i].Namespace,
				}

				schedule := veleroapi.Schedule{}

				if err := fakeClient.Get(tt.args.ctx, lookupKey, &schedule); err == nil {
					t.Errorf(" schedule should not be found = %v", lookupKey.Name)
				}
			}
		})
	}
}

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
