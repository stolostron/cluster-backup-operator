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

	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
	noopMsg                    = "Nothing to do for restore %s"

	backupPVCLabel  = "cluster.open-cluster-management.io/backup-pvc"
	pvcWaitInterval = time.Second * 10

	acmRestoreFinalizer = "restores.cluster.open-cluster-management.io/finalizer"
	ihcGroup            = "operator.open-cluster-management.io"
)

type DynamicStruct struct {
	dc  discovery.DiscoveryInterface
	dyn dynamic.Interface
}

type RestoreOptions struct {
	dynamicArgs DynamicStruct
	cleanupType v1beta1.CleanupType
	mapper      *restmapper.DeferredDiscoveryRESTMapper
}

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	KubeClient      kubernetes.Interface
	DiscoveryClient discovery.DiscoveryInterface
	DynamicClient   dynamic.Interface
	Scheme          *runtime.Scheme
	Recorder        record.EventRecorder
}

//nolint:lll
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/finalizers,verbs=update
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list
//+kubebuilder:rbac:groups=velero.io,resources=restores,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=velero.io,resources=backupstoragelocations,verbs=get;list;watch
//+kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
//nolint:funlen
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	restoreLogger := log.FromContext(ctx)
	restore := &v1beta1.Restore{}

	// velero doesn't delete expired backups if they are in FailedValidation
	// workaround and delete expired or invalid validation backups them now
	cleanupExpiredValidationBackups(ctx, req.Namespace, r.Client)

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
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
		restoreLogger.Error(err, msg)

		return ctrl.Result{}, err
	}
	internalHubResource, err := getInternalHubResource(ctx, r.DynamicClient)
	if err != nil {
		return ctrl.Result{}, err
	}

	isMarkedForDeletion := restore.GetDeletionTimestamp() != nil
	if !isMarkedForDeletion {
		if err := addResourcesFinalizer(ctx, r.Client, internalHubResource, restore); err != nil {
			restoreLogger.Info(fmt.Sprintf("addResourcesFinalizer: %s", err.Error()))
		}
	} else {
		// Run finalization logic. If the finalization
		// logic fails, don't remove the finalizer so
		// that we can retry during the next reconciliation.
		if err := finalizeRestore(ctx, r.Client, restore, veleroRestoreList); err != nil {
			// Logging err and returning nil to ensure wait
			restoreLogger.Info(fmt.Sprintf("Finalizing: %s", err.Error()))
			return ctrl.Result{RequeueAfter: pvcWaitInterval}, nil
		}
		// Remove restore finalizer for acm restore and internalhub resource if this is the only restore
		// Once all finalizers have been removed, the object will be deleted.
		return ctrl.Result{}, removeResourcesFinalizer(ctx, r.Client, internalHubResource, restore)
	}
	// check if internalhubcomponent is marked for deletion
	// delete this resource if that's the case
	if internalHubResource.GetDeletionTimestamp() != nil {
		// delete this acm resource
		return ctrl.Result{}, r.Delete(ctx, restore)
	}

	if restore.Status.Phase == v1beta1.RestorePhaseFinished ||
		restore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors {
		// don't process a restore resource if it's completed
		// try to add the finalizer
		return ctrl.Result{}, nil
	}

	// don't create restores if there is any other active resource in this namespace
	activeResourceMsg, err := isOtherResourcesRunning(ctx, r.Client, restore)
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

	// don't create restores if the cleanup option is not valid
	activeResourceMsg = isValidCleanupOption(restore)
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

	if msg, retry := validateStorageSettings(ctx, r.Client, req.Name, req.Namespace, restore); msg != "" {

		updateRestoreStatus(restoreLogger, v1beta1.RestorePhaseError, msg, restore)

		if retry {
			// retry after failureInterval
			return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
				r.Client.Status().Update(ctx, restore),
				msg,
			)
		}
		return ctrl.Result{}, errors.Wrap(
			r.Client.Status().Update(ctx, restore),
			msg,
		)
	} else if restore.Status.Phase == v1beta1.RestorePhaseEnabledError &&
		strings.Contains(restore.Status.LastMessage, "BackupStorageLocation") {
		// if the restore is in enabled error phase, and the storage location is available,
		// update status based on velero restore status
		// This allows recovery from BSL errors by reprocessing the restore
		// get the list of velero restore resources created by this acm restore
		veleroRestoreList := veleroapi.RestoreList{}
		if err := r.List(
			ctx,
			&veleroRestoreList,
			client.InNamespace(restore.Namespace),
			client.MatchingFields{restoreOwnerKey: restore.Name},
		); err == nil {

			setRestorePhase(&veleroRestoreList, restore)
		}
		return ctrl.Result{}, r.Client.Status().Update(ctx, restore)
	}

	if restore.Spec.CleanupBeforeRestore != v1beta1.CleanupTypeNone &&
		restore.Status.Phase == "" {
		// update state only at the very beginning
		restore.Status.Phase = v1beta1.RestorePhaseStarted
		restore.Status.LastMessage = "Prepare to restore, cleaning up resources"
		err = r.Client.Status().Update(ctx, restore)
		if err != nil {
			restoreLogger.Error(err, "Error updating restore status")
			return ctrl.Result{}, err
		}

	}

	isValidSync, msg := isValidSyncOptions(restore)
	sync := isValidSync && restore.IsPhaseEnabled()
	isPVCStep := isPVCInitializationStep(restore, veleroRestoreList)
	initRestoreCond := len(veleroRestoreList.Items) == 0 || sync

	if initRestoreCond || isPVCStep {
		mustwait, waitmsg, err := r.initVeleroRestores(ctx, restore, sync, &veleroRestoreList)
		if err != nil {
			msg := fmt.Sprintf(
				"unable to initialize Velero restores for restore %s/%s: %v",
				req.Namespace,
				req.Name,
				err,
			)
			restoreLogger.Error(err, msg)

			// set error status for all errors from initVeleroRestores
			updateRestoreStatus(restoreLogger, v1beta1.RestorePhaseError, err.Error(), restore)
			return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
				r.Client.Status().Update(ctx, restore),
				msg,
			)
		}

		if mustwait {
			restore.Status.Phase = v1beta1.RestorePhaseStarted
			restore.Status.LastMessage = waitmsg
			return ctrl.Result{RequeueAfter: pvcWaitInterval}, errors.Wrap(
				r.Client.Status().Update(ctx, restore),
				waitmsg,
			)
		}
	}
	if !initRestoreCond {
		r.cleanupOnRestore(ctx, restore)
	}

	// Always check for orphaned Velero restores (whose backing backups are gone)
	// This handles cases where backups are deleted/expired from storage
	r.cleanupOrphanedVeleroRestores(ctx, restore)

	if restore.Spec.SyncRestoreWithNewBackups && !isValidSync {
		restore.Status.LastMessage = restore.Status.LastMessage +
			" ; SyncRestoreWithNewBackups option is ignored because " + msg
	}

	err = r.Client.Status().Update(ctx, restore)
	return sendResult(restore, err)
}

// If any pvcs were created on the backup hub using the backup-pvc label,
// wait for the pvc to be created by the pvc configmap
// the config map is restored with the credentials backup, which is the first backup to be restored
// wait for the pvc when only the credentials backup was created and the restore is not set to sync backup data
func isPVCInitializationStep(
	acmRestore *v1beta1.Restore,
	veleroRestoreList veleroapi.RestoreList,
) bool {
	isSync := acmRestore.Spec.SyncRestoreWithNewBackups
	isActiveDataBeingRestored := *acmRestore.Spec.VeleroManagedClustersBackupName != skipRestoreStr

	if isSync && !isActiveDataBeingRestored {
		// don't have to wait, this is a sync op, and active data is not restored yet
		return false
	}

	if !isSync && !isActiveDataBeingRestored {
		// don't have to wait, this is not a sync op, but active data is not restored with this restore
		return false
	}

	// active data is requested to be restored
	// get out of wait if the ManagedClusters has been restored
	for i := range veleroRestoreList.Items {
		veleroRestore := &veleroRestoreList.Items[i]

		if veleroRestore.Spec.ScheduleName == veleroScheduleNames[ManagedClusters] {
			// at least one restore not created by the credentials schedule, seems to be passed the  creds restore
			return false
		}
	}

	// should wait
	return true
}

// call clean up resources after the velero restore is completed
// execute any other post restore tasks
func (r *RestoreReconciler) cleanupOnRestore(
	ctx context.Context,
	acmRestore *v1beta1.Restore,
) {
	restoreLogger := log.FromContext(ctx)
	// get the list of velero restore resources created by this acm restore
	veleroRestoreList := veleroapi.RestoreList{}
	if err := r.List(
		ctx,
		&veleroRestoreList,
		client.InNamespace(acmRestore.Namespace),
		client.MatchingFields{restoreOwnerKey: acmRestore.Name},
	); err != nil {
		msg := "unable to list velero restores for acm restore" +
			"namespace:" + acmRestore.Namespace +
			"name:" + acmRestore.Name
		restoreLogger.Error(err, msg)
		return
	}

	_, cleanupOnRestore := setRestorePhase(&veleroRestoreList, acmRestore)

	reconcileArgs := DynamicStruct{
		dc:  r.DiscoveryClient,
		dyn: r.DynamicClient,
	}
	restoreOptions := RestoreOptions{
		dynamicArgs: reconcileArgs,
		cleanupType: acmRestore.Spec.CleanupBeforeRestore,
		mapper:      restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(r.DiscoveryClient)),
	}

	cleanupDeltaResources(ctx, r.Client, acmRestore, cleanupOnRestore, restoreOptions)
	executePostRestoreTasks(ctx, r.Client, acmRestore)

	// set CompletionTimestamp when cleanupOnRestore is true or restore is completed
	// the CompletionTimestamp must be set after cleanupDeltaResources and executePostRestoreTasks are completed
	restoreCompleted := (acmRestore.Status.Phase == v1beta1.RestorePhaseFinished ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors ||
		acmRestore.Status.Phase == v1beta1.RestorePhaseError)
	if cleanupOnRestore || restoreCompleted {
		rightNow := metav1.Now()
		acmRestore.Status.CompletionTimestamp = &rightNow
	}
}

// cleanupOrphanedVeleroRestores deletes Velero restores whose backing Velero backups
// are missing, being deleted, or in FailedValidation state.
// It excludes "latest" restores (those currently tracked in ACM restore status) to avoid
// triggering unwanted new restores.
//
//nolint:funlen
func (r *RestoreReconciler) cleanupOrphanedVeleroRestores(
	ctx context.Context,
	acmRestore *v1beta1.Restore,
) {
	restoreLogger := log.FromContext(ctx)

	// Get all Velero restores owned by this ACM restore
	veleroRestoreList := veleroapi.RestoreList{}
	if err := r.List(
		ctx,
		&veleroRestoreList,
		client.InNamespace(acmRestore.Namespace),
		client.MatchingFields{restoreOwnerKey: acmRestore.Name},
	); err != nil {
		restoreLogger.Error(err, "Failed to list Velero restores for orphan cleanup")
		return
	}

	// Use getLatestVeleroRestores to identify which restores to KEEP
	// This includes both tracked restores and their -active variants
	latestRestores := getLatestVeleroRestores(&veleroRestoreList, acmRestore)
	latestRestoreNames := make(map[string]bool)
	for i := range latestRestores {
		latestRestoreNames[latestRestores[i].Name] = true
	}

	// Check each restore to see if its backing backup still exists
	for i := range veleroRestoreList.Items {
		restore := &veleroRestoreList.Items[i]

		// Skip if this is a current/latest restore - we don't want to delete it
		if latestRestoreNames[restore.Name] {
			continue
		}

		// Skip if restore is already being deleted
		if !restore.DeletionTimestamp.IsZero() {
			continue
		}

		// Skip if no backup name specified
		if restore.Spec.BackupName == "" {
			continue
		}

		// Check if the backup exists
		backup := &veleroapi.Backup{}
		backupKey := types.NamespacedName{
			Name:      restore.Spec.BackupName,
			Namespace: restore.Namespace,
		}

		err := r.Get(ctx, backupKey, backup)
		if err != nil {
			if k8serr.IsNotFound(err) {
				// Backup doesn't exist - delete the orphaned restore
				restoreLogger.Info(
					"Deleting orphaned Velero restore - backing backup not found",
					"restore", restore.Name,
					"backup", restore.Spec.BackupName,
				)
				if delErr := r.Delete(ctx, restore); delErr != nil && !k8serr.IsNotFound(delErr) {
					restoreLogger.Error(delErr, "Failed to delete orphaned restore", "restore", restore.Name)
				}
			}
			// Other errors are ignored - we'll check again on next reconcile
			continue
		}

		// Check if backup is being deleted or in an invalid state
		if !backup.DeletionTimestamp.IsZero() ||
			backup.Status.Phase == veleroapi.BackupPhaseDeleting ||
			backup.Status.Phase == veleroapi.BackupPhaseFailedValidation {
			restoreLogger.Info(
				"Deleting orphaned Velero restore - backing backup is being deleted or invalid",
				"restore", restore.Name,
				"backup", backup.Name,
				"backupPhase", backup.Status.Phase,
			)
			if delErr := r.Delete(ctx, restore); delErr != nil && !k8serr.IsNotFound(delErr) {
				restoreLogger.Error(delErr, "Failed to delete restore", "restore", restore.Name)
			}
		}
	}
}

func sendResult(restore *v1beta1.Restore, err error) (ctrl.Result, error) {
	if restore.Spec.SyncRestoreWithNewBackups &&
		restore.IsPhaseEnabled() {

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
			if owner == nil || owner.APIVersion != apiGVStr || owner.Kind != "Restore" {
				return nil
			}

			return []string{owner.Name}
		}); err != nil {
		return err
	}

	// watch InternalHubComponent as an unstructured resource
	ihc := &unstructured.Unstructured{}
	ihc.SetGroupVersionKind(schema.GroupVersionKind{
		Kind:    "InternalHubComponent",
		Group:   ihcGroup,
		Version: "v1",
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Restore{}).
		Owns(&veleroapi.Restore{}).
		Watches(ihc,
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				return mapFuncTriggerFinalizers(ctx, mgr.GetClient(), o)
			}), builder.WithPredicates(processFinalizersPredicate())).
		Complete(r)
}

// process InternalHubComponent resources and watch for a delete action
func mapFuncTriggerFinalizers(ctx context.Context, k8sClient client.Client,
	o client.Object) []reconcile.Request {

	reqs := []reconcile.Request{}

	if o.GetName() != "cluster-backup" {
		return reqs
	}

	if o.GetDeletionTimestamp() != nil {
		// get all acm restore resources
		rsList := &v1beta1.RestoreList{}
		err := k8sClient.List(ctx, rsList)
		if err != nil {
			return []reconcile.Request{}
		}

		for i := range rsList.Items {
			rs := rsList.Items[i]
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      rs.GetName(),
					Namespace: rs.GetNamespace(),
				},
			})
		}
	}

	return reqs
}

func processFinalizersPredicate() predicate.Predicate {
	// Only reconcile acm Restore resources when deleting the InternalHubComponent
	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return true
		},
		UpdateFunc: func(_ event.UpdateEvent) bool {
			return true
		},
		GenericFunc: func(_ event.GenericEvent) bool {
			return false
		},
	}
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

// areTrackedRestoresMissing checks if any tracked Velero restore is missing from the cluster
func areTrackedRestoresMissing(
	restore *v1beta1.Restore,
	veleroRestoreList *veleroapi.RestoreList,
) bool {
	if veleroRestoreList == nil {
		return true
	}

	existingRestores := make(map[string]bool)
	for i := range veleroRestoreList.Items {
		existingRestores[veleroRestoreList.Items[i].Name] = true
	}

	trackedNames := []string{
		restore.Status.VeleroManagedClustersRestoreName,
		restore.Status.VeleroResourcesRestoreName,
		restore.Status.VeleroGenericResourcesRestoreName,
		restore.Status.VeleroCredentialsRestoreName,
	}

	for _, name := range trackedNames {
		if name != "" && !existingRestores[name] {
			return true
		}
	}

	return false
}

// create velero.io.Restore resource for each resource type
//
//nolint:funlen
func (r *RestoreReconciler) initVeleroRestores(
	ctx context.Context,
	restore *v1beta1.Restore,
	sync bool,
	veleroRestoreList *veleroapi.RestoreList,
) (bool, string, error) {
	restoreLogger := log.FromContext(ctx)

	restoreOnlyManagedClusters := false
	if sync {
		newBackupsAvailable := isNewBackupAvailable(ctx, r.Client, restore, Resources) ||
			isNewBackupAvailable(ctx, r.Client, restore, Credentials)

		if newBackupsAvailable {
			restoreLogger.Info(
				"new backups available to sync with for this restore",
				"name", restore.Name,
				"namespace", restore.Namespace,
			)
		} else if restore.Status.VeleroManagedClustersRestoreName == "" &&
			*restore.Spec.VeleroManagedClustersBackupName == latestBackupStr {
			// check if the managed cluster resources were not restored yet
			// allow that now
			restoreOnlyManagedClusters = true
		} else {
			// check if any tracked restore is missing from the cluster
			trackedRestoresMissing := areTrackedRestoresMissing(restore, veleroRestoreList)
			if !trackedRestoresMissing {
				return false, "", nil
			}
			restoreLogger.Info(
				"tracked Velero restore(s) missing, will recreate",
				"name", restore.Name,
				"namespace", restore.Namespace,
			)
		}
	}

	// loop through resourceTypes to create a Velero restore per type
	resKeys, veleroRestoresToCreate, err := retrieveRestoreDetails(
		ctx,
		r.Client,
		r.Scheme,
		restore,
		restoreOnlyManagedClusters,
	)
	if err != nil {
		return false, "", err
	}
	if len(veleroRestoresToCreate) == 0 {
		updateRestoreStatus(
			restoreLogger,
			v1beta1.RestorePhaseFinished,
			fmt.Sprintf(noopMsg, restore.Name),
			restore,
		)
		return false, "", nil
	}

	newVeleroRestoreCreated := false

	// now create the restore resources and start the actual restore
	for resKey := range resKeys {

		key := resKeys[resKey]

		restoreObj := veleroRestoresToCreate[key]
		if veleroRestoresToCreate[key] == nil {
			// this type of backup is not restored now
			continue
		}
		if (key == ResourcesGeneric || key == Credentials) &&
			veleroRestoresToCreate[ManagedClusters] == nil {
			// if restoring the resources but not the managed clusters,
			// do not restore generic resources in the activation stage
			req := &metav1.LabelSelectorRequirement{}
			req.Key = backupCredsClusterLabel
			req.Operator = "NotIn"
			req.Values = []string{ClusterActivationLabel}

			addRestoreLabelSelector(restoreObj, *req)
		}

		isCredsClsOnActiveStep := updateLabelsForActiveResources(ctx, r.Client, restore, key, veleroRestoresToCreate)
		err := r.Create(ctx, veleroRestoresToCreate[key], &client.CreateOptions{})

		if err != nil {
			restoreLogger.Info(
				fmt.Sprintf("unable to create Velero restore for restore %s:%s, error:%s",
					veleroRestoresToCreate[key].Namespace, veleroRestoresToCreate[key].Name,
					err.Error()),
			)
			if k8serr.IsAlreadyExists(err) && key == Credentials {
				restore.Status.VeleroCredentialsRestoreName = veleroRestoresToCreate[key].Name
			}
			if k8serr.IsAlreadyExists(err) && key == ResourcesGeneric {
				restore.Status.VeleroGenericResourcesRestoreName = veleroRestoresToCreate[key].Name
			}
		} else {
			newVeleroRestoreCreated = true
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
			case ResourcesGeneric:
				restore.Status.VeleroGenericResourcesRestoreName = veleroRestoresToCreate[key].Name

			}
		}
		// check if needed to wait for pvcs to be created before the app data is restored
		if isCredsClsOnActiveStep {
			if shouldWait, waitMsg := processRestoreWait(ctx, r.Client,
				veleroRestoresToCreate[key].Name, restore.Namespace); shouldWait {
				// some PVCs were not created yet, wait for them
				return true, waitMsg, nil
			}
		}
	}

	if newVeleroRestoreCreated {
		restore.Status.Phase = v1beta1.RestorePhaseStarted
		restore.Status.LastMessage = fmt.Sprintf("Restore %s started", restore.Name)
	} else {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = fmt.Sprintf("Restore %s completed", restore.Name)
	}
	return false, "", nil
}

// checkCredsActiveExists checks if a credentials-active Velero restore exists
func checkCredsActiveExists(ctx context.Context, c client.Client, restore *v1beta1.Restore) bool {
	veleroRestoreList := &veleroapi.RestoreList{}
	if err := c.List(ctx, veleroRestoreList, client.InNamespace(restore.Namespace)); err == nil {
		for i := range veleroRestoreList.Items {
			if strings.Contains(veleroRestoreList.Items[i].Name, restore.Name) &&
				strings.Contains(veleroRestoreList.Items[i].Name, "credentials") &&
				strings.HasSuffix(veleroRestoreList.Items[i].Name, "-active") {
				return true
			}
		}
	}
	return false
}

// shouldAddActivationLabelForKey determines if activation label should be added for this resource type
func shouldAddActivationLabelForKey(
	key ResourceType,
	veleroRestoresToCreate map[ResourceType]*veleroapi.Restore,
	isRealSyncMode bool,
	credsWasSkip bool,
	resourcesWasSkip bool,
	credsActiveExists bool,
) bool {
	// Only applies when managed clusters are being restored
	if veleroRestoresToCreate[ManagedClusters] == nil {
		return false
	}

	// For ResourcesGeneric
	if key == ResourcesGeneric {
		if veleroRestoresToCreate[Resources] == nil {
			return true
		}
		if isRealSyncMode || resourcesWasSkip || credsActiveExists {
			return true
		}
		return false
	}

	// For Credentials
	if key == Credentials {
		if isRealSyncMode || credsWasSkip {
			return true
		}
		return false
	}

	return false
}

// for an activation phase update restore labels to include activation resources
func updateLabelsForActiveResources(
	ctx context.Context,
	c client.Client,
	restore *v1beta1.Restore,
	key ResourceType,
	veleroRestoresToCreate map[ResourceType]*veleroapi.Restore,
) bool {
	// check if this is the credential restore run
	// during the cluster activation step
	isCredsClsOnActiveStep := false

	restoreObj := veleroRestoresToCreate[key]

	// Check if credentials or resources were originally set to skip
	credsWasSkip := key == Credentials && restore.Spec.VeleroCredentialsBackupName != nil &&
		*restore.Spec.VeleroCredentialsBackupName == skipRestoreStr
	resourcesWasSkip := key == ResourcesGeneric && restore.Spec.VeleroResourcesBackupName != nil &&
		*restore.Spec.VeleroResourcesBackupName == skipRestoreStr

	// Only add activation label when in true sync mode or when resources were originally skipped
	// Don't add it when sync=true but managedClusters=latest from the start (non-sync scenario)
	isRealSyncMode := restore.Spec.SyncRestoreWithNewBackups &&
		(restore.Status.Phase == v1beta1.RestorePhaseEnabled)

	// Check if credentials-active Velero restore exists (indicates activation scenario)
	credsActiveExists := false
	if key == ResourcesGeneric {
		credsActiveExists = checkCredsActiveExists(ctx, c, restore)
	}

	shouldAddActivationLabel := shouldAddActivationLabelForKey(
		key, veleroRestoresToCreate, isRealSyncMode, credsWasSkip, resourcesWasSkip, credsActiveExists)

	if shouldAddActivationLabel {
		// if restoring the ManagedClusters
		// and resources are not restored or this is a sync phase
		// need to restore the generic resources for the activation phase

		// if restoring the ManagedClusters need to restore the credentials resources for the activation phase
		// if managed clusters are restored, then need to restore the credentials to get active resources

		req := &metav1.LabelSelectorRequirement{}
		req.Key = backupCredsClusterLabel
		req.Operator = "In"
		req.Values = []string{ClusterActivationLabel}

		addRestoreLabelSelector(restoreObj, *req)

		// use a different name for this activation restore; if this Restore was run using the sync operation,
		// the generic and credentials restore for this backup name already exist
		if (key == ResourcesGeneric && *restore.Spec.VeleroResourcesBackupName != skipRestoreStr) ||
			(key == Credentials && *restore.Spec.VeleroCredentialsBackupName != skipRestoreStr) {
			veleroRestoresToCreate[key].Name = veleroRestoresToCreate[key].Name + "-active"
		}
	}

	if key == Credentials && veleroRestoresToCreate[ManagedClusters] != nil {
		// this is a credentials restore with managed clusters being restored
		// wait for the creation of the PVCs defined by backup-pvc configmaps stored by this backup
		isCredsClsOnActiveStep = true
	}

	return isCredsClsOnActiveStep
}

// check if the credentials backup has any velero pvcs configuration
// then verify if the PVC have been created and wait for the
// PVC to be created by the backup-pvc policy
func processRestoreWait(
	ctx context.Context,
	c client.Client,
	restoreName string,
	restoreNamespace string,
) (bool, string) {
	restoreLogger := log.FromContext(ctx)
	restoreLogger.Info("Enter processRestoreWait " + restoreName)

	restore := &veleroapi.Restore{}
	lookupKey := types.NamespacedName{
		Name:      restoreName,
		Namespace: restoreNamespace,
	}

	status := veleroapi.RestorePhaseNew
	if err := c.Get(ctx, lookupKey, restore); err == nil {
		status = restore.Status.Phase
	}

	if status == "" || status == veleroapi.RestorePhaseNew || status == veleroapi.RestorePhaseInProgress {
		// look for the list of PVCs, wait first for the backup to complete
		restoreLogger.Info("Exit processRestoreWait wait, restore not completed")
		return true, "waiting for restore to complete " + restoreName
	}

	// look for all PVC configmaps with  label and verify the PVC was created
	configMaps := &v1.ConfigMapList{}
	vLabel, _ := labels.NewRequirement(backupPVCLabel,
		selection.Exists, []string{})

	labelSelector := labels.NewSelector()
	selector := labelSelector.Add(*vLabel)
	if err := c.List(ctx, configMaps, &client.ListOptions{
		LabelSelector: selector,
	}); err == nil {
		pvcs := []string{}
		for i := range configMaps.Items {
			restoreLogger.Info(fmt.Sprintf("checking configmap %s:%s", configMaps.Items[i].Namespace, configMaps.Items[i].Name))
			pvc := &v1.PersistentVolumeClaim{}
			pvcName := configMaps.Items[i].GetLabels()[backupPVCLabel]
			pvcNS := configMaps.Items[i].Namespace
			lookupKey := types.NamespacedName{
				Name:      pvcName,
				Namespace: pvcNS,
			}
			restoreLogger.Info(fmt.Sprintf("Check if PVC exists %s:%s", pvcNS, pvcName))
			if errPVC := c.Get(ctx, lookupKey, pvc); errPVC != nil {
				restoreLogger.Info(fmt.Sprintf("PVC not found %s:%s", pvcNS, pvcName))
				pvcs = append(pvcs, pvcName+":"+pvcNS)
			}
		}
		if len(pvcs) > 0 {
			restoreLogger.Info("Exit processRestoreWait, PVCs not found : " + strings.Join(pvcs, ", "))
			return true, "waiting for PVC " + strings.Join(pvcs, ", ")
		}
	}

	restoreLogger.Info("Exit processRestoreWait with no wait")

	return false, ""
}
