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
	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

// isRestoreFinsihed returns true when Restore is finished
func isRestoreFinsihed(restore *v1beta1.Restore) bool {
	switch {
	case restore == nil:
		return false
	case restore.Status.VeleroRestore == nil:
		return false
	case restore.Status.VeleroRestore.Status.Phase == veleroapi.RestorePhaseNew ||
		restore.Status.VeleroRestore.Status.Phase == veleroapi.RestorePhaseInProgress:
		return false
	}
	return true
}
