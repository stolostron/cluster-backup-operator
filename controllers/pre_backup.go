/*
Copyright 2022.

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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
)

const (
	msa_addon        = "managed-serviceaccount"
	msa_service_name = "auto-import-account"
	msa_label        = "authentication.open-cluster-management.io/is-managed-serviceaccount"
	backup_label     = "msa"
	addon_work_label = "open-cluster-management.io/addon-name-work"
	addon_label      = "open-cluster-management.io/addon-name-work"
	role_name        = "klusterlet"
	msa_api          = "authentication.open-cluster-management.io/v1alpha1"
	msa_kind         = "ManagedServiceAccount"

	manifest_work_name = "addon-" + msa_addon + "-import"
	defaultTTL         = 720
	manifestwork       = `{
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "ClusterRoleBinding",
        "metadata": {
            "name": "managedserviceaccount-import"
        },
        "roleRef": {
            "apiGroup": "rbac.authorization.k8s.io",
            "kind": "ClusterRole",
            "name": "%s"
        },
        "subjects": [
            {
                "kind": "ServiceAccount",
                "name": "%s",
                "namespace": "open-cluster-management-agent-addon"
            }
        ]
    }`
)

// the prepareForBackup task is executed before each run of a backup schedule
// any settings that need to be applied to the resources before the backpu starts, are being called here

// prepare resources before backing up
func (r *BackupScheduleReconciler) prepareForBackup(
	ctx context.Context,
	backupSchedule *v1beta1.BackupSchedule,
) {
	logger := log.FromContext(ctx)

	// check if user has checked the UseManagedServiceAccount option
	isMSAEnabled := backupSchedule.Spec.UseManagedServiceAccount

	// check if ManagedServiceAccount CRD exists,
	// meaning the managedservice account option is enabled on MCH
	msaKind := schema.GroupKind{
		Group: "authentication.open-cluster-management.io",
		Kind:  "ManagedServiceAccount",
	}
	msaMapping, err := r.RESTMapper.RESTMapping(msaKind, "")
	var dr dynamic.NamespaceableResourceInterface
	if err == nil {
		logger.Info("ManagedServiceAccounts is enabled, generate MSA accounts if needed")
		dr = r.DynamicClient.Resource(msaMapping.Resource)
		if dr != nil {
			if isMSAEnabled {
				prepareImportedClusters(ctx, r.Client, dr, msaMapping, backupSchedule)
			} else {
				cleanupMSAForImportedClusters(ctx, r.Client, dr, msaMapping)
			}
		}
	}

	updateHiveResources(ctx, r.Client)
	updateAISecrets(ctx, r.Client)
	updateMetalSecrets(ctx, r.Client)

	if isMSAEnabled && err == nil && dr != nil {
		// managedserviceaccount is enabled, add backup labels
		updateMSAResources(ctx, r.Client, dr)
	}
}

// if UseManagedServiceAccount is not set, clean up all MSA accounts
// created by the backup controller
func cleanupMSAForImportedClusters(
	ctx context.Context,
	c client.Client,
	dr dynamic.NamespaceableResourceInterface,
	msaMapping *meta.RESTMapping,
) {
	logger := log.FromContext(ctx)

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}
	listOptions := v1.ListOptions{LabelSelector: fmt.Sprintf("%s in (%s)", msa_label, msa_service_name)}
	if dynamiclist, err := dr.List(ctx, listOptions); err == nil {
		for i := range dynamiclist.Items {
			deleteDynamicResource(
				ctx,
				msaMapping,
				dr,
				dynamiclist.Items[i],
				delOptions,
				[]string{},
			)
		}
	}

	// delete managedclusters addons
	addons := &addonv1alpha1.ManagedClusterAddOnList{}
	label := labels.SelectorFromSet(
		map[string]string{msa_label: msa_service_name})
	if err := c.List(ctx, addons, &client.ListOptions{LabelSelector: label}); err == nil {

		for i := range addons.Items {
			if err := c.Delete(ctx, &addons.Items[i]); err != nil {
				logger.Error(
					err,
					fmt.Sprintf("failed to delete the addon %s", addons.Items[i].Name),
				)
			} else {
				logger.Info(
					fmt.Sprintf("deleted addon %s", addons.Items[i].Name),
				)
			}
		}
	}

	// delete manifest work
	manifestWorkList := &workv1.ManifestWorkList{}
	label = labels.SelectorFromSet(
		map[string]string{addon_work_label: msa_addon})
	if err := c.List(ctx, manifestWorkList, &client.ListOptions{LabelSelector: label}); err == nil {

		for i := range manifestWorkList.Items {
			if err := c.Delete(ctx, &manifestWorkList.Items[i]); err != nil {
				logger.Error(
					err,
					fmt.Sprintf("failed to delete manifestwork %s", manifestWorkList.Items[i].Name),
				)
			} else {
				logger.Info(
					fmt.Sprintf("deleted manifestwork %s", manifestWorkList.Items[i].Name),
				)
			}
		}
	}
}

// here we go over all managed clusters and find the ones imported with the hub
// create a managedserviceaccount token to be used to communicate with the managed clusters
// when moved to the passive hub; the token is used to build the auto-import-secret
// which will trigger the auto import of the cluster on the new hub
func prepareImportedClusters(ctx context.Context,
	c client.Client,
	dr dynamic.NamespaceableResourceInterface,
	msaMapping *meta.RESTMapping,
	backupSchedule *v1beta1.BackupSchedule,
) {
	logger := log.FromContext(ctx)

	secretsGeneratedNow := false

	tokenValidity := fmt.Sprintf("%vh0m0s", defaultTTL*2)
	if backupSchedule.Spec.ManagedServiceAccountTTL.Duration != 0 {
		// set user defined token TTL
		tokenValidity = fmt.Sprintf("%v", backupSchedule.Spec.ManagedServiceAccountTTL.Duration)
	}
	if backupSchedule.Spec.VeleroTTL.Duration != 0 {
		// use backup schedule TTL
		tokenValidity = fmt.Sprintf("%v", backupSchedule.Spec.VeleroTTL.Duration*2)
	}

	// get all managed clusters
	managedClusters := &clusterv1.ManagedClusterList{}
	if err := c.List(ctx, managedClusters, &client.ListOptions{}); err == nil {
		for i := range managedClusters.Items {
			managedCluster := managedClusters.Items[i]
			if managedCluster.Name == "local-cluster" ||
				isHiveCreatedCluster(ctx, c, managedCluster.Name) {
				continue
			}
			// create managedservice addon if not available
			addons := &addonv1alpha1.ManagedClusterAddOnList{}
			if err := c.List(ctx, addons, &client.ListOptions{Namespace: managedCluster.Name}); err != nil {
				continue
			}
			alreadyCreated := false
			installNamespace := ""
			for addon := range addons.Items {
				installNamespace = addons.Items[addon].Spec.InstallNamespace
				if addons.Items[addon].Name == msa_addon {
					alreadyCreated = true
					break
				}
			}
			if !alreadyCreated {
				msaAddon := &addonv1alpha1.ManagedClusterAddOn{}
				msaAddon.Name = msa_addon
				msaAddon.Namespace = managedCluster.Name
				msaAddon.Spec.InstallNamespace = installNamespace
				labels := map[string]string{
					msa_label: msa_service_name}
				msaAddon.SetLabels(labels)

				err := c.Create(ctx, msaAddon, &client.CreateOptions{})
				if err != nil {
					logger.Error(
						err,
						"Error in creating ClusterManagementAddOn",
						"name", msaAddon.Name,
						"namespace", msaAddon.Namespace,
					)
				}
			}

			//check if MSA exists
			if msaUnstructuredObj, err := dr.Namespace(managedCluster.Name).Get(ctx, msa_service_name, v1.GetOptions{}); err == nil {
				updated, err := updateMSAToken(ctx, dr, msaUnstructuredObj, managedCluster.Name, tokenValidity)
				if err != nil {
					logger.Info(err.Error(), "Failed to update MSA")
				}
				if updated {
					logger.Info(fmt.Sprintf("updated token validity for cluster %s", managedCluster.Name))
				}
			} else {
				// create ManagedServiceAccount in the managed cluster namespace
				secretsGeneratedNow = true
				msaRC := &unstructured.Unstructured{}
				msaRC.SetUnstructuredContent(map[string]interface{}{
					"apiVersion": msa_api,
					"kind":       msa_kind,
					"metadata": map[string]interface{}{
						"name":      msa_service_name,
						"namespace": managedCluster.Name,
						"labels": map[string]interface{}{
							backupCredsClusterLabel: backup_label,
							msa_label:               msa_service_name,
						},
					},
					"spec": map[string]interface{}{
						"rotation": map[string]interface{}{
							"validity": tokenValidity,
							"enabled":  true,
						},
					},
				})
				// attempt to create managedservice account for auto-import
				if _, err := dr.Namespace(managedCluster.Name).Create(ctx, msaRC, v1.CreateOptions{}); err != nil {
					logger.Info(fmt.Sprintf("Failed to create ManagedServiceAccount for cluster =%s, error : %s",
						managedCluster.Name, err.Error()))
				}
				// create ManifestWork to push the role binding
				createManifestWork(ctx, c, managedCluster.Name)
			}
		}
	}

	if secretsGeneratedNow {
		// sleep to allow secrets to be propagated
		time.Sleep(2 * time.Second)
	}

}

func updateMSAToken(
	ctx context.Context,
	dr dynamic.NamespaceableResourceInterface,
	msaUnstructuredObj *unstructured.Unstructured,
	namespaceName string,
	tokenValidity string,
) (bool, error) {

	specInfo := msaUnstructuredObj.Object["spec"]
	if specInfo == nil {
		return false, nil
	}
	patch := `[ { "op": "replace", "path": "/spec/rotation/validity", "value" : "` + tokenValidity + `" } ]`
	iter := reflect.ValueOf(specInfo).MapRange()
	for iter.Next() {
		key := iter.Key().Interface()
		if key == "rotation" {
			rotationValues := iter.Value().Interface().(map[string]interface{})
			if rotationValues != nil {
				iterRotation := reflect.ValueOf(rotationValues).MapRange()
				for iterRotation.Next() {
					if iterRotation.Key().String() == "validity" &&
						iterRotation.Value().Interface().(string) != tokenValidity {
						//update MSA validity with the latest token value
						if _, err := dr.Namespace(namespaceName).Patch(ctx, msa_service_name,
							types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
							return false, err
						}
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

// create manifest work to push the import user role binding to the managed cluster
func createManifestWork(
	ctx context.Context,
	c client.Client,
	namespace string,
) {
	logger := log.FromContext(ctx)

	manifestWorkList := &workv1.ManifestWorkList{}
	if msaLabel, err := labels.NewRequirement(addon_work_label,
		selection.In, []string{msa_addon}); err == nil {

		selector := labels.NewSelector()
		selector = selector.Add(*msaLabel)
		if err := c.List(ctx, manifestWorkList, &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: selector},
		); err == nil {

			if len(manifestWorkList.Items) == 0 {
				// create the manifest work now
				manifestWork := &workv1.ManifestWork{}
				manifestWork.Name = manifest_work_name
				manifestWork.Namespace = namespace
				manifestWork.Labels = map[string]string{addon_work_label: msa_addon}

				manifest := &workv1.Manifest{}
				manifest.Raw = []byte(fmt.Sprintf(manifestwork, role_name, msa_service_name))

				manifestWork.Spec.Workload.Manifests = []workv1.Manifest{
					*manifest,
				}

				err := c.Create(ctx, manifestWork, &client.CreateOptions{})
				if err != nil {
					logger.Error(
						err,
						"Error in creating ManifestWork",
						"name", manifest_work_name,
						"namespace", namespace,
					)
				}
			}
		}
	}
}

// get all secrets labeled by msa
func getMSASecrets(
	ctx context.Context,
	c client.Client,
	namespace string,
) []corev1.Secret {
	// add backup label for msa account secrets
	msaSecrets := &corev1.SecretList{}
	if msaLabel, err := labels.NewRequirement(msa_label,
		selection.In, []string{"true"}); err == nil {

		selector := labels.NewSelector()
		selector = selector.Add(*msaLabel)

		if namespace == "" {
			// get secrets from all namespaces
			if err := c.List(ctx, msaSecrets, &client.ListOptions{
				LabelSelector: selector,
			}); err == nil {
				return msaSecrets.Items
			}
		} else {
			// get secrets from specified namespace
			if err := c.List(ctx, msaSecrets, &client.ListOptions{
				Namespace:     namespace,
				LabelSelector: selector,
			}); err == nil {
				return msaSecrets.Items
			}
		}

	}
	return []corev1.Secret{}
}

// prepare managed service account secrets for backup
func updateMSAResources(
	ctx context.Context,
	c client.Client,
	dr dynamic.NamespaceableResourceInterface,
) {
	logger := log.FromContext(ctx)
	// add backup label for msa account secrets
	// get only unprocessed secrets, so the ones with no backup label
	secrets := getMSASecrets(ctx, c, "")
	for s := range secrets {
		secret := secrets[s]

		secretAnnotations := secret.GetAnnotations()
		if secretAnnotations == nil {
			secretAnnotations = map[string]string{}
		}

		secretTimestampUpdated := false
		// add token expiration time from parent MSA resource
		if unstructuredObj, err := dr.Namespace(secret.Namespace).Get(ctx, secret.Name, v1.GetOptions{}); err == nil {
			// look for the expiration timestamp under status
			statusInfo := unstructuredObj.Object["status"]
			if statusInfo == nil {
				continue
			}
			iter := reflect.ValueOf(statusInfo).MapRange()
			for iter.Next() {
				key := iter.Key().Interface()
				if key != "expirationTimestamp" {
					continue
				}
				// set expirationTimestamp on the secret
				secretTimestampUpdated = secretAnnotations["expirationTimestamp"] != iter.Value().Interface().(string)
				secretAnnotations["expirationTimestamp"] = iter.Value().Interface().(string)
				secret.SetAnnotations(secretAnnotations)
				break
			}
		}

		backupLabelSet := updateSecret(ctx, c, secret,
			backupCredsClusterLabel,
			backup_label, false)

		if secretTimestampUpdated || backupLabelSet {
			if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err != nil {
				logger.Error(err, "failed to update secret")
			}
		}
	}
}

// prepare hive cluster claim and cluster pool
func updateHiveResources(ctx context.Context,
	c client.Client,
) {
	logger := log.FromContext(ctx)
	// update secrets for clusterDeployments created by cluster claims
	clusterDeployments := &hivev1.ClusterDeploymentList{}
	if err := c.List(ctx, clusterDeployments, &client.ListOptions{}); err == nil {
		for i := range clusterDeployments.Items {
			clusterDeployment := clusterDeployments.Items[i]
			if clusterDeployment.Spec.ClusterPoolRef != nil {
				secrets := &corev1.SecretList{}
				if err := c.List(ctx, secrets, &client.ListOptions{
					Namespace: clusterDeployments.Items[i].Namespace,
				}); err == nil {
					// add backup labels if not set yet
					updateSecretsLabels(ctx, c, *secrets, clusterDeployments.Items[i].Name,
						backupCredsClusterLabel,
						"clusterpool")
				}

				// add a label annnotation to the resource
				// to disable the creation webhook validation
				// which doesn't allow restoring the ClusterDeployment
				labels := clusterDeployment.GetLabels()
				if labels == nil {
					labels = make(map[string]string)
				}
				labels["hive.openshift.io/disable-creation-webhook-for-dr"] = "true"
				clusterDeployment.SetLabels(labels)
				msg := "update clusterDeployment " + clusterDeployment.Name
				logger.Info(msg)
				if err := c.Update(ctx, &clusterDeployment, &client.UpdateOptions{}); err != nil {
					logger.Error(err, "failed to update clusterDeployment")
				}
			}
		}
	}

	// update secrets for cluster pools
	clusterPools := &hivev1.ClusterPoolList{}
	if err := c.List(ctx, clusterPools, &client.ListOptions{}); err == nil {
		for i := range clusterPools.Items {
			secrets := &corev1.SecretList{}
			if err := c.List(ctx, secrets, &client.ListOptions{
				Namespace: clusterPools.Items[i].Namespace,
			}); err == nil {
				updateSecretsLabels(ctx, c, *secrets, clusterPools.Items[i].Name,
					backupCredsClusterLabel,
					"clusterpool")
			}
		}
	}
}

// prepare AutomatedInstaller resources
func updateAISecrets(ctx context.Context,
	c client.Client,
) {
	// update infraSecrets
	aiSecrets := &corev1.SecretList{}
	if agentInstallLabel, err := labels.NewRequirement("agent-install.openshift.io/watch",
		selection.In, []string{"true"}); err == nil {

		// Init and add to selector.
		selector := labels.NewSelector()
		selector = selector.Add(*agentInstallLabel)
		if err := c.List(ctx, aiSecrets, &client.ListOptions{
			LabelSelector: selector,
		}); err == nil {
			for s := range aiSecrets.Items {
				updateSecret(ctx, c, aiSecrets.Items[s],
					backupCredsClusterLabel,
					"agent-install", true)
			}
		}
	}
}

// prepare metal3 resources
func updateMetalSecrets(ctx context.Context,
	c client.Client,
) {
	// update metal
	metalSecrets := &corev1.SecretList{}
	if metalInstallLabel, err := labels.NewRequirement("environment.metal3.io",
		selection.In, []string{"baremetal"}); err == nil {

		// Init and add to selector.
		selector := labels.NewSelector()
		selector = selector.Add(*metalInstallLabel)
		if err := c.List(ctx, metalSecrets, &client.ListOptions{
			LabelSelector: selector,
		}); err == nil {
			for s := range metalSecrets.Items {
				if metalSecrets.Items[s].Namespace == "openshift-machine-api" {
					// skip secrets from openshift-machine-api ns, these hosts are not backed up
					continue
				}
				updateSecret(ctx, c, metalSecrets.Items[s],
					backupCredsClusterLabel,
					"baremetal", true)
			}
		}
	}
}

// set backup label for hive secrets not having the label set
func updateSecretsLabels(ctx context.Context,
	c client.Client,
	secrets corev1.SecretList,
	prefix string,
	labelName string,
	labelValue string,
) {
	for s := range secrets.Items {
		secret := secrets.Items[s]
		if strings.HasPrefix(secret.Name, prefix) &&
			!strings.Contains(secret.Name, "-bootstrap-") {
			updateSecret(ctx, c, secret, labelName, labelValue, true)
		}
	}

}

// set backup label for hive secrets not having the label set
func updateSecret(ctx context.Context,
	c client.Client,
	secret corev1.Secret,
	labelName string,
	labelValue string,
	update bool,
) bool {
	logger := log.FromContext(ctx)
	labels := secret.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels[backupCredsHiveLabel] == "" &&
		labels[backupCredsUserLabel] == "" &&
		labels[backupCredsClusterLabel] == "" {
		// and set backup labels for secrets
		labels[labelName] = labelValue
		secret.SetLabels(labels)
		msg := "update secret " + secret.Name
		logger.Info(msg)
		if !update {
			// do not call update now
			// secret needs refresh
			return true
		}
		if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err != nil {
			logger.Error(err, "failed to update secret")
		}
		// secret needs refresh
		return true
	}
	// secret not refreshed
	return false

}
