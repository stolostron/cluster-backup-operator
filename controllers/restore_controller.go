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

	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	noopMsg                    = "Nothing to do for restore %s"
)

type DynamicStruct struct {
	dc     discovery.DiscoveryInterface
	dyn    dynamic.Interface
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

type RestoreOptions struct {
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
//+kubebuilder:rbac:groups=velero.io,resources=deletebackuprequests,verbs=create;list;watch

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
	}

	if restore.Spec.CleanupBeforeRestore != v1beta1.CleanupTypeNone &&
		restore.Status.Phase == "" {
		// update state only at the very beginning
		restore.Status.Phase = v1beta1.RestorePhaseStarted
		restore.Status.LastMessage = "Prepare to restore, cleaning up resources"
		if err = r.Client.Status().Update(ctx, restore); err != nil {
			restoreLogger.Info(err.Error())
		}

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

	isValidSync, msg := isValidSyncOptions(restore)
	sync := isValidSync && restore.Status.Phase == v1beta1.RestorePhaseEnabled

	if len(veleroRestoreList.Items) == 0 || sync {
		if err := r.initVeleroRestores(ctx, restore, sync); err != nil {
			msg := fmt.Sprintf(
				"unable to initialize Velero restores for restore %s/%s: %v",
				req.Namespace,
				req.Name,
				err,
			)
			restoreLogger.Error(err, msg)

			// set error status for all errors from initVeleroRestores
			restore.Status.Phase = v1beta1.RestorePhaseError
			restore.Status.LastMessage = err.Error()
			return ctrl.Result{RequeueAfter: failureInterval}, errors.Wrap(
				r.Client.Status().Update(ctx, restore),
				msg,
			)
		}
	} else {
		_, cleanupOnRestore := setRestorePhase(&veleroRestoreList, restore)

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
			deleteOptions: delOptions,
		}
		cleanupDeltaResources(ctx, r.Client, restore, cleanupOnRestore, restoreOptions)
		executePostRestoreTasks(ctx, r.Client, restore)
	}

	if restore.Spec.SyncRestoreWithNewBackups && !isValidSync {
		restore.Status.LastMessage = restore.Status.LastMessage +
			" ; SyncRestoreWithNewBackups option is ignored because " + msg
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

// create velero.io.Restore resource for each resource type
func (r *RestoreReconciler) initVeleroRestores(
	ctx context.Context,
	restore *v1beta1.Restore,
	sync bool,
) error {
	restoreLogger := log.FromContext(ctx)

	restoreOnlyManagedClusters := false
	if sync {
		if isNewBackupAvailable(ctx, r.Client, restore, Resources) ||
			isNewBackupAvailable(ctx, r.Client, restore, Credentials) {
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
			return nil
		}
	}

	// loop through resourceTypes to create a Velero restore per type
	veleroRestoresToCreate, err := retrieveRestoreDetails(
		ctx,
		r.Client,
		r.Scheme,
		restore,
		restoreOnlyManagedClusters,
	)
	if err != nil {
		return err
	}
	if len(veleroRestoresToCreate) == 0 {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = fmt.Sprintf(noopMsg, restore.Name)
		return nil
	}

	newVeleroRestoreCreated := false

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
			req.Values = []string{ClusterActivationLabel}
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
			req.Values = []string{ClusterActivationLabel}
			restoreObj.Spec.LabelSelector.MatchExpressions = append(
				restoreObj.Spec.LabelSelector.MatchExpressions,
				*req,
			)
		}
		err := r.Create(ctx, veleroRestoresToCreate[key], &client.CreateOptions{})
		if err != nil {
			restoreLogger.Info(
				"unable to create Velero restore for restore",
				"namespace", veleroRestoresToCreate[key].Namespace,
				"name", veleroRestoresToCreate[key].Name,
			)
			if k8serr.IsAlreadyExists(err) && key == Credentials {
				restore.Status.VeleroCredentialsRestoreName = veleroRestoresToCreate[key].Name
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
			}
		}
	}

	if newVeleroRestoreCreated {
		restore.Status.Phase = v1beta1.RestorePhaseStarted
		restore.Status.LastMessage = fmt.Sprintf("Restore %s started", restore.Name)
	} else {
		restore.Status.Phase = v1beta1.RestorePhaseFinished
		restore.Status.LastMessage = fmt.Sprintf(noopMsg, restore.Name)
	}
	return nil
}
