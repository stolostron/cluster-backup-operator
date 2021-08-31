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

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	"github.com/pkg/errors"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ResourceType is the type to contain resource type string value
type ResourceType string

const (
	// ManagedClusters resource type
	ManagedClusters ResourceType = "managedClusters"
	// Credentials resource type
	Credentials ResourceType = "credentials"
	// Resources related to applications and policies
	Resources ResourceType = "resources"
)

var (
	scheduleOwnerKey = ".metadata.controller"
	apiGVString      = v1beta1.GroupVersion.String()
	// mapping ResourceTypes to Velero schedule names
	resourceTypes = map[ResourceType]string{
		ManagedClusters: "acm-managed-clusters-schedule",
		Credentials:     "acm-credentials-schedule",
		Resources:       "acm-resources-schedule",
	}
)

// BackupScheduleReconciler reconciles a BackupSchedule object
type BackupScheduleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backupschedules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backupschedules/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backupschedules/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BackupSchedule object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *BackupScheduleReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	scheduleLogger := log.FromContext(ctx)
	backupSchedule := &v1beta1.BackupSchedule{}

	if err := r.Get(ctx, req.NamespacedName, backupSchedule); err != nil {
		// we can get not-found errors on deleted requests
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// retrieve the velero schedules (if any)
	veleroScheduleList := veleroapi.ScheduleList{}
	if err := r.List(ctx, &veleroScheduleList, client.InNamespace(req.Namespace), client.MatchingFields{scheduleOwnerKey: req.Name}); err != nil {
		scheduleLogger.Error(
			err,
			"unable to list velero schedules for schedule",
			"namespace", req.Namespace,
			"name", req.Name,
		)
		return ctrl.Result{}, err
	}

	if len(veleroScheduleList.Items) == 0 {
		if err := r.initVeleroSchedules(ctx, backupSchedule, r.Client); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		for _, veleroSchedule := range veleroScheduleList.Items {
			updateScheduleStatus(ctx, &veleroSchedule, backupSchedule)
		}
	}

	err := r.Client.Status().Update(ctx, backupSchedule)
	return ctrl.Result{}, errors.Wrap(
		err,
		fmt.Sprintf(
			"could not update status for schedule %s/%s",
			backupSchedule.Namespace,
			backupSchedule.Name,
		),
	)
}

// create velero.io.Schedule resource for each resource type that needs backup
func (r *BackupScheduleReconciler) initVeleroSchedules(
	ctx context.Context,
	backupSchedule *v1beta1.BackupSchedule,
	c client.Client,
) error {
	scheduleLogger := log.FromContext(ctx)

	// loop through resourceTypes to create a schedule per type
	for key, value := range resourceTypes {
		veleroScheduleIdentity := types.NamespacedName{
			Namespace: backupSchedule.Namespace,
			Name:      value,
		}

		veleroSchedule := &veleroapi.Schedule{}
		veleroSchedule.Name = veleroScheduleIdentity.Name
		veleroSchedule.Namespace = veleroScheduleIdentity.Namespace

		// create backup based on resource type
		veleroBackupTemplate := &veleroapi.BackupSpec{}

		switch key {
		case ManagedClusters:
			setManagedClustersBackupInfo(ctx, veleroBackupTemplate, c)
		case Credentials:
			setCredsBackupInfo(ctx, veleroBackupTemplate, c)
		case Resources:
			setResourcesBackupInfo(ctx, veleroBackupTemplate, c)
		}

		veleroSchedule.Spec.Template = *veleroBackupTemplate
		veleroSchedule.Spec.Schedule = backupSchedule.Spec.VeleroSchedule
		veleroSchedule.Spec.Template.TTL = backupSchedule.Spec.VeleroTTL

		if err := ctrl.SetControllerReference(backupSchedule, veleroSchedule, r.Scheme); err != nil {
			return err
		}

		err := c.Create(ctx, veleroSchedule, &client.CreateOptions{})
		if err != nil {
			scheduleLogger.Error(
				err,
				"Error in creating velero.io.Schedule",
				"name", veleroScheduleIdentity.Name,
				"namespace", veleroScheduleIdentity.Namespace,
			)
			return err
		}
		scheduleLogger.Info(
			"Velero schedule created",
			"name", veleroSchedule.Name,
			"namespace", veleroSchedule.Namespace,
		)

		// set veleroSchedule in backupSchedule status
		setScheduleStatus(key, veleroSchedule, backupSchedule)
	}
	return nil
}

func updateScheduleStatus(
	ctx context.Context,
	veleroSchedule *veleroapi.Schedule,
	backupSchedule *v1beta1.BackupSchedule,
) {
	scheduleLogger := log.FromContext(ctx)

	scheduleLogger.Info(
		"Updating status with a copy of velero schedule",
		"name", veleroSchedule.Name,
		"namespace", veleroSchedule.Namespace,
	)

	for key, value := range resourceTypes {
		if veleroSchedule.Name == value {
			// set veleroSchedule in backupSchedule status
			setScheduleStatus(key, veleroSchedule, backupSchedule)
		}
	}
}

func setScheduleStatus(
	resourceType ResourceType,
	veleroSchedule *veleroapi.Schedule,
	backupSchedule *v1beta1.BackupSchedule,
) {
	switch resourceType {
	case ManagedClusters:
		backupSchedule.Status.VeleroScheduleManagedClusters = veleroSchedule.DeepCopy()
	case Credentials:
		backupSchedule.Status.VeleroScheduleCredentials = veleroSchedule.DeepCopy()
	case Resources:
		backupSchedule.Status.VeleroScheduleResources = veleroSchedule.DeepCopy()
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &veleroapi.Schedule{}, scheduleOwnerKey, func(rawObj client.Object) []string {
		schedule := rawObj.(*veleroapi.Schedule)
		owner := metav1.GetControllerOf(schedule)
		if owner == nil {
			return nil
		}
		if owner.APIVersion != apiGVString || owner.Kind != "BackupSchedule" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.BackupSchedule{}).
		Owns(&veleroapi.Schedule{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Ignore updates to CR status in which case metadata.Generation does not change
				return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
			},
		}).
		Complete(r)
}
