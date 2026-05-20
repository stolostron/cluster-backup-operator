# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

The Cluster Backup Operator is a Kubernetes operator for Red Hat Advanced Cluster Management (ACM) that provides disaster recovery solutions for ACM hub clusters. It integrates with the OADP (OpenShift API for Data Protection) operator and Velero to create scheduled backups and restore capabilities for hub resources including managed clusters, applications, policies, and credentials.

## Key Commands

### Development Commands
```bash
# Build the operator binary
make build

# Run tests with linting
make test

# Run the operator locally (outside cluster)
make run

# Install CRDs to cluster
make install

# Deploy operator to cluster
make deploy IMG=<your-image>

# Remove CRDs from cluster
make uninstall

# Format code
make fmt

# Lint code
make lint

# Generate manifests and code
make generate manifests
```

### Docker Commands
```bash
# Build container image
make docker-build IMG=<your-image>

# Push container image
make docker-push IMG=<your-image>
```

### Testing Commands
```bash
# Run unit tests with coverage
KUBEBUILDER_ASSETS="$(shell make -s envtest use 1.31 -p path)" go test ./controllers/... -coverprofile cover.out

# Run specific test file
go test ./controllers/schedule_test.go -v

# Run tests with ginkgo directly
ginkgo ./controllers/...
```

## Architecture Overview

### Core Custom Resources
- **BackupSchedule** (`cluster.open-cluster-management.io/v1beta1`): Defines scheduled backup operations with cron syntax
- **Restore** (`cluster.open-cluster-management.io/v1beta1`): Manages restore operations from backup storage

### Main Controllers

#### BackupScheduleReconciler (`controllers/schedule_controller.go`)
- Manages `BackupSchedule` resources
- Creates and manages underlying Velero `Schedule` resources
- Handles backup collision detection when multiple hubs write to same storage
- Supports ManagedServiceAccount integration for automatic import of managed clusters
- Three backup types: credentials, resources, and managed clusters

#### RestoreReconciler (`controllers/restore_controller.go`)
- Manages `Restore` resources
- Orchestrates cleanup and restore operations via Velero
- Supports passive and activation data restore modes
- Can sync continuously with new backups when `syncRestoreWithNewBackups: true`

### Key Components

#### Backup Logic (`controllers/backup.go`, `controllers/pre_backup.go`)
- Resource filtering based on CRD groups and labels
- Backup collision detection and validation
- ManagedServiceAccount token generation for imported clusters

#### Restore Logic (`controllers/restore.go`, `controllers/restore_post.go`)
- Pre-restore cleanup with three modes: `None`, `CleanupRestored`, `CleanupAll`
- Post-restore operations including auto-import secret creation
- Support for continuous backup synchronization

#### Utilities (`controllers/utils.go`)
- Velero CRD presence validation
- Resource manipulation helpers
- Label and annotation management

### Directory Structure

```
api/v1beta1/           # Custom Resource Definitions
controllers/           # Controller implementations
config/                # Kubernetes manifests and configuration
  crd/bases/          # Generated CRD manifests
  samples/            # Sample CR configurations
  rbac/               # RBAC configurations
hack/                  # Development scripts and tools
```

## Key Dependencies

- **Velero**: Core backup/restore engine via `github.com/vmware-tanzu/velero`
- **Open Cluster Management**: ACM APIs via `open-cluster-management.io/api`
- **OpenShift APIs**: Hive and config APIs for OpenShift integration
- **Controller Runtime**: Kubernetes controller framework via `sigs.k8s.io/controller-runtime`

## Development Guidelines

### Testing Strategy
- Unit tests use Ginkgo/Gomega framework
- Tests cover controller reconciliation logic, backup/restore workflows, and utility functions
- Mock Kubernetes clients for testing
- Integration tests validate CRD and Velero interactions

### Code Organization
- Controllers follow standard controller-runtime patterns
- Business logic separated into dedicated files (backup.go, restore.go)
- Extensive test coverage with separate test files for each component
- Utility functions centralized in utils.go

### Build and CI
- Standard Go project with Makefile for common tasks
- golangci-lint configuration in `.golangci.yml`
- Docker builds produce operator container images
- Kubernetes manifests generated via controller-gen and kustomize

## Important Configuration

### Operator Requirements
- Requires Velero CRDs to be present before starting (checked in main.go:164)
- Runs in `open-cluster-management-backup` namespace by default
- Needs RBAC permissions for managing Velero resources and ACM resources

### Backup Storage
- Uses Velero BackupStorageLocation for backup destination
- Supports multiple backup types: credentials, resources, managed clusters
- Backup collision detection prevents multiple hubs writing to same location

### Restore Modes
- **Passive**: Restore resources without activating managed cluster connections
- **Activation**: Restore resources that activate managed clusters on new hub
- **Sync**: Continuously sync with new backups for passive hub scenarios