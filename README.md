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
cmd/alert-controller/    独立的日志告警规则调和进程
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
- MongoDB replica set（告警规则、更新记录、Deployment 与审计使用事务保证原子性）
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
| `NOVAOBS_ALERTMANAGER_WEBHOOK_TOKEN` | Alertmanager 回调 NovaObs 时使用的独立 Bearer Token；release 模式必填。 |
| `NOVAOBS_ALERT_RUNTIME_ID` | controller 管理的 Runtime ID，格式为 `vmalert-logs:<日志端点 ID>`。 |
| `NOVAOBS_ALERT_RULES_DIRECTORY` | vmalert 挂载的规则目录；controller 在其中原子替换 Runtime 规则文件。 |
| `NOVAOBS_VMALERT_RELOAD_URL` | vmalert reload 地址，例如 `http://vmalert:8880/-/reload`。 |
| `NOVAOBS_VMALERT_RULES_URL` | vmalert 规则回读地址，例如 `http://vmalert:8880/api/v1/rules`。 |

Do not commit production secrets or production kubeconfigs. Use environment variables or a secret manager for production deployments.

## 日志告警 Runtime

API 进程只管理规则生产真值和历史测试；周期计算由外部 vmalert 执行。每个 VictoriaLogs 日志端点部署一个 `alert-controller + vmalert-logs` 故障域，多个业务规则共享该 Runtime。启动示例：

```bash
export NOVAOBS_ALERT_RUNTIME_ID="vmalert-logs:<endpoint-id>"
export NOVAOBS_ALERT_RULES_DIRECTORY="/etc/vmalert/rules"
export NOVAOBS_VMALERT_RELOAD_URL="http://vmalert:8880/-/reload"
export NOVAOBS_VMALERT_RULES_URL="http://vmalert:8880/api/v1/rules"
go run ./cmd/alert-controller
```

vmalert 需要配置对应 VictoriaLogs datasource、Alertmanager notifier、规则目录 glob 和 `-configCheckInterval`。controller 会使用 MongoDB Deployment lease 串行调和，写入完整 Runtime 产物、触发 reload，并通过规则 API 回读确认后才把 Deployment 标记为 `applied`。Alertmanager 固定增加 NovaObs Webhook receiver，并以 `Authorization: Bearer <NOVAOBS_ALERTMANAGER_WEBHOOK_TOKEN>` 调用 `/api/v1/alerts/webhook/alertmanager`，同时启用 `send_resolved: true`。

`NOVAOBS_ALERT_RULES_DIRECTORY` 必须是该 Runtime 的专用目录，不能混放手工 YAML；controller 与 vmalert 必须看到同一持久卷。多副本部署需要 RWX 共享卷或后续 ConfigMap adapter，不能给每个副本各自独立目录后仍只运行一个 controller lease owner。

NovaObs 通知策略只保存 Alertmanager receiver 的稳定标识和服务范围，不保存 Webhook URL、凭据或尚未由平台下发的伪配置。receiver 标识创建后不可修改；需要换路由时新建策略并更新规则。controller 发布规则时会解析策略并同时写入 `notification_policy_id` 与 `notification_receiver` 标签，Alertmanager 按 `notification_receiver` 路由到同名 receiver。规则创建、测试和更新会拒绝不存在、已停用或跨服务的策略。

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
