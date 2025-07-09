# Restore CRD API Documentation

**Group:** `cluster.open-cluster-management.io`  
**Kind:** `Restore`  
**Version:** `v1beta1`  
**Scope:** Namespaced  
**Short Name:** `crst`

The `Restore` resource is used to restore resources from a cluster backup to a target cluster.  
It supports restoring passive data, managed cluster activation resources, and can be configured to periodically check for new backups and automatically restore them.

---

## Example

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: example-restore
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroCredentialsBackupName: latest
  veleroManagedClustersBackupName: latest
  veleroResourcesBackupName: latest
  syncRestoreWithNewBackups: false
```

---

## Spec Fields

| Field                             | Type      | Description |
|------------------------------------|-----------|-------------|
| `cleanupBeforeRestore`             | string    | Use `CleanupRestored` to delete all resources created by a previous restore before restoring new data, or `None` to skip cleanup. **Required.** |
| `excludedNamespaces`               | string[]  | Namespaces to exclude from the restore. |
| `excludedResources`                | string[]  | Resource names to exclude from the restore. |
| `hooks`                           | object    | Velero restore hooks for custom behaviors during or after restore. |
| `includedNamespaces`               | string[]  | Namespaces to include in the restore. If empty, all namespaces are included. |
| `includedResources`                | string[]  | Resource names to include in the restore. If empty, all resources in the backup are included. |
| `labelSelector`                    | object    | Label selector to filter objects for restore. |
| `namespaceMapping`                 | object    | Map of source namespace names to target namespace names for restore. |
| `orLabelSelectors`                 | object[]  | List of label selectors joined by OR for filtering objects. Cannot be used with `labelSelector`. |
| `preserveNodePorts`                | boolean   | Whether to restore old nodePorts from backup. |
| `restorePVs`                       | boolean   | Whether to restore all included PVs from snapshot. |
| `restoreStatus`                    | object    | Specifies which resources to restore the status field for. |
| `restoreSyncInterval`              | string    | Duration for checking new backups when `syncRestoreWithNewBackups` is true. Defaults to 30 minutes if not set. |
| `syncRestoreWithNewBackups`        | boolean   | If true, periodically checks for new backups and restores them. Default: false. |
| `veleroCredentialsBackupName`      | string    | Name of the Velero backup for credentials. Values: `latest`, `skip`, or a specific backup name. **Required.** |
| `veleroManagedClustersBackupName`  | string    | Name of the Velero backup for managed clusters. Values: `latest`, `skip`, or a specific backup name. **Required.** |
| `veleroResourcesBackupName`        | string    | Name of the Velero backup for resources. Values: `latest`, `skip`, or a specific backup name. **Required.** |

---

## Status Fields

| Field                             | Type      | Description |
|------------------------------------|-----------|-------------|
| `completionTimestamp`              | string    | Time the restore operation was completed. |
| `lastMessage`                      | string    | Message on the last operation. |
| `messages`                         | string[]  | Any messages encountered during the restore process. |
| `phase`                            | string    | Current phase of the restore. |
| `veleroCredentialsRestoreName`     | string    | Name of the Velero restore for credentials. |
| `veleroGenericResourcesRestoreName`| string    | Name of the Velero restore for generic resources. |
| `veleroManagedClustersRestoreName` | string    | Name of the Velero restore for managed clusters. |
| `veleroResourcesRestoreName`       | string    | Name of the Velero restore for resources. |

---

## Additional Printer Columns

- **Phase:** `.status.phase`
- **Message:** `.status.lastMessage`

---

## Description

Restore is an ACM resource that you can use to restore resources from a cluster backup to a target cluster.  
The restore resource has properties that you can use to restore only passive data or to restore managed cluster activation resources.  
Additionally, it has a property that you can use to periodically check for new backups and automatically restore them on the target cluster.

---

**This documentation is generated from the CRD definition in `config/crd/bases/cluster.open-cluster-management.io_restores.yaml`.**