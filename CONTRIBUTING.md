# Contributing to cluster-backup-operator

## Table of Contents

- [Getting Started](#getting-started)
- [Development Environment](#development-environment)
- [Building](#building)
- [Testing](#testing)
- [Linting](#linting)
- [Code Generation](#code-generation)
- [Submitting Changes](#submitting-changes)
- [Commit Sign-off](#commit-sign-off)
- [Pull Request Process](#pull-request-process)
- [CI Pipeline](#ci-pipeline)
- [Certificate of Origin](#certificate-of-origin)

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/<your-username>/cluster-backup-operator.git
   cd cluster-backup-operator
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/stolostron/cluster-backup-operator.git
   ```

## Development Environment

### Prerequisites

- Go 1.25+
- `oc` or `kubectl` CLI configured with a cluster (for running outside cluster)
- Operator SDK (for CRD/manifest generation)
- Access to an OpenShift cluster with ACM and OADP installed (for integration testing)

### Tool Installation

The Makefile will download these tools automatically into `./bin/` on first use:

- **controller-gen** (v0.17.3) — generates CRDs, RBAC, deepcopy
- **kustomize** (v4.5.2) — builds Kubernetes manifests
- **golangci-lint** (v2.7.2) — Go linter
- **setup-envtest** — sets up envtest binaries for testing

No manual tool installation is needed — just run `make` targets.

### Project Structure

```
main.go              — Operator entrypoint
api/v1beta1/         — CRD types, webhook validation, deepcopy
controllers/         — All reconciliation logic and tests
pkg/tlsconfig/       — TLS configuration utilities and tests
config/              — Kustomize manifests (CRDs, RBAC, deployment, samples)
hack/crds/           — Extra CRDs needed by envtest
```

## Building

```bash
# Build the manager binary
make build

# Build the Docker image
make docker-build IMG=<registry>/<image>:<tag>

# Push the Docker image
make docker-push IMG=<registry>/<image>:<tag>
```

## Testing

Tests use [Ginkgo v2](https://onsi.github.io/ginkgo/) with [Gomega](https://onsi.github.io/gomega/) matchers and [envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest) for a lightweight Kubernetes API server.

### Running Tests

```bash
# Run all tests (includes manifests, codegen, fmt, vet, lint)
make test
```

This runs `go test ./controllers/... ./pkg/...` with envtest.

### How envtest Works

The test suite (`controllers/suite_test.go`) starts a local API server using envtest. CRDs are loaded from two locations:
- `config/crd/bases/` — the operator's own CRDs (BackupSchedule, Restore)
- `hack/crds/` — external CRDs needed for testing (Velero, OCM, Hive, OpenShift, etc.)

If you add a dependency on a new CRD type in the controller code, you'll need to add its CRD YAML to `hack/crds/` for tests to work.

### Test Structure

- Controller tests are in `controllers/*_test.go` (one per controller/module)
- TLS tests are in `pkg/tlsconfig/tlsconfig_test.go`
- Test object constructors are in `controllers/create_helper.go` (not production code)
- Tests run in the `controllers` package (white-box testing)

### Writing Tests

- Keep test functions under 60 lines (the `funlen` linter enforces this in CI)
- Use table-driven tests where applicable
- If a test needs a Velero or ACM resource, use the constructors in `create_helper.go`
- For new CRD types, add the CRD YAML to `hack/crds/`

## Linting

```bash
# Run the full linter suite
make lint
```

The project uses golangci-lint v2 with these extra linters enabled:
- **funlen** — max 60 statements per function (ignore comments). Use `//nolint:funlen` sparingly.
- **lll** — line length limit
- **misspell** — catches common spelling mistakes
- **unparam** — detects unused function parameters

The formatter `goimports` is also enabled. Configuration is in `.golangci.yml`.

## Code Generation

After modifying CRD types in `api/v1beta1/`:

```bash
# Regenerate CRDs, RBAC, and webhook manifests
make manifests

# Regenerate DeepCopy methods
make generate
```

Always run both after changing `*_types.go` files. The CI will fail if generated files are out of date.

## Submitting Changes

### Commit Sign-off

All commits must be signed off (DCO):

```bash
git commit -s -m "Your commit message"
```

To amend:
```bash
git commit --amend --signoff
```

To sign off an entire branch:
```bash
git rebase --signoff main
```

### Pull Request Process

1. Create a feature branch from the latest `main` (or the appropriate `release-X.XX` branch)
2. Make your changes
3. Run the full check before submitting:
   ```bash
   make test
   ```
   This runs manifests, codegen, fmt, vet, lint, and tests.
4. Push to your fork and create a PR against the upstream branch
5. The PR must have at least one approval from a repo maintainer
6. All CI checks must pass

### Branch Conventions

- `main` — development branch for the next ACM release
- `release-X.XX` — release branches (e.g., `release-2.14`, `release-2.17`)
- Feature branches should be created from the target branch

## CI Pipeline

### Prow (upstream)
- Runs `make test` (unit tests with envtest)
- Runs `make lint` (golangci-lint)
- SonarCloud quality gate

### Konflux/RHTAP (downstream)
- Hermetic builds from `.tekton/` PipelineRun definitions
- Multi-arch builds (x86_64, ppc64le, s390x, arm64)
- Enterprise Contract validation
- Uses `Dockerfile.rhtap` (FIPS-enabled)

## Certificate of Origin

All contributions must be submitted under the [Apache Public License 2.0](https://www.apache.org/licenses/LICENSE-2.0). By contributing, you agree to the Developer Certificate of Origin (DCO). See the [DCO](DCO) file for details.

## Issue and Pull Request Management

Anyone may comment on issues and submit reviews. To be assigned an issue or PR, you must be a member of the [stolostron](https://github.com/stolostron) GitHub organization. Maintainers can assign with `/assign <GitHub ID>`.
