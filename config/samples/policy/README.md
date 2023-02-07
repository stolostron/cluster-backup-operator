# app-backup-policies
Stateful application Backup and Restore using ACM policies 
------

- [Getting Started](#getting-started)

------

## Getting Started
The Policies available here provide backup and restore support for stateful applications running on all managed clusters. 

The Policies should be deployed on the Red Hat Advanced Cluster Management for Kubernetes hub. They are created under the `default` namespace on the hub and are using the `hdr-app-configmap` resource available under this location to setup backup options.
- Before deploying the policies, make sure you update the required configuration under the `hdr-app-configmap`. Create the `hdr-app-configmap` configmap first.
- The `oadp-hdr-app-install` This policy deploys velero using the OADP operator to all OpenShift managed clusters with a label `acm-backup=acm-hdr-app`. The OADP operator installs the `velero` component, used to backup and restore application resources and PVs. This Policy must be installed first. Note that the Policy is set to enforce by default, which means all resources will be created on the managed clusters as soon as the Policy is installed on the hub.
- The `oadp-hdr-app-backup` Policy creates a velero `Schedule` resource which creates backups for the specified applications at defined interval. All resources from the namespaces specified with the `includedNamespaces` property, including PVs, are being backed up. The `oadp-hdr-app-backup` Policy should be created after the `oadp-hdr-app-install` Policy is installed and shows status of success. 

