# Copilot Instructions for AI Agents

This repository implements a Kubernetes operator for managing Helm charts and Kustomize via custom resources. AI agents should follow these guidelines for effective contributions:

## Architecture Overview
- **Main Components:**
  - `controller/`: Core controller logic for managing instances.
  - `applyer/`: Handles applying resources using Helm, Kustomize, and native YAML.
  - `apis/`: API definitions and registration for custom resources.
  - `cmd/installer/`: Entrypoint for the installer controller.
  - `deploy/installer/`: Helm chart and CRDs for deploying the controller.
- **Data Flow:**
  - Users create `Instance` custom resources (CRs) specifying Helm/Kustomize details.
  - Controller reconciles CRs, applies resources using the appropriate method.
  - Status and events are updated in the CRs for user visibility.

## Developer Workflows
- **Build:**
  - Use `make` for common build tasks (see `Makefile`).
- **Test:**
  - Run Go tests with `make test` or `go test ./...`.
  - Test data is in `testdata/`.
- **Debug:**
  - Main entrypoint: `cmd/installer/main.go`.
  - Use logs and CR status fields for troubleshooting.

## Project-Specific Patterns
- **Custom Resource Definitions (CRDs):**
  - Defined in `charts/installer/crds/` and referenced in controller logic.
- **Helm & Kustomize Integration:**
  - Helm logic: `applyer/helm/`
  - Kustomize logic: `applyer/kustomize/`
  - Native YAML: `applyer/native/`
- **Status Reporting:**
  - Status fields in CRs are updated for install progress and errors.

## Conventions & Patterns
- **Go Modules:**
  - Managed via `go.mod`.
- **Directory Structure:**
  - Keep logic modular by resource type (Helm, Kustomize, Native).
- **Testing:**
  - Place test data in `testdata/`.
- **Documentation:**
  - User and developer docs in `docs/` and `README.md`.

## Integration Points
- **Kubernetes API:**
  - Interacts with CRDs and standard resources.
- **Helm CLI:**
  - Uses Helm commands for chart management.
- **Kustomize:**
  - Applies manifests from remote tarballs or git revisions.

## Examples
- See `examples/` for sample specs.
- Reference `README.md` and `docs/` for usage patterns.

---

For questions or unclear patterns, review the referenced files or ask for clarification. Update this file as new conventions emerge.
