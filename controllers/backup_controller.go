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
	"sort"
	"time"

	v1alpha1 "github.com/open-cluster-management-io/cluster-backup-operator/api/v1alpha1"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
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
	apiGV           = "v1alpha1" //v1alpha1.GroupVersion.String()
	requeueInterval = time.Minute * 1
)

// BackupReconciler reconciles a Backup object
type BackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=backup.cluster.management.io,resources=backups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=backup.cluster.management.io,resources=backups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=backup.cluster.management.io,resources=backups/finalizers,verbs=update

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
	backup := &v1alpha1.Backup{}

	backupLogger.Info(fmt.Sprintf(">> Enter reconcile for Backup CRD name=%s (namespace: %s) with interval=%d", req.NamespacedName.Name, req.NamespacedName.Namespace, backup.Spec.Interval))

	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {

		// check if this is a NotFound error
		if !k8serr.IsNotFound(err) {
			backupLogger.Error(err, "unable to fetch Backup CR")
		}

		backupLogger.Info("Backup CR was not created in the %s namespace", req.NamespacedName.Namespace)
		return ctrl.Result{RequeueAfter: requeueInterval}, client.IgnoreNotFound(err)
	}

	var (
		v_err        error
		veleroBackup *veleroapi.Backup
	)
	veleroBackup, v_err = r.submitAcmBackupSettings(ctx, backup, r.Client)
	if veleroBackup != nil {
		backup.Status.VeleroBackup = veleroBackup
	}
	if v_err != nil {
		msg2 := fmt.Errorf("unable to create Velero backup for %s: %v", backup.Name, v_err)
		backupLogger.Error(v_err, v_err.Error())
		backup.Status.LastMessage = msg2.Error()
		backup.Status.Phase = "ERROR"
		backup.Status.CurrentBackup = ""
	}
	backupLogger.Info(fmt.Sprintf("<< EXIT reconcile for Backup resource name=%s (namespace: %s)", req.NamespacedName.Name, req.NamespacedName.Namespace))

	return ctrl.Result{RequeueAfter: requeueInterval}, errors.Wrap(r.Client.Status().Update(ctx, backup), "could not update status")

}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &veleroapi.Backup{}, backupOwnerKey, func(rawObj client.Object) []string {
		backup := rawObj.(*veleroapi.Backup)
		owner := metav1.GetControllerOf(backup)
		if owner == nil {
			return nil
		}
		// It must be an open-cluster-management.io Backup
		if owner.APIVersion != apiGV || owner.Kind != "Backup" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Backup{}).
		Complete(r)
}

//TODO check whether this list change for version (backup) and how we can detect whether ACM or OCM
var backupNamespacesACM = [...]string{"open-cluster-management-agent", "open-cluster-management-hub", "hive", "openshift-operator-lifecycle-manager"}
var backupNamespacesOCM = [...]string{"open-cluster-management", "open-cluster-management-hub", "hive", "openshift-operator-lifecycle-manager"}

func (r *BackupReconciler) submitAcmBackupSettings(ctx context.Context, backup *v1alpha1.Backup, c client.Client) (*veleroapi.Backup, error) {

	backupLogger := log.FromContext(ctx)
	backupLogger.Info(">> ENTER submitAcmBackupSettings for new backup")

	veleroBackup := &veleroapi.Backup{}
	veleroBackup.Name = getActiveBackupName(backup, c)
	veleroBackup.Namespace = backup.Spec.VeleroConfig.Namespace

	veleroIdentity := types.NamespacedName{
		Namespace: veleroBackup.Namespace,
		Name:      veleroBackup.Name,
	}

	// get the velero CR using the veleroIdentity
	err := r.Get(ctx, veleroIdentity, veleroBackup)
	if err != nil {
		backupLogger.Info("velero.io.Backup resource [name=%s, namespace=%s] returned error, checking if the resource was not yet created", veleroIdentity.Name, veleroIdentity.Namespace)

		// check if this is a  resource NotFound error, in which case create the resource
		if k8serr.IsNotFound(err) {

			if !canStartBackup(backup) {
				backupLogger.Info("wait for time interval ..")
				return nil, nil
			}

			msg := fmt.Sprintf("velero.io.Backup [name=%s, namespace=%s] resource NOT FOUND, creating it now", veleroIdentity.Name, veleroIdentity.Namespace)
			backupLogger.Info(msg)

			if err = ctrl.SetControllerReference(backup, veleroBackup, r.Scheme); err != nil {
				backup.Status.LastMessage = err.Error()
				return nil, err
			}
			// set ACM backup configuration
			setBackupInfo(ctx, veleroBackup, c)

			backup.Status.LastMessage = msg
			backup.Status.CurrentBackup = veleroIdentity.Name
			err = c.Create(ctx, veleroBackup, &client.CreateOptions{})

			// clean up old backups if they exceed the maxCount number
			r.cleanupBackups(ctx, backup, c)

		} else {
			msg := fmt.Sprintf("velero.io.Backup [name=%s, namespace=%s] returned ERROR, error=%s ", veleroIdentity.Name, veleroIdentity.Namespace, err.Error())
			backupLogger.Error(err, msg)
			backup.Status.LastMessage = msg
		}
	} else {
		msg := fmt.Sprintf("Current Backup [%s] phase:%s", veleroIdentity.Name, veleroBackup.Status.Phase)
		backupLogger.Info(msg)

		backup.Status.LastMessage = msg
		backup.Status.Phase = v1alpha1.StatusPhase(veleroBackup.Status.Phase)

		if veleroBackup.Status.CompletionTimestamp != nil {
			// store current backup names as the last backup
			backup.Status.LastBackup = backup.Status.CurrentBackup

			completedTime := veleroBackup.Status.CompletionTimestamp
			startTime := veleroBackup.Status.StartTimestamp
			backup.Status.CompletionTimestamp = *completedTime

			duration := completedTime.Time.Sub(startTime.Time)
			backup.Status.LastBackupDuration = getFormattedDuration(duration)
		}
	}

	return veleroBackup, err
}

// clean up old backups if they exceed the maxCount number
func (r *BackupReconciler) cleanupBackups(ctx context.Context, backup *v1alpha1.Backup, c client.Client) {
	maxBackups := backup.Spec.MaxBackups
	backupLogger := log.FromContext(ctx)

	backupLogger.Info(fmt.Sprintf("check if needed to remove backups maxBackups=%d", maxBackups))
	veleroBackupList := veleroapi.BackupList{}
	if err := c.List(ctx, &veleroBackupList, &client.ListOptions{}); err != nil {

		// this is a NotFound error
		if !k8serr.IsNotFound(err) {
			backupLogger.Info("no backups found")
		} else {
			backupLogger.Error(err, "failed to get veleroapi.BackupList")
		}
	} else {

		sliceBackups := veleroBackupList.Items[:]
		if maxBackups < len(sliceBackups) {
			// need to delete backups

			// sort backups by create time
			sort.Slice(sliceBackups, func(i, j int) bool {
				return sliceBackups[i].Status.StartTimestamp.Time.Unix() < sliceBackups[j].Status.StartTimestamp.Time.Unix()
			})
			for i := 0; i < len(sliceBackups)-maxBackups; i++ {
				// delete backup now
				backupName := sliceBackups[i].ObjectMeta.Name
				backupLogger.Info(fmt.Sprintf("delete backup %s", backupName))

				backupDeleteIdentity := types.NamespacedName{
					Name:      backupName,
					Namespace: backup.Namespace,
				}

				// get the velero CR using the backupDeleteIdentity
				veleroDeleteBackup := &veleroapi.DeleteBackupRequest{}
				err = r.Get(ctx, backupDeleteIdentity, veleroDeleteBackup)
				if err != nil {
					// check if this is a  resource NotFound error, in which case create the resource
					if k8serr.IsNotFound(err) {

						veleroDeleteBackup.Spec.BackupName = backupName
						veleroDeleteBackup.Name = backupDeleteIdentity.Name
						veleroDeleteBackup.Namespace = backupDeleteIdentity.Namespace

						if err = ctrl.SetControllerReference(backup, veleroDeleteBackup, r.Scheme); err != nil {
							backupLogger.Error(err, fmt.Sprintf("SetControllerReference for DeleteBackupRequest %s", backupName))
						}

						err = c.Create(ctx, veleroDeleteBackup, &client.CreateOptions{})
						if err != nil {
							backupLogger.Error(err, fmt.Sprintf("create  DeleteBackupRequest request error for %s", backupName))
						}
					} else {
						backupLogger.Error(err, fmt.Sprintf("Failed to create DeleteBackupRequest for resource %s", backupName))
					}
				} else {
					backupLogger.Info(fmt.Sprintf("DeleteBackupRequest already exists, skip request creation %s", backupName))
				}
			}
		}

	}
}

// set all acm backup info
func setBackupInfo(ctx context.Context, veleroBackup *veleroapi.Backup, c client.Client) {

	backupLogger := log.FromContext(ctx)
	var clusterResource bool = false
	veleroBackup.Spec.IncludeClusterResources = &clusterResource
	veleroBackup.Spec.ExcludedResources = appendUnique(veleroBackup.Spec.ExcludedResources, "certificatesigningrequests")

	for i := range backupNamespacesOCM {
		// check if the NS exists
		veleroBackup.Spec.IncludedNamespaces = appendUnique(veleroBackup.Spec.IncludedNamespaces, backupNamespacesACM[i])
	}

	managedClusterList := clusterv1.ManagedClusterList{}
	if err := c.List(ctx, &managedClusterList, &client.ListOptions{}); err != nil {
		// if NotFound error
		if !k8serr.IsNotFound(err) {
			backupLogger.Info("managed clusters resources NOT FOUND")
		} else {
			backupLogger.Error(err, "failed to get clusterv1.ManagedClusterList")
		}
	} else {
		for i := range managedClusterList.Items {
			if managedClusterList.Items[i].Name == "local-cluster" {
				continue
			}
			veleroBackup.Spec.IncludedNamespaces = appendUnique(veleroBackup.Spec.IncludedNamespaces, managedClusterList.Items[i].Name)
		}
	}

}
