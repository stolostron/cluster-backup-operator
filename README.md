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
    - [Deploy Operator using OLM] (#deploy-operator-using-olm)
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

Included fonts are licensed under the *SIL Open Font License 1.1*, and copies of this license can be found along side the corresponding fonts in the [./fonts](fonts) directory.

## Getting Started
The Cluster Back up and Restore Operator runs on the hub and depends on the OADP Operator to backup and restore ACM hub resources. You need to install the OADP Operator first as described here https://github.com/openshift/oadp-operator.

## Design
The operator defines a Backup and Restore CRD in the cluster.open-cluster-management.io group, used to setup a hub backup and restore configuration.
Based on the Backup spec, the operator creates corresponding Velero resources and sets the options needed to backup remote clusters and any other hub resources that needs to be restored.


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

### Deploy Operator using OLM

If you would like to deploy Operator using OLM, you'll need to build and push the bundle image and index image. You need to host the images on a public registry service like [quay.io](https://quay.io).

1. Build your bundle image
    ```shell
    make bundle-build REPO=<registry>
    ```
1. Push the bundle image
    ```shell
    make docker-push IMG=<bundle image name>
    ```
1. Build the index image

    This `make` target will install `opm` if it is not already installed. If
    you would like to install it in your `PATH` manually instead, get it from
    [here](https://github.com/operator-framework/operator-registry/releases).
    ```shell
    make bundle-index-build REPO=<registry>
    ```
1. Push the index image
    ```shell
    make docker-push IMG=<index image name>
    ```

## Usage

Before using Cluster Back up and Restore Operator backup or restore support you have to install the OADP Operator which will install the Velero required resources. Make sure you follow the OADP Operator installation instructions and create a Velero resource and a valid connection to a backup location where backups will be stored.
If you are trying to use the Cluster Back up and Restore Operator to backup data, you have to create a `backup` (`cluster.open-cluster-management.io` `v1beta1`) resource that will be consumed by the operator and create all the necessary resources for you.
If you are trying to use the Cluster Back up and Restore Operator to restore a backup, then you have to create a `restore` (`cluster.open-cluster-management.io` `v1beta1`) resource which will run the restore and execute any other post restore operations, such as registering restored remote clusters with the new hub.

Here you can find an example of a `backup.cluster.open-cluster-management.io` resource definition:

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Backup
metadata:
  name: backup-acm
spec:
  veleroConfigBackupProxy: 
    metadata: oadp-operator
  interval: 20
  maxBackups: 5
```
The `veleroConfigBackupProxy` `metadata` defines the namespace where the OADP Operator (so Velero) is installed. 
The `interval` value in the `spec`, defines the time interval in minutes for running the another backup. The interval takes into consideration the time taken to execute the provious backup; for example, if the previous backup took 60min to execute, the next backup will be called after interval + 60 minutes. Note: this property is work in progress, may be replaced with a Cron expression.
The `maxBackup` represents the numbed of backups after which the old backups are being removed.


This is an example of a `restore.cluster.open-cluster-management.io` resource definition

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm
spec:
  backupName: backup-acm-2021-07-30-163327
  veleroConfigRestoreProxy:
    metadata: oadp-operator
```

The `veleroConfigBackupProxy` `metadata` defines the namespace where the OADP Operator (so Velero) is installed. 
The `backupName` represents the name of the `velero.io/v1` `Backup` resource to be restored on the hub where the `cluster.open-cluster-management.io/v1beta1` `Restore` resource was created.
You can find the available Backups by going to the OADP Operator, under the Backup resource section.

In order to create an instance of `cluster.open-cluster-management.io/v1beta1` `Backup` or `Restore` in the specified namespace you can start from one of the [sample configurations](config/samples).

```shell
kubectl create -n <ns> -f config/samples/backup_v1alpha1_backup.yaml
kubectl create -n <ns> -f config/samples/restore_v1alpha1_backup.yaml
```

# Testing