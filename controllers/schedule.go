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

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/restmapper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// FailedPhaseMsg for when Velero schedule initialization failed
	FailedPhaseMsg string = "Velero schedules initialization failed"
	// NewPhaseMsg for when Velero schedule initialization succeeded
	NewPhaseMsg string = "Velero schedules are initialized"
	// EnabledPhaseMsg for when Velero schedules are processed by velero and enabled
	EnabledPhaseMsg string = "Velero schedules are enabled"
	// UnknownPhaseMsg for when some Velero schedules are not enabled
	UnknownPhaseMsg string = "Some Velero schedules are not enabled. " +
		"If the status doesn't change check the velero pod is running and " +
		"that you have created a Velero resource as documented in the install guide."
	// BackupCollisionPhaseMsg when another cluster is creating backups at the same storage location
	BackupCollisionPhaseMsg string = "Backup %s, from cluster with id [%s] is using the same storage location." +
		" This is a backup collision with current cluster [%s] backup." +
		" Review and resolve the collision then create a new BackupSchedule resource to " +
		" resume backups from this cluster."
)

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

	for key, value := range veleroBackupNames {
		if veleroSchedule.Name == value {
			// set veleroSchedule in backupSchedule status
			setVeleroScheduleInStatus(key, veleroSchedule, backupSchedule)
		}
	}
}

func setVeleroScheduleInStatus(
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

// set cumulative status of schedules
func setSchedulePhase(
	schedules *veleroapi.ScheduleList,
	backupSchedule *v1beta1.BackupSchedule,
) v1beta1.SchedulePhase {
	if backupSchedule.Status.Phase == v1beta1.SchedulePhaseBackupCollision {
		return backupSchedule.Status.Phase
	}

	if schedules == nil || len(schedules.Items) <= 0 {
		backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
		backupSchedule.Status.LastMessage = NewPhaseMsg
		return backupSchedule.Status.Phase
	}

	// get all schedules and check status for each
	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]
		if veleroSchedule.Status.Phase == "" {
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseUnknown
			backupSchedule.Status.LastMessage = UnknownPhaseMsg
			return backupSchedule.Status.Phase
		}
		if veleroSchedule.Status.Phase == veleroapi.SchedulePhaseNew {
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
			backupSchedule.Status.LastMessage = NewPhaseMsg
			return backupSchedule.Status.Phase
		}
		if veleroSchedule.Status.Phase == veleroapi.SchedulePhaseFailedValidation {
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailedValidation
			backupSchedule.Status.LastMessage = FailedPhaseMsg
			return backupSchedule.Status.Phase
		}
	}

	// if no velero schedule with FailedValidation, New or empty status, they are all enabled
	backupSchedule.Status.Phase = v1beta1.SchedulePhaseEnabled
	backupSchedule.Status.LastMessage = EnabledPhaseMsg
	return backupSchedule.Status.Phase
}

func isScheduleSpecUpdated(
	schedules *veleroapi.ScheduleList,
	backupSchedule *v1beta1.BackupSchedule,
) bool {

	if schedules == nil || len(schedules.Items) <= 0 {
		return false
	}

	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]

		if veleroSchedule.Name != veleroScheduleNames[ValidationSchedule] &&
			veleroSchedule.Spec.Template.TTL.Duration != backupSchedule.Spec.VeleroTTL.Duration {
			// validation backup TTL should be ignored here
			// since that one is using the schedule's cron job interval
			return true
		}
		if veleroSchedule.Spec.Schedule != backupSchedule.Spec.VeleroSchedule {
			return true
		}
	}

	return false
}

func parseCronSchedule(
	ctx context.Context,
	backupSchedule *v1beta1.BackupSchedule,
) []string {
	var validationErrors []string

	// cron.Parse panics if schedule is empty
	if len(backupSchedule.Spec.VeleroSchedule) == 0 {
		validationErrors = append(
			validationErrors,
			"Schedule must be a non-empty valid Cron expression",
		)
		return validationErrors
	}

	scheduleLogger := log.FromContext(ctx)

	// adding a recover() around cron.Parse because it panics on empty string and is possible
	// that it panics under other scenarios as well.
	func() {
		defer func() {
			if r := recover(); r != nil {
				validationErrors = append(validationErrors, fmt.Sprintf("invalid schedule recover: %v", r))
			}
		}()

		if _, err := cron.ParseStandard(backupSchedule.Spec.VeleroSchedule); err != nil {
			scheduleLogger.Error(
				err,
				"Error parsing schedule",
				"schedule", backupSchedule.Spec.VeleroSchedule,
			)
			validationErrors = append(validationErrors, fmt.Sprintf("invalid schedule: %v", err))
		}
	}()

	if len(validationErrors) > 0 {
		return validationErrors
	}

	return nil
}

// returns true if this schedule has generated the latest backups in the
// storage location
func scheduleOwnsLatestStorageBackups(
	ctx context.Context,
	c client.Client,
	backupSchedule *veleroapi.Schedule,
) (bool, *veleroapi.Backup) {

	logger := log.FromContext(ctx)

	backups := veleroapi.BackupList{}
	if err := c.List(ctx, &backups,
		client.MatchingLabels{BackupVeleroLabel: veleroScheduleNames[Resources]}); err != nil {
		logger.Info(err.Error())
		return true, nil
	}
	// get only acm resources backups and not in deleting state
	// which are backups starting with acm-resources-schedule
	sliceBackups := filterBackups(backups.Items[:], func(bkp veleroapi.Backup) bool {
		return bkp.Status.Phase != veleroapi.BackupPhaseDeleting
	})

	// sort backups
	sort.Slice(sliceBackups, func(i, j int) bool {
		var timeA int64
		var timeB int64
		if sliceBackups[i].Status.StartTimestamp != nil {
			timeA = sliceBackups[i].Status.StartTimestamp.Time.Unix()
		}
		if sliceBackups[j].Status.StartTimestamp != nil {
			timeB = sliceBackups[j].Status.StartTimestamp.Time.Unix()
		}
		return timeA < timeB
	})

	if len(sliceBackups) == 0 {
		return true, nil
	}
	lastBackup := sliceBackups[len(sliceBackups)-1]

	if lastBackup.Labels[BackupScheduleClusterLabel] != backupSchedule.GetLabels()[BackupScheduleClusterLabel] {
		return false, &lastBackup
	}

	return true, nil
}

// delete all velero schedules owned by this BackupSchedule
func deleteVeleroSchedules(
	ctx context.Context,
	c client.Client,
	backupSchedule *v1beta1.BackupSchedule,
	schedules *veleroapi.ScheduleList,
) error {
	scheduleLogger := log.FromContext(ctx)

	if schedules == nil || len(schedules.Items) <= 0 {
		return nil
	}

	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]
		err := c.Delete(ctx, veleroSchedule)
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

// check if there is a restore running on this cluster
// returns restoreName - the name of the running restore if one found
func isRestoreRunning(
	ctx context.Context,
	c client.Client,
	backupSchedule *v1beta1.BackupSchedule,
) string {

	scheduleLogger := log.FromContext(ctx)
	restoreName := ""

	restoreList := v1beta1.RestoreList{}
	if err := c.List(
		ctx,
		&restoreList,
		client.InNamespace(backupSchedule.Namespace),
	); err != nil {
		scheduleLogger.Error(err, "cannot list resource")
		return restoreName
	}

	if len(restoreList.Items) == 0 {
		return restoreName
	}

	for i := range restoreList.Items {
		restoreItem := restoreList.Items[i]
		if restoreItem.Status.Phase != v1beta1.RestorePhaseFinished &&
			restoreItem.Status.Phase != v1beta1.RestorePhaseFinishedWithErrors {
			restoreName = restoreItem.Name // found one running
			break
		}
	}
	return restoreName
}

func createInitialBackupForSchedule(
	ctx context.Context,
	c client.Client,
	schedule *veleroapi.Schedule,
	backupSchedue *v1beta1.BackupSchedule,
	timeStr string,
) string {

	scheduleLogger := log.FromContext(ctx)
	veleroBackup := &veleroapi.Backup{}

	if backupSchedue.Spec.NoBackupOnStart {
		// do not generate backups, exit now
		scheduleLogger.Info("skip backup creation, backupSchedue.Spec.NoBackupOnStart set to true")
		return ""
	}

	// create backup now
	veleroBackup.Name = schedule.Name + "-" + timeStr
	veleroBackup.Namespace = schedule.Namespace
	//set labels from schedule labels
	labels := make(map[string]string)
	if scheduleLabels := schedule.GetLabels(); scheduleLabels != nil {
		for k, v := range scheduleLabels {
			labels[k] = v
		}
	}
	labels[BackupVeleroLabel] = schedule.Name
	veleroBackup.SetLabels(labels)
	// set spec from schedule spec
	veleroBackup.Spec = schedule.Spec.Template

	// now create the backup
	if err := c.Create(ctx, veleroBackup, &client.CreateOptions{}); err != nil {
		scheduleLogger.Error(
			err,
			"Error in creating velero.io.Backup",
			"name", veleroBackup.Name,
			"namespace", veleroBackup.Namespace,
		)
		return ""
	}
	scheduleLogger.Info(
		"Velero backup created",
		"name", veleroBackup.Name,
		"namespace", veleroBackup.Namespace,
	)

	return veleroBackup.Name
}

func verifyMSAOption(
	ctx context.Context,
	c client.Client,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	backupSchedule *v1beta1.BackupSchedule,
) (ctrl.Result, bool, error) {
	msaKind := schema.GroupKind{
		Group: msa_group,
		Kind:  msa_kind,
	}

	scheduleLogger := log.FromContext(ctx)
	msg := "UseManagedServiceAccount option cannot be used, managedserviceaccount-preview component is not enabled"
	if useMSA := backupSchedule.Spec.UseManagedServiceAccount; useMSA {

		if _, err := mapper.RESTMapping(msaKind, ""); err != nil {
			scheduleLogger.Info("ManagedServiceAccount CRD not found")
			//cleanupMSAForImportedClusters
			cleanupMSAForImportedClusters(ctx, c, nil, nil)
			// return error
			return createFailedValidationResponse(ctx, c, backupSchedule,
				msg, true) // want to reque, if CRD is installed after
		}
	}

	return ctrl.Result{}, true, nil
}

func createFailedValidationResponse(
	ctx context.Context,
	c client.Client,
	backupSchedule *v1beta1.BackupSchedule,
	msg string,
	requeue bool,
) (ctrl.Result, bool, error) {
	scheduleLogger := log.FromContext(ctx)
	validConfiguration := false
	scheduleLogger.Info(msg)

	backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailedValidation
	backupSchedule.Status.LastMessage = msg

	if requeue {
		// retry after failureInterval
		return ctrl.Result{RequeueAfter: failureInterval},
			validConfiguration,
			errors.Wrap(
				c.Status().Update(ctx, backupSchedule),
				msg,
			)
	}

	// no retry
	return ctrl.Result{},
		validConfiguration,
		errors.Wrap(
			c.Status().Update(ctx, backupSchedule),
			msg,
		)

}
