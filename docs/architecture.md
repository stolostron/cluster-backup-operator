# Architecture — cluster-backup-operator

This document describes the internal architecture of the cluster-backup-operator, including controller design, phase state machines, data flow, and key algorithms.

For user-facing documentation (how to configure, samples, troubleshooting), see the [README](../README.md).

## High-Level Architecture

The operator runs on an ACM hub cluster and depends on the OADP operator (which installs Velero). It defines two custom resources — `BackupSchedule` and `Restore` — and two reconcilers that manage Velero `Schedule` and `Restore` objects on behalf of the user.

```
┌─────────────────────────────────────────────────────────┐
│                    ACM Hub Cluster                       │
│                                                         │
│  ┌─────────────────┐       ┌──────────────────────┐     │
│  │ BackupSchedule  │──────▶│ BackupSchedule       │     │
│  │ CR (user)       │       │ Reconciler           │     │
│  └─────────────────┘       │  - Creates 5 Velero  │     │
│                            │    Schedule objects   │     │
│                            │  - Collision detect   │     │
│                            │  - MSA setup          │     │
│                            └──────────┬───────────┘     │
│                                       │                 │
│                                       ▼                 │
│                            ┌──────────────────────┐     │
│                            │ Velero Schedules     │     │
│                            │ (5 schedules)        │────────▶ S3 Storage
│                            └──────────────────────┘     │
│                                                         │
│  ┌─────────────────┐       ┌──────────────────────┐     │
│  │ Restore         │──────▶│ Restore              │     │
│  │ CR (user)       │       │ Reconciler           │     │
│  └─────────────────┘       │  - Creates Velero    │     │
│                            │    Restore objects   │     │
│                            │  - Cleanup logic     │     │
│                            │  - Auto-import       │     │
│                            │  - Passive sync      │     │
│                            └──────────────────────┘     │
│                                                         │
│  ┌─────────────────┐                                    │
│  │ Restore Webhook │ Validates sync mode rules          │
│  └─────────────────┘                                    │
│                                                         │
│  ┌─────────────────┐                                    │
│  │ TLS Config      │ Fetches APIServer TLS profile      │
│  │ (pkg/tlsconfig) │                                    │
│  └─────────────────┘                                    │
└─────────────────────────────────────────────────────────┘
```

## Startup Sequence (main.go)

1. Register schemes (ACM, Velero, Hive, OCM, OpenShift, core)
2. Parse flags (metrics, probes, leader election, HTTP/2)
3. Create cancellable context (for TLS profile change shutdown)
4. Fetch TLS profile from APIServer (`openshifttls.FetchAPIServerTLSProfile`)
5. Build TLS config for webhook server (`tlsconfig.BuildTLSConfig`)
6. Create controller-runtime manager with webhook TLS options
7. **CRD gate loop**: Block until Velero CRDs are present (OADP installed), polling every 30s
8. Register BackupScheduleReconciler and RestoreReconciler
9. Register Restore validation webhook
10. Set up SecurityProfileWatcher (triggers shutdown on TLS profile change)
11. Start manager

## BackupScheduleReconciler

### Reconcile Flow

```
BackupSchedule CR created/updated
         │
         ▼
    Validate configuration
    ├── Valid cron expression?
    ├── BSL exists and available?
    ├── Active Restore running? (blocks non-paused schedule)
    └── MSA CRD present? (if useManagedServiceAccount=true)
         │
         ▼ (validation passed)
    Check for backup collision
    ├── Compare latest backup's backup-cluster label vs this hub's ID
    ├── If mismatch → BackupCollision (delete Velero schedules)
    └── Exception: this hub ran restore-clusters after foreign backup
         │
         ▼ (no collision)
    Create/update 5 Velero Schedules
    ├── acm-credentials-schedule
    ├── acm-resources-schedule
    ├── acm-resources-generic-schedule
    ├── acm-managed-clusters-schedule
    └── acm-validation-policy-schedule
         │
         ▼
    Aggregate Velero schedule phases → set BackupSchedule phase
    Run pre-backup tasks (MSA token creation if enabled)
```

### Phase State Machine

```
                    ┌──────────┐
     Create ───────▶│   New    │
                    └────┬─────┘
                         │ Velero schedules enabled
                         ▼
                    ┌──────────┐
              ┌────▶│ Enabled  │◀──── Healthy state
              │     └────┬─────┘
              │          │
    Resume ───┘     ┌────┴──────────────────┐
                    │                       │
                    ▼                       ▼
           ┌────────────────┐    ┌──────────────────┐
           │FailedValidation│    │ BackupCollision   │
           │                │    │                   │
           │ Invalid cron   │    │ Another hub owns  │
           │ No/bad BSL     │    │ latest backups    │
           │ Active Restore │    └──────────────────┘
           │ Missing MSA CRD│
           └────────────────┘
                    │
                    ▼
              ┌──────────┐         ┌──────────┐
              │  Paused  │         │  Failed  │
              │          │         │ (internal│
              │ spec.    │         │  error)  │
              │ paused:  │         └──────────┘
              │ true     │
              └──────────┘
```

## RestoreReconciler

### Reconcile Flow

```
Restore CR created/updated
         │
         ▼
    Check for conflicts
    ├── Another active Restore? → FinishedWithErrors
    ├── Active BackupSchedule? → FinishedWithErrors
    └── Valid cleanupBeforeRestore?
         │
         ▼
    Validate storage
    ├── BSL exists in this namespace?
    └── BSL Available?
         │
         ▼
    Run cleanup (if cleanupBeforeRestore != None)
    ├── CleanupRestored: delete resources with velero.io/backup-name != current
    └── CleanupAll: delete all resources that would be in an ACM backup
         │
         ▼
    Create Velero Restore objects
    ├── Credentials restore
    ├── Resources restore
    ├── Resources-generic restore
    ├── Managed-clusters restore (if not "skip")
    └── Activation restores (credentials-active, resources-generic-active)
         │
         ▼
    Monitor Velero restore phases → set ACM Restore phase
         │
         ├── All completed, MC=skip, sync=true → Enabled (requeue on interval)
         ├── All completed, MC=latest → Finished (run post-restore: auto-import)
         ├── Any PartiallyFailed → FinishedWithErrors
         └── Any Failed/FailedValidation → Error
```

### Phase State Machine

```
                    ┌──────────┐
     Create ───────▶│ Started  │ (cleanup phase)
                    └────┬─────┘
                         │ Velero restores created
                         ▼
                    ┌──────────┐
                    │ Running  │ (Velero restores in progress)
                    └────┬─────┘
                         │
              ┌──────────┼──────────────┐
              │          │              │
              ▼          ▼              ▼
        ┌──────────┐ ┌──────────┐ ┌────────────────────┐
        │ Finished │ │ Enabled  │ │FinishedWithErrors   │
        │          │ │          │ │                      │
        │ All done │ │ Sync     │ │ Velero partial fail  │
        │ MC=latest│ │ MC=skip  │ │ Concurrent resource  │
        └──────────┘ │ Requeues │ │ Invalid cleanup      │
                     └────┬─────┘ └────────────────────┘
                          │
                    Patch MC to "latest"
                          │
                          ▼
                    ┌──────────┐
                    │ Started  │ (activation restores)
                    └────┬─────┘
                         │
                         ▼
                    ┌──────────┐
                    │ Finished │ (activation complete, sync stops)
                    └──────────┘

        ┌──────────┐
        │  Error   │ BSL unavailable, Velero Failed/FailedValidation
        └──────────┘
```

### Passive Sync Cycle

When `syncRestoreWithNewBackups=true` and phase is `Enabled`:

1. Controller requeues after `restoreSyncInterval` (default 30m)
2. On requeue, checks if new backups are available at the storage location
3. If new backups found:
   - Runs cleanup (CleanupRestored)
   - Creates new Velero restores for passive data
   - Phase stays `Enabled` after completion
4. If MC is patched to `latest`:
   - Creates managed-cluster and activation restores
   - Phase transitions to `Finished`
   - Sync stops permanently

## Backup Content Selection (backup.go)

### How resources are selected for backup

```
Discovery: List all API groups on the cluster
         │
         ▼
    Filter by inclusion rules
    ├── API groups: *.open-cluster-management.io, *.hive.openshift.io,
    │   argoproj.io, app.k8s.io, core.observatorium.io
    └── Exclude: internal, operator, work, search, admission.hive,
        proxy, action, view, clusterview, velero.io
         │
         ▼
    Exclude specific CRDs
    ├── clustermanagementaddon, backupschedule, restore,
    │   clusterclaim.cluster, discoveredcluster
    └── local-cluster namespace
         │
         ▼
    Categorize into backup groups
    ├── Credentials: Secrets/ConfigMaps with backup labels
    ├── Resources: ACM resources (policies, apps, placements)
    ├── Resources-generic: User-labeled resources (backup label Exists)
    └── Managed-clusters: Activation data (ClusterDeployment, ManagedCluster, etc.)
```

### Label-Based Inclusion

| Label | Effect |
|-------|--------|
| `cluster.open-cluster-management.io/backup: ""` | Included in generic backup |
| `cluster.open-cluster-management.io/backup: cluster-activation` | Included in generic backup, restored only during activation |
| `velero.io/exclude-from-backup: "true"` | Excluded from all backups |
| `cluster.open-cluster-management.io/type` | Secret included in credentials backup |
| `hive.openshift.io/secret-type` | Secret included in credentials backup |

## Collision Detection (schedule.go)

```
scheduleOwnsLatestStorageBackups()
         │
         ▼
    List Velero backups with label matching acm-resources-schedule
    Sort by start timestamp
    Get latest backup
         │
         ▼
    Compare latest backup's "backup-cluster" label
    vs this hub's cluster ID (from Velero schedule labels)
         │
         ├── Match → This hub owns the backups (OK)
         │
         └── Mismatch → Check if this hub ran restore-clusters AFTER the foreign backup
              ├── Yes → Bypass collision (DR failback scenario)
              └── No → BackupCollision (delete Velero schedules)
```

## Cleanup Logic (restore_post.go)

### CleanupRestored

Removes resources from a previous ACM restore that are not in the current backup:

1. **Secrets/ConfigMaps**: Requires `velero.io/backup-name` label present AND pointing to a different backup than the current one. Also requires credential-type labels.
2. **Dynamic resources**: Iterates resource kinds from the current backup's included set. Uses label selector: `velero.io/backup-name notin (<current-backup>)`.
3. **Exclusions**: Never deletes resources with `velero.io/exclude-from-backup: true`, resources in `local-cluster` namespace, or resources in the MCH namespace.
4. **Activation filter**: When MC=`skip`, also excludes resources labeled `cluster.open-cluster-management.io/backup: cluster-activation`.

### CleanupAll

Same as CleanupRestored but drops the `velero.io/backup-name` requirement — deletes all resources that match the backup's resource kinds, regardless of whether they were created by a restore.

## Auto-Import Flow (restore_post.go + pre_backup.go)

### Backup Side (pre_backup.go)

When `useManagedServiceAccount=true` on BackupSchedule:
1. For each imported managed cluster, create a `ManagedServiceAccount` (`auto-import-account`)
2. MSA triggers token creation on the managed cluster
3. Token is pushed back to hub as a Secret under the managed cluster namespace
4. Backup controller adds `cluster.open-cluster-management.io/backup` label to the Secret
5. Creates `ManifestWork` to set up `klusterlet-bootstrap-kubeconfig` RoleBinding on the managed cluster

### Restore Side (restore_post.go)

After activation data is restored:
1. Find managed clusters in `Pending Import` state
2. For each, check if a valid MSA token exists in the backup
3. If valid, create `auto-import-secret` using the token
4. Import controller reconnects the managed cluster

## TLS Configuration (pkg/tlsconfig)

At startup, the operator fetches the TLS security profile from the OpenShift APIServer and applies it to the webhook server:

1. `FetchAPIServerTLSProfile()` — gets min TLS version and cipher suites
2. `BuildTLSConfig()` — converts profile to `[]func(*tls.Config)` options
3. HTTP/2 disabled by default (CVE-2023-44487)
4. `SecurityProfileWatcher` monitors for TLS profile changes and triggers graceful shutdown

## Key Constants and Labels (backup.go)

| Constant | Value | Purpose |
|----------|-------|---------|
| `BackupScheduleClusterLabel` | `cluster.open-cluster-management.io/backup-cluster` | Hub ID on backups |
| `RestoreClusterLabel` | `cluster.open-cluster-management.io/restore-cluster` | Hub ID on restore-clusters backups |
| `BackupVeleroLabel` | `velero.io/schedule-name` | Links backup to schedule |
| `BackupNameVeleroLabel` | `velero.io/backup-name` | Set on restored resources |
| `BackupScheduleTypeLabel` | `cluster.open-cluster-management.io/backup-schedule-type` | Backup category |
