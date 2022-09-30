/*
Copyright 2022.

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

package controllers

import (
	"context"
	"path/filepath"
	"testing"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func Test_executePostRestoreTasks(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	clusterv1.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx     context.Context
		c       client.Client
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "post activation  should NOT run now, managed clusters are skipped",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want: false,
		},
		{
			name: "post activation  should run now",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := executePostRestoreTasks(tt.args.ctx, tt.args.c,
				tt.args.restore); got != tt.want {
				t.Errorf("executePostRestoreTasks() returns = %v, want %v", got, tt.want)
			}
		})

	}
	testEnv.Stop()

}

func Test_cleanupDeltaResources(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, _ := testEnv.Start()
	scheme1 := runtime.NewScheme()
	veleroapi.AddToScheme(scheme1)
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme1})

	type args struct {
		ctx              context.Context
		c                client.Client
		restore          *v1beta1.Restore
		cleanupOnRestore bool
		restoreOptions   RestoreOptions
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "post activation  should NOT run now, state is enabled but cleanup is false",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseEnabled).object,
				cleanupOnRestore: false,
				restoreOptions:   RestoreOptions{},
			},
			want: false,
		},
		{
			name: "post activation  should run now, state is enabled AND cleanup is true",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(skipRestoreStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseEnabled).object,
				cleanupOnRestore: true,
				restoreOptions:   RestoreOptions{},
			},
			want: true,
		},
		{
			name: "post activation should run now, state is Finished - and cleanup is false, not counting",
			args: args{
				ctx: context.Background(),
				c:   k8sClient1,
				restore: createACMRestore("Restore", "veleroNamespace").
					veleroManagedClustersBackupName(latestBackupStr).
					veleroCredentialsBackupName(latestBackupStr).
					veleroResourcesBackupName(latestBackupStr).
					phase(v1beta1.RestorePhaseFinished).object,
				cleanupOnRestore: false,
				restoreOptions:   RestoreOptions{},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanupDeltaResources(tt.args.ctx, tt.args.c,
				tt.args.restore, tt.args.cleanupOnRestore, tt.args.restoreOptions); got != tt.want {
				t.Errorf("cleanupDeltaResources() returns = %v, want %v", got, tt.want)
			}
		})

	}
	testEnv.Stop()

}
