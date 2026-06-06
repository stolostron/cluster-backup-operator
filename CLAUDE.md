# CLAUDE.md — cluster-backup-operator

AI assistant context for the **Cluster Backup and Restore Operator** — an ACM hub operator that drives Velero to back up and restore Open Cluster Management hub resources.

---

## Build, Test, and Lint Commands

```bash
# Generate CRDs, RBAC manifests, and DeepCopy code
make manifests generate

# Format and vet
make fmt
make vet

# Build the manager binary (runs generate + fmt + vet first)
make build

# Run tests (runs manifests + generate + fmt + vet first, downloads envtest)
make test

# Run controller locally against the current kubeconfig
make run

# Install CRDs into the cluster
make install

# Deploy controller into the cluster (set IMG first)
make deploy IMG=<registry>/<image>:<tag>

# Build container image
make docker-build IMG=<registry>/<image>:<tag>
```

> Tests use `controller-runtime`'s envtest harness. `testbin/` is populated automatically on first `make test`.

---

## Repo Layout

```
api/v1beta1/          CRD type definitions (Backup, Restore)
controllers/          Reconciler implementations and helpers
  backup_controller.go   BackupReconciler — drives Velero Backup CRs
  restore_controller.go  RestoreReconciler — drives Velero Restore CRs
  backup.go              Backup helper functions
  restore.go             Restore helper functions
config/               Kustomize manifests (CRDs, RBAC, manager, samples)
config/samples/       Example Backup and Restore CRs
hack/                 Boilerplate and tooling scripts
testbin/              Downloaded envtest binaries (git-ignored)
bin/                  Downloaded tooling (controller-gen, kustomize)
```

---

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for a full description of the CRDs, Velero schedule strategy, backup selection logic, passive vs. activation data categories, controller reconciliation flow, and disaster-recovery patterns.

---

## Personal Config

Read `.claude/user.local.md` at the start of any task that needs an assignee, email, or project key. If the file does not exist, fall back to Claude memory (`user-config`), then placeholders. Run `make personalize` to generate it (if this repo uses Fleet Engineering tooling).

---

## Fleet Engineering Skills

| Skill | Purpose | Path |
|---|---|---|
| `bug-specialist` | Bug triage, reproduction steps, fix planning | `skills/jira/bug-specialist/` |
| `epic-specialist` | Multi-sprint epics with outcomes | `skills/jira/epic-specialist/` |
| `feature-specialist` | Large customer-facing capabilities | `skills/jira/feature-specialist/` |
| `initiative-specialist` | Multi-team strategic programs | `skills/jira/initiative-specialist/` |
| `jira-create` | Interactive issue creation with specialist delegation | `skills/jira/jira-create/` |
| `jira-specialist` | General triage, search, linking, transitions | `skills/jira/jira-specialist/` |
| `outcome-specialist` | Strategic outcomes tied to OKRs | `skills/jira/outcome-specialist/` |
| `spike-specialist` | Time-boxed research and PoC | `skills/jira/spike-specialist/` |
| `story-specialist` | User stories with acceptance criteria | `skills/jira/story-specialist/` |
| `task-specialist` | Internal technical tasks | `skills/jira/task-specialist/` |
| `agent-memory-setup` | Initialize or update CLAUDE.md / AGENTS.md | `skills/sdlc/agent-memory-setup/` |
| `finish-work` | Commit, push, open PR, update Jira | `skills/sdlc/finish-work/` |
| `pr-fix` | Fix blocked PRs: merge conflicts, CI failures, review comments | `skills/sdlc/pr-fix/` |
| `pr-review` | GitHub PR review with inline comments | `skills/sdlc/pr-review/` |
| `start-work` | Create a Jira sub-task for the current work session | `skills/sdlc/start-work/` |
| `f2f-daily-summary` | Capture daily F2F meeting notes as Jira sub-tasks | `skills/meetings/f2f-daily-summary/` |
| `f2f-epic-specialist` | Create and manage F2F meeting Epics | `skills/meetings/f2f-epic-specialist/` |
| `presentation-task` | Log a delivered presentation as a closed Jira sub-task | `skills/meetings/presentation-task/` |

Skills live in the shared [`agentic-sdlc`](https://github.com/sahare/agentic-sdlc) repository. Read the relevant `SKILL.md` before executing any multi-step workflow.
