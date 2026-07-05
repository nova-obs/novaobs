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
- MongoDB replica set（告警规则、更新记录与审计使用事务保证原子性）
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
| `NOVAOBS_ALERT_INGEST_TOKEN` | vmalert 投递到 NovaObs Alert Ingest 时使用的独立 Bearer Token；release 模式必填。 |
| `NOVAOBS_ALERT_INGEST_URL` | 日志告警 vmalert Runtime 默认写入的 NovaObs notifier base URL，例如 `http://novaobs-api:8080`。 |

Do not commit production secrets or production kubeconfigs. Use environment variables or a secret manager for production deployments.

## 日志告警 Runtime

API 进程管理规则生产真值、历史测试、Runtime artifact 编译和端点级 vmalert Runtime 发布；周期计算由部署到业务集群内的 vmalert 执行。每个 VictoriaLogs 日志端点对应一个 `vmalert-logs:<日志端点 ID>` Runtime，多个业务规则共享该 Runtime，不按业务或单条规则启动 vmalert。

在“观测接入配置”中为 K8s 集群级 VictoriaLogs 端点发布 vmalert Runtime 时，NovaObs 会优先使用请求中的 `alert_ingest_url`，其次使用 `NOVAOBS_ALERT_INGEST_URL`，并把 vmalert notifier 指向 NovaObs Alert Ingest。发布动作会生成同一个 K8s 清单：

- `ConfigMap`：保存该端点下完整 vmalert 规则 artifact。
- `Deployment`：启动 vmalert，并在 args 中写入 `-datasource.url=<VictoriaLogs datasource>` 与 `-notifier.url=<NovaObs Alert Ingest URL>`。
- `Service`：暴露 vmalert HTTP 端口，供后续 VMUI 代理或运行状态检查使用。

规则创建、更新、停用或回滚后会把规则应用状态置为 `pending`；再次在端点页应用 Runtime 会编译当前端点下全部 enabled 规则，更新规则 ConfigMap，并把该端点下规则的应用状态推进到 `applied`。这条链路不再需要独立 `alert-controller` 进程、MongoDB lease 轮询或共享规则目录写入器。

vmalert 直接向 NovaObs 投递告警时使用 `Authorization: Bearer <NOVAOBS_ALERT_INGEST_TOKEN>`。NovaObs 暴露 vmalert notifier 协议入口 `/api/v2/alerts`，并提供显式接入口 `/api/v1/alerts/ingest`；旧 `/api/v1/alerts/webhook/alertmanager` 不再保留。

NovaObs 通知策略只保存稳定 `receiver` 路由标识和服务范围，不保存 Webhook URL、凭据或尚未由平台下发的伪配置。该标识会作为统一告警平台的 `notification_receiver` 标签使用。receiver 标识创建后不可修改；需要换路由时新建策略并更新规则。规则创建、测试和更新会拒绝不存在、已停用或跨服务的策略。

旧版扁平 `alert_rules` 文档缺少服务、日志路由和通知策略，无法安全自动迁移。启动时检测到此类文档会直接失败，不会把旧数据静默隐藏成第二套真值；升级前需备份并显式清理旧占位规则，再由业务按新流程重建。

告警 RBAC 资源：

- `alerts.rule:read/manage`：读取或管理服务范围规则、实例和事件。
- `alerts.notification-policy:read/manage`：读取或管理全局/服务范围通知策略。

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
