# Restore Scenarios - Complete Reference

This document provides a comprehensive overview of all supported restore scenarios for the cluster-backup-operator, covering both sync and non-sync modes with various backup name configurations.

## Table of Contents
- [Overview](#overview)
- [Key Concepts](#key-concepts)
- [Complete Scenario Matrix](#complete-scenario-matrix)
- [Detailed Scenarios with Examples](#detailed-scenarios-with-examples)
- [Understanding Label Selectors](#understanding-label-selectors)
- [PVC Wait Logic](#pvc-wait-logic)
- [Verifying Restores](#verifying-restores)

## Overview

The cluster-backup-operator supports multiple restore scenarios to accommodate different disaster recovery workflows. The behavior differs based on:

- **Sync Mode**: Whether `syncRestoreWithNewBackups` is enabled
- **Managed Clusters**: Whether managed cluster backup is being restored
- **Backup Names**: Whether using `skip`, `latest`, or specific backup names

### Important Principles

1. **Credentials are restored FIRST** - Required for proper restoration order and PVC wait logic
2. **Sync mode uses `-active` suffix** - To avoid name collisions when editing the same restore resource
3. **Non-sync mode does NOT use `-active`** - Each restore operation uses a separate resource
4. **Label filters separate passive and activation data**:
   - `NotIn cluster-activation`: Restores only non-activation resources (passive data)
   - `In cluster-activation`: Restores only activation resources (activation data)
   - No filter: Restores ALL resources (both passive and activation)

### Critical Validation Rules

**⚠️ Sync Mode Restriction:**

When `syncRestoreWithNewBackups: true`, the `veleroManagedClustersBackupName` **MUST** be set to `skip` initially.

**Invalid Configuration (Will be rejected):**
```yaml
syncRestoreWithNewBackups: true
veleroManagedClustersBackupName: latest  # ❌ ERROR on first run!
```

**Error Message:**
> "When syncRestoreWithNewBackups is true, veleroManagedClustersBackupName must initially be set to 'skip'. To activate managed clusters, edit the restore to set veleroManagedClustersBackupName to 'latest'."

**Correct Workflow:**
```yaml
# Step 1: Create with MC=skip
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-sync
spec:
  syncRestoreWithNewBackups: true
  veleroManagedClustersBackupName: skip    # ✅ Must be skip initially
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest

# Step 2: Edit the SAME restore to activate
# (oc edit restore restore-sync -n open-cluster-management-backup)
spec:
  veleroManagedClustersBackupName: latest  # ✅ OK after initial creation
```

**Why This Rule Exists:**

Sync mode is designed for the passive→activate workflow where the same restore resource is edited. Starting with `MC=latest` would:
1. Skip the passive sync phase entirely
2. Not create passive restores that `-active` restores are meant to avoid colliding with
3. Violate the design intent of sync mode

If you want to restore everything at once without the passive phase, use **non-sync mode** instead (Case 3).

## Key Concepts

### Passive vs Activation Data

- **Passive Data**: Resources without the `cluster-activation` label
  - Policies, applications, secrets, configmaps
  - Does NOT cause managed clusters to connect to the hub

- **Activation Data**: Resources with `cluster.open-cluster-management.io/backup: cluster-activation` label
  - ManagedCluster resources, cluster deployment resources
  - DOES cause managed clusters to connect to the hub

### Sync Mode vs Non-Sync Mode

**Sync Mode (`syncRestoreWithNewBackups: true`)**:
- Continuously checks for new backups (default: every 30 minutes)
- **User can EDIT the same restore resource** to transition from passive to active
- Uses `-active` suffix to avoid name collisions
- **Requires `latest` for all backup names**
- Ends when managed clusters are activated

**Non-Sync Mode (`syncRestoreWithNewBackups: false` or not set)**:
- One-time restore operation
- **User creates NEW restore resources** for different operations
- Does NOT use `-active` suffix (no collision risk)
- Supports both `latest` and specific backup names

### Backup Name Values

- **`skip`**: Don't restore this backup type
- **`latest`**: Use the most recent backup (supported in both sync and non-sync modes)
- **Specific name** (e.g., `acm-credentials-schedule-20251029181055`): Use a specific backup (non-sync mode only)

## Complete Scenario Matrix

| Case | Sync? | MC | Creds | Resources | Label Selector | `-active` Suffix | Description |
|------|-------|-----|-------|-----------|----------------|------------------|-------------|
| **1** | ✅ Yes | skip | latest | latest | **NotIn** | **NO** | Passive sync (continuous) |
| **1b** | ✅ Yes | latest | latest | latest | **In** | **YES** | User edits same restore to activate |
| **2** | ❌ No | skip | latest/specific | latest/specific | **NotIn** | **NO** | Passive one-time |
| **3** | ❌ No | latest/specific | latest/specific | latest/specific | **NONE** | **NO** | All-at-once activation |
| **4** | ❌ No | latest/specific | skip | latest/specific | Creds:**In**<br>Res:**NONE** | Creds:**NO**<br>Res:**NO** | Activate MC, skip creds, restore resources |
| **5** | ❌ No | latest/specific | skip | skip | Creds:**In**<br>Res:**In** | Creds:**NO**<br>Res:**NO** | Activate MC only (creds/res from MC backup) |

**Note:** The scenario "Sync mode with MC=latest from the start" is **not supported** and will be rejected by validation. Sync mode requires starting with `MC=skip` and later editing to `MC=latest`.

## Detailed Scenarios with Examples

### Case 1: Passive Sync (Continuous Restore)

**Purpose:** Continuously restore passive data as new backups become available. Used for active-passive DR setups.

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm-passive-sync
  namespace: open-cluster-management-backup
spec:
  syncRestoreWithNewBackups: true
  restoreSyncInterval: 10m
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: skip
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```

**Expected Velero Restores Created:**
```
restore-acm-passive-sync-acm-credentials-schedule-TIMESTAMP
restore-acm-passive-sync-acm-resources-generic-schedule-TIMESTAMP
restore-acm-passive-sync-acm-resources-schedule-TIMESTAMP
```

**Label Selector:**
```yaml
labelSelector:
  matchExpressions:
    - key: cluster.open-cluster-management.io/backup
      operator: NotIn
      values:
        - cluster-activation
```

**Behavior:**
- Restores only passive credentials and resources
- Checks for new backups every 10 minutes
- Automatically restores new passive data
- Managed clusters remain on primary hub
- **No `-active` suffix** (no restores created yet for activation)

---

### Case 1b: Sync Activate (Edit Same Restore)

**Purpose:** Activate managed clusters by editing the existing sync restore resource.

**User Action:** Edit the restore from Case 1 and change:
```yaml
spec:
  veleroManagedClustersBackupName: latest  # Changed from skip to latest
```

**Expected NEW Velero Restores Created:**
```
restore-acm-passive-sync-acm-credentials-schedule-TIMESTAMP-active
restore-acm-passive-sync-acm-resources-generic-schedule-TIMESTAMP-active
restore-acm-passive-sync-acm-managed-clusters-schedule-TIMESTAMP
```

**Label Selector on `-active` Restores:**
```yaml
labelSelector:
  matchExpressions:
    - key: cluster.open-cluster-management.io/backup
      operator: In
      values:
        - cluster-activation
```

**Behavior:**
- Creates `-active` restores to avoid collision with existing passive restores
- Restores only activation credentials and resources (In label)
- Restores managed clusters
- Sync mode ends (restore status becomes Finished)
- Managed clusters now connect to this hub

---

### Case 2: Passive Non-Sync (One-Time)

**Purpose:** Restore passive data without activating managed clusters.

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm-passive
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: skip
  veleroCredentialsBackupName: latest  # or specific: acm-credentials-schedule-20251029181055
  veleroResourcesBackupName: latest     # or specific: acm-resources-schedule-20251029181055
```

**Expected Velero Restores Created:**
```
restore-acm-passive-acm-credentials-schedule-TIMESTAMP
restore-acm-passive-acm-resources-generic-schedule-TIMESTAMP
restore-acm-passive-acm-resources-schedule-TIMESTAMP
```

**Label Selector:**
```yaml
labelSelector:
  matchExpressions:
    - key: cluster.open-cluster-management.io/backup
      operator: NotIn
      values:
        - cluster-activation
```

**Behavior:**
- One-time restore operation
- Restores only passive data
- **No `-active` suffix**
- Managed clusters remain on original hub
- Restore status becomes Finished when complete

---

### Case 3: All-at-Once Non-Sync (The Bug Fix)

**Purpose:** Restore everything (passive + activation data + managed clusters) in a single operation.

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm-all
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: acm-managed-clusters-schedule-20251029181055
  veleroCredentialsBackupName: acm-credentials-schedule-20251029181055
  veleroResourcesBackupName: acm-resources-schedule-20251029181055
```

**Expected Velero Restores Created:**
```
restore-acm-all-acm-credentials-schedule-20251029181055
restore-acm-all-acm-resources-generic-schedule-20251029181055
restore-acm-all-acm-resources-schedule-20251029181055
restore-acm-all-acm-managed-clusters-schedule-20251029181055
```

**Label Selector:**
```yaml
# NO label selector - restores ALL data (both passive and activation)
```

**Behavior:**
- **No label filters** - restores everything from the backup
- **No `-active` suffix**
- Restores all credentials (passive + activation)
- Restores all resources (passive + activation)
- Restores managed clusters
- Managed clusters connect to this hub
- **This is the bug fix** - previously would have added In label + `-active`, missing passive data

---

### Case 4: Activate with Skip Credentials

**Purpose:** Activate managed clusters and restore resources, but skip credentials (already restored).

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-activate-skip-creds
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: skip
  veleroResourcesBackupName: latest
```

**Expected Velero Restores Created:**
```
restore-activate-skip-creds-acm-credentials-schedule-TIMESTAMP       # activation creds from MC backup
restore-activate-skip-creds-acm-resources-generic-schedule-TIMESTAMP # all generic resources
restore-activate-skip-creds-acm-resources-schedule-TIMESTAMP         # all resources
restore-activate-skip-creds-acm-managed-clusters-schedule-TIMESTAMP
```

**Label Selectors:**

*Credentials (created from MC backup timestamp):*
```yaml
labelSelector:
  matchExpressions:
    - key: cluster.open-cluster-management.io/backup
      operator: In
      values:
        - cluster-activation
```

*Generic Resources (no filter):*
```yaml
# NO label selector - restores ALL generic resources
```

**Behavior:**
- Credentials are skipped BUT activation credentials are restored using MC backup timestamp
- Activation credentials get In label (only activation data)
- Generic resources have NO filter (restores all)
- Resources have NO filter (restores all)

---

### Case 5: Activate Managed Clusters Only

**Purpose:** Restore only managed clusters and activation data, skip credentials and resources.

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-activate-only
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: latest
  veleroCredentialsBackupName: skip
  veleroResourcesBackupName: skip
```

**Expected Velero Restores Created:**
```
restore-activate-only-acm-credentials-schedule-TIMESTAMP         # activation creds from MC backup
restore-activate-only-acm-resources-generic-schedule-TIMESTAMP   # activation resources from MC backup
restore-activate-only-acm-managed-clusters-schedule-TIMESTAMP
```

**Label Selector (for Credentials and Generic Resources):**
```yaml
labelSelector:
  matchExpressions:
    - key: cluster.open-cluster-management.io/backup
      operator: In
      values:
        - cluster-activation
```

**Behavior:**
- Credentials and resources are skipped BUT activation data is still restored
- Uses MC backup timestamp to find associated credentials/resources backups
- Only activation data is restored (In label selector)
- Managed clusters connect to this hub

---

### Case 7: All-at-Once with PVC Wait

**Purpose:** Restore everything with specific backup names, including PVC wait logic.

**Prerequisites:** A ConfigMap with PVC label exists:
```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: acm-pvcs-mongo-storage
  namespace: open-cluster-management-backup
  labels:
    cluster.open-cluster-management.io/backup-pvc: mongo-storage
data:
  key: value
```

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm-creds-20251029181055
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: acm-managed-clusters-schedule-20251029181055
  veleroCredentialsBackupName: acm-credentials-schedule-20251029181055
  veleroResourcesBackupName: acm-resources-schedule-20251029181055
```

**Initial Status (Waiting for PVC):**
```yaml
status:
  lastMessage: 'waiting for PVC mongo-storage:open-cluster-management-backup'
  phase: Started
  veleroCredentialsRestoreName: restore-acm-creds-20251029181055-acm-credentials-schedule-20251029181055
```

**After PVC Created:**

Create the PVC:
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: mongo-storage
  namespace: open-cluster-management-backup
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
```

**Final Status:**
```yaml
status:
  completionTimestamp: '2025-10-30T19:26:26Z'
  lastMessage: All Velero restores have run successfully
  phase: Finished
  veleroCredentialsRestoreName: restore-acm-creds-20251029181055-acm-credentials-schedule-20251029181055
  veleroGenericResourcesRestoreName: restore-acm-creds-20251029181055-acm-resources-generic-schedule-20251029181055
  veleroManagedClustersRestoreName: restore-acm-creds-20251029181055-acm-managed-clusters-schedule-20251029181055
  veleroResourcesRestoreName: restore-acm-creds-20251029181055-acm-resources-schedule-20251029181055
```

**Label Selector:**
```yaml
# NO label selector - restores ALL credentials and resources
```

**Behavior:**
- Credentials restore is created first
- Controller finds ConfigMap with `backup-pvc` label
- Waits for PVC `mongo-storage` to be created
- Once PVC exists, continues with other restores
- No label filters - restores ALL data
- No `-active` suffix

---

### Case 8: Passive with Specific Backup Names

**Purpose:** Restore passive data using specific backup names (not `latest`).

**Configuration:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-acm-passive-specific
  namespace: open-cluster-management-backup
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: skip
  veleroCredentialsBackupName: acm-credentials-schedule-20251029181055
  veleroResourcesBackupName: acm-resources-schedule-20251029181055
```

**Expected Velero Restores Created:**
```
restore-acm-passive-specific-acm-credentials-schedule-20251029181055
restore-acm-passive-specific-acm-resources-generic-schedule-20251029181055
restore-acm-passive-specific-acm-resources-schedule-20251029181055
```

**Label Selector:**
```yaml
labelSelector:
  matchExpressions:
    - key: cluster.open-cluster-management.io/backup
      operator: NotIn
      values:
        - cluster-activation
```

**Behavior:**
- Restores only passive data (NotIn activation)
- No `-active` suffix
- Uses specific backup names instead of `latest`

## Understanding Label Selectors

### When NotIn Label is Applied

**Condition:** `ManagedClusters == skip`

**Code Location:** `restore_controller.go` lines 554-564

```go
if (key == ResourcesGeneric || key == Credentials) &&
    veleroRestoresToCreate[ManagedClusters] == nil {
    // Add NotIn activation label
}
```

**Result:** Restores only non-activation resources

### When In Label is Applied

**Condition:** One of the following:
1. **Sync mode** AND `ManagedClusters != skip`
2. **Non-sync mode** AND `ManagedClusters != skip` AND credentials/resources were originally set to `skip`

**Code Location:** `restore_controller.go` lines 639-655

```go
if (key == Credentials && MC != nil && (SyncMode || credsWasSkip)) ||
   (key == ResourcesGeneric && MC != nil && (SyncMode || resourcesWasSkip)) {
    // Add In activation label
    // Add -active suffix if not skip
}
```

**Result:** Restores only activation resources

### When NO Label is Applied

**Condition:** Non-sync mode with `ManagedClusters != skip` AND credentials/resources NOT set to skip

**Result:** Restores ALL resources (both passive and activation)

## PVC Wait Logic

### Purpose

Some policies require PVCs to be created before data can be restored. The credentials backup may contain ConfigMaps with the label `cluster.open-cluster-management.io/backup-pvc` that specify required PVCs.

### How It Works

1. **Credentials restore is created first** (priority 0)
2. **Controller checks** for ConfigMaps with `backup-pvc` label in credentials backup
3. **If found**, controller waits for the specified PVC to exist
4. **Once PVC exists**, credentials restore completes
5. **Then**, other restores (resources, generic resources, managed clusters) are created

### Code Location

**Check Logic:** `restore_controller.go` - `processRestoreWait()` function  
**Triggered When:** `isCredsClsOnActiveStep == true` (credentials + managed clusters)

### Example ConfigMap

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: acm-pvcs-mongo-storage
  namespace: open-cluster-management-backup
  labels:
    cluster.open-cluster-management.io/backup-pvc: mongo-storage
data:
  some-key: some-value
```

This ConfigMap tells the controller to wait for a PVC named `mongo-storage` in namespace `open-cluster-management-backup`.

## Comparison: Sync vs Non-Sync

| Aspect | Sync Mode | Non-Sync Mode |
|--------|-----------|---------------|
| **Restore Resource** | User edits SAME resource | User creates NEW resources |
| **Backup Names** | Must use `latest` | Can use `latest` or specific names |
| **`-active` Suffix** | YES (avoids collision) | NO (no collision risk) |
| **Label Filter (Passive)** | NotIn activation | NotIn activation |
| **Label Filter (Activate)** | In activation + `-active` | NO filter OR In activation (if skip) |
| **Typical Use Case** | Active-Passive DR | Single-step DR or two-step DR |

## Two-Step Disaster Recovery Workflow

### Using Non-Sync Mode (Recommended)

**Step 1 - Restore Passive Data:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-step1-passive
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: skip
  veleroCredentialsBackupName: acm-credentials-schedule-20251029181055
  veleroResourcesBackupName: acm-resources-schedule-20251029181055
```
- Restores passive credentials and resources (NotIn label)
- Managed clusters stay on primary hub

**Step 2 - Activate Managed Clusters:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-step2-activate  # NEW restore resource
spec:
  cleanupBeforeRestore: CleanupRestored
  veleroManagedClustersBackupName: acm-managed-clusters-schedule-20251029181055
  veleroCredentialsBackupName: skip  # Already restored in step 1
  veleroResourcesBackupName: skip    # Already restored in step 1
```
- Creates credentials restore with In label (activation only) from MC backup
- Creates generic resources restore with In label (activation only) from MC backup
- Restores managed clusters
- Managed clusters connect to this hub

### Using Sync Mode

**Step 1 - Start Passive Sync:**
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Restore
metadata:
  name: restore-passive-sync
spec:
  syncRestoreWithNewBackups: true
  veleroManagedClustersBackupName: skip
  veleroCredentialsBackupName: latest
  veleroResourcesBackupName: latest
```

**Step 2 - Edit to Activate:**
```bash
oc edit restore restore-passive-sync -n open-cluster-management-backup
```
Change `veleroManagedClustersBackupName: latest`

## Verifying Restores

### Check ACM Restore Status

```bash
oc get restore.cluster.open-cluster-management.io -n open-cluster-management-backup
oc describe restore.cluster.open-cluster-management.io <restore-name> -n open-cluster-management-backup
```

### Check Velero Restores

```bash
# List all Velero restores
oc get restore.velero.io -n open-cluster-management-backup

# Check for -active restores (should only exist in sync mode or when creds/resources were skip)
oc get restore.velero.io -n open-cluster-management-backup | grep -- -active

# Check label selectors on a specific restore
oc get restore.velero.io <restore-name> -n open-cluster-management-backup -o yaml | grep -A5 labelSelector
```

### Expected Restore Names

**Sync Mode - Passive Phase:**
```
restore-name-acm-credentials-schedule-TIMESTAMP
restore-name-acm-resources-generic-schedule-TIMESTAMP
```

**Sync Mode - After Activation:**
```
restore-name-acm-credentials-schedule-TIMESTAMP
restore-name-acm-credentials-schedule-TIMESTAMP-active         # <-- -active suffix
restore-name-acm-resources-generic-schedule-TIMESTAMP
restore-name-acm-resources-generic-schedule-TIMESTAMP-active   # <-- -active suffix
restore-name-acm-managed-clusters-schedule-TIMESTAMP
```

**Non-Sync Mode - All Cases:**
```
# NO -active suffix (unless credentials/resources were originally skip)
restore-name-acm-credentials-schedule-TIMESTAMP
restore-name-acm-resources-generic-schedule-TIMESTAMP
restore-name-acm-managed-clusters-schedule-TIMESTAMP
```

## Validation Rules

### Sync Mode Requirements

When `syncRestoreWithNewBackups: true`:

| Requirement | Rule | Validation |
|-------------|------|------------|
| **Backup names** | Must use `latest` (not specific names) | ✅ Enforced |
| **Initial MC value** | Must be `skip` on first creation | ✅ Enforced |
| **Credentials** | Must be `latest` | ✅ Enforced |
| **Resources** | Must be `latest` | ✅ Enforced |

**Implementation:** The validation occurs in `controllers/restore.go` - `isValidSyncOptions()` function.

**When Validation is Checked:**
- On restore resource creation
- On restore resource update
- Prevents invalid configurations before any restores are created

### Non-Sync Mode Requirements

When `syncRestoreWithNewBackups: false` or not set:

| Requirement | Rule |
|-------------|------|
| **Backup names** | Can use `skip`, `latest`, or specific names |
| **MC value** | Can be `skip`, `latest`, or specific name |
| **Workflow** | Create separate restore resources (don't edit existing ones) |

## Quick Reference

### When do I see `-active` restores?

✅ **Sync mode** when managed clusters are activated (user edits restore resource from MC=skip to MC=latest)  
✅ **Non-sync mode** when credentials/resources were set to `skip` but MC is being restored  
❌ **Never in non-sync mode** when credentials/resources are set to `latest` or specific names  
❌ **Never in sync mode on first run** (prevented by validation)

### When are label selectors applied?

| Scenario | Label Selector |
|----------|----------------|
| MC == skip (passive) | **NotIn** activation |
| Sync mode + MC != skip | **In** activation (on `-active` restores) |
| Non-sync + MC != skip + Creds/Res was skip | **In** activation |
| Non-sync + MC != skip + Creds/Res NOT skip | **NO filter** |

### Restore Priority Order

Restores are created in this order:
1. **Credentials** (priority 0) - **May pause for PVC wait**
2. CredentialsHive (priority 1)
3. CredentialsCluster (priority 2)
4. **Resources** (priority 3)
5. **ResourcesGeneric** (priority 4)
6. **ManagedClusters** (priority 5)

## Related Documentation

- [Main README](../README.md#restoring-a-backup)
- [Restore API Reference](api-crd-restore.md)
- [BackupSchedule API Reference](api-crd-backupschedule.md)
- [Active-Passive Configuration](../README.md#active-passive-configuration-design)

## Troubleshooting

### Sync mode validation error

**Error:** "When syncRestoreWithNewBackups is true, veleroManagedClustersBackupName must initially be set to 'skip'..."

**Cause:** Trying to create a sync restore with `veleroManagedClustersBackupName` set to `latest` from the beginning.

**Solution:** 
1. Create the restore with `veleroManagedClustersBackupName: skip`
2. Wait for passive data to sync
3. When ready to activate, edit the restore to set `veleroManagedClustersBackupName: latest`

**Alternative:** If you want to restore everything at once (not using sync workflow), set `syncRestoreWithNewBackups: false` or omit it.

### Restore stuck in "Started" phase

**Possible Cause:** Waiting for PVC creation

**Check:**
```bash
# Check restore status message
oc describe restore.cluster.open-cluster-management.io <restore-name> -n open-cluster-management-backup

# Look for ConfigMaps with backup-pvc label
oc get configmap -n open-cluster-management-backup -l cluster.open-cluster-management.io/backup-pvc

# Check if required PVC exists
oc get pvc -n open-cluster-management-backup
```

**Solution:** Create the required PVC as specified in the ConfigMap label

### Activation data not restored

**Possible Cause:** Label selector filtering out activation resources

**Check:**
```bash
# Check if restore has label selector
oc get restore.velero.io <restore-name> -n open-cluster-management-backup -o yaml | grep -A5 labelSelector
```

**Expected:**
- If you see `NotIn cluster-activation`: This is passive mode, activation data is filtered out (correct)
- If you see `In cluster-activation`: This is activation mode, only activation data is restored (correct)
- If you see no labelSelector: All data should be restored

### Missing `-active` restores in sync mode

**Possible Cause:** Managed clusters not activated yet

**Check:**
```bash
# Verify managed clusters backup name
oc get restore.cluster.open-cluster-management.io <restore-name> -n open-cluster-management-backup -o yaml | grep veleroManagedClustersBackupName
```

**Expected:** Should be `latest` (not `skip`) for activation


