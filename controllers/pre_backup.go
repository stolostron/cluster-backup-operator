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
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
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
	// manifest work suffix used and created in 2.8.2
	mwork_custom_282 = "-custom"
	// manifest work suffix used and created 2.8.3 and onward
	mwork_custom_283      = "-custom-2"
	hive_label            = "hive.openshift.io/disable-creation-webhook-for-dr"
	hive_label_path       = "/metadata/labels/hive.openshift.io~1disable-creation-webhook-for-dr"
	msa_addon             = "managed-serviceaccount"
	msa_service_name      = "auto-import-account"
	msa_service_name_pair = "auto-import-account-pair" // #nosec G101 -- This is a false positive
	msa_label             = "authentication.open-cluster-management.io/is-managed-serviceaccount"
	backup_label          = "msa"
	addon_work_label      = "open-cluster-management.io/addon-name-work"
	addon_label           = "open-cluster-management.io/addon-name-work"
	role_name             = "klusterlet-bootstrap-kubeconfig"
	msa_api               = "authentication.open-cluster-management.io/v1beta1"

	manifest_work_name                   = "addon-" + msa_addon + "-import"
	manifest_work_name_pair              = "addon-" + msa_addon + "-import-pair"
	manifest_work_name_binding_name      = "managedserviceaccount-import"
	manifest_work_name_binding_name_pair = "managedserviceaccount-import-pair"

	defaultTTL     = 720
	defaultAddonNS = "open-cluster-management-agent-addon"
	manifestwork   = `{
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "ClusterRoleBinding",
        "metadata": {
            "name": "%s"
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
                "namespace": "%s"
            }
        ]
    }`

	update_msg = "Updated secret %s in ns %s"
)

// the prepareForBackup task is executed before each run of a backup schedule
// any settings that need to be applied to the resources before the backpu starts, are being called here

// prepare resources before backing up
func (r *BackupScheduleReconciler) prepareForBackup(
	ctx context.Context,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	backupSchedule *v1beta1.BackupSchedule,
) {
	logger := log.FromContext(ctx)

	// check if user has checked the UseManagedServiceAccount option
	useMSA := backupSchedule.Spec.UseManagedServiceAccount

	// check if ManagedServiceAccount CRD exists,
	// meaning the managedservice account option is enabled on MCH
	msaKind := schema.GroupKind{
		Group: msa_group,
		Kind:  msa_kind,
	}

	msaMapping, err := mapper.RESTMapping(msaKind, "")
	var dr dynamic.NamespaceableResourceInterface
	if err == nil {
		logger.Info("ManagedServiceAccounts is enabled, generate MSA accounts if needed", "useMSA", useMSA)
		dr = r.DynamicClient.Resource(msaMapping.Resource)
		if dr != nil {
			if useMSA {
				prepareImportedClusters(ctx, r.Client, dr, msaMapping, backupSchedule)
			} else {
				cleanupMSAForImportedClusters(ctx, r.Client, dr, msaMapping)
			}
		}
	} else {
		logger.Info("ManagedServiceAccounts are not enabled")
	}

	hiveDeploymentMapping, _ := mapper.RESTMapping(schema.GroupKind{
		Group: "hive.openshift.io",
		Kind:  "ClusterDeployment",
	}, "")

	updateHiveResources(ctx, r.Client, r.DynamicClient.Resource(hiveDeploymentMapping.Resource))
	updateAISecrets(ctx, r.Client)
	updateMetalSecrets(ctx, r.Client)

	if useMSA && err == nil && dr != nil {
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

	if msaMapping != nil {

		// delete ManagedServiceAccounts with msa_service_name label
		listOptions := v1.ListOptions{LabelSelector: fmt.Sprintf("%s in (%s)", msa_label, msa_service_name)}
		if dynamiclist, err := dr.List(ctx, listOptions); err == nil {
			for i := range dynamiclist.Items {
				deleteDynamicResource(
					ctx,
					msaMapping,
					dr,
					dynamiclist.Items[i],
					[]string{},
					false, // don't skip resource if ExcludeBackupLabel is set
				)
			}
		}
	}

	// delete managedclusters addons with msa_service_name label
	addons := &addonv1alpha1.ManagedClusterAddOnList{}
	label := labels.SelectorFromSet(
		map[string]string{msa_label: msa_service_name})
	if err := c.List(ctx, addons, &client.ListOptions{LabelSelector: label}); err == nil {

		for i := range addons.Items {
			logger.Info(fmt.Sprintf("deleting addon %s", addons.Items[i].Name))
			if err := c.Delete(ctx, &addons.Items[i]); err == nil {
				logger.Info(
					fmt.Sprintf(" addon deleted %s", addons.Items[i].Name),
				)
			}
		}
	}

	// delete manifest work with msa_addon label
	manifestWorkList := &workv1.ManifestWorkList{}
	label = labels.SelectorFromSet(
		map[string]string{addon_work_label: msa_addon})
	if err := c.List(ctx, manifestWorkList, &client.ListOptions{LabelSelector: label}); err == nil {

		for i := range manifestWorkList.Items {
			logger.Info(
				fmt.Sprintf("deleting manifestwork %s", manifestWorkList.Items[i].Name),
			)
			if err := c.Delete(ctx, &manifestWorkList.Items[i]); err == nil {
				logger.Info(
					fmt.Sprintf("deleted manifestwork %s", manifestWorkList.Items[i].Name),
				)
			}
		}
	}

	// clean up MSA secrets for clusters not accessible at this time
	// these clusters are not being cleaned up by the MSA remote addon
	secrets := getMSASecrets(ctx, c, "")
	for s := range secrets {
		secret := secrets[s]
		logger.Info(
			fmt.Sprintf("deleting MSA secret %s", secret.Name),
		)
		if err := c.Delete(ctx, &secret); err == nil {
			logger.Info(
				fmt.Sprintf("deleted MSA secret %s", secret.Name),
			)
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
	_ *meta.RESTMapping,
	backupSchedule *v1beta1.BackupSchedule,
) {
	logger := log.FromContext(ctx)

	secretsGeneratedNow := false

	tokenValidity := fmt.Sprintf("%vh0m0s", defaultTTL*2)
	if backupSchedule.Spec.VeleroTTL.Duration != 0 {
		// use backup schedule TTL
		tokenValidity = fmt.Sprintf("%v", backupSchedule.Spec.VeleroTTL.Duration*2)
	}
	if backupSchedule.Spec.ManagedServiceAccountTTL.Duration != 0 {
		// set user defined token TTL
		tokenValidity = fmt.Sprintf("%v", backupSchedule.Spec.ManagedServiceAccountTTL.Duration)
	}

	localClusterFound := false
	hiveClusterCount := 0 // Hive clusters, no MSA ManagedClusterAddOn required
	msaClusterCount := 0  // Other mgd clusters, MSA ManagedClusterAddOn should be created

	// get all managed clusters
	managedClusters := &clusterv1.ManagedClusterList{}
	err := c.List(ctx, managedClusters, &client.ListOptions{})
	if err != nil {
		logger.Error(err, "Unable to list managed clusters")
		//FIXME: handle error
		return
	}
	for i := range managedClusters.Items {
		managedCluster := managedClusters.Items[i]
		if isLocalCluster(&managedCluster) {
			localClusterFound = true
			// No MSA needed
			continue
		}

		if isHiveCreatedCluster(ctx, c, managedCluster.Name) {
			hiveClusterCount++
			// No MSA needed
			continue
		}

		// MSA required for this mgd cluster
		msaClusterCount++

		// create managedservice addon if not available
		addons := &addonv1alpha1.ManagedClusterAddOnList{}
		if err := c.List(ctx, addons, &client.ListOptions{Namespace: managedCluster.Name}); err != nil {
			logger.Error(err, "Unable to list managedclusteraddons in namespace %s", managedCluster.Name)
			//FIXME: handle error
			continue
		}

		installNamespace := ""
		alreadyCreated := false
		for addon := range addons.Items {
			if addons.Items[addon].Name == msa_addon {
				alreadyCreated = true
				if addons.Items[addon].Status.Namespace != "" {
					installNamespace = addons.Items[addon].Status.Namespace
				} else {
					logger.Info("ManagedClusterAddOn status namespace not set",
						"addon", msa_addon, "cluster", managedCluster.Name)
				}
				break
			}
		}
		// use default addon ns when the ManagedClusterAddOn installNamespace is not set
		if installNamespace == "" {
			installNamespace = defaultAddonNS
		}

		if !alreadyCreated {
			msaAddon := &addonv1alpha1.ManagedClusterAddOn{}
			msaAddon.Name = msa_addon
			msaAddon.Namespace = managedCluster.Name
			// Not setting msaAddon.Spec.InstallNamespace - will leave default
			labels := map[string]string{
				msa_label: msa_service_name,
			}
			msaAddon.SetLabels(labels)

			logger.Info(fmt.Sprintf("Attempt to create ManagedClusterAddOn %s for cluster =%s",
				msaAddon.Name, msaAddon.Namespace))
			if err := c.Create(ctx, msaAddon, &client.CreateOptions{}); err == nil {
				logger.Info(fmt.Sprintf("Created ManagedClusterAddOn %s for cluster =%s",
					msaAddon.Name, msaAddon.Namespace))
			}
		}

		secretCreatedNowForCluster, _, _ := createMSA(ctx, c, dr, tokenValidity,
			msa_service_name, managedCluster.Name, time.Now(), installNamespace)
		// create ManagedServiceAccount pair if needed
		// the pair MSA is used to generate a token at half
		// the interval of the initial MSA so that any backup will contain
		// a valid token, either from the initial MSA or pair
		secretCreatedNowForPairCluster, _, _ := createMSA(ctx, c, dr, tokenValidity,
			msa_service_name_pair, managedCluster.Name, time.Now(), installNamespace)

		secretsGeneratedNow = secretsGeneratedNow ||
			secretCreatedNowForCluster ||
			secretCreatedNowForPairCluster
	}

	logger.Info("prepareImportedClusters",
		"total mgd clusters", len(managedClusters.Items),
		"localClusterFound (no MSA required)", localClusterFound,
		"hiveClusterCount (no MSA required)", hiveClusterCount,
		"msaClusterCount", msaClusterCount)

	if secretsGeneratedNow {
		// sleep to allow secrets to be propagated
		time.Sleep(2 * time.Second)
	}

}

// returns true if the pair token needs to be generated now
// if passed the half duration for the token validity, generate the pair token
func shouldGeneratePairToken(
	secrets []corev1.Secret,
	currentTime time.Time,
) bool {

	generateMSA := false
	for s := range secrets {
		secret := secrets[s]

		if secret.GetAnnotations() == nil ||
			secret.GetAnnotations()["expirationTimestamp"] == "" ||
			secret.GetAnnotations()["lastRefreshTimestamp"] == "" {
			continue
		}

		expiryTime, err := time.Parse(time.RFC3339, secret.GetAnnotations()["expirationTimestamp"])
		if err != nil || expiryTime.IsZero() {
			continue
		}
		creationTime, err := time.Parse(time.RFC3339, secret.GetAnnotations()["lastRefreshTimestamp"])
		if err != nil || creationTime.IsZero() {
			continue
		}

		// this should be the token ttl value, divided by 2
		halfInterval := expiryTime.Sub(creationTime).Milliseconds() / 2
		// this is the time interval since the token was last updated
		timeFromTokenCreation := currentTime.In(time.UTC).Sub(creationTime).Milliseconds()
		if halfInterval < timeFromTokenCreation &&
			(timeFromTokenCreation-halfInterval <= time.Duration(time.Minute*15).Milliseconds()) {
			// if passed the half duration for the token validity,
			// and the current time is less then 15 min from the half time
			// then generate the pair token
			generateMSA = true
			break
		}
	}

	return generateMSA
}

// create ManagedServiceAccount
func createMSA(
	ctx context.Context,
	c client.Client,
	dr dynamic.NamespaceableResourceInterface,
	tokenValidity string,
	name string,
	managedClusterName string,
	currentTime time.Time,
	installNamespace string,
) (bool, bool, error) {

	logger := log.FromContext(ctx)

	// delete manifest works prefixed with -custom
	// they are created in 2.8.2 and have a rolebinding overlapping with the default ns
	// get rid of them; will be relaced by the -custom-2 manifest works
	deleteCustomManifestWork(ctx, c, managedClusterName, manifest_work_name)
	deleteCustomManifestWork(ctx, c, managedClusterName, manifest_work_name_pair)

	if name == msa_service_name {
		// attempt to create ManifestWork to push the role binding, if not created already
		createManifestWork(ctx, c, managedClusterName, name,
			manifest_work_name_binding_name, msa_service_name, manifest_work_name, installNamespace)

		if installNamespace != defaultAddonNS {
			// attempt to create the ManifestWork in the default NS even if the installNamespace is set to custom ns
			// this is to cover the case in 2.9 and onward when the user manually sets the installNamespace to a custom value
			// in this version, the MSA framework ignores the custom value and creates the MSA in the addon default NS
			createManifestWork(ctx, c, managedClusterName, name,
				manifest_work_name_binding_name, msa_service_name, manifest_work_name, defaultAddonNS)
		}
	}

	secretsGeneratedNow := false

	//check if MSA exists
	if obj, err := dr.Namespace(managedClusterName).Get(ctx, name, v1.GetOptions{}); err == nil {
		// MSA exists, check if token needs to be updated based on the tokenValidity value
		secretsUpdated, err := updateMSAToken(ctx, dr, obj, name, managedClusterName, tokenValidity)
		if err != nil {
			logger.Error(err, "Failed to update MSA")
		}
		if secretsUpdated {
			logger.Info(fmt.Sprintf("updated token validity for cluster %s", managedClusterName))
		}
		return secretsGeneratedNow, secretsUpdated, err
	}

	//MSA does not exist

	// initial MSA must be always created
	generateMSA := name == msa_service_name

	if name == msa_service_name_pair {
		// for the MSA pair, generate one only when the initial MSA token exists and
		// current time is half between creation and expiration time for that token
		generateMSA = shouldGeneratePairToken(getMSASecrets(ctx, c, managedClusterName), currentTime)

		if generateMSA {
			// attempt to create ManifestWork for the pair, if not created already
			createManifestWork(ctx, c, managedClusterName, name,
				manifest_work_name_binding_name_pair, msa_service_name_pair, manifest_work_name_pair, installNamespace)

			if installNamespace != defaultAddonNS {
				// attempt to create the ManifestWork in the default NS even if the installNamespace is set to custom ns
				// this is to cover the case in 2.9 and onward when the user manually sets the installNamespace to a custom value
				// in this version, the MSA framework ignores the custom value and creates the MSA in the addon default NS
				createManifestWork(ctx, c, managedClusterName, name,
					manifest_work_name_binding_name_pair, msa_service_name_pair, manifest_work_name_pair, defaultAddonNS)
			}
		}
	}

	if generateMSA {

		// delete any secret with the same name as the MSA
		msaSecret := corev1.Secret{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: managedClusterName,
		}, &msaSecret); err == nil {
			// found an MSA secret with the same name as the MSA; delete it, it will be recreated by the MSA
			logger.Info("Deleting MSA secret %s in ns %s", name, managedClusterName)
			if err := c.Delete(ctx, &msaSecret); err == nil {
				logger.Info("Deleted MSA secret %s in ns %s", name, managedClusterName)
			}
		}

		// create ManagedServiceAccount in the managed cluster namespace
		secretsGeneratedNow = true
		msaRC := &unstructured.Unstructured{}
		msaRC.SetUnstructuredContent(map[string]interface{}{
			"apiVersion": msa_api,
			"kind":       msa_kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": managedClusterName,
				"labels": map[string]interface{}{
					msa_label: msa_service_name,
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
		logger.Info(fmt.Sprintf("Attempt to create ManagedServiceAccount for cluster =%s", managedClusterName))
		if _, err := dr.Namespace(managedClusterName).Create(ctx, msaRC, v1.CreateOptions{}); err == nil {
			logger.Info(fmt.Sprintf("Created ManagedServiceAccount for cluster =%s", managedClusterName))
		} else {
			logger.Error(err, "Cannot create MSA")
		}

	}

	return secretsGeneratedNow, false, nil

}

func updateMSAToken(
	ctx context.Context,
	dr dynamic.NamespaceableResourceInterface,
	msaUnstructuredObj *unstructured.Unstructured,
	serviceName string,
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
		if key != "rotation" {
			continue
		}
		rotationValues := iter.Value().Interface().(map[string]interface{})
		iterRotation := reflect.ValueOf(rotationValues).MapRange()
		for iterRotation.Next() {
			if iterRotation.Key().String() == "validity" &&
				iterRotation.Value().Interface().(string) != tokenValidity {
				//update MSA validity with the latest token value
				_, err := dr.Namespace(namespaceName).Patch(ctx, serviceName,
					types.JSONPatchType, []byte(patch), v1.PatchOptions{})

				return true, err
			}
		}
	}
	return false, nil
}

// create manifest work to push the import user role binding to the managed cluster
func deleteCustomManifestWork(
	ctx context.Context,
	c client.Client,
	namespace string,
	mworkName string,
) {
	logger := log.FromContext(ctx)

	custommwork := &workv1.ManifestWork{}
	if err := c.Get(ctx, types.NamespacedName{Name: mworkName + mwork_custom_282,
		Namespace: namespace}, custommwork); err == nil {

		// delete the resource
		logger.Info("Deleting manifest work %s in ns %s", mwork_custom_282, namespace)
		if err := c.Delete(ctx, custommwork); err != nil {
			logger.Error(err, "Failed to delete manifest work")
		}
	}
}

// create manifest work to push the import user role binding to the managed cluster
func createManifestWork(
	ctx context.Context,
	c client.Client,
	namespace string,
	name string,
	mworkbindingName string,
	msaserviceName string,
	mworkName string,
	installNamespace string,
) {
	logger := log.FromContext(ctx)

	mwork := mworkName
	mworkBinding := mworkbindingName
	if installNamespace != defaultAddonNS {
		// to be used for the case when the MSA uses a custom ns, in 2.8 and 2.7
		// starting with 2.9 and onwards the installNamespace for the MSA is ignored
		// so in this cases the custom manifest work is not going to be used

		// use this to create a custom manifest work name
		mwork = mworkName + mwork_custom_283
		// use a different role binding name for the custom ns
		mworkBinding = mworkbindingName + mwork_custom_283
	}

	manifestWorkList := &workv1.ManifestWorkList{}
	if msaLabel, err := labels.NewRequirement(addon_work_label,
		selection.In, []string{msa_addon}); err == nil {

		selector := labels.NewSelector()
		selector = selector.Add(*msaLabel)
		if err := c.List(ctx, manifestWorkList, &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: selector},
		); err == nil {

			alreadyExists := false
			for i := range manifestWorkList.Items {
				if manifestWorkList.Items[i].Name == mwork {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				// create the manifest work now
				manifestWork := &workv1.ManifestWork{}
				manifestWork.Name = mwork
				manifestWork.Namespace = namespace
				manifestWork.Labels = map[string]string{
					addon_work_label:        msa_addon,
					backupCredsClusterLabel: ClusterActivationLabel}

				manifest := &workv1.Manifest{}
				manifest.Raw = []byte(fmt.Sprintf(manifestwork, mworkBinding, role_name, msaserviceName, installNamespace))

				manifestWork.Spec.Workload.Manifests = []workv1.Manifest{
					*manifest,
				}

				logger.Info(fmt.Sprintf("Attempt to create ManifestWork %s in ns %s, for ns=%s",
					mwork, namespace, installNamespace))
				if err := c.Create(ctx, manifestWork, &client.CreateOptions{}); err == nil {
					logger.Info(fmt.Sprintf("Created ManifestWork %s in ns %s for ns=%s",
						mwork, namespace, installNamespace))
				} else {
					logger.Error(err, "Failed to create ManifestWork")
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
				return retrieveMSAImportSecrets(msaSecrets.Items)
			}
		} else {
			// get secrets from specified namespace
			if err := c.List(ctx, msaSecrets, &client.ListOptions{
				Namespace:     namespace,
				LabelSelector: selector,
			}); err == nil {
				return retrieveMSAImportSecrets(msaSecrets.Items)
			}
		}

	}
	return []corev1.Secret{}
}

// return only secrets with a prefix of msa_service_name
func retrieveMSAImportSecrets(
	secrets []corev1.Secret,
) []corev1.Secret {

	msaSecrets := []corev1.Secret{}

	for i := range secrets {
		if strings.HasPrefix(secrets[i].Name, msa_service_name) {
			msaSecrets = append(msaSecrets, secrets[i])
		}
	}

	return msaSecrets
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

		secretTimestampUpdated := false
		// add token expiration time from parent MSA resource
		if unstructuredObj, err := dr.Namespace(secret.Namespace).Get(ctx, secret.Name,
			v1.GetOptions{}); err == nil {
			secretTimestampUpdated = updateMSASecretTimestamp(ctx, dr, unstructuredObj, &secret)
		}
		backupLabelSet := updateSecret(ctx, c, secret,
			backupCredsClusterLabel,
			backup_label, false)

		if secretTimestampUpdated || backupLabelSet {
			logger.Info(fmt.Sprintf("Attempt to update secret %s in ns %s", secret.Name, secret.Namespace))

			if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err == nil {
				logger.Info(fmt.Sprintf(update_msg, secret.Name, secret.Namespace))
			}
		}
	}
}

// find the MSA account under the secret namespace and use the
// MSA status expiration info to annotate the secret
func updateMSASecretTimestamp(
	ctx context.Context,
	dr dynamic.NamespaceableResourceInterface,
	unstructuredObj *unstructured.Unstructured,
	secret *corev1.Secret) bool {

	secretTimestampUpdated := false
	lastRefreshTimestampUpdated := false
	// look for the expiration timestamp under status
	statusInfo := unstructuredObj.Object["status"]
	if statusInfo == nil {
		return secretTimestampUpdated
	}

	secretAnnotations := secret.GetAnnotations()
	if secretAnnotations == nil {
		secretAnnotations = map[string]string{}
	}

	iter := reflect.ValueOf(statusInfo).MapRange()
	for iter.Next() {
		key := iter.Key().Interface()
		if key == "expirationTimestamp" {
			// set expirationTimestamp on the secret
			secretTimestampUpdated = secretAnnotations["expirationTimestamp"] != iter.Value().Interface().(string)
			secretAnnotations["expirationTimestamp"] = iter.Value().Interface().(string)
			secret.SetAnnotations(secretAnnotations)
		}
		if key == "tokenSecretRef" {
			// set lastRefreshTimestamp on the secret
			refMap := iter.Value().Interface().(map[string]interface{})
			refiter := reflect.ValueOf(refMap).MapRange()
			for refiter.Next() {
				key := refiter.Key().Interface().(string)
				if key == "lastRefreshTimestamp" {
					lastRefreshTimestampUpdated = secretAnnotations["lastRefreshTimestamp"] != refiter.Value().Interface().(string)
					secretAnnotations["lastRefreshTimestamp"] = refiter.Value().Interface().(string)
					secret.SetAnnotations(secretAnnotations)
					break
				}
			}
		}
	}

	return secretTimestampUpdated || lastRefreshTimestampUpdated

}

// prepare hive cluster claim and cluster pool
func updateHiveResources(ctx context.Context,
	c client.Client,
	dr dynamic.NamespaceableResourceInterface,
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
					Namespace: clusterDeployment.Namespace,
				}); err == nil {
					// add backup labels if not set yet
					updateSecretsLabels(ctx, c, *secrets, clusterDeployment.Name,
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
				if labels[hive_label] != "" {
					// label already set
					continue
				}
				logger.Info("Patching disable-creation-webhook-for-dr label on deployment " + clusterDeployment.Name)

				patch := `[ { "op": "add", "path": "` + hive_label_path + `", "value": "true" } ]`
				if _, err := dr.Namespace(clusterDeployment.GetNamespace()).Patch(ctx, clusterDeployment.GetName(),
					types.JSONPatchType, []byte(patch), v1.PatchOptions{}); err != nil {
					logger.Error(err, "cannot patch with hive label hive.openshift.io~1disable-creation-webhook-for-dr")
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
	logger := log.FromContext(ctx)

	for s := range secrets.Items {
		secret := secrets.Items[s]
		//exclude import secrets
		if secret.Name == secret.Namespace+"-import" {
			// remove backup label if set by previus code
			// we don't want hive import secrets to be backed up
			if secret.GetLabels()[labelName] == labelValue {
				// remove this label
				delete(secret.GetLabels(), labelName)

				msg := fmt.Sprintf("Updating secret %s in ns %s, removing label %s", secret.Name, secret.Namespace, labelName)
				logger.Info(msg)
				if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err == nil {
					logger.Info(fmt.Sprintf(update_msg, secret.Name, secret.Namespace))
				}
			}
			continue
		}

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
			// secret does not need refresh
			return true
		}
		if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err == nil {
			logger.Info(fmt.Sprintf(update_msg, secret.Name, secret.Namespace))
		}
		// secret needs refresh
		return true
	}
	// secret not refreshed
	return false

}
