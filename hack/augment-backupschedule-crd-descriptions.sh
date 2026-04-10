#!/usr/bin/env bash
# Re-applies ACM-specific descriptions to Velero Schedule template fields embedded in the
# BackupSchedule CRD. controller-gen copies Velero's BackupSpec without ACM context; this
# script runs after `make manifests` to restore those additions.
set -euo pipefail
CRD="${1:-config/crd/bases/cluster.open-cluster-management.io_backupschedules.yaml}"
export AUGMENT_BACKUPSCHEDULE_CRD="$CRD"
python3 <<'PY'
import os
import pathlib
import sys

path = pathlib.Path(os.environ["AUGMENT_BACKUPSCHEDULE_CRD"])
text = path.read_text()

marker = "For ACM hub backups, increase this if CSI"
if marker in text:
    print(f"ACM Velero field descriptions already present in {path}, skipping")
    sys.exit(0)

replacements = [
    (
        """                          csiSnapshotTimeout:
                            description: |-
                              CSISnapshotTimeout specifies the time used to wait for CSI VolumeSnapshot status turns to
                              ReadyToUse during creation, before returning error as timeout.
                              The default value is 10 minute.
                            type: string""",
        """                          csiSnapshotTimeout:
                            description: |-
                              CSISnapshotTimeout specifies the time used to wait for CSI VolumeSnapshot status turns to
                              ReadyToUse during creation, before returning error as timeout.
                              The default value is 10 minute.
                              For ACM hub backups, increase this if CSI snapshot creation for hub PVCs (for example storage-backed
                              volumes for hub components) is slow; otherwise keep the default unless Velero backup logs show CSI timeout errors.
                            type: string""",
    ),
    (
        """                          datamover:
                            description: |-
                              DataMover specifies the data mover to be used by the backup.
                              If DataMover is "" or "velero", the built-in data mover will be used.
                            type: string""",
        """                          datamover:
                            description: |-
                              DataMover specifies the data mover to be used by the backup.
                              If DataMover is "" or "velero", the built-in data mover will be used.
                              For ACM hub backups, set or change this when your storage class requires the Velero node-agent data
                              mover path instead of snapshots alone (for example large or non-snapshot volumes); leave empty for typical snapshot-based hub backups.
                            type: string""",
    ),
    (
        """                          defaultVolumesToFsBackup:
                            description: |-
                              DefaultVolumesToFsBackup specifies whether pod volume file system backup should be used
                              for all volumes by default.
                            nullable: true
                            type: boolean""",
        """                          defaultVolumesToFsBackup:
                            description: |-
                              DefaultVolumesToFsBackup specifies whether pod volume file system backup should be used
                              for all volumes by default.
                              For ACM hub backups, set to true when hub workloads use volumes that cannot use snapshots or when
                              file-system backup is required for specific hub data; otherwise leave false/unset to prefer snapshot backup where supported.
                            nullable: true
                            type: boolean""",
    ),
]

for old, new in replacements:
    if old not in text:
        print(
            "augment-backupschedule-crd-descriptions: expected Velero block not found; "
            "update hack/augment-backupschedule-crd-descriptions.sh if Velero CRD schema changed",
            file=sys.stderr,
        )
        sys.exit(1)
    text = text.replace(old, new)

path.write_text(text)
print(f"Updated {path}")
PY
