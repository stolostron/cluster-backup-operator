# Architecture — Cluster Backup and Restore Operator

The **Cluster Backup and Restore Operator** runs on an ACM hub cluster and automates the backup and restore of hub resources using [Velero](https://velero.io/) (installed via the [OADP Operator](https://github.com/openshift/oadp-operator)).

---

## CRDs

### `Backup` (`cluster.open-cluster-management.io/v1beta1`)

Short name: `cbkp`

| Field | Purpose |
|---|---|
| `spec.veleroConfigBackupProxy.metadata` | Namespace where OADP/Velero is installed |
| `spec.interval` | Minutes to wait after backup completion before starting the next |
| `spec.maxBackups` | Maximum number of Velero Backup CRs to retain (oldest deleted first, errors first) |

Status tracks `phase`, `currentBackup`, `lastBackup`, `completionTimestamp`, `backupDuration`, and the full `veleroBackup` object.

### `Restore` (`cluster.open-cluster-management.io/v1beta1`)

Short name: `crst`

| Field | Purpose |
|---|---|
| `spec.backupName` | Name of the `velero.io/Backup` to restore |
| `spec.veleroConfigRestoreProxy.metadata` | Namespace where OADP/Velero is installed |
| `spec.veleroConfigRestoreProxy.detachManagedClusters` | If true, detach managed clusters from the backed-up hub before restoring |
| `spec.veleroConfigRestoreProxy.BackedUpHubSecretName` | Secret to access the backed-up hub during cluster detachment |

Status tracks `phase`, `lastMessage`, and the full `veleroRestore` object.

---

## Velero Schedules

The operator creates **5 Velero Schedule CRs** in the `open-cluster-management-backup` namespace, organised by data category:

| Schedule Name | Data Category | Contents |
|---|---|---|
| `acm-credentials-schedule` | Passive | ACM credentials and secrets |
| `acm-resources-schedule` | Passive | Core ACM hub resources |
| `acm-resources-generic-schedule` | Passive | Generic/app resources not covered above |
| `acm-managed-clusters-schedule` | **Activation** | Managed cluster activation data |
| `acm-validation-policy-schedule` | Passive | Validation policies |

### Data Categories

**Passive data** (credentials, resources, apps) can be restored to a passive standby hub. Restoring passive data alone does **not** reconnect managed clusters to the new hub; the original hub remains active.

**Activation data** (`acm-managed-clusters-schedule`) contains the managed-cluster resources needed to re-register clusters. Restoring this schedule on the new hub reconnects all managed clusters, making the new hub active. This is the final step in a DR failover.

---

## Backup Selection

### Included by Default

Resources whose API group matches any of:

- `*.open-cluster-management.io`
- `*.hive.openshift.io`
- `argoproj.io`
- `app.k8s.io`

Namespaces included:

- `open-cluster-management-agent`
- `open-cluster-management-hub`
- `hive`
- `openshift-operator-lifecycle-manager`
- `open-cluster-management-observability`
- All app Channel namespaces (excluding `open-cluster-management/charts-v1`)
- All ManagedCluster namespaces (excluding `local-cluster`)

### Excluded by Default

Resources whose API group matches:

- `internal.*`
- `operator.*`
- `work.*`
- `search.*`
- `velero.io`

### Including Custom Resources

To include a custom resource in backups, add the label:

```yaml
cluster.open-cluster-management.io/backup: ""
```

---

## Controller Flow

### BackupReconciler

1. Reconcile loop triggered by `Backup` CR changes; requeues every 1 minute.
2. Calls `getActiveBackupName` — returns existing in-progress backup name, or generates a new timestamped name.
3. If the Velero Backup CR does not exist:
   - Calls `canStartBackup` to check the interval since last completion.
   - Calls `cleanupBackups` to delete oldest backups exceeding `maxBackups` (error-status backups deleted first).
   - Calls `setBackupInfo` to populate included namespaces (ACM, observability, channels, managed clusters).
   - Creates the Velero `Backup` CR.
4. If the Velero Backup CR exists: reads phase/progress and updates `Backup.Status`.
5. Updates `Backup.Status` via `Status().Update`.

### RestoreReconciler

1. Reconcile loop triggered by `Restore` CR changes; requeues every 1 minute until finished.
2. Calls `getVeleroRestoreName` to derive the Velero Restore name (`<restore-name>-<backup-name>`).
3. If the Velero Restore CR does not exist: sets `spec.backupName` and creates it.
4. If the Velero Restore CR exists: reads phase and updates `Restore.Status`.
5. When restore reaches a terminal phase (`Completed`, `Failed`, `PartiallyFailed`, `FailedValidation`), stops requeuing.

---

## Namespace Layout

| Namespace | Contents |
|---|---|
| `open-cluster-management-backup` | Velero Schedules, Backup CRs, Restore CRs created by this operator |
| `open-cluster-management` | ACM hub components (backed up, not operator home) |
| `open-cluster-management-agent` | Agent resources (backed up) |
| `open-cluster-management-hub` | Hub resources (backed up) |
| `hive` | Hive resources (backed up) |
| OADP namespace (configurable) | Velero deployment, `BackupStorageLocation` |

---

## Active/Passive Disaster Recovery

For active/passive hub DR, set `syncRestoreWithNewBackups: true` on the `Restore` CR. The restore controller will continuously sync the passive hub with the latest backup, keeping it warm. When failover is needed:

1. Restore passive data schedules on the new hub (hub is passive, clusters stay connected to old hub).
2. Restore activation data (`acm-managed-clusters-schedule`) to reconnect managed clusters to the new hub.

**Collision detection:** If the same resource exists on both hubs simultaneously (split-brain), the operator emits a warning event. Operators should resolve collisions before completing the failover.

---

## Dependencies

| Dependency | Version | Purpose |
|---|---|---|
| `sigs.k8s.io/controller-runtime` | v0.8.3 | Operator framework |
| `github.com/vmware-tanzu/velero` | v1.6.1 | Backup/restore engine |
| `github.com/open-cluster-management/api` | v0.0.0-20210527 | ManagedCluster types |
| `github.com/open-cluster-management/multicloud-operators-channel` | v1.2.2 | Channel types for app namespace discovery |
| `k8s.io/api`, `k8s.io/apimachinery` | v0.20.2 | Kubernetes core types |
