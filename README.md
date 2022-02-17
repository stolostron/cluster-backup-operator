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
    - [What is backed up](#what-is-backed-up)
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
The Cluster Back up and Restore Operator provides disaster recovery solutions for the case when the ACM hub goes down and need to be recreated. Scenarios outside the scope of this component : disaster recovery scenarios for applications running on managed clusters or scenarios where the managed clusters go down. 

The Cluster Back up and Restore Operator runs on the hub and depends on the [OADP Operator](https://github.com/openshift/oadp-operator) to create a connection to a backup storage location on the ACM hub, which is then used to backup and restore ACM hub resources. 

The Cluster Back up and Restore Operator chart is installed automatically by the MultiClusterHub resource, when installing or upgrading to version 2.5 of the Advanced Cluster Management operator. The OADP Operator is also installed automatically by a hook defined on the Cluster Back up and Restore Operator chart.

Before you can use the cluster backup operator, the [OADP Operator](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md) must be configured to set the connection to the storage location where backups will be saved. Make sure you follow the steps to create the [secret for the cloud storage](https://github.com/openshift/oadp-operator#creating-credentials-secret) where the backups are going to be saved, then use that secret when creating the [DataProtectionApplication resource](https://github.com/openshift/oadp-operator/blob/master/docs/install_olm.md#create-the-dataprotectionapplication-custom-resource) to setup the connection to the storage location.

<b>Note</b>: The Cluster Back up and Restore Operator chart installs the [backup-restore-enabled](https://github.com/stolostron/cluster-backup-chart/blob/main/stable/cluster-backup-chart/templates/hub-backup-pod.yaml) Policy, used to inform on issues with the backup and restore component. The Policy templates check if the required pods are running, storage location is available, backups are available at the defined location and no erros status is reported by the main resources. This Policy is intended to help notify the Hub admin of any backup issues as the hub is active and expected to produce backups.



## Design

The operator defines the `BackupSchedule.cluster.open-cluster-management.io` resource, used to setup acm backup schedules, and `Restore.cluster.open-cluster-management.io` resource, used to process and restore these backups.
The operator creates corresponding Velero or DataProtectionApplication resources depending what version of the operator is used and sets the options needed to backup remote clusters and any other hub resources that needs to be restored.

![Cluster Backup Controller Dataflow](images/cluster-backup-controller-dataflow.png)


## What is backed up

The Cluster Back up and Restore Operator solution provides backup and restore support for all ACM hub resources: managed clusters, applications, policies, bare metal assets.
It  provides support for backing up any third party resources extending the basic ACM installation. 
With this backup solution, you can define a cron based backup schedule that runs at specified time intervals and continuously backup the latest version of the Hub content.

The steps below show how the Cluster Back up and Restore Operator finds the resources to be backed up.
With this approach the backup up includes all CRDs installed on the ACM Hub, including any extensions to the ACM solution using third parties components.
1. Exclude all resources in the MultiClusterHub namespace. This is to avoid backing up installation resources which are linked to the current Hub configuration that should not be backed up.
2. Backup all CRDs with an api version suffixed by `.open-cluster-management.io`. This will cover all ACM resources.
3. Additionally, backup all CRDs from these api groups: `argoproj.io`,`app.k8s.io`,`core.observatorium.io`,`hive.openshift.io`
4. Exclude ACM CRDs from the following api groups: `clustermanagementaddon`, `observabilityaddon`, `applicationmanager`,`certpolicycontroller`,`iampolicycontroller`,`policycontroller`,`searchcollector`,`workmanager`,`backupschedule`,`restore`,`clusterclaim.cluster.open-cluster-management.io`
5. Backup secrets and configmaps with one of the following label annotations. Any secret or configmap setting at least one of these labels is being backed up :
`cluster.open-cluster-management.io/type`, `hive.openshift.io/secret-type`, `cluster.open-cluster-management.io/backup`
6. Use this label annotation for any other resources that should be backed up and are not included in the above criteria: `cluster.open-cluster-management.io/backup`
7. Resources picked up by the above rules that should not be backed up, can be explicitly excluded when setting this label annotation: `velero.io/exclude-from-backup=true` 



## Scheduling a cluster backup 

A backup schedule is activated when creating the `backupschedule.cluster.open-cluster-management.io` resource, as shown [here](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_backupschedule.yaml)

After you create a `backupschedule.cluster.open-cluster-management.io` resource you should be able to run `oc get bsch -n <oadp-operator-ns>` and get the status of the scheduled cluster backups. The `<oadp-operator-ns>` is the namespace where BackupSchedule was created and it should be the same namespace where the OADP Operator was installed.

The `backupschedule.cluster.open-cluster-management.io` created 6 `schedule.velero.io` resources, used to generate the backups.

Run `os get schedules -A | grep acm` to view the list of backup scheduled.

Resources are backed up in 3 separate groups:
1. credentials backup ( 3 backup files, for hive, acm and generic backups )
2. resources backup ( 2 backup files, one for the ACM resources and second for generic resources, labeled with `cluster.open-cluster-management.io/backup`)

3. managed clusters backup ( one backup containing only resources which result in activating the managed cluster connection to the hub where the backup was restored on)


<b>Note</b>:

a. The resources backup in step 2 above contains managed cluster specific resources but does not contain the subset of resources which will make the manage cluster connect to this hub. These resources, also called managed clusters activation resources, are stored by the managed clusters backup. When you restore just the resources backup on a hub, the Hub sees all managed clusters but they are in a detached state. The managed clusters are still connected to the initial hub.

b. Only managed clusters created using the hive api will be automatically connected with the Hub when the `acm-managed-clusters` backup is restored on another hub. All other managed clusters will show up as `Pending Import` and must be imported back on the new hub.

c. When restoring a backup on a new hub, make sure the old hub from where the backup was created is shut down, otherwise the old hub will try to reimport the managed clusters as soon as the managed cluster reconciliation finds the managed clusters are no longer available.

## Restoring a backup

In a usual restore scenario, the hub where the backups have been executed becomes unavailable and data backed up needs to be moved to a new hub. This is done by running the cluster restore operation on the hub where the backed up data needs to be moved to. In this case, the restore operation is executed on a different hub than the one where the backup was created. 

There are also cases where you want to restore the data on the same hub where the backup was collected, in order to recover data from a previous snapshot. In this case both restore and backup operations are executed on the same hub.

A restore backup is executed when creating the `restore.cluster.open-cluster-management.io` resource on the hub. A few samples are available [here](https://github.com/stolostron/cluster-backup-operator/tree/main/config/samples)
- use the [passive sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive.yaml) if you want to restore all resources on the new hub but you don't want to have the managed clusters moved over yet. You can use this restore configuration when the initial hub is still up and you want to prevent the managed clusters to change ownership. You could use this to just view the initial hub configuration or to prepare the new hub to take over when needed; in that case just restore the managed clusters resources.
- use the [passive activation sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive_activate.yaml) when you want to move the managed clusters to this hub. In this case it is assumed that the hub has been preped already with the other data, using the [passive sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore_passive.yaml)
- use the [restore sample](https://github.com/stolostron/cluster-backup-operator/blob/main/config/samples/cluster_v1beta1_restore.yaml) if you want to restore all data at once and make this hub take over the managed clusters as well.

After you create a `restore.cluster.open-cluster-management.io` resource on the hub, you should be able to run `oc get restore -n <oadp-operator-ns>` and get the status of the restore operation. You should also be able to verify on your  hub that the backed up resources contained by the backup file have been created.

<b>Note:</b> 

a. The `restore.cluster.open-cluster-management.io` resource is executed once. After the restore operation is completed, if you want to run another restore operation on the same hub, you have to create a new `restore.cluster.open-cluster-management.io` resource.

b. Although you can create multiple `restore.cluster.open-cluster-management.io` resources, only one is allowed to be executing at any moment in time.

c. The restore operation allows to restore all 3 backup types created by the backup operation, although you can choose to install only a certain type (only managed clusters or only user credentials or only hub resources). 

The restore defines 3 required spec properties, defining the restore logic for the 3 type of backed up files. 
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

Here you can find an example of a `backupschedule.cluster.open-cluster-management.io` resource definition:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: BackupSchedule
metadata:
  name: schedule-acm
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
spec:
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```


In order to create an instance of `backupschedule.cluster.open-cluster-management.io` or `restore.cluster.open-cluster-management.io` you can start from one of the [sample configurations](config/samples).
Replace the `<oadp-operator-ns>` with the namespace name used to install the OADP Operator.


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

