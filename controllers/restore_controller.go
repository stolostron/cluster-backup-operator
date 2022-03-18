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
	"strings"
	"time"

	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

var (
	apiGVStr = v1beta1.GroupVersion.String()
	// PublicAPIServerURL the public URL for the APIServer
	PublicAPIServerURL = ""
)

const (
	restoreOwnerKey            = ".metadata.controller"
	skipRestoreStr      string = "skip"
	latestBackupStr     string = "latest"
	restoreSyncInterval        = time.Minute * 30
)

type DynamicStruct struct {
	dc     discovery.DiscoveryInterface
	dyn    dynamic.Interface
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

type RestoreOptions struct {
	cleanupType   v1beta1.CleanupType
	deleteOptions metav1.DeleteOptions
	dynamicArgs   DynamicStruct
}

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	KubeClient      kubernetes.Interface
	DiscoveryClient discovery.DiscoveryInterface
	DynamicClient   dynamic.Interface
	RESTMapper      *restmapper.DeferredDiscoveryRESTMapper
	Scheme          *runtime.Scheme
	Recorder        record.EventRecorder
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/finalizers,verbs=update
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list
//+kubebuilder:rbac:groups=velero.io,resources=restores,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	restoreLogger := log.FromContext(ctx)
	restore := &v1beta1.Restore{}

	// velero doesn't delete expired backups if they are in FailedValidation
	// workaround and delete expired or invalid validation backups them now
	cleanupExpiredValidationBackups(ctx, req.Namespace, r.Client)

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if restore.Status.Phase == v1beta1.RestorePhaseFinished ||
		restore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors {
		// don't process a restore resource if it's completed
		return ctrl.Result{}, nil
	}

	// don't create restores if there is any other active resource in this namespace
	activeResourceMsg, err := r.isOtherResourcesRunning(ctx, restore)
	if err != nil {
		return ctrl.Result{}, err
	}
	if activeResourceMsg != "" {
		updateRestoreStatus(
			restoreLogger,
			v1beta1.RestorePhaseFinishedWithErrors,
			activeResourceMsg,
			restore,
		)
		return ctrl.Result{}, errors.Wrap(
			r.Client.Status().Update(ctx, restore),
			activeResourceMsg,
		)
	}

	// don't create restores if backup storage location doesn't exist or is not avaialble
	veleroStorageLocations := &veleroapi.BackupStorageLocationList{}
	if err := r.Client.List(ctx, veleroStorageLocations, &client.ListOptions{}); err != nil ||
		veleroStorageLocations == nil || len(veleroStorageLocations.Items) == 0 {

		msg := "velero.io.BackupStorageLocation resources not found. " +
			"Verify you have created a konveyor.openshift.io.Velero or oadp.openshift.io.DataProtectionApplications resource."
		updateRestoreStatus(restoreLogger, v1beta1.RestorePhaseError, msg, restore)
		// retry after failureInterval
		return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, restore),
			msg,
		)
	}

	// look for available VeleroStorageLocation
	// and keep track of the velero oadp namespace
	isValidStorageLocation, veleroNamespace := isValidStorageLocationDefined(
		*veleroStorageLocations,
	)

	// if no valid storage location found wait for valid value
	if !isValidStorageLocation {
		msg := "Backup storage location not available in namespace " + req.Namespace +
			". Check velero.io.BackupStorageLocation and validate storage credentials."
		updateRestoreStatus(restoreLogger, v1beta1.RestorePhaseError, msg, restore)

		// retry after failureInterval
		return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
			r.Client.Status().Update(ctx, restore),
			msg,
		)
	}

	// return error if the cluster restore file is not in the same namespace with velero
	if veleroNamespace != req.Namespace {
		msg := fmt.Sprintf(
			"Restore resource [%s/%s] must be created in the velero namespace [%s]",
			req.Namespace,
			req.Name,
			veleroNamespace,
		)
		updateRestoreStatus(restoreLogger, v1beta1.RestorePhaseError, msg, restore)

		return ctrl.Result{}, errors.Wrap(
			r.Client.Status().Update(ctx, restore),
			msg,
		)
	}

	// retrieve the velero restore (if any)
	veleroRestoreList := veleroapi.RestoreList{}
	if err := r.List(
		ctx,
		&veleroRestoreList,
		client.InNamespace(req.Namespace),
		client.MatchingFields{restoreOwnerKey: req.Name},
	); err != nil {

		msg := "unable to list velero restores for restore" +
			"namespace:" + req.Namespace +
			"name:" + req.Name

		restoreLogger.Error(
			err,
			msg,
		)
		return ctrl.Result{}, err
	}

	sync := isValidSyncOptions(restore) && restore.Status.Phase == v1beta1.RestorePhaseEnabled

	if len(veleroRestoreList.Items) == 0 || sync {
		if err := r.initVeleroRestores(ctx, restore, sync); err != nil {
			msg := fmt.Sprintf(
				"unable to initialize Velero restores for restore %s/%s: %v",
				req.Namespace,
				req.Name,
				err,
			)
			restoreLogger.Error(
				err,
				msg,
			)
			// set error status for all errors from initVeleroRestores
			restore.Status.Phase = v1beta1.RestorePhaseError
			restore.Status.LastMessage = err.Error()
			return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
				r.Client.Status().Update(ctx, restore),
				msg,
			)
		}
	} else {
		setRestorePhase(&veleroRestoreList, restore)
	}

	err = r.Client.Status().Update(ctx, restore)
	return sendResult(restore, err)
}

func sendResult(restore *v1beta1.Restore, err error) (ctrl.Result, error) {

	if restore.Spec.SyncRestoreWithNewBackups &&
		restore.Status.Phase == v1beta1.RestorePhaseEnabled {

		tryAgain := restoreSyncInterval
		if restore.Spec.RestoreSyncInterval.Duration != 0 {
			tryAgain = restore.Spec.RestoreSyncInterval.Duration
		}
		return ctrl.Result{RequeueAfter: tryAgain}, errors.Wrap(
			err,
			fmt.Sprintf(
				"could not update status for restore %s/%s",
				restore.Namespace,
				restore.Name,
			),
		)
	}

	return ctrl.Result{}, errors.Wrap(
		err,
		fmt.Sprintf("could not update status for restore %s/%s", restore.Namespace, restore.Name),
	)
}

// set cumulative status of restores
func setRestorePhase(
	veleroRestoreList *veleroapi.RestoreList,
	restore *v1beta1.Restore,
) {

	if restore.Status.Phase == v1beta1.RestorePhaseEnabled {
		return
	}

	if veleroRestoreList == nil || len(veleroRestoreList.Items) == 0 {
		if isSkipAllRestores(restore) {
			restore.Status.Phase = v1beta1.RestorePhaseFinished
			restore.Status.LastMessage = fmt.Sprintf("Nothing to do for restore %s", restore.Name)
			return
		}
		restore.Status.Phase = v1beta1.RestorePhaseStarted
		restore.Status.LastMessage = fmt.Sprintf("Restore %s started", restore.Name)
		return
	}

	// get all velero restores and check status for each
	partiallyFailed := false
	for i := range veleroRestoreList.Items {
		veleroRestore := veleroRestoreList.Items[i].DeepCopy()

		if veleroRestore.Status.Phase == "" {
			restore.Status.Phase = v1beta1.RestorePhaseUnknown
			restore.Status.LastMessage = fmt.Sprintf(
				"Unknown status for Velero restore %s",
				veleroRestore.Name,
			)
			return
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhaseNew {
			restore.Status.Phase = v1beta1.RestorePhaseStarted
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s has started",
				veleroRestore.Name,
			)
			return
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhaseInProgress {
			restore.Status.Phase = v1beta1.RestorePhaseRunning
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s is currently executing",
				veleroRestore.Name,
			)
			return
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhaseFailed ||
			veleroRestore.Status.Phase == veleroapi.RestorePhaseFailedValidation {
			restore.Status.Phase = v1beta1.RestorePhaseError
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s has failed validation or encountered errors",
				veleroRestore.Name,
			)
			return
		}
		if veleroRestore.Status.Phase == veleroapi.RestorePhasePartiallyFailed {
			partiallyFailed = true
			continue
		}
	}

	// sync is enabled only when the backup name for managed clusters is set to skip
	if isValidSyncOptions(restore) &&
		*restore.Spec.VeleroManagedClustersBackupName == skipRestoreStr {
		restore.Status.Phase = v1beta1.RestorePhaseEnabled
		restore.Status.LastMessage = "Velero restores have run to completion, " +
			"restore will continue to sync with new backups"
	} else if partiallyFailed {
		restore.Status.Phase = v1beta1.RestorePhaseFinishedWithErrors
		restore.Status.LastMessage = "Velero restores have run to completion but encountered 1+ errors"
	} else {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = "All Velero restores have run successfully"
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&veleroapi.Restore{},
		restoreOwnerKey,
		func(rawObj client.Object) []string {
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
			return []string{owner.Name}
		}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Restore{}).
		Owns(&veleroapi.Restore{}).
		//WithOptions(controller.Options{MaxConcurrentReconciles: 3}). TODO: enable parallelism as soon attaching works
		Complete(r)
}

// mostRecent defines type and code to sort velero backups
// according to start timestamp
type mostRecent []veleroapi.Backup

func (backups mostRecent) Len() int { return len(backups) }

func (backups mostRecent) Swap(i, j int) {
	backups[i], backups[j] = backups[j], backups[i]
}
func (backups mostRecent) Less(i, j int) bool {
	return backups[j].Status.StartTimestamp.Before(backups[i].Status.StartTimestamp)
}

// getVeleroBackupName returns the name of velero backup will be restored
func (r *RestoreReconciler) getVeleroBackupName(
	ctx context.Context,
	restore *v1beta1.Restore,
	resourceType ResourceType,
	backupName string,
) (string, *veleroapi.Backup, error) {

	veleroBackups := &veleroapi.BackupList{}
	if err := r.Client.List(ctx, veleroBackups, client.InNamespace(restore.Namespace)); err != nil {
		return "", nil, fmt.Errorf("unable to list velero backups: %v", err)
	}
	if len(veleroBackups.Items) == 0 {
		return "", nil, fmt.Errorf("no velero backups found")
	}

	if backupName == latestBackupStr {
		// backup name not available, find a proper backup
		// filter available backups to get only the ones related to this resource type
		relatedBackups := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
			return strings.Contains(bkp.Name, veleroScheduleNames[resourceType]) &&
				bkp.Status.Phase == veleroapi.BackupPhaseCompleted
		})
		if len(relatedBackups) == 0 {
			return "", nil, fmt.Errorf("no backups found")
		}
		sort.Sort(mostRecent(relatedBackups))
		return relatedBackups[0].Name, &relatedBackups[0], nil
	}

	// get the backup name for this type of resource, based on the requested resource timestamp
	switch resourceType {
	case CredentialsHive, CredentialsCluster, ResourcesGeneric:
		// first try to find a backup for this resourceType with the exact timestamp
		var computedName string
		backupTimestamp := strings.LastIndex(backupName, "-")
		if backupTimestamp != -1 {
			computedName = veleroScheduleNames[resourceType] + backupName[backupTimestamp:]
		}
		exactTimeBackup := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
			return computedName == bkp.Name
		})
		if len(exactTimeBackup) != 0 {
			return exactTimeBackup[0].Name, &exactTimeBackup[0], nil
		}
		// next try to find a backup with StartTimestamp in 30s range of the target timestamp
		targetTimestamp, err := getBackupTimestamp(backupName)
		if err != nil || targetTimestamp.IsZero() {
			return "", nil, fmt.Errorf(
				"cannot find %s Velero Backup for resourceType %s",
				backupName,
				string(resourceType),
			)
		}
		timeRangeBackups := filterBackups(veleroBackups.Items[:], func(bkp veleroapi.Backup) bool {
			if !strings.Contains(bkp.Name, veleroScheduleNames[resourceType]) ||
				bkp.Status.StartTimestamp == nil {
				return false
			}
			if targetTimestamp.Sub(bkp.Status.StartTimestamp.Time).Seconds() > 30 ||
				bkp.Status.StartTimestamp.Time.Sub(targetTimestamp).Seconds() > 30 {
				return false // not related, more then 30s appart
			}
			return true
		})
		if len(timeRangeBackups) != 0 {
			return timeRangeBackups[0].Name, &timeRangeBackups[0], nil
		}
		// if no backups within the range, return error
		return "", nil, fmt.Errorf(
			"cannot find %s Velero Backup for resourceType %s",
			backupName,
			string(resourceType),
		)
	}

	// for Credentials, Resources, ManagedClusters use the exact backupName set by the user
	veleroBackup := veleroapi.Backup{}
	err := r.Get(
		ctx,
		types.NamespacedName{Name: backupName, Namespace: restore.Namespace},
		&veleroBackup,
	)
	if err == nil {
		return backupName, &veleroBackup, nil
	}
	return "", nil, fmt.Errorf("cannot find %s Velero Backup: %v", backupName, err)
}

// create velero.io.Restore resource for each resource type
func (r *RestoreReconciler) initVeleroRestores(
	ctx context.Context,
	restore *v1beta1.Restore,
	sync bool,
) error {
	restoreLogger := log.FromContext(ctx)

	if sync {
		if r.isNewBackupAvailable(ctx, restore, Resources) {
			restoreLogger.Info(
				"new backups available to sync with for this restore",
				"name", restore.Name,
				"namespace", restore.Namespace,
			)
		} else {
			return nil
		}
	}

	// loop through resourceTypes to create a Velero restore per type
	veleroRestoresToCreate, backupsForVeleroRestores, err := r.retrieveRestoreDetails(
		ctx,
		restore,
	)
	if err != nil {
		return err
	}
	if len(veleroRestoresToCreate) == 0 {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = fmt.Sprintf("Nothing to do for restore %s", restore.Name)
		return nil
	}

	// clean up resources only if requested
	if err := r.prepareForRestore(ctx, *restore, veleroRestoresToCreate,
		backupsForVeleroRestores); err != nil {
		return err
	}

	// now create the restore resources and start the actual restore
	for key := range veleroRestoresToCreate {

		restoreObj := veleroRestoresToCreate[key]
		if key == ResourcesGeneric &&
			veleroRestoresToCreate[ManagedClusters] == nil {
			// if restoring the resources but not the managed clusters,
			// do not restore generic resources in the activation stage
			if restoreObj.Spec.LabelSelector == nil {
				labels := &metav1.LabelSelector{}
				restoreObj.Spec.LabelSelector = labels

				requirements := make([]metav1.LabelSelectorRequirement, 0)
				restoreObj.Spec.LabelSelector.MatchExpressions = requirements
			}
			req := &metav1.LabelSelectorRequirement{}
			req.Key = backupCredsClusterLabel
			req.Operator = "NotIn"
			req.Values = []string{"cluster-activation"}
			restoreObj.Spec.LabelSelector.MatchExpressions = append(
				restoreObj.Spec.LabelSelector.MatchExpressions,
				*req,
			)
		}
		if key == ResourcesGeneric &&
			veleroRestoresToCreate[Resources] == nil {
			// if restoring the ManagedClusters and resources are not restored
			// need to restore the generic resources for the activation phase
			if restoreObj.Spec.LabelSelector == nil {
				labels := &metav1.LabelSelector{}
				restoreObj.Spec.LabelSelector = labels

				requirements := make([]metav1.LabelSelectorRequirement, 0)
				restoreObj.Spec.LabelSelector.MatchExpressions = requirements
			}
			req := &metav1.LabelSelectorRequirement{}
			req.Key = backupCredsClusterLabel
			req.Operator = "In"
			req.Values = []string{"cluster-activation"}
			restoreObj.Spec.LabelSelector.MatchExpressions = append(
				restoreObj.Spec.LabelSelector.MatchExpressions,
				*req,
			)
		}
		if err := r.Create(ctx, veleroRestoresToCreate[key], &client.CreateOptions{}); err != nil {
			restoreLogger.Error(
				err,
				"unable to create Velero restore for restore",
				"namespace", veleroRestoresToCreate[key].Namespace,
				"name", veleroRestoresToCreate[key].Name,
			)
			restore.Status.LastMessage = fmt.Sprintf(
				"Cannot create velero restore resource %s/%s: %v",
				veleroRestoresToCreate[key].Namespace,
				veleroRestoresToCreate[key].Name,
				err,
			)
			return err
		}

		r.Recorder.Event(
			restore,
			v1.EventTypeNormal,
			"Velero restore created:",
			veleroRestoresToCreate[key].Name,
		)

		switch key {
		case ManagedClusters:
			restore.Status.VeleroManagedClustersRestoreName = veleroRestoresToCreate[key].Name
		case Credentials:
			restore.Status.VeleroCredentialsRestoreName = veleroRestoresToCreate[key].Name
		case Resources:
			restore.Status.VeleroResourcesRestoreName = veleroRestoresToCreate[key].Name
		}
	}
	restore.Status.Phase = v1beta1.RestorePhaseStarted
	restore.Status.LastMessage = fmt.Sprintf("Restore %s started", restore.Name)

	return nil
}

// retrieve the backup details for this restore object
// based on the restore spec options
func (r *RestoreReconciler) retrieveRestoreDetails(
	ctx context.Context,
	acmRestore *v1beta1.Restore,
) (map[ResourceType]*veleroapi.Restore,
	map[ResourceType]*veleroapi.Backup,
	error) {

	restoreLogger := log.FromContext(ctx)

	restoreKeys := make([]ResourceType, 0, len(veleroScheduleNames)-1) // ignore validation backup
	for key := range veleroScheduleNames {
		if key == ValidationSchedule {
			// ignore validation backup; this is used for the policy
			// to validate that there are backups schedules enabled
			continue
		}
		restoreKeys = append(restoreKeys, key)
	}
	// sort restores to restore last credentials, first resources
	// credentials could have owners in the resources path
	sort.Slice(restoreKeys, func(i, j int) bool {
		return restoreKeys[i] > restoreKeys[j]
	})
	veleroRestoresToCreate := make(map[ResourceType]*veleroapi.Restore, len(restoreKeys))
	backupsForVeleroRestores := make(map[ResourceType]*veleroapi.Backup, len(restoreKeys))

	for i := range restoreKeys {
		backupName := latestBackupStr

		key := restoreKeys[i]
		switch key {
		case ManagedClusters:
			if acmRestore.Spec.VeleroManagedClustersBackupName != nil {
				backupName = *acmRestore.Spec.VeleroManagedClustersBackupName
			}
		case Credentials, CredentialsHive, CredentialsCluster:
			if acmRestore.Spec.VeleroCredentialsBackupName != nil {
				backupName = *acmRestore.Spec.VeleroCredentialsBackupName
			}
		case Resources:
			if acmRestore.Spec.VeleroResourcesBackupName != nil {
				backupName = *acmRestore.Spec.VeleroResourcesBackupName
			}
		case ResourcesGeneric:
			if acmRestore.Spec.VeleroResourcesBackupName != nil {
				backupName = *acmRestore.Spec.VeleroResourcesBackupName
			}
			if backupName == skipRestoreStr &&
				acmRestore.Spec.VeleroManagedClustersBackupName != nil {
				// if resources is set to skip but managed clusters are restored
				// we still need the generic resources
				// for the resources with the label value 'cluster-activation'
				backupName = *acmRestore.Spec.VeleroManagedClustersBackupName
			}
		}

		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName == "" {
			acmRestore.Status.LastMessage = fmt.Sprintf(
				"Backup name not found for resource type: %s",
				key,
			)
			return veleroRestoresToCreate, backupsForVeleroRestores, fmt.Errorf(
				"backup name not found",
			)
		}

		if backupName == skipRestoreStr {
			continue
		}

		veleroRestore := &veleroapi.Restore{}
		veleroBackupName, veleroBackup, err := r.getVeleroBackupName(
			ctx,
			acmRestore,
			key,
			backupName,
		)
		if err != nil {
			restoreLogger.Info(
				"backup name not found, skipping restore for",
				"name", acmRestore.Name,
				"namespace", acmRestore.Namespace,
				"type", key,
			)
			acmRestore.Status.LastMessage = fmt.Sprintf(
				"Backup %s Not found for resource type: %s",
				backupName,
				key,
			)

			if key != CredentialsHive && key != CredentialsCluster && key != ResourcesGeneric {
				// ignore missing hive or cluster key backup files
				// for the case when the backups were created with an older controller version
				return veleroRestoresToCreate, backupsForVeleroRestores, err
			}
		} else {
			veleroRestore.Name = getValidKsRestoreName(acmRestore.Name, veleroBackupName)

			veleroRestore.Namespace = acmRestore.Namespace
			veleroRestore.Spec.BackupName = veleroBackupName

			if err := ctrl.SetControllerReference(acmRestore, veleroRestore, r.Scheme); err != nil {
				acmRestore.Status.LastMessage = fmt.Sprintf(
					"Could not set controller reference for resource type: %s",
					key,
				)
				return veleroRestoresToCreate, backupsForVeleroRestores, err
			}
			veleroRestoresToCreate[key] = veleroRestore
			backupsForVeleroRestores[key] = veleroBackup
		}
	}
	return veleroRestoresToCreate, backupsForVeleroRestores, nil
}

// before restore clean up resources if required
func (r *RestoreReconciler) prepareForRestore(
	ctx context.Context,
	acmRestore v1beta1.Restore,
	veleroRestoresToCreate map[ResourceType]*veleroapi.Restore,
	backupsForVeleroRestores map[ResourceType]*veleroapi.Backup,
) error {

	if ok := findValue([]string{v1beta1.CleanupTypeAll,
		v1beta1.CleanupTypeNone,
		v1beta1.CleanupTypeRestored},
		string(acmRestore.Spec.CleanupBeforeRestore)); !ok {

		msg := "invalid CleanupBeforeRestore value : " +
			string(acmRestore.Spec.CleanupBeforeRestore)
		acmRestore.Status.LastMessage = msg
		return fmt.Errorf(msg)
	}

	// clean up resources only if requested
	if acmRestore.Spec.CleanupBeforeRestore == v1beta1.CleanupTypeNone {
		return nil
	}

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	reconcileArgs := DynamicStruct{
		dc:     r.DiscoveryClient,
		dyn:    r.DynamicClient,
		mapper: r.RESTMapper,
	}

	restoreOptions := RestoreOptions{
		dynamicArgs:   reconcileArgs,
		cleanupType:   acmRestore.Spec.CleanupBeforeRestore,
		deleteOptions: delOptions,
	}

	for key := range veleroRestoresToCreate {

		additionalLabel := ""
		if key == ManagedClusters &&
			veleroRestoresToCreate[ResourcesGeneric] == nil {
			// process here generic resources with an activation label
			// since generic resources are not restored
			additionalLabel = "cluster.open-cluster-management.io/backup in (cluster-activation)"
			prepareRestoreForBackup(
				ctx,
				restoreOptions,
				ResourcesGeneric,
				backupsForVeleroRestores[key],
				additionalLabel,
			)
		}
		if key == ResourcesGeneric &&
			veleroRestoresToCreate[ManagedClusters] == nil {
			// managed clusters not restored
			// don't clean up resources with cluster-activation label
			additionalLabel = "cluster.open-cluster-management.io/backup," +
				"cluster.open-cluster-management.io/backup notin (cluster-activation)"

		}
		prepareRestoreForBackup(
			ctx,
			restoreOptions,
			key,
			backupsForVeleroRestores[key],
			additionalLabel,
		)

	}

	return nil
}
