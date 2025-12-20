/*
Copyright 2025.

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

package v1beta1

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var restorelog = logf.Log.WithName("restore-resource")

func (r *Restore) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(r).
		Complete()
}

//nolint:lll
//+kubebuilder:webhook:path=/validate-cluster-open-cluster-management-io-v1beta1-restore,mutating=false,failurePolicy=fail,sideEffects=None,groups=cluster.open-cluster-management.io,resources=restores,verbs=create;update,versions=v1beta1,name=vrestore.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &Restore{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (r *Restore) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	restore, ok := obj.(*Restore)
	if !ok {
		return nil, fmt.Errorf("expected a Restore object but got %T", obj)
	}
	restorelog.Info("validate create", "name", restore.Name)

	return restore.validateRestore()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (r *Restore) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	restore, ok := newObj.(*Restore)
	if !ok {
		return nil, fmt.Errorf("expected a Restore object but got %T", newObj)
	}
	restorelog.Info("validate update", "name", restore.Name)

	return restore.validateRestore()
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (r *Restore) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	restore, ok := obj.(*Restore)
	if !ok {
		return nil, fmt.Errorf("expected a Restore object but got %T", obj)
	}
	restorelog.Info("validate delete", "name", restore.Name)

	// No validation needed on delete
	return nil, nil
}

// validateRestore validates the Restore resource
func (r *Restore) validateRestore() (admission.Warnings, error) {
	var warnings admission.Warnings

	// Validate sync mode configuration
	if r.Spec.SyncRestoreWithNewBackups {
		if err := r.validateSyncMode(); err != nil {
			return warnings, err
		}
	}

	return warnings, nil
}

// validateSyncMode validates sync mode specific requirements
func (r *Restore) validateSyncMode() error {
	// Check all backup names are set
	if r.Spec.VeleroManagedClustersBackupName == nil ||
		r.Spec.VeleroCredentialsBackupName == nil ||
		r.Spec.VeleroResourcesBackupName == nil {
		return fmt.Errorf("syncRestoreWithNewBackups requires all backup names to be set")
	}

	mcBackup := strings.ToLower(strings.TrimSpace(*r.Spec.VeleroManagedClustersBackupName))
	credsBackup := strings.ToLower(strings.TrimSpace(*r.Spec.VeleroCredentialsBackupName))
	resourcesBackup := strings.ToLower(strings.TrimSpace(*r.Spec.VeleroResourcesBackupName))

	// MC must be skip or latest
	if mcBackup != "skip" && mcBackup != "latest" {
		return fmt.Errorf(
			"syncRestoreWithNewBackups requires veleroManagedClustersBackupName "+
				"to be 'skip' or 'latest', got: %s", mcBackup)
	}

	// Credentials must be latest
	if credsBackup != "latest" {
		return fmt.Errorf(
			"syncRestoreWithNewBackups requires veleroCredentialsBackupName "+
				"to be 'latest', got: %s", credsBackup)
	}

	// Resources must be latest
	if resourcesBackup != "latest" {
		return fmt.Errorf(
			"syncRestoreWithNewBackups requires veleroResourcesBackupName "+
				"to be 'latest', got: %s", resourcesBackup)
	}

	// Validate MC=skip on first creation (Phase != Enabled)
	// When Phase=Enabled, the restore was already created with MC=skip
	// and is now being edited to MC=latest (activation)
	if r.Status.Phase != RestorePhaseEnabled &&
		mcBackup != "skip" &&
		r.Status.VeleroManagedClustersRestoreName == "" {
		return fmt.Errorf(
			"when syncRestoreWithNewBackups is true, veleroManagedClustersBackupName " +
				"must initially be set to 'skip'. To activate managed clusters, " +
				"edit the restore to set veleroManagedClustersBackupName to 'latest'")
	}

	return nil
}
