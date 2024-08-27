// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	ocinfrav1 "github.com/openshift/api/config/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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
						veleroapi.BackupStorageLocation{
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
						veleroapi.BackupStorageLocation{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "valid-storage",
								Namespace: "default",
								OwnerReferences: []v1.OwnerReference{
									v1.OwnerReference{
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
						veleroapi.BackupStorageLocation{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "valid-storage",
								Namespace: "default1",
								OwnerReferences: []v1.OwnerReference{
									v1.OwnerReference{
										Kind: "DataProtectionApplication",
									},
								},
							},
							Status: veleroapi.BackupStorageLocationStatus{
								Phase: veleroapi.BackupStorageLocationPhaseAvailable,
							},
						},
						veleroapi.BackupStorageLocation{
							TypeMeta: metav1.TypeMeta{
								APIVersion: "velero/v1",
								Kind:       "BackupStorageLocation",
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "valid-storage",
								Namespace: "default",
								OwnerReferences: []v1.OwnerReference{
									v1.OwnerReference{
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
				}},
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
				}},
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
				}},
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
				}},
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
				}},
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

	conditionTypeAvailableTrue := v1.Condition{
		Type:   "ManagedClusterConditionAvailable",
		Status: v1.ConditionTrue,
	}

	conditionTypeAvailableFalse := v1.Condition{
		Status: v1.ConditionFalse,
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

	type args struct {
		ctx context.Context
		c   client.Client
	}
	tests := []struct {
		name     string
		args     args
		err_nil  bool
		want_msg string
		url      string
	}{
		{
			name: "no clusterversion scheme defined",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
			},
			err_nil:  false,
			want_msg: "unknown",
		},
		{
			name: "clusterversion scheme is defined but no resource",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
			},
			err_nil:  true,
			want_msg: "unknown",
		},
		{
			name: "clusterversion resource with no id",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
			},
			err_nil:  true,
			want_msg: "",
		},
		{
			name: "clusterversion resource with id",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
			},
			err_nil:  true,
			want_msg: "aaa",
		},
	}

	for index, tt := range tests {
		if index == 1 {
			//add clusterversion scheme
			err := ocinfrav1.AddToScheme(scheme1)
			if err != nil {
				t.Errorf("Error adding api to scheme: %s", err.Error())
			}
		}
		if index == len(tests)-2 {
			// add a cr with no id
			err := k8sClient1.Create(tt.args.ctx, crNoVersion, &client.CreateOptions{})
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		if index == len(tests)-1 {
			// add a cr with id
			err := k8sClient1.Delete(tt.args.ctx, crNoVersion) // so that this is not picked up here
			if err != nil {
				t.Errorf("Error deleting: %s", err.Error())
			}
			err = k8sClient1.Create(tt.args.ctx, crWithVersion, &client.CreateOptions{})
			if err != nil {
				t.Errorf("Error creating: %s", err.Error())
			}
		}
		t.Run(tt.name, func(t *testing.T) {
			if version, err := getHubIdentification(tt.args.ctx, tt.args.c); (err == nil) != tt.err_nil ||
				version != tt.want_msg {
				t.Errorf("getHubIdentification() returns no error = %v, want %v and version=%v want=%v", err == nil, tt.err_nil, version, tt.want_msg)
			}
		})
	}
	// clean up
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}
}

func Test_VeleroCRDsPresent_NotPresent(t *testing.T) {
	// Test env with no additional CRDs (velero crds will not be present)
	testEnv := &envtest.Environment{ErrorIfCRDPathMissing: true}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}

	// clean up after
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Fatalf("Error stopping testenv: %s", err.Error())
		}
	}()

	scheme1 := runtime.NewScheme()
	err = veleroapi.AddToScheme(scheme1) // for velero types
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	// test client to testEnv above with no velero CRDs
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}

	t.Run("velero CRDs not present", func(t *testing.T) {
		crdsPresent, err := VeleroCRDsPresent(context.Background(), k8sClient1)
		if err != nil {
			t.Errorf("VeleroCRDsPresent() returned an unexpected error %s", err.Error())
		}
		if crdsPresent {
			t.Errorf("VeleroCRDsPresent() should return false when CRDs not present")
		}
	})
}

func Test_VeleroCRDsPresent(t *testing.T) {
	// Test env with our dependent CRDs loaded
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "hack", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Error starting testEnv: %s", err.Error())
	}

	// clean up after
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Fatalf("Error stopping testenv: %s", err.Error())
		}
	}()

	scheme1 := runtime.NewScheme()
	err = veleroapi.AddToScheme(scheme1) // for velero types
	if err != nil {
		t.Fatalf("Error adding api to scheme: %s", err.Error())
	}

	// test client to testEnv above with no velero CRDs
	k8sClient1, err := client.New(cfg, client.Options{Scheme: scheme1})
	if err != nil {
		t.Fatalf("Error starting client: %s", err.Error())
	}

	// Rely on testEnv setup in suite_test.go (this will have all the CRDs)
	// (use k8sClient setup in BeforeSuite that talks to the testEnv)
	t.Run("velero CRDs present", func(t *testing.T) {
		crdsPresent, err := VeleroCRDsPresent(context.Background(), k8sClient1)
		if err != nil {
			t.Errorf("VeleroCRDsPresent() returned an unexpected error %s", err.Error())
		}
		if !crdsPresent {
			t.Errorf("VeleroCRDsPresent() should return true when CRDs are present")
		}
	})
}

func labelSelectorArrayEqual(a []*v1.LabelSelector, b []v1.LabelSelector) bool {
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

func labelSelectorEqual(a *v1.LabelSelector, b *v1.LabelSelector) bool {

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

	emptyLabelSelector := make([]v1.LabelSelector, 0)

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
		wantOrLabelSelector []v1.LabelSelector
		wantLabelSelector   v1.LabelSelector
	}{
		{
			name: "no user defined orLabelSelector or LabelSelector, get empty match expressions",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					object,
				labelSelector: metav1.LabelSelectorRequirement{},
			},
			wantOrLabelSelector: emptyLabelSelector,
			wantLabelSelector:   v1.LabelSelector{},
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
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req3,
							},
						},
					}).
					object,
				labelSelector: clusterActivationReq,
			},
			wantOrLabelSelector: []metav1.LabelSelector{
				metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req1,
						req2,
						clusterActivationReq,
					},
				},
				metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req3,
						clusterActivationReq,
					},
				},
			},
			wantLabelSelector: v1.LabelSelector{},
		},
		{
			name: "using user defined orLabelSelector, no cluster activation LabelSelector",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					orLabelSelector([]*metav1.LabelSelector{
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req3,
							},
						},
					}).
					object,
				labelSelector: metav1.LabelSelectorRequirement{},
			},
			wantOrLabelSelector: []metav1.LabelSelector{
				metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req1,
						req2,
					},
				},
				metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req3,
					},
				},
			},
			wantLabelSelector: v1.LabelSelector{},
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
			// the user can set both, still, so verifing that the cluster activation LabelSelector goes to the orLabelSelector in this case
			name: "using both user defined orLabelSelector and LabelSelector, add cluster activation LabelSelector to orLabelSelector",
			args: args{
				veleroRestore: createRestore("restore", "restore-ns").
					orLabelSelector([]*metav1.LabelSelector{
						&metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								req1,
								req2,
							},
						},
						&metav1.LabelSelector{
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
				metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						req1,
						req2,
						clusterActivationReq,
					},
				},
				metav1.LabelSelector{
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
				t.Errorf("OrLabelSelectors not as expected  = %v, want %v got=%v", tt.name, tt.wantOrLabelSelector, tt.args.veleroRestore.Spec.OrLabelSelectors)
			}

			if !labelSelectorEqual(tt.args.veleroRestore.Spec.LabelSelector, &tt.wantLabelSelector) {
				t.Errorf("LabelSelector not as expected  = %v, want %v got=%v", tt.name, tt.wantLabelSelector, tt.args.veleroRestore.Spec.LabelSelector)
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
	}

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
				ctx:   context.Background(),
				c:     k8sClient1,
				msg:   "BackupSchedule is paused.",
				phase: v1beta1.SchedulePhasePaused,
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
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(true).
				object,
			wantDeletedSchedules: []veleroapi.Schedule{
				creds,
				cls,
				res,
			},
			wantScheduleStatusNil:   false, // backup schedule status doesn't get updated bc of delete error
			wantReturn:              ctrl.Result{},
			wantBackupSchedulePhase: v1beta1.SchedulePhaseEnabled,
			wantMsg:                 "",
		},
		{
			name: "backup schedule in collision",
			args: args{
				ctx:   context.Background(),
				c:     k8sClient1,
				msg:   "collision",
				phase: v1beta1.SchedulePhaseBackupCollision,
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
				ctx:   context.Background(),
				c:     k8sClient1,
				msg:   "BackupSchedule is paused.",
				phase: v1beta1.SchedulePhasePaused,
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
				ctx:   context.Background(),
				c:     k8sClient1,
				msg:   "some value",
				phase: v1beta1.SchedulePhaseEnabled,
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
			},
			wantBackupSchedule: *createBackupSchedule("acm-schedule", "default").
				paused(false).
				phase(v1beta1.SchedulePhaseEnabled).
				object,
			wantDeletedSchedules:    []veleroapi.Schedule{},
			wantScheduleStatusNil:   true,
			wantReturn:              ctrl.Result{},
			wantBackupSchedulePhase: "",
			wantMsg:                 "",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if i == 1 {
				// create the scheme now, to have the delete resource fail
				_ = veleroapi.AddToScheme(scheme1) // for velero types
				_ = v1beta1.AddToScheme(scheme1)   // for acm backup types
			}

			if i > 0 {
				if err := k8sClient1.Create(tt.args.ctx, tt.args.backupSchedule, &client.CreateOptions{}); err != nil {
					t.Errorf("Failed to create backup schedule name =%v ns =%v , err=%v",
						tt.args.backupSchedule.Name,
						tt.args.backupSchedule.Namespace,
						err.Error())

				}

				// create resources
				for i := range tt.args.veleroScheduleList.Items {
					if err := k8sClient1.Create(tt.args.ctx, &tt.args.veleroScheduleList.Items[i], &client.CreateOptions{}); err != nil {
						t.Errorf("Failed to create schedule name =%v ns =%v , err=%v",
							tt.args.veleroScheduleList.Items[i].Name,
							tt.args.veleroScheduleList.Items[i].Namespace,
							err.Error())

					}
				}
			}

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

				if err := k8sClient1.Get(tt.args.ctx, lookupKey, &schedule); err == nil {
					t.Errorf(" schedule should not be found = %v", lookupKey.Name)
				}
			}

			// first test (i=0) will not create velero schedules, it intentionally doesn't add the api to the
			// client scheme - so no cleanup required
			if i > 0 {
				// clean up
				err = k8sClient1.Delete(tt.args.ctx, tt.args.backupSchedule)
				if client.IgnoreNotFound(err) != nil {
					t.Errorf("Error deleting: %s", err.Error())
				}

				for i := range tt.args.veleroScheduleList.Items {
					err := k8sClient1.Delete(tt.args.ctx, &tt.args.veleroScheduleList.Items[i])
					if client.IgnoreNotFound(err) != nil {
						t.Errorf("Error deleting: %s", err.Error())
					}
				}
			}
		})
	}
	// clean up
	if err := testEnv.Stop(); err != nil {
		t.Fatalf("Error stopping testenv: %s", err.Error())
	}
}
