# app-backup-policies
Application DR using ACM policies 
------

- [Scenario](#scenario)
- [Prerequisites](#prerequisites)
  - [ConfigMap](#configmap)
  - [Install policy](#install-policy)
    - [Prereq for placing this policy on the hub](#prereq-for-placing-this-policy-on-the-hub)
  - [Install report policy](#install-report-policy)
- [Backup applications](#backup-applications)
- [Restore applications](#restore-applications)

------

## Scenario
The Policies available here provide backup and restore support for stateful applications running on  managed clusters or hub. Velero is used to backup and restore applications data. The product is installed using the OADP operator, which the `oadp-hdr-app-install` policy installs and configure on each target cluster.

You can use these policies to backup stateful applications (`oadp-hdr-app-backup` policy) or to restore applications backups (`oadp-hdr-app-restore` policy).

The policies should be installed on the hub managing clusters where you want to create stateful applications backups, or the hub managing clusters where you plan to restore the application backups. 

Both backup and restore policies can be installed on the same hub, if this hub manages clusters where applications need to be backed up or restored. A managed cluster can either be a backup target or a restore target though, not both at the same time. 


## Prerequisites

1. You can run `oc apply -k ./` to apply all resources at the same time on the hub. 

2. On the cluster you want to backup or restore apps set this label : acm-pv-dr-install="true". 
This places the the oadp-hdr-app-install policy on this cluster, which installs velero and configure the connections to the storage.

3.a On the cluster you want to backup the apps set this label : acm-pv-dr="backup". 
This places the the oadp-hdr-app-backup policy on this cluster, which schedules the backups.

3.b On the cluster you want to restore the apps set this label : acm-pv-dr="restore". 
This places the the oadp-hdr-app-restore policy on this cluster, which creates a restore operation.

You can also apply them one at the time :

### Apply ConfigMap

Before you install the policies you have to apply the `hdr-app-configmap` ConfigMap available here. 

`oc apply -f ./hdr-app-configmap`

You create the configmap on the hub, the same hub where the policies will be installed.

The configmap sets configuration options for the backup storage location, for the backup schedule backing up applications, and for the restore resource used to restore applications backups.

Make sure you <b>update all settings with valid values</b> before applying the `hdr-app-configmap` resource on the hub.


### Apply the Install policy

Create the `oadp-hdr-app-install` :

`oc apply -f ./oadp-hdr-app-install`

Create this policy on the hub managing clusters where you want to create stateful applications backups,
or where you restore these backup.  
The policy is set to enforce.

Make sure the `hdr-app-configmap`'s storage settings are properly set before applying this policy.


Before creating the backup or restore policy, create the `oadp-hdr-app-install` and `oadp-hdr-app-install-report` policies: 

`oc apply -f ./oadp-hdr-app-install`

`oc apply -f ./oadp-hdr-app-install-report`

The  `oadp-hdr-app-install` installs velero and configures the connection to the storage.

The  `oadp-hdr-app-install-report` reports on any runtime or configuration error.

#### Prereq for placing this policy on the hub


<b>Important:</b>

If the hub is one of the clusters where this policy will be placed, and the `backupNS=open-cluster-management-backup` then first enable cluster-backup on `MultiClusterHub`. 

The MultiClusterHub resource looks for the cluster-backup option and if set to false, it uninstalls OADP from the `open-cluster-management-backup` and deletes the namespace.


### Apply the Install report policy

Create the `oadp-hdr-app-install-report` :

`oc apply -f ./oadp-hdr-app-install-report`

This policy reports on any configuration errors for the application backup or restore scenarios.
Install this policy on the hub, after you install the oadp-hdr-app-install policy

The policy is set to inform as it only validates the installed configuration.

## Backup applications

If the hub manages clusters where stateful applications are running, and you want to create backups for these applications, then on the hub you must apply the `oadp-hdr-app-backup` policy:

`oc apply -f ./oadp-hdr-app-backup`

If the managed cluster (or hub) has the label `acm-pv-dr=backup` then the oadp-hdr-app-backup policy 
is propagated to this cluster for an application backup schedule. This cluster produces applications backups.
Make sure the `hdr-app-configmap`'s backup schedule resource settings are properly set before applying this policy.

This policy is enforced by default.

This policy creates a velero schedule to all managed clusters with a label `acm-pv-dr=backup`.
The schedule is used to backup applications resources and PVs.
The schedule uses the `backup.nsToBackup` `hdr-app-configmap` property to specify the namespaces for the applications to backup. 


## Restore applications

If the hub manages clusters where stateful applications backups must be restored, then you must install the `oadp-hdr-app-restore` policy:

`oc apply -f ./oadp-hdr-app-restore`


If the managed cluster (or hub) has the label `acm-pv-dr=restore` then the oadp-hdr-app-restore policy 
is propagated to this cluster for backup restore operation. This cluster restores applications backup.
Make sure the `hdr-app-configmap`'s restore resource settings are properly set before applying this policy.

This policy is enforced by default.

This policy creates a velero restore resource to all managed clusters 
with a label `acm-pv-dr=restore`. The restore resource is used to restore applications resources and PVs
from a selected backup.
The restore uses the `nsToRestore` hdr-app-configmap property to specify the namespaces for the applications to restore 






