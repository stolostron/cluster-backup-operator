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
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
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
	// Credentials resource type for user created credentials
	Credentials ResourceType = "credentials"
	// CredentialsHive resource type for hive secrets
	CredentialsHive ResourceType = "credentialsHive"
	// CredentialsCluster Credentials resource type for managed cluster secrets
	CredentialsCluster ResourceType = "credentialsCluster"
	// Resources related to applications and policies
	Resources ResourceType = "resources"
	// schedule used by the backup Policy to validate that there are active backups running
	// and stored to the storage location, using the schedule cron time
	ValidationSchedule = "validation"
	// ResourcesGeneric Genric Resources related to applications and policies
	// these are user resources, except secrets, labeled with cluster.open-cluster-management.io/backup
	// secrets labeled with cluster.open-cluster-management.io/backup are already backed up under credentialsCluster
	ResourcesGeneric ResourceType = "resourcesGeneric"

	msa_kind  = "ManagedServiceAccount"
	msa_group = "authentication.open-cluster-management.io"
)

// SecretType is the type of secret
type SecretType string

const (
	// HiveSecret hive created secrets
	HiveSecret SecretType = "hive"
	// ClusterSecret managed cluster secrets
	ClusterSecret SecretType = "cluster"
	// UserSecret user defined secrets
	UserSecret SecretType = "user"
)
const updateStatusFailedMsg = "Could not update status"

const (
	failureInterval          = time.Second * 60
	collisionControlInterval = time.Minute * 5
	scheduleOwnerKey         = ".metadata.controller"
)

// BackupScheduleReconciler reconciles a BackupSchedule object
type BackupScheduleReconciler struct {
	client.Client
	DiscoveryClient discovery.DiscoveryInterface
	DynamicClient   dynamic.Interface
	Scheme          *runtime.Scheme
}

//nolint:lll
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backupschedules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backupschedules/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=backupschedules/finalizers,verbs=update
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterpools,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=channels,verbs=get;list;watch
//+kubebuilder:rbac:groups=velero.io,resources=schedules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=get;list;watch
//+kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
//
//nolint:funlen
func (r *BackupScheduleReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	scheduleLogger := log.FromContext(ctx)

	// velero doesn't delete expired backups if they are in FailedValidation
	// workaround and delete expired or invalid validation backups them now
	cleanupExpiredValidationBackups(ctx, req.Namespace, r.Client)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(r.DiscoveryClient),
	)

	backupSchedule := &v1beta1.BackupSchedule{}
	if result, validConfiguration, err := r.isValidateConfiguration(ctx, mapper,
		req,
		backupSchedule); !validConfiguration {
		// return if the backup configuration on this hub is not properly set
		return result, err
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
		return ctrl.Result{}, err
	}

	if backupSchedule.Spec.Paused {
		// backup schedule is paused
		msg := "BackupSchedule is paused."
		return updateBackupSchedulePhaseWhenPaused(ctx, r.Client, veleroScheduleList,
			backupSchedule, v1beta1.SchedulePhasePaused, msg)
	}

	collisionMsg := ""
	// enforce backup collision only if this schedule was NOT created now ( current time - creation > 5)
	// in this case ignore any collisions since the user had initiated this backup
	if len(veleroScheduleList.Items) > 0 &&
		metav1.Now().Sub(veleroScheduleList.Items[0].CreationTimestamp.Time).Seconds() > 5 &&
		backupSchedule.Status.Phase != "" &&
		backupSchedule.Status.Phase != v1beta1.SchedulePhaseNew {
		isThisTheOwner, lastBackup, err := scheduleOwnsLatestStorageBackups(ctx, r.Client, &veleroScheduleList.Items[0])
		if err != nil {
			return ctrl.Result{}, err
		}
		if !isThisTheOwner {
			// set exception status, because another cluster is creating backups
			// and storing them at the same location
			// we risk a backup collision, as more then one cluster seems to be
			// backing up data in the same location
			collisionMsg = fmt.Sprintf(BackupCollisionPhaseMsg,
				lastBackup.GetName(),
				lastBackup.GetLabels()[BackupScheduleClusterLabel],
				veleroScheduleList.Items[0].GetLabels()[BackupScheduleClusterLabel],
			)
			scheduleLogger.Info(collisionMsg)
		} else {
			// check if an existing hub restore was created
			// after this backup schedule and report a collision
			_, collisionMsg = isRestoreHubAfterSchedule(ctx, r.Client, &veleroScheduleList.Items[0])
		}
		// add any missing labels and create any resources required by the backup and restore process
		err = r.prepareForBackup(ctx, mapper, backupSchedule)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if collisionMsg != "" {
		// collision found
		return updateBackupSchedulePhaseWhenPaused(ctx, r.Client, veleroScheduleList,
			backupSchedule, v1beta1.SchedulePhaseBackupCollision, collisionMsg)
	}
	// no velero schedules, so create them
	if len(veleroScheduleList.Items) == 0 {
		clusterID, _ := getHubIdentification(ctx, r.Client)
		err := r.initVeleroSchedules(ctx, mapper, backupSchedule, clusterID)
		if err != nil {
			msg := fmt.Errorf(FailedPhaseMsg+": %v", err) //nolint:staticcheck  // for capitalized err msg
			scheduleLogger.Error(err, err.Error())
			backupSchedule.Status.LastMessage = msg.Error()
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailed
		} else {
			backupSchedule.Status.LastMessage = NewPhaseMsg
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
		}
		statusUpdateErr := r.Client.Status().Update(ctx, backupSchedule)
		if err == nil { // Don't mask previous error
			err = statusUpdateErr
		}
		return ctrl.Result{RequeueAfter: collisionControlInterval}, err
	}

	// if any velero schedule is deleted manually, recreate them all to have the same backup due time
	if len(veleroScheduleList.Items) < len(veleroScheduleNames) {
		if err := deleteVeleroSchedules(ctx, r.Client, backupSchedule, &veleroScheduleList); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: collisionControlInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, backupSchedule),
			updateStatusFailedMsg,
		)
	}

	// check for any updates that are required for velero schedules based on backupSchedule and hub resources
	if result, updated, err := isVeleroSchedulesUpdateRequired(ctx, r.Client,
		getResourcesToBackup(ctx, r.DiscoveryClient), veleroScheduleList, backupSchedule); updated {
		return result, err
	}

	// velero schedules already exist, update schedule status with latest velero schedules
	for i := range veleroScheduleList.Items {
		updateScheduleStatus(ctx, &veleroScheduleList.Items[i], backupSchedule)
	}
	setSchedulePhase(&veleroScheduleList, backupSchedule)

	err := r.Client.Status().Update(ctx, backupSchedule)
	return ctrl.Result{RequeueAfter: collisionControlInterval}, errors.Wrap(
		err,
		fmt.Sprintf(
			"could not update status for schedule %s/%s",
			backupSchedule.Namespace,
			backupSchedule.Name,
		),
	)
}

// validate backup configuration
func (r *BackupScheduleReconciler) isValidateConfiguration(
	ctx context.Context,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	req ctrl.Request,
	backupSchedule *v1beta1.BackupSchedule,
) (ctrl.Result, bool, error) {
	validConfiguration := false
	scheduleLogger := log.FromContext(ctx)

	if err := r.Get(ctx, req.NamespacedName, backupSchedule); err != nil {
		return ctrl.Result{}, validConfiguration, client.IgnoreNotFound(err)
	}

	if backupSchedule.Status.Phase == v1beta1.SchedulePhaseBackupCollision {
		scheduleLogger.Info("ignore resource in SchedulePhaseBackupCollision state")
		return ctrl.Result{}, validConfiguration, nil
	}

	// don't create schedule if an active restore exists
	// allow to create paused schedules even if a restore is running
	if restoreName := isRestoreRunning(ctx, r.Client, backupSchedule); restoreName != "" &&
		!backupSchedule.Spec.Paused {
		msg := "Restore resource " + restoreName + " is currently active, " +
			"verify that any active restores are removed."
		return createFailedValidationResponse(ctx, r.Client, backupSchedule,
			msg, true)
	}

	// don't create schedules if backup storage location doesn't exist or is not avaialable
	veleroStorageLocations := &veleroapi.BackupStorageLocationList{}
	if err := r.List(ctx, veleroStorageLocations, &client.ListOptions{}); err != nil ||
		len(veleroStorageLocations.Items) == 0 {

		msg := "velero.io.BackupStorageLocation resources not found. " +
			"Verify you have created a konveyor.openshift.io.Velero or oadp.openshift.io.DataProtectionApplications resource."

		return createFailedValidationResponse(ctx, r.Client, backupSchedule,
			msg, true)
	}

	// look for available VeleroStorageLocation
	// and keep track of the velero oadp namespace
	isValidStorageLocation := isValidStorageLocationDefined(
		veleroStorageLocations.Items,
		req.Namespace,
	)

	// if no valid storage location found wait for valid value
	if !isValidStorageLocation {
		msg := "Backup storage location is not available. " +
			"Check velero.io.BackupStorageLocation and validate storage credentials."
		return createFailedValidationResponse(ctx, r.Client, backupSchedule,
			msg, true)
	}

	// check MSA status for backup schedules
	return verifyMSAOption(ctx, r.Client, mapper, backupSchedule)
}

// create velero.io.Schedule resource for each resource type that needs backup
//
//nolint:funlen
func (r *BackupScheduleReconciler) initVeleroSchedules(
	ctx context.Context,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	backupSchedule *v1beta1.BackupSchedule,
	clusterID string,
) error {
	scheduleLogger := log.FromContext(ctx)

	resourcesToBackup := getResourcesToBackup(ctx, r.DiscoveryClient)

	// sort schedule names to create first the credentials schedules, then clusters, last resources
	scheduleKeys := make([]ResourceType, 0, len(veleroScheduleNames))
	for key := range veleroScheduleNames {
		scheduleKeys = append(scheduleKeys, key)
	}
	sort.Sort(SortResourceType(scheduleKeys))
	// swap the last two items to put the resources last, after the resourcesGeneric
	swapF := reflect.Swapper(scheduleKeys)
	if len(scheduleKeys) > 3 {
		// swap resources and resourcesGeneric, so resources is the last backup to be created
		swapF(2, 3)
	}

	// add any missing labels and create any resources required by the backup and restore process
	err := r.prepareForBackup(ctx, mapper, backupSchedule)
	if err != nil {
		return err
	}

	// use this when generating the backups so all have the same timestamp
	currentTime := time.Now().Format("20060102150405")

	// get local cluster name
	localClusterName, err := getLocalClusterName(ctx, r.Client)
	if err != nil || localClusterName == "" {
		// if not found, or error, set to the default local-cluster
		localClusterName = localClusterLabel
	}

	// loop through schedule names to create a Velero schedule per type
	for _, scheduleKey := range scheduleKeys {
		veleroScheduleIdentity := types.NamespacedName{
			Namespace: backupSchedule.Namespace,
			Name:      veleroScheduleNames[scheduleKey],
		}

		veleroSchedule := &veleroapi.Schedule{}
		veleroSchedule.Name = veleroScheduleIdentity.Name
		veleroSchedule.Namespace = veleroScheduleIdentity.Namespace

		// set backup schedule name as label annotation
		labels := veleroSchedule.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[BackupScheduleNameLabel] = backupSchedule.Name
		labels[BackupScheduleTypeLabel] = string(scheduleKey)
		// set cluster uid
		labels[BackupScheduleClusterLabel] = clusterID

		veleroSchedule.SetLabels(labels)

		// create backup based on resource type
		veleroBackupTemplate := &veleroapi.BackupSpec{}
		if scheduleKey == ManagedClusters || scheduleKey == Resources {
			veleroBackupTemplate.ExcludedNamespaces = appendUnique(
				veleroBackupTemplate.ExcludedNamespaces,
				localClusterName,
			)
		}

		switch scheduleKey {
		case ManagedClusters:
			setManagedClustersBackupInfo(veleroBackupTemplate, resourcesToBackup)
		case Credentials:
			setCredsBackupInfo(veleroBackupTemplate)
		case Resources:
			setResourcesBackupInfo(ctx, veleroBackupTemplate, resourcesToBackup,
				backupSchedule.Namespace, r.Client)
		case ResourcesGeneric:
			setGenericResourcesBackupInfo(veleroBackupTemplate, resourcesToBackup)
		case ValidationSchedule:
			veleroBackupTemplate = setValidationBackupInfo(
				veleroBackupTemplate,
				backupSchedule,
			)
		}

		if len(backupSchedule.Spec.VolumeSnapshotLocations) > 0 {
			veleroBackupTemplate.VolumeSnapshotLocations = backupSchedule.Spec.VolumeSnapshotLocations
		}
		if backupSchedule.Spec.UseOwnerReferencesInBackup {
			veleroSchedule.Spec.UseOwnerReferencesInBackup = &backupSchedule.Spec.UseOwnerReferencesInBackup
		}
		if backupSchedule.Spec.SkipImmediately {
			veleroSchedule.Spec.SkipImmediately = &backupSchedule.Spec.SkipImmediately
		}
		veleroSchedule.Spec.Template = *veleroBackupTemplate
		veleroSchedule.Spec.Schedule = backupSchedule.Spec.VeleroSchedule
		veleroSchedule.Spec.Paused = backupSchedule.Spec.Paused // Set pause state to match BackupSchedule
		if backupSchedule.Spec.VeleroTTL.Duration != 0 && scheduleKey != ValidationSchedule {
			// TTL for a validation backup is already set using the cron job interval
			veleroSchedule.Spec.Template.TTL = backupSchedule.Spec.VeleroTTL
		}
		// this is always successful since veleroSchedule is defined now
		if err := ctrl.SetControllerReference(backupSchedule, veleroSchedule, r.Scheme); err == nil {
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
			setVeleroScheduleInStatus(scheduleKey, veleroSchedule, backupSchedule)
			// if initial backup needs to be created, process it here
			createInitialBackupForSchedule(ctx, r.Client, r.Scheme,
				veleroSchedule, backupSchedule, currentTime)
		}
	}
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
			if owner == nil || owner.APIVersion != apiGVString || owner.Kind != "BackupSchedule" {
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
