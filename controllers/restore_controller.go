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

	"github.com/pkg/errors"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1beta1 "github.com/open-cluster-management-io/cluster-backup-operator/api/v1beta1"
)

var (
	restoreRequeueInterval = time.Minute * 1
)

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Restore object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	restoreLogger := log.FromContext(ctx)
	restore := &v1beta1.Restore{}

	restoreLogger.Info(fmt.Sprintf(">> Enter reconcile for Restore CR name=%s (namespace: %s)", req.NamespacedName.Name, req.NamespacedName.Namespace))

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {

		// check if this is a NotFound error
		if !k8serr.IsNotFound(err) {
			restoreLogger.Error(err, "unable to fetch Restore CR")
		}

		restoreLogger.Info("Restore CR was not created in the %s namespace", req.NamespacedName.Namespace)
		return ctrl.Result{RequeueAfter: restoreRequeueInterval}, client.IgnoreNotFound(err)
	}

	var (
		err           error
		veleroRestore *veleroapi.Restore
	)
	veleroRestore, err = r.submitAcmRestoreSettings(ctx, restore, r.Client)
	if veleroRestore != nil {
		restore.Status.VeleroRestore = veleroRestore
	}
	if err != nil {
		msg := fmt.Errorf("unable to create Velero restore for %s: %v", restore.Name, err)
		restoreLogger.Error(err, err.Error())
		restore.Status.LastMessage = msg.Error()
		restore.Status.Phase = "Failed"
	}
	restoreLogger.Info(fmt.Sprintf("<< EXIT reconcile for Restore resource name=%s (namespace: %s)", req.NamespacedName.Name, req.NamespacedName.Namespace))

	if IsRestoreFinsihed(restore) {
		return ctrl.Result{}, errors.Wrap(r.Client.Status().Update(ctx, restore), "could not update status")
	}

	return ctrl.Result{RequeueAfter: restoreRequeueInterval}, errors.Wrap(r.Client.Status().Update(ctx, restore), "could not update status")
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Restore{}).
		Complete(r)
}

func (r *RestoreReconciler) submitAcmRestoreSettings(ctx context.Context, restore *v1beta1.Restore, c client.Client) (*veleroapi.Restore, error) {

	restoreLogger := log.FromContext(ctx)
	restoreLogger.Info(">> ENTER submitAcmRestoreSettings for new restore")

	veleroRestore := &veleroapi.Restore{}
	veleroRestore.Name = getVeleroRestoreName(restore)
	veleroRestore.Namespace = restore.Spec.VeleroConfig.Namespace

	veleroIdentity := types.NamespacedName{
		Namespace: veleroRestore.Namespace,
		Name:      veleroRestore.Name,
	}

	// get the velero CRD using the veleroIdentity
	err := r.Get(ctx, veleroIdentity, veleroRestore)

	if err != nil {
		restoreLogger.Info("velero.io.Restore resource [name=%s, namespace=%s] returned error, checking if the resource was not yet created", veleroIdentity.Name, veleroIdentity.Namespace)

		// check if this is a resource NotFound error, in which case create the resource
		if k8serr.IsNotFound(err) {

			msg := fmt.Sprintf("velero.io.Restore [name=%s, namespace=%s] resource NOT FOUND, creating it now", veleroIdentity.Name, veleroIdentity.Namespace)
			restoreLogger.Info(msg)

			// set ACM restore configuration
			veleroRestore.Spec.BackupName = restore.Spec.BackupName

			restore.Status.LastMessage = msg
			err = c.Create(ctx, veleroRestore, &client.CreateOptions{})
		} else {
			msg := fmt.Sprintf("velero.io.Restore [name=%s, namespace=%s] returned ERROR, error=%s ", veleroIdentity.Name, veleroIdentity.Namespace, err.Error())
			restoreLogger.Error(err, msg)
			restore.Status.LastMessage = msg
		}
	} else {
		msg := fmt.Sprintf("Restore [%s] is currently in phase:%s", veleroIdentity.Name, veleroRestore.Status.Phase)
		restoreLogger.Info(msg)

		restore.Status.LastMessage = msg
		restore.Status.Phase = v1beta1.StatusPhase(veleroRestore.Status.Phase)
	}

	return veleroRestore, err
}
