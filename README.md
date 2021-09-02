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

Before you can use the cluster operator, you first need to install the OADP Operator as described [here](https://github.com/openshift/oadp-operator#installing-operator).
Make sure you follow the steps to create the [secret for the cloud storage](https://github.com/openshift/oadp-operator#creating-credentials-secret) where the backups are going to be saved, then use that secret when creating the [Velero resource](https://github.com/openshift/oadp-operator#creating-velero-cr).

The Cluster Back up and Restore Operator resources must be created in the same namespace where the OADP Operator is installed. 


## Design
The operator defines the `Backup.cluster.open-cluster-management.io` and `Restore.cluster.open-cluster-management.io` resources, used to setup a hub backup and restore configuration.
The operator creates corresponding Velero resources and sets the options needed to backup remote clusters and any other hub resources that needs to be restored.


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

If you are trying to use the Cluster Back up and Restore Operator to backup data, you have to create a `backup.cluster.open-cluster-management.io` resource which will be consumed by the operator and create all the necessary intermediary backup resources.

If you are trying to use the Cluster Back up and Restore Operator to restore a backup, then you have to create a `restore.cluster.open-cluster-management.io` resource which will run the restore and execute any other post restore operations, such as registering restored remote clusters with the new hub.

Here you can find an example of a `backup.cluster.open-cluster-management.io` resource definition:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Backup
metadata:
  name: backup-acm
spec:
  interval: 20
  maxBackups: 5
```

The `interval` value in the `spec` defines the time interval in minutes for running another backup. The interval takes into consideration the time taken to execute the previous backup; for example, if the previous backup took 60 minutes to execute, the next backup will be called after `interval` + 60 minutes. 

The `maxBackup` represents the numbed of backups after which the old backups are being removed.

This is an example of a `restore.cluster.open-cluster-management.io` resource definition

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
spec:
  backupName: backup-acm-2021-07-30-163327
```

The `backupName` represents the name of the `backup.velero.io` resource to be restored on the hub where the  `restore.cluster.open-cluster-management.io` resource was created.

In order to create an instance of `backup.cluster.open-cluster-management.io` or `restore.cluster.open-cluster-management.io` in the OADP Operator namespace you can start from one of the [sample configurations](config/samples).

```shell
kubectl create -n <oadp-operator-ns> -f config/samples/cluster_v1beta1_backup.yaml
kubectl create -n <oadp-operator-ns> -f config/samples/cluster_v1beta1_backup.yaml
```

# Testing

## Backup resources

After you create a `backup.cluster.open-cluster-management.io` resource you should be able to run `oc get cbkp -n <oadp-operator-ns>` and get the status of the backup executions.
Replace the `<oadp-operator-ns>` with the namespace name used to install the OADP Operator (the default value for the OADP Operator install namespace is `oadp-operator`).

In the example below, the last completed backup is `backup-acm-2021-08-10-140404` and a new backup is currently running `backup-acm-2021-08-10-151345`.
The name of the backup follows this template: `acm-backup-<timestamp>`

Example of a `backup.cluster.open-cluster-management.io` execution with a `backup.velero.io` in progress:

```
$ oc get cbkp -n oadp-operator
NAMESPACE       NAME         PHASE        BACKUP                         LASTBACKUP                     LASTBACKUPTIME         DURATION   MESSAGE
oadp-operator   backup-acm   InProgress   backup-acm-2021-08-10-151345   backup-acm-2021-08-10-140404   2021-08-10T18:22:07Z   18m3s      Velero Backup [backup-acm-2021-08-10-151345] phase:InProgress ItemsBackedUp[439], TotalItems[1410]
```

Running the command below returns all `backup.velero.io` resources, created in the same namespace with the OADP Operator. 

You should also be able to see the backup files under the storage location defined when installing the OADP Operator.

```
$ oc get backup -n <oadp-operator-ns>
```


Example of a Backup execution with no backup in progress:

```
$ oc get cbkp -A
NAMESPACE       NAME         PHASE       BACKUP                         LASTBACKUP                     LASTBACKUPTIME         DURATION   MESSAGE
oadp-operator   backup-acm   Completed   backup-acm-2021-08-10-151345   backup-acm-2021-08-10-151345   2021-08-10T19:21:06Z   7m21s      velero Backup [backup-acm-2021-08-10-151345] phase:Completed ItemsBackedUp[1410], TotalItems[1410]
```

## Restore a backup

The restore operation should be run on a different hub then the one where the backup was created.

After you create a `restore.cluster.open-cluster-management.io` resource on the new hub, you should be able to run `oc get restore -n <oadp-operator-ns>` and get the status of the restore operation. You should also be able to verify on your new hub that the backed up resources contained by the backup file have been created.

A restore operation is executed only once. If a backup name is specified on the restore resource, the resources from that backup are being restored. If no backup file is specified, the last valid backup file is being used instead.

