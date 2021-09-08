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
	"time"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	kubeclient "k8s.io/client-go/kubernetes"
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

const (
	managedClusterImportInterval            = 10 * time.Second // as soon restore is finished we start to poll for managedcluster registration
	BootstrapHubKubeconfigSecretName        = "bootstrap-hub-kubeconfig"
	OpenClusterManagementAgentNamespaceName = "open-cluster-management-agent" // TODO: double check whether this can change
)

type GetKubeClientFromSecretFunc func(*corev1.Secret) (kubeclient.Interface, error)

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	GetKubeClientFromSecret GetKubeClientFromSecretFunc // returns  a kubeclient from a kubeconfig secret. Exposed for tests
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list
//+kubebuilder:rbac:groups=velero.io,resources=restores,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups=operator.openshift.io,resources=configs,verbs=get;list;watch
//+kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	restoreLogger := log.FromContext(ctx)
	restore := &v1beta1.Restore{}

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// retrieve the velero restore (if any)
	veleroRestoreList := veleroapi.RestoreList{}
	// TODO: double check if oadp set its owner key
	if err := r.List(ctx, &veleroRestoreList, client.InNamespace(req.Namespace), client.MatchingFields{restoreOwnerKey: req.Name}); err != nil {
		restoreLogger.Error(err, "unable to list velero restores for restore %s/%s", req.Namespace, req.Name)
		return ctrl.Result{}, err
	}

	switch {
	case len(veleroRestoreList.Items) == 0:
		veleroRestore, err := r.initVeleroRestore(ctx, restore)
		if err != nil {
			restoreLogger.Error(err, "unable to initialize velero restore for restore %s/%s", req.Namespace, req.Name)
			return ctrl.Result{}, err
		}
		if err = r.Create(ctx, veleroRestore, &client.CreateOptions{}); err != nil {
			restoreLogger.Error(err, "unable to create velero restore for restore %s/%s", req.Namespace, req.Name)
			return ctrl.Result{}, err
		}
		r.Recorder.Event(restore, v1.EventTypeNormal, "Velero Restore created:", fmt.Sprintf("%s/%s", veleroRestore.Namespace, veleroRestore.Name))
		restore.Status.VeleroRestoreName = veleroRestore.Name
		apimeta.SetStatusCondition(&restore.Status.Conditions,
			metav1.Condition{
				Type:    v1beta1.RestoreStarted,
				Status:  metav1.ConditionTrue,
				Reason:  v1beta1.RestoreReasonStarted,
				Message: fmt.Sprintf("Velero Restore %s Started", veleroRestore.Name),
			})
	case len(veleroRestoreList.Items) == 1:
		veleroRestore := veleroRestoreList.Items[0].DeepCopy()
		switch {
		case isRestoreFinished(restore):
			return ctrl.Result{}, nil // won't do more
		case isVeleroRestoreFinished(veleroRestore):
			r.Recorder.Event(restore, v1.EventTypeNormal, "Velero Restore finished:", fmt.Sprintf("%s/%s", veleroRestore.Namespace, veleroRestore.Name))
			allAttached, err := r.reattachManagedClusters(ctx)
			if err != nil {
				return ctrl.Result{}, err
			}
			switch {
			case allAttached:
				apimeta.SetStatusCondition(&restore.Status.Conditions,
					metav1.Condition{
						Type:    v1beta1.RestoreComplete,
						Status:  metav1.ConditionTrue,
						Reason:  v1beta1.RestoreReasonRunning,
						Message: fmt.Sprintf("Restore Complete %s", restore.Name),
					})
				return ctrl.Result{}, r.Client.Status().Update(ctx, restore)
			case !allAttached:
				apimeta.SetStatusCondition(&restore.Status.Conditions,
					metav1.Condition{
						Type:    v1beta1.RestoreReattaching,
						Status:  metav1.ConditionTrue,
						Reason:  v1beta1.RestoreReasonRunning,
						Message: fmt.Sprintf("Restore %s is re-attaching clusters", restore.Name),
					})
				return ctrl.Result{RequeueAfter: time.Second * 10}, r.Client.Status().Update(ctx, restore) // to wait for managed cluster re-attaching phase...
			}
		default:
			apimeta.SetStatusCondition(&restore.Status.Conditions,
				metav1.Condition{
					Type:    v1beta1.RestoreStarted,
					Status:  metav1.ConditionTrue,
					Reason:  v1beta1.RestoreReasonRunning,
					Message: fmt.Sprintf("Velero Restore %s still running", veleroRestore.Name),
				})
		}

	default:
		// TODO: handles multiple velero restores:
		// check if one velero is still running... update status and wait
		// if all finished handleManagedClusters
	}

	err := r.Client.Status().Update(ctx, restore)
	return ctrl.Result{}, errors.Wrap(err, fmt.Sprintf("could not update status for restore %s/%s", restore.Namespace, restore.Name))
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.GetKubeClientFromSecret = getKubeClientFromSecret
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

// mostRecentWithLessErrors defines type and code to sort velero backups according to number of errors and start timestamp
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
func (r *RestoreReconciler) getVeleroBackupName(ctx context.Context, restore *v1beta1.Restore) (string, error) {
	if restore.Spec.VeleroBackupName != nil {
		return *restore.Spec.VeleroBackupName, nil
	}
	veleroBackups := &veleroapi.BackupList{}
	if err := r.Client.List(ctx, veleroBackups, client.InNamespace(restore.Namespace)); err != nil {
		return "", fmt.Errorf("unable to list velero backups: %v", err)
	}
	if len(veleroBackups.Items) == 0 {
		return "", fmt.Errorf("not available backups found")
	}
	sort.Sort(mostRecentWithLessErrors(veleroBackups.Items))
	return veleroBackups.Items[0].Name, nil
}

func (r *RestoreReconciler) initVeleroRestore(ctx context.Context, restore *v1beta1.Restore) (*veleroapi.Restore, error) {
	veleroRestore := &veleroapi.Restore{}

	veleroBackupName, err := r.getVeleroBackupName(ctx, restore)
	if err != nil {
		return nil, err
	}
	veleroRestore.Name = restore.Name + "-" + veleroBackupName

	veleroRestore.Namespace = restore.Namespace
	veleroRestore.Spec.BackupName = veleroBackupName

	if err := ctrl.SetControllerReference(restore, veleroRestore, r.Scheme); err != nil {
		return nil, err
	}
	return veleroRestore, nil
}

// reattachManagedClusters is the entry points for all the managed clusters to be re-attached
// currently it loops across all the managed cluster namespaces
func (r *RestoreReconciler) reattachManagedClusters(ctx context.Context) (bool, error) {
	shouldUpdate := false
	namespaceList := v1.NamespaceList{}
	onlyManagedClusterNamespaces := labels.NewSelector() // TODO adds label for velero backup only
	req, err := labels.NewRequirement("cluster.open-cluster-management.io/managedCluster", selection.Exists, []string{})
	if err != nil {
		return shouldUpdate, fmt.Errorf("unable to init selector for  managed cluster namespaces: %v", err)
	}
	onlyManagedClusterNamespaces.Add(*req)
	if err := r.Client.List(ctx, &namespaceList, client.MatchingLabelsSelector{Selector: onlyManagedClusterNamespaces}); err != nil {
		return shouldUpdate, fmt.Errorf("unable to list managed cluster namespaces: %v", err)
	}

	var errors []error
	allAttached := true
	for i := range namespaceList.Items {
		attached, err := r.reattachManagedCluster(ctx, namespaceList.Items[i].Name)
		if err != nil {
			errors = append(errors, err)
		}
		allAttached = allAttached && attached
	}
	return shouldUpdate, utilerrors.NewAggregate(errors)
}

// reattachManagedCluster handles the registration of the managed cluster
func (r *RestoreReconciler) reattachManagedCluster(ctx context.Context, managedClusterName string) (bool, error) {
	managedCluster := clusterv1.ManagedCluster{}
	attached := false
	err := r.Client.Get(ctx, types.NamespacedName{Name: managedClusterName}, &managedCluster)
	if err != nil {
		// Since we don't backup managedclusters and we don't find it it means the re-attaching process is on goning or need to be started
		if apierrors.IsNotFound(err) {
			secrets := v1.SecretList{}
			if err = r.Client.List(ctx, &secrets, client.InNamespace(managedClusterName)); err != nil { // fetch secrets from managedcluster namespace
				return attached, fmt.Errorf("cannot list secrets in namespace %s: %v", managedClusterName, err)
			}
			adminKubeconfigSecrets := []corev1.Secret{}
			bootstrapSATokenSecrets := []corev1.Secret{}
			if err = filterSecrets(ctx, &secrets, &adminKubeconfigSecrets, &bootstrapSATokenSecrets); err != nil {
				return attached, fmt.Errorf("unable to get kubeconfig secrets for managed cluster %s: %v", managedClusterName, err)
			}

			var (
				managedClusterKubeClient kubeclient.Interface = nil
				errors                                        = []error{}
			)
			for _, s := range adminKubeconfigSecrets {
				managedClusterKubeClient, err = r.GetKubeClientFromSecret(&s)
				if err != nil {
					errors = append(errors, fmt.Errorf("unable to get kubernetes client: %v", err))
				}
			}
			if managedClusterKubeClient == nil {
				return attached, utilerrors.NewAggregate(errors)
			}

			apiServer, err := getPublicAPIServerURL(r.Client)
			if err != nil {
				return attached, fmt.Errorf("unable to get public APIServer ULR: %v", err)
			}

			// Getting bootstrap-hub-kubeconfig
			currentBoostrapHubKubeconfig, err := managedClusterKubeClient.CoreV1().Secrets(OpenClusterManagementAgentNamespaceName).Get(ctx, BootstrapHubKubeconfigSecretName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					// We need to create it...
					var newBoostrapHubKubeconfigSecret *corev1.Secret = nil
					for _, s := range bootstrapSATokenSecrets {
						newBoostrapHubKubeconfigSecret, err = newBootstrapHubKubeconfig(ctx, apiServer, s)
						if err != nil {
							errors = append(errors, fmt.Errorf("unable to create new boostrap-hub-kubeconfig: %v", err))
							return attached, utilerrors.NewAggregate(errors)
						}
					}
					if _, err := managedClusterKubeClient.CoreV1().Secrets(OpenClusterManagementAgentNamespaceName).Create(ctx, newBoostrapHubKubeconfigSecret, metav1.CreateOptions{}); err != nil {
						return attached, fmt.Errorf("unable to create new boostrap-hub-kubeconfig: %v", err)
					}
					return attached, nil // new boostrap-hub-kubeconfig created
				}
				return attached, fmt.Errorf("cannot get boostrap-hub-kubeconfig from managedclsuter %s: %v", managedClusterName, err)
			}
			if currentBoostrapHubKubeconfig.DeletionTimestamp != nil {
				return attached, fmt.Errorf("%s is being deleted", BootstrapHubKubeconfigSecretName) // is begin deleted
			}
			// Otherwise the currentBoostrapHubKubeconfig exists and we need to open it
			// looking if the server is the current one
			server, err := getDefaultClusterServerFromKubeconfigSecret(currentBoostrapHubKubeconfig)
			if err != nil {
				return attached, fmt.Errorf("unable to find server from current boostrap-hub-kubecoinfg: %v", err)
			}
			if server != apiServer { // if the curent boostrap-hub-kubeconfig has different server name let's remove it
				if err = managedClusterKubeClient.CoreV1().Secrets(OpenClusterManagementAgentNamespaceName).Delete(ctx, BootstrapHubKubeconfigSecretName, *metav1.NewDeleteOptions(0)); err != nil {
					return attached, fmt.Errorf("unable to delete boostrap-hub-kubeconfig for %s: %v", managedClusterName, err)
				}
			}

			// check cluster role and cluster role bindings
			if err := createClusterRoleIfNeeded(ctx, r.Client, initManagedClusterBoostrapClusterRole(managedClusterName)); err != nil {
				return attached, err
			}
			if err := createClusterRoleBindingIfNeeded(ctx, r.Client, initManagedClusterBoostrapClusterRoleBinding(managedClusterName)); err != nil {
				return attached, err
			}
			if err := createClusterRoleIfNeeded(ctx, r.Client, initManagedClusterClusterRole(managedClusterName)); err != nil {
				return attached, err
			}
			if err := createClusterRoleBindingIfNeeded(ctx, r.Client, initManagedClusterClusterRoleBinding(managedClusterName)); err != nil {
				return attached, err
			}

			if approveManagedClusterCSR(ctx, r.Client, managedClusterName); err != nil {
				return attached, err
			}

		} // end of if IsNotFound(err)
		return attached, fmt.Errorf("registation error for managed cluster %s: %v", managedClusterName, err)
	} // end of err!=nil

	// check clusterroles and clusterrolebindings

	// In case managedCluster is already approved we stop here
	if managedCluster.Spec.HubAcceptsClient {
		attached = true
		return attached, nil
	}

	// Check CSR
	if approveManagedClusterCSR(ctx, r.Client, managedClusterName); err != nil {
		return attached, err
	}

	// Approve managed cluster
	if acceptManagedCluster(ctx, r.Client, managedClusterName); err != nil {
		return attached, err
	}

	attached = true
	return attached, nil
}
