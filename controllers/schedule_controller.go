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
	"strings"
	"time"

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

const updateStatusFailedMsg = "Could not update status"

var (
	// Time interval to reque delete backups if they exceed maxBackups number
	deleteBackupRequeueInterval = time.Minute * 60
	failureInterval             = time.Second * 10
)

var (
	scheduleOwnerKey = ".metadata.controller"
	apiGVString      = v1beta1.GroupVersion.String()
	// mapping ResourceTypes to Velero schedule names
	// create credentials schedule first since this is the fastest one, followed by resources
	resourceTypes = map[ResourceType]string{
		Credentials:     "acm-credentials-schedule",
		Resources:       "acm-resources-schedule",
		ManagedClusters: "acm-managed-clusters-schedule",
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
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=channels,verbs=get;list;watch
//+kubebuilder:rbac:groups=velero.io,resources=schedules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create;list;watch
//+kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *BackupScheduleReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	scheduleLogger := log.FromContext(ctx)
	backupSchedule := &v1beta1.BackupSchedule{}

	if err := r.Get(ctx, req.NamespacedName, backupSchedule); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// don't create schedules if backup storage location doesn't exist or is not avaialble
	veleroStorageLocations := &veleroapi.BackupStorageLocationList{}
	if err := r.Client.List(ctx, veleroStorageLocations, &client.ListOptions{}); err != nil ||
		veleroStorageLocations == nil || len(veleroStorageLocations.Items) == 0 {

		backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailedValidation
		backupSchedule.Status.LastMessage = "velero.io.BackupStorageLocation resources not found. " +
			"Verify you have created a konveyor.openshift.io.Velero resource."

		// retry after failureInterval
		return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, backupSchedule),
			updateStatusFailedMsg,
		)
	}

	var isValidStorageLocation bool = false
	for i := 0; i < len(veleroStorageLocations.Items); i++ {
		if veleroStorageLocations.Items[i].Status.Phase == veleroapi.BackupStorageLocationPhaseAvailable {
			// one valid storage location found, assume storage is accessible
			isValidStorageLocation = true
			break
		}
	}

	// if no valid storage location found wait for valid value
	if !isValidStorageLocation {
		backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailedValidation
		backupSchedule.Status.LastMessage = "Backup storage location is not available. " +
			"Check velero.io.BackupStorageLocation and validate storage credentials."

		// retry after failureInterval
		return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, backupSchedule),
			updateStatusFailedMsg,
		)
	}

	// validate the cron job schedule
	errs := parseCronSchedule(ctx, backupSchedule)
	if len(errs) > 0 {
		backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailedValidation
		backupSchedule.Status.LastMessage = strings.Join(errs, ",")

		return ctrl.Result{}, errors.Wrap(
			r.Client.Status().Update(ctx, backupSchedule),
			updateStatusFailedMsg,
		)
	}

	// retrieve the velero schedules (if any)
	veleroScheduleList := veleroapi.ScheduleList{}
	if err := r.List(
		ctx,
		&veleroScheduleList,
		client.InNamespace(req.Namespace),
		client.MatchingFields{scheduleOwnerKey: req.Name},
	); err != nil {
		scheduleLogger.Error(
			err,
			"unable to list velero schedules for schedule",
			"namespace", req.Namespace,
			"name", req.Name,
		)
		return ctrl.Result{}, err
	}

	// no velero schedules, so create them
	if len(veleroScheduleList.Items) == 0 {
		err := r.initVeleroSchedules(ctx, backupSchedule)
		if err != nil {
			msg := fmt.Errorf(FailedPhaseMsg+": %v", err)
			scheduleLogger.Error(err, err.Error())
			backupSchedule.Status.LastMessage = msg.Error()
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailed
		} else {
			backupSchedule.Status.LastMessage = NewPhaseMsg
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
		}

		return ctrl.Result{RequeueAfter: deleteBackupRequeueInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, backupSchedule),
			updateStatusFailedMsg,
		)
	}

	// Velero doesn't watch modifications to its schedules
	// delete velero schedules if their spec needs to be updated
	// New velero schedules will be created in the next reconcile triggerd by the deletion
	if isScheduleSpecUpdated(&veleroScheduleList, backupSchedule) {
		if err := r.deleteVeleroSchedules(ctx, backupSchedule, &veleroScheduleList); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: deleteBackupRequeueInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, backupSchedule),
			updateStatusFailedMsg,
		)
	}

	// velero schedules already exist, update schedule status with latest velero schedules
	for i := range veleroScheduleList.Items {
		updateScheduleStatus(ctx, &veleroScheduleList.Items[i], backupSchedule)
	}
	setSchedulePhase(&veleroScheduleList, backupSchedule)

	// clean up old backups if they exceed the maxBackups number after backupDeleteRequeueInterval
	cleanupBackups(ctx, backupSchedule.Spec.MaxBackups*len(resourceTypes), r.Client)

	err := r.Client.Status().Update(ctx, backupSchedule)
	return ctrl.Result{RequeueAfter: deleteBackupRequeueInterval}, errors.Wrap(
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
			setManagedClustersBackupInfo(ctx, veleroBackupTemplate, r.Client)
		case Credentials:
			setCredsBackupInfo(ctx, veleroBackupTemplate, r.Client)
		case Resources:
			setResourcesBackupInfo(ctx, veleroBackupTemplate, r.Client)
		}

		veleroSchedule.Spec.Template = *veleroBackupTemplate
		veleroSchedule.Spec.Schedule = backupSchedule.Spec.VeleroSchedule
		if backupSchedule.Spec.VeleroTTL.Duration != 0 {
			veleroSchedule.Spec.Template.TTL = backupSchedule.Spec.VeleroTTL
		}

		if err := ctrl.SetControllerReference(backupSchedule, veleroSchedule, r.Scheme); err != nil {
			return err
		}

		err := r.Create(ctx, veleroSchedule, &client.CreateOptions{})
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
		setVeleroScheduleInStatus(key, veleroSchedule, backupSchedule)
	}
	return nil
}

// delete all velero schedules owned by this BackupSchedule
func (r *BackupScheduleReconciler) deleteVeleroSchedules(
	ctx context.Context,
	backupSchedule *v1beta1.BackupSchedule,
	schedules *veleroapi.ScheduleList,
) error {
	scheduleLogger := log.FromContext(ctx)

	if schedules == nil || len(schedules.Items) <= 0 {
		return nil
	}

	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]
		err := r.Delete(ctx, veleroSchedule)
		if err != nil {
			scheduleLogger.Error(
				err,
				"Error in deleting Velero schedule",
				"name", veleroSchedule.Name,
				"namespace", veleroSchedule.Namespace,
			)
			return err
		}
		scheduleLogger.Info(
			"Deleted Velero schedule",
			"name", veleroSchedule.Name,
			"namespace", veleroSchedule.Namespace,
		)
	}

	backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
	backupSchedule.Status.LastMessage = NewPhaseMsg
	backupSchedule.Status.VeleroScheduleCredentials = nil
	backupSchedule.Status.VeleroScheduleManagedClusters = nil
	backupSchedule.Status.VeleroScheduleResources = nil

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&veleroapi.Schedule{},
		scheduleOwnerKey,
		func(rawObj client.Object) []string {
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
