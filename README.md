# cluster-backup-operator
Cluster Back up and Restore Operator 
------

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Work in Progress](#work-in-progress)
- [Community, discussion, contribution, and support](#community-discussion-contribution-and-support)
- [License](#license)
- [Getting Started](#getting-started)
  - [OADP Operator installed by the backup chart](#oadp-operator-installed-by-the-backup-chart)
  - [Policy to inform on backup configuration issues](#policy-to-inform-on-backup-configuration-issues)
  - [Resource Requests and Limits Customization](#resource-requests-and-limits-customization)
  - [Protecting data using Server-Side Encryption](#protecting-data-using-server-side-encryption)
- [Design](#design)
  - [Cluster Backup and Restore flow](#cluster-backup-and-restore-flow)
  - [What is backed up](#what-is-backed-up)
    - [Steps to identify backup data](#steps-to-identify-backup-data)
    - [Extending backup data](#extending-backup-data)
    - [Resources restored at managed clusters activation time](#resources-restored-at-managed-clusters-activation-time)
  - [Passive data](#passive-data)
  - [Managed clusters activation data](#managed-clusters-activation-data)
- [Scheduling a cluster backup](#scheduling-a-cluster-backup)
  - [Backup Collisions](#backup-collisions)
- [Restoring a backup](#restoring-a-backup)
  - [Prepare the new hub](#prepare-the-new-hub)
  - [Restoring backups](#restoring-backups)
    - [Restoring passive resources and check for new backups](#restoring-passive-resources-and-check-for-new-backups)
    - [Restoring passive resources](#restoring-passive-resources)
    - [Restoring activation resources](#restoring-activation-resources)
      - [Primary cluster must be shut down](#primary-cluster-must-be-shut-down)
    - [Restoring all resources](#restoring-all-resources)
  - [Cleaning up the hub before restore](#cleaning-up-the-hub-before-restore)
  - [View restore events](#view-restore-events)
- [Restoring imported managed clusters](#restoring-imported-managed-clusters)
  - [Automatically connecting clusters using ManagedServiceAccount](#automatically-connecting-clusters-using-managedserviceaccount)
  - [Enabling the automatic import feature](#enabling-the-automatic-import-feature)
  - [Limitations with the automatic import feature](#limitations-with-the-automatic-import-feature)
- [Backup validation using a Policy](#backup-validation-using-a-policy)
  - [OADP channel validation](#oadp-channel-validation)
  - [Pod validation](#pod-validation)
  - [Data Protection Application validation](#data-protection-application-validation)
  - [Backup Storage validation](#backup-storage-validation)
  - [BackupSchedule collision validation](#backupschedule-collision-validation)
  - [BackupSchedule and Restore status validation](#backupschedule-and-restore-status-validation)
  - [Backups exist validation](#backups-exist-validation)
  - [Backups are running to completion](#backups-are-running-to-completion)
  - [Backups are actively running as a cron job](#backups-are-actively-running-as-a-cron-job)
  - [Automatic import feature validation](#automatic-import-feature-validation)
- [Active passive configuration design](#active-passive-configuration-design)
  - [Setting up an active passive configuration](#setting-up-an-active-passive-configuration)
  - [Disaster recovery](#disaster-recovery)
- [Setting up your development environment](#setting-up-your-development-environment)
  - [Prerequiste tools](#prerequiste-tools)
  - [Installation](#installation)
    - [Outside the cluster](#outside-the-cluster)
    - [Inside the cluster](#inside-the-cluster)
  - [Usage](#usage)
  - [Testing](#testing)
    - [Schedule  a backup](#schedule--a-backup)
    - [Restore a backup](#restore-a-backup)
- [ACM Backup and Restore Performance in a Large-Scale Environment](#acm-backup-and-restore-performance-in-a-large-scale-environment)
  - [First attempt with cleanupBeforeRestore set to None:](#first-attempt-with-cleanupbeforerestore-set-to-none)
  - [Second attempt with cleanupBeforeRestore set to CleanupRestored:](#second-attempt-with-cleanupbeforerestore-set-to-cleanuprestored)
- [Virtual Machine Backup and Restore Performance in a Large-Scale Environment](#virtual-machine-backup-and-restore-performance-in-a-large-scale-environment)
  - [Test Environment](#test-environment)
  - [Test Scenarios and Results](#test-scenarios-and-results)
    - [Scenario 1](#scenario-1)
    - [Scenario 2](#scenario-2)
    - [Scenario 3](#scenario-3)
    - [Scenario 4](#scenario-4)
    - [Scenario 5](#scenario-5)
  - [Notes](#notes)
- [OADP Version Relationship](#oadp-version-relationship)
- [Using custom OADP Version](#using-custom-oadp-version)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

------

## Work in Progress
We are in the process of enabling this repo for community contribution. See wiki [here](https://open-cluster-management.io/concepts/architecture/).

## Community, discussion, contribution, and support

Check the [CONTRIBUTING Doc](CONTRIBUTING.md) for how to contribute to the repo.

## License

This project is licensed under the *Apache License 2.0*. A copy of the license can be found in [LICENSE](LICENSE).


## Getting Started
The Cluster Back up and Restore Operator provides disaster recovery solutions for the case when the Red Hat Advanced Cluster Management for Kubernetes hub goes down and needs to be recreated. Scenarios outside the scope of this component : disaster recovery scenarios for applications running on managed clusters or scenarios where the managed clusters go down. 

The Cluster Back up and Restore Operator runs on the Red Hat Advanced Cluster Management for Kubernetes hub and depends on the [OADP Operator](https://github.com/openshift/oadp-operator) to create a connection to a backup storage location on the hub, which is then used to backup and restore user created hub resources. 

The Cluster Back up and Restore Operator chart is not installed automatically. Starting with Red Hat Advanced Cluster Management version 2.5, in order to enable the backup component, you have to set the `cluster-backup` option to `true` on the MultiClusterHub resource. The OADP Operator will be installed automatically with the Cluster Back up and Restore Operator chart, as a chart hook. The backup chart and OADP Operator are both installed under the `open-cluster-management-backup` namespace.

Before you can use the Cluster Back up and Restore operator, the [OADP Operator](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md) must be configured to set the connection to the storage location where backups will be saved. Make sure you follow the steps to create the [secret for the cloud storage](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md#create-credentials-secret) where the backups are going to be saved, then use that secret when creating the [DataProtectionApplication resource](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md#create-the-dataprotectionapplication-custom-resource) to setup the connection to the storage location.


### OADP Operator installed by the backup chart
The Cluster Back up and Restore Operator chart is not installed automatically.
Starting with Red Hat Advanced Cluster Management version 2.5, the Cluster Back up and Restore Operator chart is installed by setting the `cluster-backup` option to `true` on the `MultiClusterHub` resource. 

The Cluster Back up and Restore Operator chart in turn automatically installs the [OADP Operator](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md), in the `open-cluster-management-backup` namespace, which is the namespace where the chart is installed. 

<b>Note</b>: 
- The OADP Operator 1.0 has disabled building multi-arch builds and only produces x86_64 builds for the official release. This means that if you are using an architecture other than x86_64, the OADP Operator installed by the chart will have to be replaced with the right version. In this case, uninstall the OADP Operator and find the operator matching your architecture then install it.
- If you have previously installed and used the OADP Operator on this hub, you should uninstall this version since the backup chart works now with the operator installed in the chart's namespace. This should not affect your old backups and previous work. Just use the same storage location for the [DataProtectionApplication resource](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md#create-the-dataprotectionapplication-custom-resource) owned by the OADP Operator installed with the backup chart and it will access the same backup data as the previous operator. The only difference is that velero backup resources are now loaded under the new OADP Operator namespace on this hub.


### Policy to inform on backup configuration issues
The Cluster Back up and Restore Operator chart installs the [backup-restore-enabled](https://github.com/stolostron/cluster-backup-chart/blob/main/stable/cluster-backup-chart/templates/hub-backup-pod.yaml) Policy, used to inform on issues with the backup and restore component. The Policy templates check if the required pods are running, storage location is available, backups are available at the defined location and no error status is reported by the main resources. This Policy is intended to help notify the Hub admin of any backup issues as the hub is active and expected to produce backups.

### Resource Requests and Limits Customization
When Velero is initially installed, Velero pod is set with default cpu and memory limits as defined below.

```
resources:
  limits:
    cpu: "1"
    memory: 256Mi
  requests:
    cpu: 500m
    memory: 128Mi
```

These limits work well with regular scenarios but may need to be updated when your cluster backs up a large number of resources. For instance, when back up is executed on a hub managing 2000 clusters, Velero pod crashes due to the out-of-memory error (OOM). The following configuration allows backup to complete for this scenario.

```
  limits:
    cpu: "2"
    memory: 1Gi
  requests:
    cpu: 500m
    memory: 256Mi
```

In order to update the Velero pod resource(cpu, memory) limits and requests, you need to update the `DataProtectionApplication` resource and insert the resourceAllocation template for the Velero pod, as described below:

```
apiVersion: oadp.openshift.io/v1alpha1
kind: DataProtectionApplication
metadata:
  name: velero
  namespace: open-cluster-management-backup
spec:
...
  configuration:
...
    velero:
      podConfig:
        resourceAllocations:
          limits:
            cpu: "2"
            memory: 1Gi
          requests:
            cpu: 500m
            memory: 256Mi
```

Refer to [Velero Resource Requests and Limits Customization](https://github.com/openshift/oadp-operator/blob/master/docs/config/resource_req_limits.md) to find out more about the `DataProtectionApplication` parameters for setting the Velero pod resource requests and limits.


### Protecting data using Server-Side Encryption
Server-side encryption is the encryption of data at its destination by the application or service that receives it. Our backup mechanism itself does not encrypt data while in-transit (as it travels to and from backup storage location) or at rest (while it is stored on disks at backup storage location), instead it relies on the native mechanisms in the object and snapshot systems. <br><br>
It is strongly recommended to encrypt the data at its destination using the available backup storage server-side encryption. The backup contains resources such as credentials and configuration files that should be encrypted when stored outside of the hub cluster.

For server-side encryption using AWS provider, you can use `serverSideEncryption` and `kmsKeyId` configurations as explained in [this sample AWS BackupStorageLocation](https://github.com/vmware-tanzu/velero-plugin-for-aws/blob/main/backupstoragelocation.md).
The following sample specifies an AWS KMS key ID when setting up the DataProtectionApplication resource:
```yaml
spec:
  backupLocations:
    - velero:
        config:
          kmsKeyId: 502b409c-4da1-419f-a16e-eif453b3i49f
          profile: default
          region: us-east-1
```
Refer to [Velero supported storage providers](https://github.com/vmware-tanzu/velero/blob/main/site/content/docs/main/supported-providers.md) to find out about all of the configurable parameters of other storage providers.


## Design

The content below describes the backup and restore flow using the Cluster Back up and Restore Operator solution, with details on what and how the data is backed up and restored.
Once you are familiar with these concepts you can follow the [Active passive configuration design](#active-passive-configuration-design) section to understand how to build a complete DR solution with an ACM hub set as a primary, active configuration, managing clusters, and one or more ACM hubs setup to take over in a disaster scenario.


### Cluster Backup and Restore flow

The operator defines the `BackupSchedule.cluster.open-cluster-management.io` resource, used to setup Red Hat Advanced Cluster Management for Kubernetes backup schedules, and the `Restore.cluster.open-cluster-management.io` resource, used to process and restore these backups.
The operator sets the options required to backup remote clusters configuration and any other hub resources that need to be restored.

![Cluster Backup Controller Dataflow](images/cluster-backup-controller-dataflow.png)

### What is backed up

The Cluster Back up and Restore Operator solution provides backup and restore support for all Red Hat Advanced Cluster Management for Kubernetes hub resources like managed clusters, applications, policies, bare metal assets.
It  provides support for backing up any third party resources extending the basic hub installation. 
With this backup solution, you can define a cron based backup schedule which runs at specified time intervals and continuously backs up the latest version of the hub content.
When the hub needs to be replaced or in a disaster scenario when the hub goes down, a new hub can be deployed and backed up data moved to the new hub, so that the new hub replaces the old one.

#### Steps to identify backup data

The steps below show how the Cluster Back up and Restore Operator finds the resources to be backed up.
With this approach the backup includes all CRDs installed on the hub, including any extensions using third parties components.


1. Exclude all resources in the MultiClusterHub namespace. This is to avoid backing up installation resources which are linked to the current Hub identity and should not be backed up.
2. Backup all CRDs with an api version suffixed by `.open-cluster-management.io` and `.hive.openshift.io`. This will cover all Advanced Cluster Management resources.
3. Additionally, backup all CRDs from these api groups: `argoproj.io`,`app.k8s.io`,`core.observatorium.io`,`hive.openshift.io` under the resources backup and all CRDs from this group `agent-install.openshift.io` under the managed-clusters backup.
4. Exclude all CRDs from the following api groups : 
		"internal.open-cluster-management.io",
		"operator.open-cluster-management.io",
		"work.open-cluster-management.io",
		"search.open-cluster-management.io",
		"admission.hive.openshift.io",
		"proxy.open-cluster-management.io",
		"action.open-cluster-management.io",
		"view.open-cluster-management.io",
		"clusterview.open-cluster-management.io",
		"velero.io",
5. Exclude the following CRDs; they are part of the included api groups but are either not needed or they are being recreated by owner resources, which are also backed up: 		
    "clustermanagementaddon.addon.open-cluster-management.io",
		"backupschedule.cluster.open-cluster-management.io",
		"restore.cluster.open-cluster-management.io",
		"clusterclaim.cluster.open-cluster-management.io",
		"discoveredcluster.discovery.open-cluster-management.io",
		"placementdecisions.cluster.open-cluster-management.io",
6. Backup secrets and configmaps with one of the following labels:
`cluster.open-cluster-management.io/type`, `hive.openshift.io/secret-type`, `cluster.open-cluster-management.io/backup`
7. Use this label for any other resources that should be backed up and are not included in the above criteria: `cluster.open-cluster-management.io/backup`
Example :
```yaml
apiVersion: my.group/v1alpha1
kind: MyResource
metadata:
  labels:
    cluster.open-cluster-management.io/backup: ""
```
- <b>Note</b> that secrets used by the `hive.openshift.io.ClusterDeployment` resource need to be backed up and they are automatically annotated with the `cluster.open-cluster-management.io/backup` label only when the cluster is created using the console UI. If the hive cluster is deployed using gitops instead, the `cluster.open-cluster-management.io/backup` label must be manually added to the secrets used by this `ClusterDeployment`.

8. Resources picked up by the above rules that should not be backed up, can be explicitly excluded when setting this label: `velero.io/exclude-from-backup: "true"`
Example :
```yaml
apiVersion: my.group/v1alpha1
kind: MyResource
metadata:
  labels:
    velero.io/exclude-from-backup: "true"
```

#### Extending backup data
Third party components can choose to back up their resources with the ACM backup by adding the `cluster.open-cluster-management.io/backup` label to these resources. The value of the label could be any string, including an empty string. It is indicated though to set a value that can be later on used to easily identify the component backing up this resource. For example `cluster.open-cluster-management.io/backup: idp` if the components are provided by an idp solution.

<b>Note:</b> 
Use the `cluster-activation` value for the `cluster.open-cluster-management.io/backup` label if you want the resources to be restored when the managed clusters activation resources are restored. Restoring the managed clusters activation resources result in managed clusters being actively managed by the hub where the restore was executed. 

#### Resources restored at managed clusters activation time

As mentioned above, when you add the `cluster.open-cluster-management.io/backup` label to a resource, this resource is automatically backed up under the `acm-resources-generic-schedule` backup. If any of these resources need to be restored only when the managed clusters are moved to the new hub, so when the `veleroManagedClustersBackupName:latest` is used on the restored resource, then you have to set the label value to `cluster-activation`. This will ensure the resource is not restored unless the managed cluster activation is called.
Example :
```yaml
apiVersion: my.group/v1alpha1
kind: MyResource
metadata:
  labels:
    cluster.open-cluster-management.io/backup: cluster-activation
``` 

Aside of these activation data resources, identified by using the `cluster.open-cluster-management.io/backup: cluster-activation` label and stored by the `acm-resources-generic-schedule` backup, the Cluster Back up and Restore Operator includes by default a few resources in the activation set. These resources are backed up by the `acm-managed-clusters-schedule`:
  - managedcluster.cluster.open-cluster-management.io
  - klusterletaddonconfig.agent.open-cluster-management.io
  - managedclusteraddon.addon.open-cluster-management.io
  - managedclusterset.cluster.open-cluster-management.io
  - managedclusterset.clusterview.open-cluster-management.io
  - managedclustersetbinding.cluster.open-cluster-management.io
  - clusterpool.hive.openshift.io
  - clusterclaim.hive.openshift.io
  - clusterdeployment.hive.openshift.io
  - machinepool.hive.openshift.io
  - clustersync.hiveinternal.openshift.io
  - clustercurator.cluster.open-cluster-management.io

### Passive data

Passive data is backup data such as secrets, configmaps, apps, policies and all the managed cluster custom resources which are not resulting in activating the connection between managed clusters and hub where these resources are being restored on. These resources are stored by the credentials backup and resources backup files.

### Managed clusters activation data

Managed clusters activation data or activation data, is backup data which when restored on a new hub will result in managed clusters being actively managed by the hub where the restore was executed. Activation data resources are stored by the managed clusters backup, and by the resources-generic backup, using the `cluster.open-cluster-management.io/backup: cluster-activation` label. More details about the activation resources are available with the [backup section](#resources-restored-at-managed-clusters-activation-time)

## Scheduling a cluster backup 

A backup schedule is activated when creating the `backupschedule.cluster.open-cluster-management.io` resource, as shown [here](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_backupschedule.yaml)

After you create a `backupschedule.cluster.open-cluster-management.io` resource you should be able to run `oc get bsch -n open-cluster-management-backup` and get the status of the scheduled cluster backups.

The `backupschedule.cluster.open-cluster-management.io` creates a set of `schedule.velero.io` resources, used to generate the backups.

Run `oc get schedules -A | grep acm` to view the list of backup scheduled.

Resources are backed up in 3 separate groups:
1. credentials backup - one backup file, storing hive, ACM and user created secrets and configmaps
2. resources backup - 2 backup files, one for the ACM resources and second for generic resources, labeled with `cluster.open-cluster-management.io/backup`
3. managed clusters backup, schedule labeled with `cluster.open-cluster-management.io/backup-schedule-type: acm-managed-clusters` - one backup containing only resources which result in activating the managed cluster connection to the hub where the backup was restored on


<b>Note</b>:

a. The backup file created in step 2. above contains managed cluster specific resources but does not contain the subset of resources which will result in managed clusters connect to this hub. These resources, also called activation resources, are contained by the managed clusters backup, created in step 3. When you restore on a new hub just the resources from step 1 and 2 above, the new hub does not shows any managed clusters on the Clusters page. The managed clusters are still connected to the original hub that had produced the backup files. Managed clusters will show on the restore hub only when the activation data is restored on this cluster.

b. Only managed clusters created using the hive api will be automatically connected with the new hub when the `acm-managed-clusters` backup from step 3 is restored on another hub. Read more about this under the [Restoring imported managed clusters](#restoring-imported-managed-clusters) section.


### Backup Collisions

As hubs change from passive to primary clusters and back, different clusters can backup up data at the same storage location. This could result in backup collisions, which means the latest backups are generated by a hub who is no longer the designated primary hub. That hub produces backups because the `BackupSchedule.cluster.open-cluster-management.io` resource is still Enabled on this hub, but it should no longer write backup data since that hub is no longer a primary hub.
Situations when a backup collision could happen:
1. Primary hub goes down unexpectedly:
    - 1.1) Primary hub, Hub1, goes down 
    - 1.2) Hub1 backup data is restored on Hub2
    - 1.3) The admin creates the `BackupSchedule.cluster.open-cluster-management.io` resource on Hub2. Hub2 is now the primary hub and generates backup data to the common storage location. 
    - 1.4) Hub1 comes back to life unexpectedly. Since the `BackupSchedule.cluster.open-cluster-management.io` resource is still enabled on Hub1, it will resume writting backups to the same storage location as Hub2. Both Hub1 and Hub2 are now writting backup data at the same storage location. Any cluster restoring the latest backups from this storage location could pick up Hub1 data instead of Hub2.
2. The admin tests the disaster scenario by making Hub2 a primary hub:
    - 2.1) Hub1 is stopped
    - 2.2) Hub1 backup data is restored on Hub2
    - 2.3) The admin creates the `BackupSchedule.cluster.open-cluster-management.io` resource on Hub2. Hub2 is now the primary hub and generates backup data to the common storage location. 
    - 2.4) After the disaster test is completed, the admin will revert to the previous state and make Hub1 the primary hub:
        - 2.4.1) Hub1 is started. Hub2 is still up though and the `BackupSchedule.cluster.open-cluster-management.io` resource is Enabled on Hub2. Until Hub2 `BackupSchedule.cluster.open-cluster-management.io` resource is deleted or Hub2 is stopped, Hub2 could write backups at any time at the same storage location, corrupting the backup data. Any cluster restoring the latest backups from this location could pick up Hub2 data instead of Hub1. The right approach here would have been to first stop Hub2 or delete the `BackupSchedule.cluster.open-cluster-management.io` resource on Hub2, then start Hub1.

In order to avoid and to report this type of backup collisions, a BackupCollision state exists for a  `BackupSchedule.cluster.open-cluster-management.io` resource. The controller checks regularly if the latest backup in the storage location has been generated from the current cluster. If not, it means that another cluster has more recently written backup data to the storage location so this hub is in collision with another hub.

In this case, the current hub `BackupSchedule.cluster.open-cluster-management.io` resource status is set to BackupCollision and the `Schedule.velero.io` resources created by this resource are deleted to avoid data corruption. The BackupCollision is reported by the [backup Policy](https://github.com/stolostron/cluster-backup-chart/blob/main/stable/cluster-backup-chart/templates/hub-backup-pod.yaml). The admin should verify what hub must be the one writting data to the  storage location, then remove the `BackupSchedule.cluster.open-cluster-management.io` resource from the invalid hub and recreated a new `BackupSchedule.cluster.open-cluster-management.io` resource on the valid, primary hub, to resume the backup on this hub. 

Example of a schedule in `BackupCollision` state:

```
oc get backupschedule -A
NAMESPACE       NAME               PHASE             MESSAGE
openshift-adp   schedule-hub-1   BackupCollision   Backup acm-resources-schedule-20220301234625, from cluster with id [be97a9eb-60b8-4511-805c-298e7c0898b3] is using the same storage location. This is a backup collision with current cluster [1f30bfe5-0588-441c-889e-eaf0ae55f941] backup. Review and resolve the collision then create a new BackupSchedule resource to  resume backups from this cluster.
```

## Restoring a backup

### Prepare the new hub
Before running the restore operation on a new hub, you need to manually configure the hub and install the same operators as on the initial hub. 
You have to install the Red Hat Advanced Cluster Management for Kubernetes operator, in the same namespace as the initial hub, then create the [DataProtectionApplication resource](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md#create-the-dataprotectionapplication-custom-resource) and connect to the same storage location where the initial hub had backed up data.
Use the same configuration as on the initial hub for the `MultiClusterHub` resource created by the Red Hat Advanced Cluster Management for Kubernetes operator, including any changes to the `MultiClusterEngine` resource.

If the initial hub had any other operators installed, such as `Ansible Automation Platform`, `Red Hat OpenShift GitOps`, `cert-manager` you have to install them now, before running the restore operation, and using the same namespace as the primary hub operators. This ensure the new hub is configured in the same way as the initial hub. 

<b>Note:</b><br>
The settings for the `local-cluster` managed cluster resource, such as owning managed cluster set, are not restored on new hubs.
This is because the `local-cluster` managed cluster resource is not being backed up since the resource contains local cluster specific information, such as cluster url details. Restoring this content and overwriting the `local-cluster` data on a new hub would corrupt the cluster where the restore is executed. As a result, any configuration changes applied to the `local-cluster` resource on the primary hub - such as updating the owning managed cluster set from `default` to another managed cluster set - should be manually applied on the restored cluster.

### Restoring backups
In a usual restore scenario, the hub where the backups have been executed becomes unavailable and data backed up needs to be moved to a new hub. This is done by running the restore operation on the hub where the backed up data needs to be moved to. In this case, the restore operation is executed on a different hub than the one where the backup was created. 

There are also cases where you want to restore the data on the same hub where the backup was collected, in order to recover data from a previous snapshot. In this case both restore and backup operations are executed on the same hub.

A restore backup is executed when creating the `restore.cluster.open-cluster-management.io` resource on the hub. A few samples are available [here](https://github.com/stolostron/cluster-backup-operator/tree/main/config/samples)

By passive data we mean resource that don't result in activating the connection between the new hub and managed clusters. When the passive data is restored on the new hub, the managed clusters do not show up on the restored hub Clusters page. The hub that produced the backup is still managing these clusters.

By activation data we mean resources that, when restored on the new hub, result in making the managed clusters to be managed by the new hub. The new hub is now the active hub, managing the clusters.

#### Restoring passive resources and check for new backups

Use the [restore passive with sync sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive_sync.yaml) if you want to restore passive data then keep checking if new backups are available and restore them automatically. For this automatic restore of new backups to work, the restore must set `syncRestoreWithNewBackups` property to `true` and must only restore latest, passive data. So for this option to work, you need to set `VeleroResourcesBackupName` and `VeleroCredentialsBackupName` to `latest` and the `VeleroManagedClustersBackupName` to `skip` - as soon as the `VeleroManagedClustersBackupName` is set to `latest`, the managed clusters are activated on the new hub and this hub becomes a primary hub. When this happens, the restore resource is set to `Finished` and the `syncRestoreWithNewBackups` is ignored, even if set to `true`. The restore operation has completed.

By default, when `syncRestoreWithNewBackups` is set to `true`, the controller checks for new backups every 30 minutes. If new backups are found, it restores the backed up resources. You can update the duration after which you want the controller to check for new backups using this property `restoreSyncInterval`. 

For example, the resource below checks for new backups every 10 minutes.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm-passive-sync
spec:
  syncRestoreWithNewBackups: true # restore again when new backups are available
  restoreSyncInterval: 10m # check for new backups every 10 minutes
  cleanupBeforeRestore: CleanupRestored 
  veleroManagedClustersBackupName: skip
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```

#### Restoring passive resources

Use the [passive sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive.yaml) if you want to restore all resources on the new hub but you don't want to have the managed clusters be managed by the new hub. You can use this restore configuration when the initial hub is still up and you want to prevent the managed clusters to change ownership. You could use this restore option when you want to view the initial hub content using the new hub or to prepare the new hub to take over when needed. In the case of takeover, just restore the managed clusters resources using the [passive activation sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive_activate.yaml); the managed clusters will now connect with the new hub.

#### Restoring activation resources

Use the [passive activation sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive_activate.yaml) when you want for this hub to manage the clusters. In this case it is assumed that the other data has been restored already on this hub using the [passive sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive.yaml)

##### Primary cluster must be shut down

When restoring activation resources using the `veleroManagedClustersBackupName: latest` option on the restore resource, make sure the old hub from where the backups have been created is shut down, otherwise the old hub will try to reconnect with the managed clusters as soon as the managed cluster reconciliation addons find the managed clusters are no longer available, so both hubs will try to manage the clusters at the same time.


#### Restoring all resources

Use the [restore sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore.yaml) if you want to restore all data at once and make this hub take over the managed clusters in one step.

After you create a `Restore.cluster.open-cluster-management.io` resource on the hub, you should be able to run `oc get restore -n open-cluster-management-backup` and get the status of the restore operation. You should also be able to verify on your hub that the backed up resources contained by the backup file have been created.

### Cleaning up the hub before restore
Velero updates existing resources if they have changed with the currently restored backup. It does not clean up delta resources, which are resources created by a previous restore and not part of the currently restored backup. This limits the scenarios that can be used when restoring hub data on a new hub. Unless the restore is applied only once, the new hub could not be relibly used as a passive configuration: the data on this hub is not reflective of the data available with the restored resources.

To address this limitation, when a `Restore.cluster.open-cluster-management.io` resource is created, the Cluster Back up and Restore Operator runs a post restore operation  which will clean up the hub and remove any resources created by a previous acm restore and not part of the currently restored backup.

The post restore cleanup option uses the `cleanupBeforeRestore` property to identify the subset of objects to clean up. These are the options you could set for this clean up: 
- `None` : no clean up necessary, just call Velero restore. This is to be used on a brand new hub and when running the restore and restore all resources, active and passive data.
- `CleanupRestored` : clean up all resources created by a previous acm restore and not part of the currently restored backup.
- `CleanupAll` : clean up all resources on the hub which could be part of an acm backup, even if they were not created as a result of a restore operation. This is to be used when content has been created on this hub before the restore operation is executed. Use this option with extreme caution  as this will also cleanup resources on the hub created by the user, not just by a previously restored backup. It is strongly recommended to use the `CleanupRestored` option instead and to refrain from manually updating hub content when the hub is designated as a passive candidate for a disaster scenario. Use a clean hub as a passive cluster. Avoid  situations where you have to swipe the cluster using the `CleanupAll` option; this is given as a last alternative.

<b>Note:</b> 

1. Velero sets a `PartiallyFailed` status for a velero restore resource if the backup restored had no resources. This means that a `restore.cluster.open-cluster-management.io` resource could be in `PartiallyFailed` status if any of the `restore.velero.io` resources created did not restore any resources because the corresponding backup was empty.

2. The `restore.cluster.open-cluster-management.io` resource is executed once, unless you use the `syncRestoreWithNewBackups:true` to keep restoring passive data when new backups are available.For this case, follow the [restore passive with sync sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive_sync.yaml). After the restore operation is completed, if you want to run another restore operation on the same hub, you have to create a new `restore.cluster.open-cluster-management.io` resource.

3. Although you can create multiple `restore.cluster.open-cluster-management.io` resources, only one is allowed to be executing at any moment in time.

4. The restore operation allows to restore all 3 backup types created by the backup operation, although you can choose to install only a certain type (only managed clusters or only user credentials or only hub resources). 

The restore defines 3 required spec properties, defining the restore logic for the 3 types of backed up files. 
- `veleroManagedClustersBackupName` is used to define the restore option for the managed clusters. 
- `veleroCredentialsBackupName` is used to define the restore option for the user credentials. 
- `veleroResourcesBackupName` is used to define the restore option for the hub resources (applications and policies). 

The valid options for the above properties are : 
  - `latest` - restore the last available backup file for this type of backup
  - `skip` - do not attempt to restore this type of backup with the current restore operation
  - `<backup_name>` - restore the specified backup pointing to it by name

Below you can see a sample available with the operator.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```

### View restore events

Use the `oc describe Restore.cluster.open-cluster-management.io -n <oadp-n> <restore-name>` command to get information about restore events.
A sample output is shown below

```yaml
Spec:
  Cleanup Before Restore:               CleanupRestored
  Restore Sync Interval:                4m
  Sync Restore With New Backups:        true
  Velero Credentials Backup Name:       latest
  Velero Managed Clusters Backup Name:  skip
  Velero Resources Backup Name:         latest
Status:
  Last Message:                     Velero restores have run to completion, restore will continue to sync with new backups
  Phase:                            Enabled
  Velero Credentials Restore Name:  example-acm-credentials-schedule-20220406171919
  Velero Resources Restore Name:    example-acm-resources-schedule-20220406171920
Events:
  Type    Reason                   Age   From                Message
  ----    ------                   ----  ----                -------
  Normal  Prepare to restore:      76m   Restore controller  Cleaning up resources for backup acm-credentials-hive-schedule-20220406155817
  Normal  Prepare to restore:      76m   Restore controller  Cleaning up resources for backup acm-credentials-cluster-schedule-20220406155817
  Normal  Prepare to restore:      76m   Restore controller  Cleaning up resources for backup acm-credentials-schedule-20220406155817
  Normal  Prepare to restore:      76m   Restore controller  Cleaning up resources for backup acm-resources-generic-schedule-20220406155817
  Normal  Prepare to restore:      76m   Restore controller  Cleaning up resources for backup acm-resources-schedule-20220406155817
  Normal  Velero restore created:  74m   Restore controller  example-acm-credentials-schedule-20220406155817
  Normal  Velero restore created:  74m   Restore controller  example-acm-resources-generic-schedule-20220406155817
  Normal  Velero restore created:  74m   Restore controller  example-acm-resources-schedule-20220406155817
  Normal  Velero restore created:  74m   Restore controller  example-acm-credentials-cluster-schedule-20220406155817
  Normal  Velero restore created:  74m   Restore controller  example-acm-credentials-hive-schedule-20220406155817
  Normal  Prepare to restore:      64m   Restore controller  Cleaning up resources for backup acm-resources-schedule-20220406165328
  Normal  Prepare to restore:      62m   Restore controller  Cleaning up resources for backup acm-credentials-hive-schedule-20220406165328
  Normal  Prepare to restore:      62m   Restore controller  Cleaning up resources for backup acm-credentials-cluster-schedule-20220406165328
  Normal  Prepare to restore:      62m   Restore controller  Cleaning up resources for backup acm-credentials-schedule-20220406165328
  Normal  Prepare to restore:      62m   Restore controller  Cleaning up resources for backup acm-resources-generic-schedule-20220406165328
  Normal  Velero restore created:  61m   Restore controller  example-acm-credentials-cluster-schedule-20220406165328
  Normal  Velero restore created:  61m   Restore controller  example-acm-credentials-schedule-20220406165328
  Normal  Velero restore created:  61m   Restore controller  example-acm-resources-generic-schedule-20220406165328
  Normal  Velero restore created:  61m   Restore controller  example-acm-resources-schedule-20220406165328
  Normal  Velero restore created:  61m   Restore controller  example-acm-credentials-hive-schedule-20220406165328
  Normal  Prepare to restore:      38m   Restore controller  Cleaning up resources for backup acm-resources-generic-schedule-20220406171920
  Normal  Prepare to restore:      38m   Restore controller  Cleaning up resources for backup acm-resources-schedule-20220406171920
  Normal  Prepare to restore:      36m   Restore controller  Cleaning up resources for backup acm-credentials-hive-schedule-20220406171919
  Normal  Prepare to restore:      36m   Restore controller  Cleaning up resources for backup acm-credentials-cluster-schedule-20220406171919
  Normal  Prepare to restore:      36m   Restore controller  Cleaning up resources for backup acm-credentials-schedule-20220406171919
  Normal  Velero restore created:  36m   Restore controller  example-acm-credentials-cluster-schedule-20220406171919
  Normal  Velero restore created:  36m   Restore controller  example-acm-credentials-schedule-20220406171919
  Normal  Velero restore created:  36m   Restore controller  example-acm-resources-generic-schedule-20220406171920
  Normal  Velero restore created:  36m   Restore controller  example-acm-resources-schedule-20220406171920
  Normal  Velero restore created:  36m   Restore controller  example-acm-credentials-hive-schedule-20220406171919


```

## Restoring imported managed clusters 

Only managed clusters connected with the primary hub using the hive api will be automatically connected with the new hub where the activation data is restored. These clusters have been created on the primary hub using the `Create cluster` action available from the Clusters tab. Managed clusters connected with the initial hub using the  `Import cluster` action will show up as `Pending Import` when the activation data is restored, and must be imported back on the new hub. The reason the hive managed clusters can be connected with the new hub is that hive stores the managed cluster kubeconfig under the managed cluster's namespace on the hub, and this is being backed up and restored on the new hub. The import controller will next update the bootstrap kubeconfig on the managed cluster using the restored configuration. This information is only available for managed clusters created using the hive api and is not available for imported clusters.<br>

The current workaround for reconnecting imported clusters on the new hub is to manually create the [auto-import-secret](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.5/html/clusters/managing-your-clusters#importing-the-cluster-auto-import-secret) after the restore operation is executed. The `auto-import-secret` should be created under the managed cluster namespace for each cluster in `Pending Import` state, using a kubeconfig or token with enough permissions for the import component to execute the auto import on the new hub. <br>
For a large number of imported managed clusters, this is a very tedious operation since it is executed manually for each managed cluster. It increases the RTO time and requires the user who runs the restore operation to have access for each managed cluster to a token that can be used to connect with the managed cluster. This token must have a `klusterlet` role binding or a role with equivalent permissions.

We propose a new solution for automatically connecting imported clusters to the new hub, using the ManagedServiceAccount feature, as explained below. 

### Automatically connecting clusters using ManagedServiceAccount

The backup controller implements a solution to automatically connect imported clusters on the new hub. This solution is available with the backup controller packaged with ACM and uses the [ManagedServiceAccount](https://github.com/open-cluster-management-io/managed-serviceaccount) component on the primary hub to create a token for each of the imported clusters. This token is backed up under each managed cluster namespace and is set to use a `klusterlet-bootstrap-kubeconfig` `ClusterRole` binding, which allows the token to be used by an auto import operation. The `klusterlet-bootstrap-kubeconfig` `ClusterRole` can only get or update the `bootstrap-hub-kubeconfig` secret. <br>
When the activation data is next restored on the new hub, the restore controller runs a post restore operation and looks for all managed clusters in Pending Import state. For these managed clusters, it checks if there is a valid token generated by the `ManagedServiceAccount` and if found, it creates an `auto-import-secret` using this token. As a result, the import component will try to reconnect the managed cluster and if the cluster is accessible the operation should be successful.

###  Enabling the automatic import feature

The automatic import feature using the ManagedServiceAccount component is disabled by default. To enable this feature: <br>

1. Enable the `ManagedServiceAccount` component on `MultiClusterEngine`. 
```yaml
apiVersion: multicluster.openshift.io/v1
kind: MultiClusterEngine
metadata:
  name: multiclusterhub
spec:
  overrides:
    components:
      - enabled: true
        name: managedserviceaccount <<
```
2. Enable the automatic import feature for the `BackupSchedule.cluster.open-cluster-management.io` resource. 
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: BackupSchedule
metadata:
  name: schedule-acm-msa
spec:
  veleroSchedule: 0 */2 * * *
  veleroTtl: 120h
  useManagedServiceAccount: true  <<
```

The default token validity duration is set to twice the value of `veleroTtl` to increase the chance of the token being valid for all backups storing the token for their entire life cycle. In some cases, you might need to control how long a token is valid by setting a value for the *optional* `managedServiceAccountTTL` property.

Use `managedServiceAccountTTL` with caution if you need to update the default TTL for the generated tokens. Changing the token TTL from the default value might result in producing backups with tokens set to expire during the life cycle of the backup. As a result, the import feature does not work for the managed clusters.

*Important*: Do not use `managedServiceAccountTTL` unless you need to control how long the token is valid.

See the following example for using the `managedServiceAccountTTL` property:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: BackupSchedule
metadata:
  name: schedule-acm-msa
spec:
  veleroSchedule: 0 */2 * * *
  veleroTtl: 120h
  useManagedServiceAccount: true 
  managedServiceAccountTTL: 300h 
```

Once the `useManagedServiceAccount` is set to `true`, the backup controller will start processing imported managed clusters and for each of them:
- Creates a `ManagedServiceAddon` named `managed-serviceaccount`.
- Creates a `ManagedServiceAccount` named `auto-import-account` and sets the token validity as defined by the `BackupSchedule`. 
- The `ManagedServiceAccount` resource triggers on the managed cluster the creation of a token with the same name, which is next pushed back on the hub under the managed cluster namespace. This hub secret will be backed up. <br><b>Note:</b> The token is created only if the managed cluster is accessible. If the managed cluster is not accessible at the time the  ManagedServiceAccount is created, the token will be created at a later time, when the managed cluster becomes available.
- For each of the `ManagedServiceAccount` resources, the backup controller creates a `ManifestWork` to setup on the managed cluster a `klusterlet-bootstrap-kubeconfig` `RoleBinding` for the `ManagedServiceAccount` token. The `klusterlet-bootstrap-kubeconfig` `ClusterRole` can only get or update the `bootstrap-hub-kubeconfig` secret.
<br>

<b>Note:</b>

You can disable the automatic import cluster feature at any time by setting the `useManagedServiceAccount` option to `false` on the `BackupSchedule` resource. Removing the property has the same result since the default value is set to `false`. In this case, the backup controller will remove all created resources, `ManagedServiceAddon`, `ManagedServiceAccount` and `ManifestWork`, which in turn will delete the auto import token, on the hub and managed cluster.
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: BackupSchedule
metadata:
  name: schedule-acm-msa
spec:
  veleroSchedule: 0 */2 * * *
  veleroTtl: 120h
  useManagedServiceAccount: false  <<
```
<br>

### Limitations with the automatic import feature 

There are a set of limitations with the above approach which could result in the managed cluster not being auto imported when moving to a new hub. These are the situations that can result in the managed cluster not being imported:
1. Since the `auto-import-secret` created on restore uses the `ManagedServiceAccount` token to connect to the managed cluster, the managed cluster must also provide the kube `apiserver` information. The `apiserver` must be set on the `ManagedCluster` resource as in the sample below. Only OCP clusters have this `apiserver` setup automatically when the cluster is imported on the hub. For any other type of managed clusters, such as EKS clusters, this information must be set manually by the user, otherwise the automatic import feature will ignore these clusters and they stay in `Pending Import` when moved to the restore hub. 
```yaml
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: managed-cluster-name
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
  managedClusterClientConfigs:
      url: <apiserver>
```
2. The backup controller is regularly looking for imported managed clusters and it creates the [ManagedServiceAccount](https://github.com/open-cluster-management-io/managed-serviceaccount) resource under the managed cluster namespace as soon as such managed cluster is found. This should trigger a token creation on the managed cluster. If the managed cluster is not accessible at the time this operation is executed though, for example the managed cluster is hibernating or is down, the `ManagedServiceAccount` is unable to create the token. As a result, if a hub backup is run at this time, the backup will not contain a token to auto import the managed cluster.
3. It is possible for a `ManagedServiceAccount` secret to not be included in a backup if the backup schedule runs before the backup label is set on the `ManagedServiceAccount` secret. `ManagedServiceAccount` secrets don't have the `cluster.open-cluster-management.io/backup` label set on creation. For this reason, the backup controller looks regularly for `ManagedServiceAccount` secrets under the managed clusters namespaces, and adds the backup label if not found. 
4. If the `auto-import-account` secret token is valid and is backed up but the restore operation is run at a time when the token available with the backup has already expired, the auto import operation fails. In this case, the `restore.cluster.open-cluster-management.io` resource status should report the invalid token issue for each managed cluster in this situation. 

## Backup validation using a Policy

The Cluster Backup and Restore Operator [helm chart](https://github.com/stolostron/multiclusterhub-operator/tree/main/pkg/templates/charts/toggle/cluster-backup) installs [backup-restore-enabled](https://github.com/stolostron/multiclusterhub-operator/blob/main/pkg/templates/charts/toggle/cluster-backup/templates/hub-backup-pod.yaml) and [backup-restore-auto-import](https://github.com/stolostron/multiclusterhub-operator/blob/main/pkg/templates/charts/toggle/cluster-backup/templates/hub-backup-auto-import.yaml) Policies, designed to provide information on issues with the backup and restore component. 

These policies include a set of templates that check for the following constraints and alerts when any of them are violated.

### OADP channel validation
When you enable the backup component on the MultiClusterHub, the cluster backup and restore operator Helm chart can automatically install the OADP operator in the open-cluster-management-backup namespace, or you can install it manually in that namespace. The OADP channel you select for manual installation must match or exceed the version set by the Red Hat Advanced Cluster Management backup and restore operator Helm chart. Since the OADP Operator and Velero Custom Resource Definitions (CRDs) are cluster-scoped, you cannot have multiple versions on the same cluster. You must install the same version in the open-cluster-management-backup namespace and any other namespaces.

The following templates check for availability and validate the OADP installation:

- `oadp-operator-exists` template verifies if the OADP operator is installed in the open-cluster-management-backup namespace.

- `oadp-channel-validation` template ensures the OADP operator version in the open-cluster-management-backup namespace matches or exceeds the version set by the Red Hat Advanced Cluster Management cluster backup and restore operator.

- `custom-oadp-channel-validation` template checks if OADP operators in other namespaces match the version in the open-cluster-management-backup namespace.

### Pod validation
The following templates check the pod status for the backup component and dependencies:
- `acm-backup-pod-running` template checks if Backup and Restore operator pod is running 
- `oadp-pod-running` template checks if OADP operator pod is running
- `velero-pod-running` template checks if Velero pod is running

### Data Protection Application validation
- `data-protection-application-available` template checks if a  `DataProtectionApplication.oadp.openshift.io` resource was created. This OADP resource sets up Velero configurations.  

### Backup Storage validation
- `backup-storage-location-available` template checks if a  `BackupStorageLocation.velero.io` resource was created and the status is `Available`. This implies that the connection to the backup storage is valid.

### BackupSchedule collision validation
- `acm-backup-clusters-collision-report` template checks that if a `BackupSchedule.cluster.open-cluster-management.io` exists on the current hub, its state is not `BackupCollision`. This verifies that the current hub is not in collision with any other hub when writing backup data to the storage location. For a definition of the BackupCollision state read the [Backup Collisions section](#backup-collisions) 

### BackupSchedule and Restore status validation
- `acm-backup-phase-validation` template checks that if a `BackupSchedule.cluster.open-cluster-management.io` exists on the current cluster, the status is not in (Failed, or empty state). This ensures that if this cluster is the primary hub and is generating backups, the `BackupSchedule.cluster.open-cluster-management.io` status is healthy.
- the same template checks that if a `Restore.cluster.open-cluster-management.io` exists on the current cluster, the status is not in (Failed, or empty state). This ensures that if this cluster is the secondary hub and is restoring backups, the `Restore.cluster.open-cluster-management.io` status is healthy.

### Backups exist validation
- `acm-managed-clusters-schedule-backups-available` template checks if `Backup.velero.io` resources are available at the location specified by the `BackupStorageLocation.velero.io` and the backups were created by a `BackupSchedule.cluster.open-cluster-management.io` resource. This validates that the backups have been executed at least once, using the Backup and restore operator.

### Backups are running to completion
- `acm-backup-in-progress-report` template checks if `Backup.velero.io` resources are stuck InProgress. This validation is added because with a large number of resources, the velero pod restarts as the backup executes and the backup stays in progress without proceeding to completion. During a normal backup though, the backup resources are in progress at some point of the execution but they don't get stuck in this phase and run to completion. It is normal to see the `acm-backup-in-progress-report` template reporting a warning during the time the schedule is running and backups are in progress.

### Backups are actively running as a cron job
- This validation is done by the `backup-schedule-cron-enabled` template. It checks that a `BackupSchedule.cluster.open-cluster-management.io` is actively running and creating new backups at the storage location.  The template verifies there is a `Backup.velero.io` with a label `velero.io/schedule-name: acm-validation-policy-schedule` at the storage location. The `acm-validation-policy-schedule` backups are set to expire after the time set for the backups cron schedule. If no cron job is running anymore to create backups, the old `acm-validation-policy-schedule` backup is deleted because it expired and a new one is not created. So if no `acm-validation-policy-schedule` backup exists at any moment in time, it means that there are no active cron jobs generating acm backups.

This Policy is intended to help notify the Hub admin of any backup issues as the hub is active and expected to produce or restore backups.

### Automatic import feature validation
The following templates verify the existence of a ManagedServiceAccount secret and the required label to be included in ACM backups:
- `auto-import-account-secret` template checks whether a ManagedServiceAccount secret is created under managed cluster namespaces other than local-cluster. The backup controller regularly scans for imported managed clusters and creates the ManagedServiceAccount resource under the managed cluster namespace as soon as such a managed cluster is discovered. This process triggers token creation on the managed cluster. However, if the managed cluster is not accessible at the time of this operation (e.g., the managed cluster is hibernating or down), the ManagedServiceAccount is unable to create the token. Consequently, if a hub backup is executed during this period, the backup will lack a token for auto-importing the managed cluster.
- `auto-import-backup-label` template verifies the existence of a ManagedServiceAccount secret under managed cluster namespaces other than local-cluster. If found, it enforces the `cluster.open-cluster-management.io/backup` label on it if it doesn't already exist. This label is crucial for including the ManagedServiceAccount secrets in ACM backups.

## Active passive configuration design

### Setting up an active passive configuration

In an active passive configuration you have 
- one hub, called active or primary hub, which manages the clusters and is backing up resources at defined time intervals, using the `BackupSchedule.cluster.open-cluster-management.io` resource
- one or more passive hubs, which are continously retrieving the latest backups and restoring the [passive data](#passive-data). The passive hubs use the `Restore.cluster.open-cluster-management.io` resource to keep restoring passive data posted by the primary hub, when new backup data is available. These hubs are on standby to become a primary hub when the primary hub goes down. They are connected to the same storage location where the primary hub backs up data so they can access the primary hub backups. For more details on how to setup this automatic restore configuration see the [Restoring passive resources and check for new backups](#restoring-passive-resources-and-check-for-new-backups) section.

In the image below, the active hub manages the remote clusters and backs up hub data at regular intervals.
The passive hubs restore this data, except for the managed clusters activation data, which would move the managed clusters to the passive hub. The passive hubs can restore the passive data [continously](#restoring-passive-resources-and-check-for-new-backups), or as a [one time operation](#restoring-passive-resources).

![Active Passive Configuration Dataflow](images/active-passive-configuration-dataflow.png)

### Disaster recovery

When the primary hub goes down, one of the passive hubs is chosen by the admin to take over the managed clusters. In the image below, the admin decides to use Hub N as the new primary hub. These are the steps taken to have Hub N become a primary hub: 
1. Hub N restores the [Managed Cluster activation data](#managed-clusters-activation-data). At this point, the managed clusters connect with Hub N.
2. The admin starts a backup on the new primary Hub N, by creating a `BackupSchedule.cluster.open-cluster-management.io` resource and storing the backups at the same storage location as the initial primary hub. All other passive hubs will now restore [passive data](#passive-data) using the backup data created by the new primary hub, unaware that the primary hub has changed. Hub N behaves now as the primary hub, managing clusters and backing up data.


![Disaster Recovery](images/disaster-recovery.png)

Note: 
- Step 1 is not automated since the admin should decide if the primary hub is down and needs to be replaced, or there is some network communication error between the hub and managed clusters. The admin also decides which passive hub should become primary. If desired, this step could be automated using the Policy integration with Ansible jobs: the admin can setup an Ansible job to be executed when the [Backup Policy](#backup-validation-using-a-policy) reports backup execution errors.
- Although Step 2 above is manual, the admin will be notified using the [Backups are actively running as a cron job](#backups-are-actively-running-as-a-cron-job) if he omits to start creating backups from the new primary hub. 



## Setting up your development environment

### Prerequiste tools
- Operator SDK

### Installation

To install the Cluster Back up and Restore Operator, you can either run it outside the cluster,
for faster iteration during development, or inside the cluster.

First we require installing the Operator CRD:

```shell
make build
make install
```

Then proceed to the installation method you prefer below.

#### Outside the cluster

If you would like to run the Cluster Back up and Restore Operator outside the cluster, execute:

```shell
make run
```

#### Inside the cluster

If you would like to run the Operator inside the cluster, you'll need to build
a container image. You can use a local private registry, or host it on a public
registry service like [quay.io](https://quay.io).

1. Build your image:
    ```shell
    make docker-build IMG=<registry>/<imagename>:<tag>
    ```
1. Push the image:
    ```shell
    make docker-push IMG=<registry>/<imagename>:<tag>
    ```
1. Deploy the Operator:
    ```shell
    make deploy IMG=<registry>/<imagename>:<tag>
    ```


### Usage

Here you can find an example of a `backupschedule.cluster.open-cluster-management.io` resource definition:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: BackupSchedule
metadata:
  name: schedule-acm
  namespace: open-cluster-management-backup
spec:
  veleroSchedule: 0 */6 * * * # Create a backup every 6 hours
  veleroTtl: 72h # deletes scheduled backups after 72h; optional, if not specified, the maximum default value set by velero is used - 720h
```

- `veleroSchedule` is a required property and defines a cron job for scheduling the backups.

- `veleroTtl` is an optional property and defines the expiration time for a scheduled backup resource. If not specified, the maximum default value set by velero is used, which is 720h.


This is an example of a `restore.cluster.open-cluster-management.io` resource definition

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```


In order to create an instance of `backupschedule.cluster.open-cluster-management.io` or `restore.cluster.open-cluster-management.io` you can start from one of the [sample configurations](config/samples).

```shell
kubectl create -n open-cluster-management-backup -f config/samples/cluster_v1beta1_backupschedule.yaml
kubectl create -n open-cluster-management-backup -f config/samples/cluster_v1beta1_restore.yaml
```

### Testing

#### Schedule  a backup 

After you create a `backupschedule.cluster.open-cluster-management.io` resource you should be able to run `oc get bsch -n open-cluster-management-backup` and get the status of the scheduled cluster backups.

In the example below, you have created a `backupschedule.cluster.open-cluster-management.io` resource named schedule-acm.

The resource status shows the definition for the 3 `schedule.velero.io` resources created by this cluster backup scheduler. 

```
$ oc get bsch -n   open-cluster-management-backup
NAME           PHASE
schedule-acm   
```

#### Restore a backup

After you create a `restore.cluster.open-cluster-management.io` resource on the new hub, you should be able to run `oc get restore -n open-cluster-management-backup` and get the status of the restore operation. You should also be able to verify on the new hub that the backed up resources contained by the backup file have been created.

Velero sets a `PartiallyFailed` status for a velero restore resource if the backup restored had no resources. This means that a `restore.cluster.open-cluster-management.io` resource could be in `PartiallyFailed` status if any of the `restore.velero.io` resources created did not restore any resources because the corresponding backup was empty. 

The restore defines 3 required spec properties, defining the restore logic for the 3 types of backed up files. 
- `veleroManagedClustersBackupName` is used to define the restore option for the managed clusters. 
- `veleroCredentialsBackupName` is used to define the restore option for the user credentials. 
- `veleroResourcesBackupName` is used to define the restore option for the hub resources (applications and policies). 

The valid options for the above properties are : 
  - `latest` - restore the last available backup file for this type of backup
  - `skip` - do not attempt to restore this type of backup with the current restore operation
  - `<backup_name>` - restore the specified backup pointing to it by name

The `cleanupBeforeRestore` property is used to clean up resources before the restore is executed. More details about this options [here](#cleaning-up-the-hub-before-restore).

<b>Note:</b> The `restore.cluster.open-cluster-management.io` resource is executed once. After the restore operation is completed, if you want to run another restore operation on the same hub, you have to create a new `restore.cluster.open-cluster-management.io` resource.


Below is an example of a `restore.cluster.open-cluster-management.io` resource, restoring all 3 types of backed up files, using the latest available backups:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```

You can define a restore operation where you only restore the managed clusters:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: None
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: skip
  veleroResourcesBackupName: skip
```

The sample below restores the managed clusters from backup `acm-managed-clusters-schedule-20210902205438` :

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: None
  veleroManagedClustersBackupName: acm-managed-clusters-schedule-20210902205438
  veleroCredentialsBackupName: skip
  veleroResourcesBackupName: skip
```

## ACM Backup and Restore Performance in a Large-Scale Environment

The following performance results are from ACM backup and restore operations conducted in a large-scale environment, where a central hub manages 3,500 Single Node OpenShift (SNO) clusters.

ACM version: 2.11.0-DOWNSTREAM-2024-07-10-21-49-48 (RC3)

### First attempt with cleanupBeforeRestore set to None:

1. Backup

```
# oc get backup -n open-cluster-management-backup  -ojson | jq '.items[] | .metadata.name,.status.phase, .status.startTimestamp, .status.completionTimestamp,.status.progress.itemsBackedUp'
"acm-credentials-schedule-20240715173125"
"Completed"
"2024-07-15T17:31:25Z"
"2024-07-15T17:32:33Z"
18220
"acm-managed-clusters-schedule-20240715173125"
"Completed"
"2024-07-15T17:32:33Z"
"2024-07-15T17:34:33Z"
58304
"acm-resources-generic-schedule-20240715173125"
"Completed"
"2024-07-15T17:34:33Z"
"2024-07-15T17:36:37Z"
1
"acm-resources-schedule-20240715173125"
"Completed"
"2024-07-15T17:36:37Z"
"2024-07-15T17:37:14Z"
7466
"acm-validation-policy-schedule-20240715173125"
"Completed"
"2024-07-15T17:37:14Z"
"2024-07-15T17:37:15Z"
1

# for i in $(mc ls  minio/dr4hub/velero/backups | awk '{print $5}' ); do echo $i $(mc ls  minio/dr4hub/velero/backups/"$i" --json | jq -s 'map(.size) | add' | numfmt --to=iec-i --suffix=B --padding=7); done
acm-credentials-schedule-20240715173125/ 64MiB
acm-managed-clusters-schedule-20240715173125/ 39MiB
acm-resources-generic-schedule-20240715173125/ 21KiB
acm-resources-schedule-20240715173125/ 3.0MiB
acm-validation-policy-schedule-20240715173125/ 16KiB
```

2. Restore passive - cleanupBeforeRestore=None
   
```
# oc get restore -A
NAMESPACE                        NAME                  PHASE      MESSAGE
open-cluster-management-backup   restore-acm-passive   Finished   All Velero restores have run successfully


# oc get restore.velero -n open-cluster-management-backup -ojson | jq -r '.items[] | .metadata.name,.status.phase, .status.startTimestamp, .status.completionTimestamp,.status.progress.itemsRestored'
restore-acm-passive-acm-credentials-schedule-20240715173125
Completed
2024-07-15T17:59:10Z
2024-07-15T18:04:47Z
18220
restore-acm-passive-acm-resources-generic-schedule-20240715173125
Completed
2024-07-15T18:07:16Z
2024-07-15T18:07:16Z
1
restore-acm-passive-acm-resources-schedule-20240715173125
Completed
2024-07-15T18:04:47Z
2024-07-15T18:07:16Z
7451
```

### Second attempt with cleanupBeforeRestore set to CleanupRestored:

1. Backup:
   
```
# oc get backup -n open-cluster-management-backup  -ojson | jq '.items[] | .metadata.name,.status.phase, .status.startTimestamp, .status.completionTimestamp,.status.progress.itemsBackedUp'
"acm-credentials-schedule-20240724203526"
"Completed"
"2024-07-24T20:35:27Z"
"2024-07-24T20:36:35Z"
17678
"acm-managed-clusters-schedule-20240724203526"
"Completed"
"2024-07-24T20:36:34Z"
"2024-07-24T20:38:37Z"
56556
"acm-resources-generic-schedule-20240724203526"
"PartiallyFailed"
"2024-07-24T20:38:37Z"
"2024-07-24T20:40:51Z"
1
"acm-resources-schedule-20240724203526"
"Completed"
"2024-07-24T20:40:51Z"
"2024-07-24T20:41:23Z"
7299
"acm-validation-policy-schedule-20240724203526"
"Completed"
"2024-07-24T20:41:23Z"
"2024-07-24T20:41:24Z"
1

# for i in $(./mc ls minio/dr4hub/velero/backups | awk '{print $5}' ); do echo $i $(./mc ls minio/dr4hub/velero/backups/"$i" --json | jq -s 'map(.size) | add' | numfmt --to=iec-i --suffix=B --padding=7); done
acm-credentials-schedule-20240724203526/ 62MiB
acm-managed-clusters-schedule-20240724203526/ 38MiB
acm-resources-generic-schedule-20240724203526/ 21KiB
acm-resources-schedule-20240724203526/ 2.9MiB
acm-validation-policy-schedule-20240724203526/ 16KiB
```

2. Restore passive - cleanupBeforeRestore=CleanupRestored

```
# oc get restore -n open-cluster-management-backup restore-acm-passive -oyaml 
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"cluster.open-cluster-management.io/v1beta1","kind":"Restore","metadata":{"annotations":{},"name":"restore-acm-passive","namespace":"open-cluster-management-backup"},"spec":{"cleanupBeforeRestore":"CleanupRestored","veleroCredentialsBackupName":"latest","veleroManagedClustersBackupName":"skip","veleroResourcesBackupName":"latest"}}
  creationTimestamp: "2024-07-24T20:48:16Z"
  generation: 1
  name: restore-acm-passive
  namespace: open-cluster-management-backup
  resourceVersion: "30421543"
  uid: d9f6b3f3-98df-44b2-be6d-0c024317790d
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroCredentialsBackupName: latest
  veleroManagedClustersBackupName: skip
  veleroResourcesBackupName: latest
status:
  lastMessage: All Velero restores have run successfully
  phase: Finished
  veleroCredentialsRestoreName: restore-acm-passive-acm-credentials-schedule-20240724203526
  veleroGenericResourcesRestoreName: restore-acm-passive-acm-resources-generic-schedule-20240724203526
  veleroResourcesRestoreName: restore-acm-passive-acm-resources-schedule-20240724203526


# oc get restore.velero -n open-cluster-management-backup -ojson | jq -r '.items[] | .metadata.name,.status.phase, .status.startTimestamp, .status.completionTimestamp,.status.progress.itemsRestored'
restore-acm-passive-acm-credentials-schedule-20240724203526
Completed
2024-07-24T20:48:16Z
2024-07-24T20:53:52Z
17678
restore-acm-passive-acm-resources-generic-schedule-20240724203526
Completed
2024-07-24T20:56:29Z
2024-07-24T20:56:30Z
1
restore-acm-passive-acm-resources-schedule-20240724203526
Completed
2024-07-24T20:53:52Z
2024-07-24T20:56:29Z
7284
```

## Virtual Machine Backup and Restore Performance in a Large-Scale Environment

This document summarizes backup and restore performance results for virtual machines (VMs) in a large-scale environment. Tests were conducted on sizeable VMs within a dedicated test cluster.

### Test Environment

- **Platform:** Bare Metal (KVM) – OpenShift Container Platform (OCP) 4.17.17  
- **Storage:** OpenShift Data Foundation (ODF)  
  - **Raw Capacity:** 1.46 TiB  
- **Backup Target:** AWS S3  
- **ACM Version:** 2.13.2  

---

### Test Scenarios and Results

#### Scenario 1

- **VM Count:** 1  
- **Disk Size:** 100 GiB  
- **Backup Time:** ~7 minutes  
  - **Start:** `2025-03-27T19:42:02Z`  
  - **End:** `2025-03-27T19:49:24Z`  
- **Restore Time:** ~15 minutes  
  - **Start:** `2025-03-27T19:53:20Z`  
  - **End:** `2025-03-27T20:08:33Z`

---

#### Scenario 2

- **VM Count:** 1  
- **Disk Size:** 200 GiB  
- **Backup Time:** ~13 minutes  
  - **Start:** `2025-03-27T20:50:08Z`  
  - **End:** `2025-03-27T21:03:10Z`  
- **Restore Time:** ~22 minutes  
  - **Start:** `2025-03-27T21:15:02Z`  
  - **End:** `2025-03-27T21:42:49Z`

---

#### Scenario 3

- **VM Count:** 1  
- **Disk Size:** 300 GiB  
- **Backup Time:** ~20 minutes  
  - **Start:** `2025-03-27T18:11:08Z`  
  - **End:** `2025-03-27T18:30:20Z`  
- **Restore Time:** ~50 minutes  
  - **Start:** `2025-03-27T18:46:50Z`  
  - **End:** `2025-03-27T19:26:49Z`

---

#### Scenario 4

- **VM Count:** 3  
- **Disk Size:** 100 GiB (each)  
- **Backup Time:** ~8 minutes  
  - **Start:** `2025-04-07T13:37:06Z`  
  - **End:** `2025-04-07T13:45:04Z`  
- **Restore Time:** ~28 minutes  
  - **Start:** `2025-04-07T14:48:48Z`  
  - **End:** `2025-04-07T15:16:35Z`

---

#### Scenario 5

- **VM Count:** 2  
- **Disk Size:** 200 GiB (each)  
- **Backup Time:** ~13 minutes  
  - **Start:** `2025-04-07T15:31:04Z`  
  - **End:** `2025-04-07T15:44:36Z`  
- **Restore Time:** ~55 minutes  
  - **Start:** `2025-04-07T15:52:59Z`  
  - **End:** `2025-04-07T16:43:05Z`

---

### Notes

- **Storage Requirement for Restore:**
  Ensure that free/raw storage capacity is **greater than three times** the total size of the VM disks being restored.  
  ODF’s default `full ratio` threshold is **85%**, so available storage must stay below this threshold for restore operations to complete successfully.


## OADP Version Relationship

The Cluster Back up and Restore Operator chart automatically installs the [OADP Operator](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md), in the `open-cluster-management-backup` namespace, which is the namespace where the chart is installed. Here is the default mapping of versions:

| ACM Version | OADP Version |
|:------------| -----------: |
| 2.11        | 1.4          |
| 2.10.4      | 1.4          |
| 2.10        | 1.3          |
| 2.9.3       | 1.3          |
| 2.9         | 1.2          |
| 2.8.5       | 1.3          |
| 2.8.4       | 1.2          |
| 2.8         | 1.1          |

## Using custom OADP Version

The OADP version installed by the backup and restore operator may be overridden using this annotation on the MultiClusterHub resource:

  `installer.open-cluster-management.io/oadp-subscription-spec: '{"channel": "stable-1.4"}'`


You set this annotation on the MultiClusterHub before enabling the `cluster-backup` option on the MultiClusterHub resource. Below is an example of the annotation used to install the OADP 1.5 version. 

```yaml
apiVersion: operator.open-cluster-management.io/v1
kind: MultiClusterHub
metadata:
  annotations:
    installer.open-cluster-management.io/oadp-subscription-spec: '{"channel": "stable-1.5","installPlanApproval": "Automatic","name":
      "redhat-oadp-operator","source": "redhat-operators","sourceNamespace": "openshift-marketplace"}'
  name: multiclusterhub
spec: {}
```

Note that if you use this option to get a different OADP version than the one installed by default by the backup and restore operator, you need to make sure this version is supported on the OpenShift version used by the hub cluster. Use this override option with caution as it may result in unsupported configurations.

Below is a list of ACM versions supporting to install a custom OADP version using this annotation:

| ACM Version   | Support different OADP version |
|:--------------| -----------: |
| 2.11 and up   | <b>Yes</b>   |
| 2.10.5        | <b>Yes</b>   |
| 2.10 - 2.10.4 | No           |
| 2.9.5         | <b>Yes</b>   |
| 2.9 - 2.9.4   | No           |
| 2.8.8*        | <b>Yes</b>   |
| 2.8 - 2.8.7   | No           |
| 2.7           | No           |


<!---
Date: Sept/9/2024
-->