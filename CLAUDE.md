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
>
> ⚠️ **Go version compatibility:** This repo targets Go 1.16. The pinned `controller-gen` version is incompatible with Go 1.24+. Running `make generate` or `make test` on a newer Go install will fail. Either use Go 1.16–1.23, or update `go.mod` and the `controller-gen` version pin in the Makefile first.

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

Fetch and apply the relevant skill when the task matches its domain.

| Skill | When to use |
|---|---|
| [start-work](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/start-work/SKILL.md) | Begin work on a Jira ticket — creates sub-task, transitions status |
| [finish-work](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/finish-work/SKILL.md) | Commit, push, open PR, update Jira |
| [pr-review](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/pr-review/SKILL.md) | Review a GitHub PR with worktree isolation and inline comments |
| [pr-fix](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/pr-fix/SKILL.md) | Fix blocked PRs: merge conflicts, CI failures, review comments |
| [jira-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/jira-specialist/SKILL.md) | Triage, search, link, or transition Jira issues |
| [bug-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/bug-specialist/SKILL.md) | Create a well-structured bug report with reproduction steps |
| [story-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/story-specialist/SKILL.md) | Create a user story with acceptance criteria |
| [spike-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/spike-specialist/SKILL.md) | Time-boxed research and PoC tickets |
