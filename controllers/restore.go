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
	v1alpha1 "github.com/open-cluster-management-io/cluster-backup-operator/api/v1alpha1"
)

// IsRestoreFinsihed returns true when Restore is finished
func IsRestoreFinsihed(restore *v1alpha1.Restore) bool {
	if restore.Status.RestoreProxyReference == nil {
		return false
	}
	return true
}

// name used by the velero restore resource, created by the restore acm controller
func getVeleroRestoreName(restore *v1alpha1.Restore) string {
	return restore.Name + "-" + restore.Spec.BackupName
}
