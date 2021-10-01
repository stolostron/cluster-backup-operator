# cluster-backup-operator
Cluster Back up and Restore Operator. 
------

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

  - [Work in Progress](#work-in-progress)
  - [Community, discussion, contribution, and support](#community-discussion-contribution-and-support)
  - [License](#license)
  - [Getting Started](#getting-started)
  - [Design](#design)
    - [Scheduling a cluster backup](#scheduling-a-cluster-backup)
    - [Restoring a backup](#restoring-a-backup)
- [Setting up Your Dev Environment](#setting-up-your-dev-environment)
  - [Prerequiste Tools](#prerequiste-tools)
  - [Installation](#installation)
    - [Outside the Cluster](#outside-the-cluster)
    - [Inside the Cluster](#inside-the-cluster)
- [Usage](#usage)
- [Testing](#testing)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

------

## Work in Progress
We are in the process of enabling this repo for community contribution. See wiki [here](https://open-cluster-management.io/concepts/architecture/).

## Community, discussion, contribution, and support

Check the [CONTRIBUTING Doc](CONTRIBUTING.md) for how to contribute to the repo.

## License

This project is licensed under the *Apache License 2.0*. A copy of the license can be found in [LICENSE](LICENSE).


## Getting Started
The Cluster Back up and Restore Operator runs on the hub and depends on the [OADP Operator](https://github.com/openshift/oadp-operator) to install [Velero](https://velero.io/) on the ACM hub, which is then used to backup and restore ACM hub resources. 

Before you can use the cluster operator, you first need to install the OADP Operator as described [here](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md).
Make sure you follow the steps to create the [secret for the cloud storage](https://github.com/openshift/oadp-operator#creating-credentials-secret) where the backups are going to be saved, then use that secret when creating the [Velero resource](https://github.com/openshift/oadp-operator#creating-velero-cr).

The Cluster Back up and Restore Operator resources must be created in the same namespace where the OADP Operator is installed. 


## Design
The operator defines the `BackupSchedule.cluster.open-cluster-management.io` resource, used to setup acm backup schedules, and `Restore.cluster.open-cluster-management.io` resource, used to process and restore these backups.
The operator creates corresponding Velero resources and sets the options needed to backup remote clusters and any other hub resources that needs to be restored.

![Cluster Backup Controller Dataflow](images/cluster-backup-controller-dataflow.png)

## Scheduling a cluster backup 

After you create a `backupschedule.cluster.open-cluster-management.io` resource you should be able to run `oc get bsch -n <oadp-operator-ns>` and get the status of the scheduled cluster backups. The `<oadp-operator-ns>` is the namespace where BackupSchedule was created and it should be the same namespace where the OADP Operator was installed.

The  `backupschedule.cluster.open-cluster-management.io` creates 3 `schedule.velero.io` resources:
- `acm-managed-clusters-schedule`, used to schedule backups for the managed cluster resources, including: managed clusters, cluster pools and cluster sets. 
  - <b>Note</b>:
    - Only managed clusters created using the hive api will be automatically imported when the backup is restored on another hub. All other managed clusters will show up as `Pending Import` and must be imported back on the new hub.
    - When restoring a backup on a new hub, make sure the old hub from where the backup was created is shut down, otherwise the old hub will try to reimport the managed clusters as soon as the managed cluster reconciliation finds the managed clusters are no longer available.
    - The following resources are being picked up by this backup; they are required for restoring all managed clusters information on the new hub: 
      - Secrets and config maps from the `hive` and `openshift-operator-lifecycle-manager` namespaces and from all the `ManagedClusters` resources namespaces created on the hub.
      - Cluster level resource `ManagedCluster`.
      - Other namespaced scoped resources used to restore the managed cluster details: `ServiceAccount`, `ManagedClusterInfo`, `ManagedClusterSet`, `ManagedClusterSetBindings`, `KlusterletAddonConfig`, `ManagedClusterView`, `ClusterPool`, `ClusterProvision`, `ClusterDeployment`, `ClusterSyncLease`, `ClusterSync`, `ClusterCurator`.
- `acm-credentials-schedule`, used to schedule backups for the user created credentials and any copy of those credentials. These credentials are identified by the `cluster.open-cluster-management.io/type` label selector; all secrets defining the label selector will be included in the backup.
  - <b>Note</b>: If you have any user defined private channels, you can include the channel secrets in this credentials backup if you set the `cluster.open-cluster-management.io/type` label selector to this secret. Without this, channel secrets will not be picked up by the cluster backup and will have to be recreated on the restored cluster.
- `acm-resources-schedule`, used to schedule backups for the applications and policy resources, including any  required resources, such as `channels`, `subscriptions`, `deployables` and `placementRules` for applications and `placementBindings`, `placement`, `placementDecisions` for `policies`. No resources are being collected from the `local-cluster` or `open-cluster-management` namespaces.

## Restoring a backup

In a usual restore scenario, the hub where the backups have been executed becomes unavailable and data backed up needs to be moved to a new hub. This is done by running the cluster restore operation on the hub where the backed up data needs to be moved to. In this case, the restore operation is executed on a different hub than the one where the backup was created. 

There are also cases where you want to restore the data on the same hub where the backup was collected, in order to recover data from a previous snapshot. In this case both restore and backup operations are executed on the same hub.

After you create a `restore.cluster.open-cluster-management.io` resource on the hub, you should be able to run `oc get restore -n <oadp-operator-ns>` and get the status of the restore operation. You should also be able to verify on your  hub that the backed up resources contained by the backup file have been created.

<b>Note:</b> The `restore.cluster.open-cluster-management.io` resource is executed once. After the restore operation is completed, if you want to run another restore operation on the same hub, you have to create a new `restore.cluster.open-cluster-management.io` resource.

The restore operation allows to restore all 3 backup types created by the backup operation, although you can choose to install only a certain type (only managed clusters or only user credentials or only hub resources). 

The restore defines 3 required spec properties, defining the restore logic for the 3 type of backed up files. 
- `veleroManagedClustersBackupName` is used to define the restore option for the managed clusters. 
- `veleroCredentialsBackupName` is used to define the restore option for the user credentials. 
- `veleroResourcesBackupName` is used to define the restore option for the hub resources (applications and policies). 

The valid options for the above properties are : 
  - `latest` - restore the last available backup file for this type of backup
  - `skip` - do not attempt to restore this type of backup with the current restore operation
  - `<backup_name>` - restore the specified backup pointing to it by name

Below you can see the sample available with the operator.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
spec:
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```

# Setting up Your Dev Environment

## Prerequiste Tools
- Operator SDK

## Installation

To install the Cluster Back up and Restore Operator, you can either run it outside the cluster,
for faster iteration during development, or inside the cluster.

First we require installing the Operator CRD:

```shell
make build
make install
```

Then proceed to the installation method you prefer below.

### Outside the Cluster

If you would like to run the Cluster Back up and Restore Operator outside the cluster, execute:

```shell
make run
```

### Inside the Cluster

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


## Usage

Before using Cluster Back up and Restore Operator backup or restore support you have to install the [OADP Operator](https://github.com/openshift/oadp-operator) which will install [Velero](https://velero.io/). 

Make sure you follow the OADP Operator installation instructions and create a Velero resource and a valid connection to a backup storage location where backups will be stored. Check the install and setup steps [here](https://github.com/openshift/oadp-operator#installing-operator).

The Cluster Back up and Restore Operator resources must be created in the same namespace where the OADP Operator is installed. 

If you are trying to use the Cluster Backup and Restore Operator to schedule data backups, you have to create a `backupschedule.cluster.open-cluster-management.io` resource which will be consumed by the operator and create all the necessary intermediary schedule backup resources.

If you are trying to use the Cluster Back up and Restore Operator to restore a backup, then you have to create a `restore.cluster.open-cluster-management.io` resource which will run the restore and execute any other post restore operations, such as registering restored remote clusters with the new hub.

Here you can find an example of a `backupschedule.cluster.open-cluster-management.io` resource definition:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: BackupSchedule
metadata:
  name: schedule-acm
spec:
  maxBackups: 10 # maximum number of backups after which old backups should be removed
  veleroSchedule: 0 */6 * * * # Create a backup every 6 hours
  veleroTtl: 72h # deletes scheduled backups after 72h; optional, if not specified, the maximum default value set by velero is used - 720h
```

- `maxBackup` is a required property and represents the maximum number of backups after which old backups are being removed.

- `veleroSchedule` is a required property and defines a cron job for scheduling the backups.

- `veleroTtl` is an optional property and defines the expiration time for a scheduled backup resource. If not specified, the maximum default value set by velero is used, which is 720h.


This is an example of a `restore.cluster.open-cluster-management.io` resource definition

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
spec:
  backupName: acm-managed-clusters-schedule-20210902205438
```

The `backupName` represents the name of the `backup.velero.io` resource to be restored on the hub where the  `restore.cluster.open-cluster-management.io` resource was created.

In order to create an instance of `backupschedule.cluster.open-cluster-management.io` or `restore.cluster.open-cluster-management.io` you can start from one of the [sample configurations](config/samples).
Replace the `<oadp-operator-ns>` with the namespace name used to install the OADP Operator (the default value for the OADP Operator install namespace is `oadp-operator`).


```shell
kubectl create -n <oadp-operator-ns> -f config/samples/cluster_v1beta1_backupschedule.yaml
kubectl create -n <oadp-operator-ns> -f config/samples/cluster_v1beta1_restore.yaml
```

# Testing

## Schedule  a backup 

After you create a `backupschedule.cluster.open-cluster-management.io` resource you should be able to run `oc get bsch -n <oadp-operator-ns>` and get the status of the scheduled cluster backups.

In the example below, you have created a `backupschedule.cluster.open-cluster-management.io` resource named schedule-acm.

The resource status shows the definition for the 3 `schedule.velero.io` resources created by this cluster backup scheduler. 

```
$ oc get bsch -n <oadp-operator-ns>
NAME           PHASE
schedule-acm   
```

## Restore a backup

After you create a `restore.cluster.open-cluster-management.io` resource on the new hub, you should be able to run `oc get restore -n <oadp-operator-ns>` and get the status of the restore operation. You should also be able to verify on the new hub that the backed up resources contained by the backup file have been created.

The restore defines 3 required spec properties, defining the restore logic for the 3 type of backed up files. 
- `veleroManagedClustersBackupName` is used to define the restore option for the managed clusters. 
- `veleroCredentialsBackupName` is used to define the restore option for the user credentials. 
- `veleroResourcesBackupName` is used to define the restore option for the hub resources (applications and policies). 

The valid options for the above properties are : 
  - `latest` - restore the last available backup file for this type of backup
  - `skip` - do not attempt to restore this type of backup with the current restore operation
  - `<backup_name>` - restore the specified backup pointing to it by name

<b>Note:</b> The `restore.cluster.open-cluster-management.io` resource is executed once. After the restore operation is completed, if you want to run another restore operation on the same hub, you have to create a new `restore.cluster.open-cluster-management.io` resource.


Below is an example of a `restore.cluster.open-cluster-management.io` resource, restoring all 3 types of backed up files, using the latest available backups:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
spec:
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
spec:
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
spec:
  veleroManagedClustersBackupName: acm-managed-clusters-schedule-20210902205438
  veleroCredentialsBackupName: skip
  veleroResourcesBackupName: skip
```
