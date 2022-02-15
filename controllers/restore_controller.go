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
	restoreOwnerKey        = ".metadata.controller"
	skipRestoreStr  string = "skip"
	latestBackupStr string = "latest"
)

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

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if restore.Status.Phase == v1beta1.RestorePhaseFinished ||
		restore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors {
		// don't process a restore resource if it's completed
		return ctrl.Result{}, nil
	}

	otherRestoreName, err := r.isOtherRestoresRunning(ctx, restore)
	if err != nil {
		return ctrl.Result{}, err
	}
	if otherRestoreName != "" {
		msg := "Restore instance " + otherRestoreName + " is currently running, ignoring this request."
		updateRestoreStatus(restoreLogger, v1beta1.RestorePhaseFinishedWithErrors, msg, restore)
		return ctrl.Result{}, errors.Wrap(
			r.Client.Status().Update(ctx, restore),
			msg,
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

	if len(veleroRestoreList.Items) == 0 {
		if err := r.initVeleroRestores(ctx, restore); err != nil {
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
			return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
				r.Client.Status().Update(ctx, restore),
				msg,
			)
		}
	} else {
		setRestorePhase(&veleroRestoreList, restore)

		if (restore.Status.Phase == v1beta1.RestorePhaseFinished ||
			restore.Status.Phase == v1beta1.RestorePhaseFinishedWithErrors) &&
			*restore.Spec.VeleroManagedClustersBackupName != skipRestoreStr {
			// this cluster was activated so create the backup schedule from this cluster now
			r.becomeActiveCluster(ctx, *restore)
		}

	}

	err = r.Client.Status().Update(ctx, restore)
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
			restore.Status.Phase = v1beta1.RestorePhaseFinishedWithErrors
			restore.Status.LastMessage = fmt.Sprintf(
				"Velero restore %s has run to completion but encountered 1+ errors",
				veleroRestore.Name,
			)
			return
		}
	}

	// if no velero restore with error, new or inprogress status, they are all completed
	restore.Status.Phase = v1beta1.RestorePhaseFinished
	restore.Status.LastMessage = "All Velero restores have run successfully"
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

// mostRecentWithLessErrors defines type and code to sort velero backups
// according to number of errors and start timestamp
type mostRecentWithLessErrors []veleroapi.Backup

func (backups mostRecentWithLessErrors) Len() int { return len(backups) }

func (backups mostRecentWithLessErrors) Swap(i, j int) {
	backups[i], backups[j] = backups[j], backups[i]
}
func (backups mostRecentWithLessErrors) Less(i, j int) bool {
	if backups[i].Status.Errors < backups[j].Status.Errors {
		return true
	}
	if backups[i].Status.Errors > backups[j].Status.Errors {
		return false
	}
	return backups[j].Status.StartTimestamp.Before(backups[i].Status.StartTimestamp)
}

// getVeleroBackupName returns the name of velero backup will be restored
func (r *RestoreReconciler) getVeleroBackupName(
	ctx context.Context,
	restore *v1beta1.Restore,
	resourceType ResourceType,
	backupName string,
) (string, *veleroapi.Backup, error) {

	var computedName string

	if backupName == latestBackupStr {
		// backup name not available, find a proper backup
		veleroBackups := &veleroapi.BackupList{}
		if err := r.Client.List(ctx, veleroBackups, client.InNamespace(restore.Namespace)); err != nil {
			return "", nil, fmt.Errorf("unable to list velero backups: %v", err)
		}
		if len(veleroBackups.Items) == 0 {
			return "", nil, fmt.Errorf("no backups found")
		}
		// filter available backups to get only the ones related to this resource type
		relatedBackups := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
			return strings.Contains(bkp.Name, veleroScheduleNames[resourceType]) &&
				bkp.Status.Phase == veleroapi.BackupPhaseCompleted
		})
		if len(relatedBackups) == 0 {
			return "", nil, fmt.Errorf("no backups found")
		}
		sort.Sort(mostRecentWithLessErrors(relatedBackups))
		return relatedBackups[0].Name, &relatedBackups[0], nil
	}
	// get the backup name for this type of resource, based on the requested resource timestamp
	backupTimestamp := strings.LastIndex(backupName, "-")
	if backupTimestamp != -1 {
		computedName = veleroScheduleNames[resourceType] + backupName[backupTimestamp:]
	}

	veleroBackup := veleroapi.Backup{}
	err := r.Get(
		ctx,
		types.NamespacedName{Name: computedName, Namespace: restore.Namespace},
		&veleroBackup,
	)
	if err == nil {
		return computedName, &veleroBackup, nil
	}
	return "", nil, fmt.Errorf("cannot find %s Velero Backup: %v", computedName, err)
}

// check if there are other restores that are not complete yet
func (r *RestoreReconciler) isOtherRestoresRunning(
	ctx context.Context,
	restore *v1beta1.Restore,
) (string, error) {
	restoreLogger := log.FromContext(ctx)

	restoreList := v1beta1.RestoreList{}
	if err := r.List(
		ctx,
		&restoreList,
		client.InNamespace(restore.Namespace),
	); err != nil {

		msg := "unable to list restore resources" +
			"namespace:" + restore.Namespace

		restoreLogger.Error(
			err,
			msg,
		)
		return "", err
	}

	if len(restoreList.Items) == 0 {
		return "", nil
	}

	for i := range restoreList.Items {
		restoreItem := restoreList.Items[i]
		if restoreItem.Name == restore.Name {
			continue
		}
		if restoreItem.Status.Phase != v1beta1.RestorePhaseFinished &&
			restoreItem.Status.Phase != v1beta1.RestorePhaseFinishedWithErrors {
			return restoreItem.Name, nil
		}
	}

	return "", nil
}

// create velero.io.Restore resource for each resource type
func (r *RestoreReconciler) initVeleroRestores(
	ctx context.Context,
	restore *v1beta1.Restore,
) error {
	restoreLogger := log.FromContext(ctx)
	restore.Status.Phase = v1beta1.RestorePhaseStarted
	restore.Status.LastMessage = "Restore in progress"
	r.Client.Status().Update(ctx, restore)

	restoreKeys := make([]ResourceType, 0, len(veleroScheduleNames))
	for key := range veleroScheduleNames {
		restoreKeys = append(restoreKeys, key)
	}
	// sort restores to restore last credentials, first resources
	// credentials could have owners in the resources path
	sort.Slice(restoreKeys, func(i, j int) bool {
		return restoreKeys[i] > restoreKeys[j]
	})
	veleroRestoresToCreate := make(map[ResourceType]*veleroapi.Restore, len(restoreKeys))
	backupsForVeleroRestores := make(map[ResourceType]*veleroapi.Backup, len(restoreKeys))

	// loop through resourceTypes to create a Velero restore per type
	for i := range restoreKeys {
		backupName := latestBackupStr

		key := restoreKeys[i]
		switch key {
		case ManagedClusters:
			if restore.Spec.VeleroManagedClustersBackupName != nil {
				backupName = *restore.Spec.VeleroManagedClustersBackupName
			}
		case Credentials, CredentialsHive, CredentialsCluster:
			if restore.Spec.VeleroCredentialsBackupName != nil {
				backupName = *restore.Spec.VeleroCredentialsBackupName
			}
		case Resources, ResourcesGeneric:
			if restore.Spec.VeleroResourcesBackupName != nil {
				backupName = *restore.Spec.VeleroResourcesBackupName
			}
		}

		backupName = strings.ToLower(strings.TrimSpace(backupName))

		if backupName == "" {
			restore.Status.LastMessage = fmt.Sprintf(
				"Backup name not found for resource type: %s",
				key,
			)
			return fmt.Errorf("backup name not found")
		}

		if backupName == skipRestoreStr {
			continue
		}

		veleroRestore := &veleroapi.Restore{}
		veleroBackupName, veleroBackup, err := r.getVeleroBackupName(ctx, restore, key, backupName)
		if err != nil {
			restoreLogger.Info(
				"backup name not found, skipping restore for",
				"name", restore.Name,
				"namespace", restore.Namespace,
				"type", key,
			)
			restore.Status.LastMessage = fmt.Sprintf(
				"Backup %s Not found for resource type: %s",
				backupName,
				key,
			)

			if key != CredentialsHive && key != CredentialsCluster && key != ResourcesGeneric {
				// ignore missing hive or cluster key backup files
				// for the case when the backups were created with an older controller version
				return err
			}
		} else {
			veleroRestore.Name = getValidKsRestoreName(restore.Name, veleroBackupName)

			veleroRestore.Namespace = restore.Namespace
			veleroRestore.Spec.BackupName = veleroBackupName

			if err := ctrl.SetControllerReference(restore, veleroRestore, r.Scheme); err != nil {
				restore.Status.LastMessage = fmt.Sprintf(
					"Could not set controller reference for resource type: %s",
					key,
				)
				return err
			}
			veleroRestoresToCreate[key] = veleroRestore
			backupsForVeleroRestores[key] = veleroBackup
		}
	}

	if len(veleroRestoresToCreate) == 0 {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = fmt.Sprintf("Nothing to do for restore %s", restore.Name)
		return nil
	}

	// clean up resources only if requested
	if restore.Spec.CleanupBeforeRestore {
		deletePolicy := metav1.DeletePropagationForeground
		deleteOptions := metav1.DeleteOptions{
			PropagationPolicy: &deletePolicy,
		}

		for key := range veleroRestoresToCreate {
			prepareForRestore(ctx, r.Client, r.DiscoveryClient, r.DynamicClient,
				key, backupsForVeleroRestores[key], r.RESTMapper, deleteOptions)
		}
	}

	// now create the restore resources and start the actual restore
	for key := range veleroRestoresToCreate {
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
