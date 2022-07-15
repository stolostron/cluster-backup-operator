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
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

// the prepareForBackup task is executed before each run of a backup schedule
// any settings that need to be applied to the resources before the backup starts, are being called here

// prepare resources before backing up
func (r *BackupScheduleReconciler) prepareForBackup(
	ctx context.Context,
	backupSchedule *v1beta1.BackupSchedule,
) {
	updateHiveResources(ctx, r.Client)
	updateAISecrets(ctx, r.Client)
	updateMetalSecrets(ctx, r.Client)
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
