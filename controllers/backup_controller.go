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
	"context"
	"fmt"
	"time"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	"github.com/pkg/errors"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	backupOwnerKey  = ".metadata.controller"
	apiGV           = v1beta1.GroupVersion.String()
	requeueInterval = time.Second * 10
)

// BackupReconciler reconciles a Backup object
type BackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backups/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=channels,verbs=get;list;watch
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Backup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	backupLogger := log.FromContext(ctx)
	backup := &v1beta1.Backup{}

	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if !k8serr.IsNotFound(err) {
			backupLogger.Error(err, "unable to fetch Backup CR")
		}

		backupLogger.Info(
			fmt.Sprintf("Backup CR was not created in the %s namespace",
				req.NamespacedName.Namespace,
			))
		return ctrl.Result{RequeueAfter: requeueInterval}, client.IgnoreNotFound(err)
	}

	var (
		v_err        error
		veleroBackup *veleroapi.Backup
	)
	veleroBackup, v_err = r.submitAcmBackupSettings(ctx, backup, r.Client)
	if veleroBackup != nil {
		backup.Status.VeleroBackups[0] = veleroBackup
	}
	if v_err != nil {
		msg2 := fmt.Errorf("unable to create Velero backup for %s: %v", backup.Name, v_err)
		backupLogger.Error(v_err, v_err.Error())
		backup.Status.LastMessage = msg2.Error()
		backup.Status.Phase = v1beta1.ErrorStatusPhase
		backup.Status.CurrentBackup = ""
	}

	// if backup is complete wake up after the specified backup.Spec.Interval, don't reque every > requeueInterval
	if veleroBackup != nil && isBackupPhaseFinished(backup.Status.Phase) {
		nextDuration := time.Minute * time.Duration(backup.Spec.Interval)
		return ctrl.Result{
				RequeueAfter: nextDuration,
			}, errors.Wrap(
				r.Client.Status().Update(ctx, backup),
				"could not update status",
			)

	} else {
		return ctrl.Result{RequeueAfter: requeueInterval}, errors.Wrap(r.Client.Status().Update(ctx, backup), "could not update status")
	}

}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &veleroapi.Backup{}, backupOwnerKey, func(rawObj client.Object) []string {
		backup := rawObj.(*veleroapi.Backup)
		owner := metav1.GetControllerOf(backup)
		if owner == nil {
			return nil
		}
		if owner.APIVersion != apiGV || owner.Kind != "Backup" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Backup{}).
		Complete(r)
}

func (r *BackupReconciler) submitAcmBackupSettings(
	ctx context.Context,
	backup *v1beta1.Backup,
	c client.Client,
) (*veleroapi.Backup, error) {

	backupLogger := log.FromContext(ctx)

	veleroIdentity := types.NamespacedName{
		Namespace: backup.Spec.VeleroConfig.Namespace,
		Name:      r.getActiveBackupName(ctx, backup, c),
	}

	veleroBackup := &veleroapi.Backup{}
	veleroBackup.Name = veleroIdentity.Name
	veleroBackup.Namespace = veleroIdentity.Namespace

	// get the velero CR using the veleroIdentity
	err := r.Get(ctx, veleroIdentity, veleroBackup)
	if err != nil {
		backupLogger.Info(fmt.Sprintf(
			"velero.io.Backup resource [name=%s, namespace=%s] returned error, checking if the resource was not yet created",
			veleroIdentity.Name,
			veleroIdentity.Namespace,
		))

		// check if this is a  resource NotFound error, in which case create the resource
		if k8serr.IsNotFound(err) {

			if !canStartBackup(backup) {
				return nil, nil
			}

			msg := fmt.Sprintf(
				"velero.io.Backup [name=%s, namespace=%s] resource NOT FOUND, creating it now",
				veleroIdentity.Name,
				veleroIdentity.Namespace,
			)
			backupLogger.Info(msg)

			// clean up old backups if they exceed the maxCount number
			r.cleanupBackups(ctx, backup, c)

			// set ACM backup configuration for managed clusters
			setManagedClustersBackupInfo(ctx, &veleroBackup.Spec, c)
			err = c.Create(ctx, veleroBackup, &client.CreateOptions{})

			if veleroBackup != nil {
				updateLastBackupStatus(backup)

				//clean up
				backup.Status.VeleroBackups = nil

				// add backup to list of backups
				backup.Status.VeleroBackups = append(backup.Status.VeleroBackups, veleroBackup)
			}
			backup.Status.LastMessage = msg
			backup.Status.CurrentBackup = veleroIdentity.Name

			if err != nil {
				backupLogger.Error(err, "create backup error")
			}

		} else {
			msg := fmt.Sprintf("velero.io.Backup [name=%s, namespace=%s] returned ERROR, error=%s ", veleroIdentity.Name, veleroIdentity.Namespace, err.Error())
			backupLogger.Error(err, msg)
			backup.Status.LastMessage = msg
		}
	} else {
		if backup.Status.VeleroBackups == nil {
			backup.Status.VeleroBackups = append(backup.Status.VeleroBackups, veleroBackup)
		} else {
			backup.Status.VeleroBackups[0] = veleroBackup
		}

		veleroStatus := getBackupPhase(backup.Status.VeleroBackups)
		msg := fmt.Sprintf("Velero Backup [%s] ", veleroIdentity.Name)

		if veleroBackup.Status.Progress != nil {
			msg = fmt.Sprintf("%s [clusters: ItemsBackedUp[%d], TotalItems[%d]]", msg, veleroBackup.Status.Progress.ItemsBackedUp, veleroBackup.Status.Progress.TotalItems)
		}
		msgStatusNil := "If the status is empty check the velero pod is running and that you have created a Velero resource as documented in the install guide."
		msgStatusFailed := "Check if the velero.io.BackupStorageLocation resource points to a valid storage."

		if veleroStatus == "" {
			msg = fmt.Sprintf("%sEmpty. %s", msg, msgStatusNil)
		}
		if veleroStatus == "Failed" {
			msg = fmt.Sprintf("%s. %s", msg, msgStatusFailed)
		}
		backup.Status.LastMessage = msg
	}

	// create credentials backup
	r.createBackupForResource("creds", backup, veleroBackup, ctx, c)
	// create resources backup
	r.createBackupForResource("resource", backup, veleroBackup, ctx, c)

	updateLastBackupStatus(backup)

	return veleroBackup, err
}
