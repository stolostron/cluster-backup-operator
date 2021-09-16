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
	"regexp"
	"sort"
	"strings"
	"time"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	"github.com/pkg/errors"

	certsv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

var (
	apiGVStr           = v1beta1.GroupVersion.String()
	PublicAPIServerURL = ""
)

const (
	restoreOwnerKey                         = ".metadata.controller"
	managedClusterImportInterval            = 20 * time.Second                // as soon restore is finished we start to poll for managedcluster registration
	BootstrapHubKubeconfigSecretName        = "bootstrap-hub-kubeconfig"      /* #nosec G101 */
	OpenClusterManagementAgentNamespaceName = "open-cluster-management-agent" // TODO: this can change. Get the klusterlet.spec
	OCMManagedClusterNamespaceLabelKey      = "cluster.open-cluster-management.io/managedCluster"
)

type GetKubeClientFromSecretFunc func(*corev1.Secret) (kubeclient.Interface, error)

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	KubeClient kubernetes.Interface
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list
//+kubebuilder:rbac:groups=velero.io,resources=restores,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups=operator.openshift.io,resources=configs,verbs=get;list;watch
//+kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests/approval,verbs=update
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
	if err := r.List(ctx, &veleroRestoreList, client.InNamespace(req.Namespace), client.MatchingFields{restoreOwnerKey: req.Name}); err != nil {
		restoreLogger.Error(
			err,
			"unable to list velero restores for restore %s/%s",
			req.Namespace,
			req.Name,
		)
		return ctrl.Result{}, err
	}

	switch {
	case len(veleroRestoreList.Items) == 0:
		if err := r.initVeleroRestores(ctx, restore); err != nil {
			restoreLogger.Error(
				err,
				"unable to initialize Velero restores for restore %s/%s",
				req.Namespace,
				req.Name,
			)
			return ctrl.Result{}, err
		}

	case len(veleroRestoreList.Items) > 0:
		for i := range veleroRestoreList.Items {
			veleroRestore := veleroRestoreList.Items[i].DeepCopy()
			switch {
			case isVeleroRestoreFinished(veleroRestore):
				r.Recorder.Event(
					restore,
					v1.EventTypeNormal,
					"Velero Restore finished",
					fmt.Sprintf(
						"%s finished",
						veleroRestore.Name,
					),
				) // TODO add check on conditions to avoid multiple events
				// the rest of this case is related only to managedclusters restores
				if !strings.Contains(
					veleroRestore.Name,
					veleroScheduleNames[ManagedClusters],
				) {
					apimeta.SetStatusCondition(&restore.Status.Conditions,
						metav1.Condition{
							Type:    v1beta1.RestoreComplete,
							Status:  metav1.ConditionTrue,
							Reason:  v1beta1.RestoreReasonFinished,
							Message: fmt.Sprintf("Restore Complete %s", veleroRestore.Name),
						})
					break
				}

				allAttached, err := r.attachManagedClusters(ctx, restore)
				if err != nil {
					restoreLogger.Error(err, "unable to attach managed clusters")
					return ctrl.Result{}, err
				}
				switch {
				case allAttached:
					restoreLogger.Info(
						"All managed cluster attached",
					) // TODO generate event when all managedcluster attached
					apimeta.SetStatusCondition(&restore.Status.Conditions,
						metav1.Condition{
							Type:    v1beta1.RestoreComplete,
							Status:  metav1.ConditionTrue,
							Reason:  v1beta1.RestoreReasonFinished,
							Message: fmt.Sprintf("Restore Complete %s", veleroRestore.Name),
						})
				case !allAttached:
					restoreLogger.V(4).Info("Not all managed clusters attached... Rescheduling")
					apimeta.SetStatusCondition(&restore.Status.Conditions,
						metav1.Condition{
							Type:   v1beta1.RestoreAttaching,
							Status: metav1.ConditionTrue,
							Reason: v1beta1.RestoreReasonRunning,
							Message: fmt.Sprintf(
								"Restore %s is attaching clusters",
								veleroRestore.Name,
							),
						})
					// to wait for managed cluster re-attaching phase
					return ctrl.Result{
							RequeueAfter: managedClusterImportInterval,
						}, r.Client.Status().
							Update(ctx, restore)
				}
			case isVeleroRestoreRunning(veleroRestore):
				apimeta.SetStatusCondition(&restore.Status.Conditions,
					metav1.Condition{
						Type:    v1beta1.RestoreStarted,
						Status:  metav1.ConditionTrue,
						Reason:  v1beta1.RestoreReasonRunning,
						Message: fmt.Sprintf("Velero Restore %s is running", veleroRestore.Name),
					})
			default:
				apimeta.SetStatusCondition(&restore.Status.Conditions,
					metav1.Condition{
						Type:    v1beta1.RestoreStarted,
						Status:  metav1.ConditionFalse,
						Reason:  v1beta1.RestoreReasonRunning,
						Message: fmt.Sprintf("Velero Restore %s is running", veleroRestore.Name),
					})
			}
		}
	default: // (should never happen)
	}

	err := r.Client.Status().Update(ctx, restore)
	return ctrl.Result{}, errors.Wrap(
		err,
		fmt.Sprintf("could not update status for restore %s/%s", restore.Namespace, restore.Name),
	)
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
func (r *RestoreReconciler) getVeleroBackupName(
	ctx context.Context,
	restore *v1beta1.Restore,
	resourceType ResourceType,
) (string, error) {
	var veleroBackupName *string
	switch resourceType {
	case ManagedClusters:
		veleroBackupName = restore.Spec.VeleroManagedClustersBackupName
	case Credentials:
		veleroBackupName = restore.Spec.VeleroCredentialsBackupName
	case Resources:
		veleroBackupName = restore.Spec.VeleroResourcesBackupName
	}
	// TODO: check whether name is valid
	if veleroBackupName != nil && len(*veleroBackupName) > 0 {
		veleroBackup := veleroapi.Backup{}
		err := r.Get(ctx,
			types.NamespacedName{Name: *veleroBackupName,
				Namespace: restore.Namespace},
			&veleroBackup)
		if err == nil {
			return *veleroBackupName, nil
		}
		return "", fmt.Errorf("cannot find %s Velero Backup: %v",
			*veleroBackupName, err)
	}
	// backup name not available, find a proper backup
	veleroBackups := &veleroapi.BackupList{}
	if err := r.Client.List(ctx, veleroBackups, client.InNamespace(restore.Namespace)); err != nil {
		return "", fmt.Errorf("unable to list velero backups: %v", err)
	}
	if len(veleroBackups.Items) == 0 {
		return "", fmt.Errorf("no backups found")
	}
	// filter available backups to get only the ones related to this resource type
	relatedBackups := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
		return strings.Contains(bkp.Name, veleroScheduleNames[resourceType])
	})
	sort.Sort(mostRecentWithLessErrors(relatedBackups))
	return relatedBackups[0].Name, nil
}

// create velero.io.Restore resource for each resource type
func (r *RestoreReconciler) initVeleroRestores(
	ctx context.Context,
	restore *v1beta1.Restore,
) error {
	restoreLogger := log.FromContext(ctx)

	// loop through resourceTypes to create a Velero restore per type
	for key := range veleroScheduleNames {
		veleroRestore := &veleroapi.Restore{}
		veleroBackupName, err := r.getVeleroBackupName(ctx, restore, key)
		if err != nil {
			return err
		}
		// TODO check length of produced name
		veleroRestore.Name = restore.Name + "-" + veleroBackupName

		veleroRestore.Namespace = restore.Namespace
		veleroRestore.Spec.BackupName = veleroBackupName

		if err := ctrl.SetControllerReference(restore, veleroRestore, r.Scheme); err != nil {
			return err
		}

		if err = r.Create(ctx, veleroRestore, &client.CreateOptions{}); err != nil {
			restoreLogger.Error(
				err,
				"unable to create Velero restore for restore %s/%s",
				veleroRestore.Namespace,
				veleroRestore.Name,
			)
			return err
		}

		r.Recorder.Event(restore, v1.EventTypeNormal, "Velero restore created:", veleroRestore.Name)

		switch key {
		case ManagedClusters:
			restore.Status.VeleroManagedClustersRestoreName = veleroRestore.Name
		case Credentials:
			restore.Status.VeleroCredentialsRestoreName = veleroRestore.Name
		case Resources:
			restore.Status.VeleroResourcesRestoreName = veleroRestore.Name
		}

		apimeta.SetStatusCondition(&restore.Status.Conditions,
			metav1.Condition{
				Type:    v1beta1.RestoreStarted,
				Status:  metav1.ConditionTrue,
				Reason:  v1beta1.RestoreReasonStarted,
				Message: fmt.Sprintf("Velero restore %s started", veleroRestore.Name),
			})
	}

	return nil
}

// attachManagedClusters is the entry points for all the managed clusters to be re-attached
// currently it loops across all the managed cluster namespaces
func (r *RestoreReconciler) attachManagedClusters(
	ctx context.Context,
	restore *v1beta1.Restore,
) (bool, error) {
	allAttached := false
	namespaceList := v1.NamespaceList{}
	labelSelector, _ := labels.Parse(OCMManagedClusterNamespaceLabelKey)
	if err := r.Client.List(ctx, &namespaceList, &client.ListOptions{LabelSelector: labelSelector}); err != nil {
		return allAttached, fmt.Errorf("unable to list managed cluster namespaces: %v", err)
	}
	var errors []error
	allAttached = true
	for i := range namespaceList.Items {
		if namespaceList.Items[i].Name == "local-cluster" {
			continue
		}
		attached, err := r.attachManagedCluster(ctx, restore, namespaceList.Items[i].Name)
		if err != nil {
			attached = false
			errors = append(errors, err)
		}
		allAttached = allAttached && attached
	}
	return allAttached, utilerrors.NewAggregate(errors)
}

// attachManagedCluster handles the registration of the managed cluster
func (r *RestoreReconciler) attachManagedCluster(
	ctx context.Context,
	restore *v1beta1.Restore,
	managedClusterName string,
) (bool, error) {
	restoreLogger := log.FromContext(ctx).WithName(managedClusterName)
	restoreLogger.V(4).Info("Attaching managedcluster")
	managedCluster := clusterv1.ManagedCluster{}
	attached := false
	err := r.Client.Get(ctx, types.NamespacedName{Name: managedClusterName}, &managedCluster)
	if err != nil {
		// Since we don't backup managedclusters and we don't find it it means the attaching process is on goning or need to be started
		if apierrors.IsNotFound(err) {
			secrets := v1.SecretList{} // fetch secrets from managedcluster namespace
			if err = r.Client.List(ctx, &secrets, client.InNamespace(managedClusterName)); err != nil {
				return attached, fmt.Errorf(
					"cannot list secrets in namespace %s: %v",
					managedClusterName,
					err,
				)
			}
			mcHandler, err := NewManagedClusterHandler(ctx, managedClusterName, &secrets)
			if err != nil {
				return attached, fmt.Errorf(
					"unable to handle managedcluster %s: %v",
					managedClusterName,
					err,
				)
			}
			if mcHandler == nil {
				return attached, fmt.Errorf(
					"unable to handle managedcluster %s, admin secret not found",
					managedClusterName,
				)
			}
			currentBoostrapHubKubeconfig, err := mcHandler.Client.CoreV1().
				Secrets(OpenClusterManagementAgentNamespaceName).
				Get(ctx,
					BootstrapHubKubeconfigSecretName, metav1.GetOptions{})
			// Getting bootstrap-hub-kubeconfig
			if err != nil {
				if apierrors.IsNotFound(err) { // cannot find, create it
					restoreLogger.V(4).
						Info("Current Boostrap HUB Kubeconfiig not found", "cluster", managedClusterName)
					newBoostrapHubKubeconfigSecret, err := mcHandler.GetNewBoostrapHubKubeconfigSecret(
						ctx,
					)
					if err != nil {
						return attached, fmt.Errorf(
							"unable to instantiate new boostrap-hub-kubeconfig: %v",
							err,
						)
					}
					restoreLogger.V(4).
						Info("To create boostrap-hub-kubeconfig ", "cluster", managedClusterName)
					if _, err := mcHandler.Client.CoreV1().Secrets(OpenClusterManagementAgentNamespaceName).Create(ctx,
						newBoostrapHubKubeconfigSecret, metav1.CreateOptions{}); err != nil {
						return attached, fmt.Errorf(
							"unable to create new boostrap-hub-kubeconfig: %v",
							err,
						)
					}
					restoreLogger.V(4).
						Info("New bootstrap-hub-kubeconfig created", "cluster", managedClusterName)
					return attached, nil
				}
				return attached, fmt.Errorf(
					"cannot get boostrap-hub-kubeconfig from managedclsuter %s: %v",
					managedClusterName,
					err,
				)
			}

			if currentBoostrapHubKubeconfig.DeletionTimestamp != nil {
				return attached, fmt.Errorf(
					"%s is being deleted",
					BootstrapHubKubeconfigSecretName,
				) // is begin deleted
			}
			// The currentBoostrapHubKubeconfig exists. We need to open it
			// looking if the server is the current one
			server, err := getDefaultClusterServerFromKubeconfigSecret(currentBoostrapHubKubeconfig)
			if err != nil {
				return attached, fmt.Errorf(
					"unable to find server from current boostrap-hub-kubecoinfg: %v",
					err,
				)
			}
			apiServer, err := getPublicAPIServerURL(r.Client)
			if err != nil {
				return attached, fmt.Errorf("unable to get public APIServer ULR: %v", err)
			}
			if server != apiServer { // if the curent boostrap-hub-kubeconfig has different server name let's remove it
				restoreLogger.V(4).Info("To Delete the current  boostrap-hub-kubeconfig",
					"server", server,
					"apiServer", apiServer,
					"cluster", managedClusterName)
				if err = mcHandler.Client.CoreV1().Secrets(OpenClusterManagementAgentNamespaceName).Delete(ctx,
					BootstrapHubKubeconfigSecretName, *metav1.NewDeleteOptions(0)); err != nil {
					return attached, fmt.Errorf(
						"unable to delete boostrap-hub-kubeconfig for %s: %v",
						managedClusterName,
						err,
					)
				}
			}

			// check cluster role and cluster role bindings
			if err := r.createClusterRoleIfNeeded(ctx, restore, initManagedClusterAdminClusterRole(managedClusterName)); err != nil {
				return attached, err
			}
			if err := r.createClusterRoleIfNeeded(ctx, restore, initManagedClusterViewClusterRole(managedClusterName)); err != nil {
				return attached, err
			}
			if err := r.createClusterRoleIfNeeded(ctx, restore,
				initManagedClusterBootstrapClusterRole(managedClusterName)); err != nil {
				return attached, err
			}
			if err := r.createClusterRoleBindingIfNeeded(ctx, restore,
				initManagedClusterBootstrapClusterRoleBinding(managedClusterName)); err != nil {
				return attached, err
			}
			if err := r.createClusterRoleIfNeeded(ctx, restore, initManagedClusterClusterRole(managedClusterName)); err != nil {
				return attached, err
			}
			if err := r.createClusterRoleBindingIfNeeded(ctx, restore, initManagedClusterClusterRoleBinding(managedClusterName)); err != nil {
				return attached, err
			}
			if err := r.approveCSRIfNeeded(ctx, restore, managedClusterName); err != nil {
				return attached, err
			}
			return attached, nil // no error but not attached yet
		} // end of if IsNotFound(err)
		return attached, fmt.Errorf(
			"registation error for managed cluster %s: %v",
			managedClusterName,
			err,
		)
	} // end of err!=nil

	// In case managedCluster is already approved we stop here.
	if managedCluster.Spec.HubAcceptsClient {
		attached = true
		return attached, nil
	}
	if err := r.approveCSRIfNeeded(ctx, restore, managedClusterName); err != nil {
		return attached, err
	}
	// Approve managed cluster
	if err := r.acceptManagedCluster(ctx, restore, managedClusterName); err != nil {
		return attached, err
	}
	attached = true
	return attached, nil
}

// approveManagedClusterCSRIfNeeded approves the CSR for the managed cluster
func (r *RestoreReconciler) approveCSRIfNeeded(
	ctx context.Context,
	restore *v1beta1.Restore,
	managedClusterName string,
) error {
	restoreLogger := log.FromContext(ctx).WithName(restore.Name)
	csrList := certsv1.CertificateSigningRequestList{}
	if err := r.List(ctx, &csrList, &client.ListOptions{}); err != nil {
		return fmt.Errorf("unable to list CSR: %v", err)
	}
	var (
		clusterNameCSRRegex *regexp.Regexp = nil
		err                 error          = nil
	)
	for i := range csrList.Items {
		if clusterNameCSRRegex == nil {
			clusterNameCSRRegex, err = regexp.Compile("^" + managedClusterName + "-")
			if err != nil {
				return fmt.Errorf(
					"unable to initialize REGEX to filter CSR for cluster %s: %v",
					managedClusterName,
					err,
				)
			}
		}
		if clusterNameCSRRegex.Match([]byte(csrList.Items[i].Name)) &&
			!isCertificateRequestApproved(&csrList.Items[i]) {
			restoreLogger.V(4).Info("About to approve CSR", "Name", csrList.Items[i].Name)
			csrList.Items[i].Status.Conditions = append(csrList.Items[i].Status.Conditions,
				certsv1.CertificateSigningRequestCondition{
					Type:           certsv1.CertificateApproved,
					Status:         corev1.ConditionTrue,
					Reason:         v1beta1.CSRReasonApprovedReason,
					Message:        "cluster-backup-operator approved during restore",
					LastUpdateTime: metav1.Now(),
				})
			certificateSigningRequest := r.KubeClient.CertificatesV1().CertificateSigningRequests()
			if _, err := certificateSigningRequest.UpdateApproval(ctx, csrList.Items[i].Name,
				&csrList.Items[i], metav1.UpdateOptions{}); err != nil {
				restoreLogger.Error(err, "unable to approve", "CSR", csrList.Items[i].Name)
				return fmt.Errorf("unable to approve CSR %s for: %v", csrList.Items[i].Name, err)
			}
			r.Recorder.Event(
				restore,
				v1.EventTypeNormal,
				"CSR approved",
				fmt.Sprintf("approved %s", csrList.Items[i].Name),
			)

			restoreLogger.V(4).Info("Approved", "CSR", csrList.Items[i].Name)
			return nil
		}
	}
	return nil // All CSR already approved or no CSR to approve
}

// acceptManagedCluster accepts the managed clsuter
func (r *RestoreReconciler) acceptManagedCluster(
	ctx context.Context,
	restore *v1beta1.Restore,
	managedClusterName string,
) error {
	managedCluster := clusterv1.ManagedCluster{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "",
		Name: managedClusterName}, &managedCluster); err != nil {
		return fmt.Errorf(
			"unable to retrieve managed cluster during update %s: %v",
			managedClusterName,
			err,
		)
	}
	managedCluster.Spec.HubAcceptsClient = true
	if err := r.Update(ctx, &managedCluster, &client.UpdateOptions{}); err != nil {
		return fmt.Errorf("unable to update managed cluster , %s: %v", managedClusterName, err)
	}
	// generate an event...
	return nil
}

//createClusterRoleIfNeeded creates a ClusterRole
func (r *RestoreReconciler) createClusterRoleIfNeeded(
	ctx context.Context,
	restore *v1beta1.Restore,
	clusterRole *rbacv1.ClusterRole,
) error {
	restoreLogger := log.FromContext(ctx).WithName(restore.Name)
	t := &rbacv1.ClusterRole{}
	if err := r.Get(ctx, types.NamespacedName{Name: clusterRole.Name}, t); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, clusterRole, &client.CreateOptions{}); err != nil {
				return fmt.Errorf("couldn't create the clusterrole: %v", err)
			}
			restoreLogger.V(4).Info("ClusterRole created", "name", clusterRole.Name)
			return nil
		}
		return fmt.Errorf("couldn't verify if clusterrole exists: %v", err)
	}
	return nil
}

// createClusterRoleBindingIfNeeded creates a ClusterRoleBinding
func (r *RestoreReconciler) createClusterRoleBindingIfNeeded(
	ctx context.Context,
	restore *v1beta1.Restore,
	clusterRoleBinding *rbacv1.ClusterRoleBinding,
) error {
	restoreLogger := log.FromContext(ctx).WithName(restore.Name)
	t := &rbacv1.ClusterRoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: clusterRoleBinding.Name}, t); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, clusterRoleBinding, &client.CreateOptions{}); err != nil {
				return fmt.Errorf("couldn't create the clusterrole binding: %v", err)
			}
			restoreLogger.V(4).Info("ClusterRoleBinding created", "name", clusterRoleBinding.Name)
			return nil
		}
		return fmt.Errorf("couldn't verify if clusterrolebinding exists: %v", err)
	}
	return nil
}
