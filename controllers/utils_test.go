// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"testing"

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_isValidStorageLocationDefined(t *testing.T) {
	type args struct {
		veleroStorageLocations *veleroapi.BackupStorageLocationList
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
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := isValidStorageLocationDefined(*tt.args.veleroStorageLocations); got != tt.want {
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
			gotname, gotkind := getResourceDetails(*&tt.args.resourceName)
			if gotname != tt.wantname || gotkind != tt.wantkind {
				t.Errorf("getResourceDetails() = %v,%v, want %v,%v", gotname, gotkind, tt.wantname, tt.wantkind)
			}
		})
	}
}
