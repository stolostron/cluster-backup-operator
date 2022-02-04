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

package controllers

import (
	"strings"

	"github.com/go-logr/logr"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func isVeleroRestoreFinished(restore *veleroapi.Restore) bool {
	switch {
	case restore == nil:
		return false
	case len(restore.Status.Phase) == 0 || // if no restore exists
		restore.Status.Phase == veleroapi.RestorePhaseNew ||
		restore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return false
	}
	return true
}

func isVeleroRestoreRunning(restore *veleroapi.Restore) bool {
	switch {
	case restore == nil:
		return false
	case
		restore.Status.Phase == veleroapi.RestorePhaseNew ||
			restore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return true
	}
	return false
}

func isSkipAllRestores(restore *v1beta1.Restore) bool {

	backupName := ""

	if restore.Spec.VeleroManagedClustersBackupName != nil {
		backupName = *restore.Spec.VeleroManagedClustersBackupName
		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName != skipRestoreStr {
			return false
		}
	}

	if restore.Spec.VeleroCredentialsBackupName != nil {
		backupName = *restore.Spec.VeleroCredentialsBackupName
		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName != skipRestoreStr {
			return false
		}
	}

	if restore.Spec.VeleroResourcesBackupName != nil {
		backupName = *restore.Spec.VeleroResourcesBackupName
		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName != skipRestoreStr {
			return false
		}
	}

	return true
}

func updateRestoreStatus(
	logger logr.Logger,
	status v1beta1.RestorePhase,
	msg string,
	restore *v1beta1.Restore,
) {
	logger.Info(msg)

	restore.Status.Phase = status
	restore.Status.LastMessage = msg
}
