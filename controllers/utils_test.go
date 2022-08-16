// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"testing"
	"time"

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
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
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import-no-annotations",
							Namespace: "managed1",
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import-no-expiration",
							Namespace: "managed1",
							Annotations: map[string]string{
								"lastRefreshTimestamp": "2022-07-26T15:25:34Z",
							},
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import-invalid-expiration",
							Namespace: "managed1",
							Annotations: map[string]string{
								"expirationTimestamp": "aaa",
							},
						},
					},
				}},
			want: "",
		},
		{
			name: "MSA has secrets but expirationTimestamp is before current time ",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"expirationTimestamp": fourHoursAgo,
							},
						},
					},
				}},
			want: "",
		},
		{
			name: "MSA has secrets with valid expirationTimestamp but no token",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"expirationTimestamp": nextHour,
							},
						},
						Data: map[string][]byte{
							"token1": []byte("aaa"),
						},
					},
				}},
			want: "",
		},
		{
			name: "MSA has secrets with valid expirationTimestamp and one valid token",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"expirationTimestamp": nextHour,
							},
						},
						Data: map[string][]byte{
							"token1": []byte("aaa"),
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"expirationTimestamp": nextHour,
							},
						},
						Data: map[string][]byte{
							"token": []byte("YWRtaW4="),
						},
					},
				}},
			want: "YWRtaW4=",
		},
		{
			name: "MSA has secrets with valid expirationTimestamp and with token",
			args: args{
				currentTime: current,
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"expirationTimestamp": nextHour,
							},
						},
						Data: map[string][]byte{
							"token": []byte("YWRtaW4="),
						},
					},
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
