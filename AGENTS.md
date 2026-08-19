# AI Agent Guide — cluster-backup-operator

This file provides AI-specific guidance for working with this repository. It supplements the [README](README.md) and [CONTRIBUTING](CONTRIBUTING.md) with context that helps AI coding assistants generate correct, consistent code.

## What This Operator Does

The cluster-backup-operator provides disaster recovery for ACM (Red Hat Advanced Cluster Management) hub clusters. It creates and manages Velero backup schedules and restores for hub configuration — managed clusters, policies, applications, credentials, and other hub resources.

It does NOT handle application DR on managed clusters or Velero internals (that's the OADP operator).

## Repository Structure

```
main.go                     — Entrypoint: scheme registration, manager setup, TLS config, Velero CRD gate
api/v1beta1/                — CRD types (BackupSchedule, Restore), deepcopy, webhook validation
controllers/
  schedule_controller.go    — BackupScheduleReconciler (creates/manages Velero schedules)
  schedule.go               — Schedule helpers: collision detection, phase aggregation, cron validation
  backup.go                 — Backup content selection: API group filtering, label rules, exclusions
  restore_controller.go     — RestoreReconciler (creates/manages Velero restores, sync logic)
  restore.go                — Restore planning, phase logic, sync detection, backup selection
  restore_post.go           — Post-restore: cleanup (CleanupRestored/CleanupAll), auto-import
  pre_backup.go             — Pre-backup: MSA addon/token lifecycle, Hive secret preparation
  utils.go                  — Shared helpers: sorting, hub ID, BSL check, Velero CRD probe
  create_helper.go          — Test-only constructors for fake Velero/ACM objects
pkg/tlsconfig/              — TLS configuration utilities (BuildTLSConfig, GetTLSProfileType)
config/                     — Kustomize: CRDs, RBAC, manager deployment, webhook patches
config/samples/             — Example CRs for all backup/restore scenarios
hack/crds/                  — Extra CRDs required by envtest (Velero, OCM, Hive, OpenShift, etc.)
images/                     — Architecture diagrams (PNG) referenced by README
docs/                       — Architecture and design documentation
```

### Other Key Files

| File | Purpose |
|------|---------|
| `PROJECT` | Kubebuilder v3 project file — defines CRD scaffolding |
| `OWNERS` | Prow approvers and reviewers for this repo |
| `COMPONENT_NAME` | Component name for CI build harness (`cluster-backup-controller`) |
| `COMPONENT_VERSION` | Component version for CI build harness |
| `sonar-project.properties` | SonarCloud config — excludes `api/v1beta1/` and `main.go` from analysis |
| `Makefile.prow` | CI wrapper for Makefile — `unit-tests` target calls `make test` |
| `.github/dependabot.yml` | Weekly Go module updates on main |
| `.github/renovate.json` | Renovate — disabled on release branches, vulnerability alerts enabled |
| `.github/workflows/auto-label-release-branch.yml` | Auto-labels PRs targeting release branches |

## Key Design Patterns

### Two Reconcilers
- **BackupScheduleReconciler** watches `BackupSchedule` CRs and creates 5 Velero `Schedule` objects
- **RestoreReconciler** watches `Restore` CRs and creates Velero `Restore` objects in sequence

### CRD Gate in main.go
The operator blocks startup until Velero CRDs are present (OADP installed), polling every 30 seconds. This is intentional — don't remove it.

### Phase-Driven State Machines
Both `BackupSchedule` and `Restore` use phase fields to track state. Phase transitions are computed in `setSchedulePhase()` and `setRestorePhase()`. These functions are critical — changes must be carefully validated.

### Package-Level Variables in backup.go
`backupManagedClusterResources`, `excludedAPIGroups`, `excludedCRDs`, etc. are package-level slices that define what gets backed up. `processResourcesToBackup()` works with a local copy to avoid concurrent mutation.

## Coding Conventions

### Go Style
- Go 1.25
- `goimports` for formatting
- Structured logging: `log.FromContext(ctx).Info("message", "key", value)`
- Error wrapping: use `fmt.Errorf("...: %w", err)` (not `github.com/pkg/errors` for new code)
- All exported functions and types need doc comments

### Linting
- golangci-lint v2.7.2 with extra linters: `funlen`, `lll`, `misspell`, `unparam`
- `funlen` limit: 60 statements per function. Use `//nolint:funlen` sparingly — prefer decomposition
- Run `make lint` before submitting

### Testing
- Framework: Ginkgo v2 + Gomega with envtest
- Test CRDs are in `hack/crds/` (Velero, OCM, Hive, OpenShift)
- All controller tests are in `controllers/*_test.go`
- `pkg/tlsconfig/` has its own unit tests
- Test target: `make test` (runs `./controllers/...` and `./pkg/...`)
- Test helpers/constructors are in `create_helper.go` — these are test-only, not production code

### Naming
- Velero schedule names follow the pattern: `acm-{type}-schedule` (e.g., `acm-credentials-schedule`)
- Backup labels use the prefix `cluster.open-cluster-management.io/`
- Constants for label keys and schedule names are in `backup.go`

### CI and Branching
- PRs are squash-merged via Tide
- `main` fast-forwards to the current release branch (e.g., `release-2.17`) on post-submit
- CI uses `Makefile.prow` which wraps the regular Makefile
- Required checks: images, unit-tests, sonar, pr-image-mirror, crd-and-gen-files-check
- Release branches also require Konflux build + Enterprise Contract checks

### Build and Dockerfile
- **Dockerfile** — Used by Prow CI. Builder: `stolostron/builder:go1.25-linux`. `CGO_ENABLED=0`.
- **Dockerfile.rhtap** — Used by Konflux. Builder: `brew.registry.redhat.io` with FIPS (`GOEXPERIMENT=strictfipsruntime`). `CGO_ENABLED=1`. Includes Red Hat labels and LICENSE copy.
- Both Dockerfiles must copy the same source directories. If you add a new top-level package (like `pkg/`), add `COPY <dir>/ <dir>/` to **both** Dockerfiles.
- Multi-arch builds (x86_64, ppc64le, s390x, arm64) are done by Konflux only.

### Konflux Pipeline
- `.tekton/` contains PipelineRun definitions per release branch (e.g., `cluster-backup-operator-acm-217-push.yaml`)
- Pipeline is shared from `stolostron/konflux-build-catalog` (`pipelines/common.yaml`)
- Builds are hermetic with gomod prefetch
- Push builds trigger on merge to release branches; PR builds trigger on pull requests
- Slack notifications are sent on push builds via `backup-team-slack-notification-secret`

## Important Constraints

### Only One Active Restore
The operator enforces that only one Restore can be active at a time. "Active" means any phase except `Finished` or `FinishedWithErrors`. A second Restore gets `FinishedWithErrors`.

### BackupSchedule vs Restore Conflict
A non-paused BackupSchedule cannot coexist with an active Restore (gets `FailedValidation`). A paused schedule can coexist. A completed Restore does not block a schedule.

### Webhook Validation
The Restore webhook (`api/v1beta1/restore_webhook.go`) enforces a two-step workflow for sync mode: create with `veleroManagedClustersBackupName: skip`, then update to `latest` when phase is `Enabled`.

### Collision Detection
`scheduleOwnsLatestStorageBackups()` in `schedule.go` compares the `backup-cluster` label on the latest backup against this hub's cluster ID. If they differ, the schedule enters `BackupCollision`.

### local-cluster Exclusion
The `local-cluster` namespace is excluded from managed-clusters and resources backups. This is intentional — restoring local-cluster data would corrupt the target hub.

### CleanupRestored Logic
`cleanupDeltaForResourcesBackup()` in `restore_post.go` uses label selectors based on `velero.io/backup-name` to identify resources from a previous restore that are not in the current backup. Resources without this label are not cleaned up in `CleanupRestored` mode.

## Common Pitfalls

- Don't append to package-level slices in `backup.go` without using a local copy
- Don't create a second Restore resource — edit the existing one
- Don't rely on Go default TLS settings — the operator explicitly configures TLS from the APIServer profile
- Don't skip the Velero CRD gate in main.go — it prevents crashes when OADP isn't installed
- `PartiallyFailed` Velero restores are often normal (empty backup files) — don't treat them as hard errors
- Test functions must stay under 60 lines or the CI `funlen` linter will fail

## Related Repositories

| Repo | What it contains |
|------|-----------------|
| [multiclusterhub-operator](https://github.com/stolostron/multiclusterhub-operator) | Helm chart that deploys this operator (`pkg/templates/charts/toggle/cluster-backup/`) |
| [OADP operator](https://github.com/openshift/oadp-operator) | Installs Velero, manages DataProtectionApplication |
| [Velero](https://github.com/vmware-tanzu/velero) | Backup/restore engine used by this operator |

## Further Reading

- [README.md](README.md) — Full design documentation, backup/restore flows, samples, scale numbers
- [docs/architecture.md](docs/architecture.md) — Controller design, phase state machines, data flow
- [Official docs](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.16/html/business_continuity/index)
