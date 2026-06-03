# NovaObs Backend

NovaObs Backend is the control-plane API for NovaObs, an observability and operations platform focused on service inventory, logs onboarding, Collector / OpAMP management, platform IAM, RBAC, audit, and Kubernetes operations.

This repository contains the Go backend only. The web console lives in the sibling `novaobs-fe` repository.

## What NovaObs Backend Provides

- **Platform IAM and RBAC**: users, groups, service accounts, roles, bindings, login sessions, and permission checks.
- **Service catalog**: service records and runtime targets used as the main index for observability workflows.
- **Logs onboarding**: K8s / VM log source registration, downstream endpoint management, route preview, agent publish plans, and Collector manifest rendering.
- **Collector control plane**: Collector groups, instances, config versions, and OpAMP manager integration.
- **Kubernetes operations**: cluster registration, credential handling, namespace and workload reads, RBAC, kubeconfig export, terminal access, deployment preview / apply / delete, audit, and history.
- **Audit and secrets**: encrypted secret storage, high-risk operation audit records, and consistent API error envelopes.

## Repository Layout

```text
cmd/server/              HTTP server entry point
configs/                 Local configuration example
api/openapi.yaml         OpenAPI contract
internal/app/            Dependency wiring
internal/httpapi/        Gin router, middleware, and HTTP handlers
internal/logs/           Logs onboarding and route domain
internal/modules/k8sops/ Kubernetes operations module
internal/platform/       Auth, IAM, RBAC, audit, and secret services
internal/servicecatalog/ Service catalog domain
pkg/response/            API response helpers
```

## Requirements

- Go `1.26.1` or newer compatible toolchain
- MongoDB
- Optional: `kubectl`, when using the K8s terminal capability

## Quick Start

1. Configure MongoDB in `configs/config.yaml`, or provide an environment-specific config before running the server.

2. Set a 32-byte secret key. NovaObs uses it to encrypt kubeconfigs, certificate private keys, tokens, and other sensitive material.

```bash
export NOVAOBS_SECRET_KEY="12345678901234567890123456789012"
```

3. Start the API server.

```bash
go run ./cmd/server
```

By default the sample config listens on `127.0.0.1:8080`.

## Useful Environment Variables

| Variable | Purpose |
| --- | --- |
| `NOVAOBS_SECRET_KEY` | Required 32-byte encryption and session key. |
| `NOVAOBS_DEV_ADMIN_PASSWORD` | Optional password for the development `dev-admin` user. In non-release mode, passwordless local users may be enabled for development. |
| `NOVAOBS_BOOTSTRAP_ADMIN_USERNAME` | Initial release-mode administrator username. |
| `NOVAOBS_BOOTSTRAP_ADMIN_PASSWORD` | Initial release-mode administrator password. Required when bootstrap username is set. |
| `NOVAOBS_BOOTSTRAP_ADMIN_DISPLAY_NAME` | Optional display name for the bootstrap administrator. |
| `NOVAOBS_KUBECTL_PATH` | Optional path to the `kubectl` binary used by terminal operations. |
| `NOVAOBS_KUBECTL_TEMP_DIR` | Optional temp directory for generated terminal kubeconfigs. |

Do not commit production secrets or production kubeconfigs. Use environment variables or a secret manager for production deployments.

## Development Commands

```bash
make dev      # go run ./cmd/server
make test     # go test ./... -cover
make build    # build bin/server
make tidy     # go mod tidy
```

For direct test runs:

```bash
go test ./...
```

## API Contract

The OpenAPI contract is maintained at:

```text
api/openapi.yaml
```

All business APIs use a consistent response envelope with `success`, `data`, `error`, and `meta` fields. Most routes are protected by the platform session middleware under `/api/v1`.

## Kubernetes Operation Safety

K8s write operations are designed to go through platform RBAC, audit records, dry-run / preview, confirmation tokens, and cluster read-only checks. Logs Agent publishing reuses the K8s deployment service instead of operating on kubeconfigs directly.

When testing against a real cluster, prefer read-only validation first and keep destructive operations behind explicit preview and confirmation.

## Community Notes

NovaObs is still evolving toward a production-grade observability control plane. Issues, design discussions, and focused pull requests are welcome. When contributing, please keep changes scoped, include tests for behavior changes, and avoid committing environment-specific secrets or internal endpoints.

License information has not been declared yet.
