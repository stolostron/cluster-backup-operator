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

	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

var (
	restoreOwnerKey = ".metadata.controller"
	apiGVStr        = v1beta1.GroupVersion.String()
)

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
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

	//restoreLogger.Info(fmt.Sprintf(">> Enter reconcile for Restore CR name=%s (namespace: %s)", req.NamespacedName.Name, req.NamespacedName.Namespace))

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// retrieve the velero restore (if any)
	veleroRestoreList := veleroapi.RestoreList{}
	if err := r.List(ctx, &veleroRestoreList, client.InNamespace(req.Namespace), client.MatchingFields{restoreOwnerKey: req.Name}); err != nil {
		restoreLogger.Error(err, "unable to list velero restores for restore %s/%s", req.Namespace, req.Name)
		return ctrl.Result{}, err
	}

	switch {
	case len(veleroRestoreList.Items) == 0:
		veleroRestore, err := r.initVeleroRestore(ctx, restore)
		if err != nil {
			restoreLogger.Error(err, "unable to create velero restore for restore %s/%s", req.Namespace, req.Name)
			return ctrl.Result{}, err
		}
		if err = r.Create(ctx, veleroRestore, &client.CreateOptions{}); err != nil {
			restoreLogger.Error(err, "unable to create velero restore for restore %s/%s", req.Namespace, req.Name)

			return ctrl.Result{}, err
		}
		r.Recorder.Event(restore, v1.EventTypeNormal, "Velero Restore created:", fmt.Sprintf("%s/%s", veleroRestore.Namespace, veleroRestore.Name))
		restore.Status.VeleroRestore = veleroRestore.DeepCopy()

	case len(veleroRestoreList.Items) == 1:
		if isRestoreFinsihed(restore) {
			r.managedClustersHandler(restore)
		}
	default:
		// TODO: handles multiple velero restores:
		// check if one velero is still running... update status and wait
		// if all finished handleManagedClusters
		//for i := range veleroRestoreList.Items {}
	}

	err := r.Client.Status().Update(ctx, restore)
	return ctrl.Result{}, errors.Wrap(err, fmt.Sprintf("could not update status for restore %s/%s", restore.Namespace, restore.Name))
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &veleroapi.Restore{}, restoreOwnerKey, func(rawObj client.Object) []string {
		// grab the job object, extract the owner...
		job := rawObj.(*veleroapi.Restore)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ..should be a Restore in Group cluster.open-cluster-management.io
		if owner.APIVersion != apiGVStr || owner.Kind != "Restore" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Restore{}).
		Owns(&veleroapi.Restore{}).
		Complete(r)
}

// name used by the velero restore resource, created by the restore acm controller
func (r *RestoreReconciler) getVeleroRestoreName(ctx context.Context, restore *v1beta1.Restore) (string, error) {
	backupName, err := r.getVeleroBackupName(ctx, restore)
	if err != nil {
		return "", err
	}
	return restore.Name + "-" + backupName, err
}

func (r *RestoreReconciler) getVeleroBackupName(ctx context.Context, restore *v1beta1.Restore) (string, error) {
	if restore.Spec.VeleroBackupName != nil {
		return *restore.Spec.VeleroBackupName, nil
	}
	//r.Client.List(ctx)
	return "thebackup", nil
}

func (r *RestoreReconciler) initVeleroRestore(ctx context.Context, restore *v1beta1.Restore) (*veleroapi.Restore, error) {
	veleroRestore := &veleroapi.Restore{}
	var err error
	veleroRestore.Name, err = r.getVeleroRestoreName(ctx, restore)
	if err != nil {
		return veleroRestore, err
	}
	veleroRestore.Namespace = restore.Spec.VeleroConfig.VeleroNamespace
	if err := ctrl.SetControllerReference(restore, veleroRestore, r.Scheme); err != nil {
		return nil, err
	}

	return veleroRestore, nil
}

func (r *RestoreReconciler) managedClustersHandler(restore *v1beta1.Restore) (bool, error) {
	shouldUpdate := true
	return shouldUpdate, nil
}
