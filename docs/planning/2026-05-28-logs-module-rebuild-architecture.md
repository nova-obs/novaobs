# NovaObs Logs 模块重构架构草案

> 状态说明：本文是 2026-05-28 的重构草案，部分内容已被当前实现演进替代。当前运行代码以 `K8s / VM` 两类接入来源、`VL / ES / Kafka` 日志下游端点、自动派生采集域、同一 K8s 采集域多服务命名 `logs/<service>` pipeline 为准；不要再按草案中的 `k8s_hostpath`、VL-only 或手选 AgentGroup 语义实现新功能。

## 目标

这次 Logs 变动按“日志大模块重做”处理，不继续把旧 Pipeline 作为产品主心智，也不把所有能力塞进一个页面。

新的主线是：

```text
服务 -> 运行目标 -> 日志来源 -> 采集 Agent -> VictoriaLogs 端点 -> VMUI / 告警
```

设计目标：

- 服务目录仍是业务服务索引。
- Logs 大模块只保留四个清晰入口：日志分析、采集 Agent、日志告警、接入配置。
- Agent 配置和状态仍要可见，但不再让用户先理解 Pipeline 片段。
- 支持一个服务写入多个下游，用于共用 VL、业务独立 VL、旁路对账和迁移。
- 第一版不做完整自研日志查询 UI，Explore 以内嵌 VMUI 为主。

## 非目标

- 不把旧 `pipeline` API 当新模型继续扩展。
- 不在前端继续展示“Pipeline 配置编排”作为 Logs 第一层入口。
- 不在数据库保存明文 token、CA、用户名密码。
- 不在第一版实现复杂流式告警引擎，先建立规则到服务和 VL 端点的绑定。

## 后端模块边界

建议新增领域模块，先用一个 `internal/logs` 聚合域承载公共模型和协调逻辑，再按职责拆子包：

```text
internal/logs
  model.go
  service.go
  repository.go
  validation.go
  vmui.go
  agent_projection.go
  workspace.go

internal/logs/analysis
internal/logs/agents
internal/logs/alerts
internal/logs/onboarding
internal/logs/endpoints
```

子包职责：

| 子域 | 职责 | 不做 |
| --- | --- | --- |
| `analysis` | 服务选择、VMUI 链接、最近查询上下文、样例日志探测 | 不自研完整查询 DSL 编辑器 |
| `agents` | AgentGroup / AgentInstance 状态、配置 hash、发布状态、最近错误 | 不直接替代 OpAMP |
| `alerts` | 日志告警规则、服务/VL 端点绑定、预览、启停 | 不实现复杂流式计算引擎 |
| `onboarding` | 日志来源、VL 端点、路由绑定、接入校验 | 不暴露旧 Pipeline 片段编辑 |
| `endpoints` | VictoriaLogs 端点底层管理能力 | 不作为独立主导航入口 |

### 模块职责

`internal/logs` 作为大模块聚合层负责：

- VictoriaLogs 端点登记、探测、禁用、删除保护。
- 服务日志来源登记。
- 服务到日志链路的路由关系。
- AgentGroup / AgentInstance 的日志视角投影。
- VMUI 链接生成。
- 日志告警规则和服务、VL 端点的绑定。
- Logs 大模块工作台聚合视图。

`internal/logs` 不负责：

- 不直接替代 OpAMP。
- 不直接操作 Collector 实例生命周期。
- 不实现完整 Pipeline 片段编辑器。
- 不做 K8s 写操作。

## 核心模型

### LogEndpoint

```go
type LogEndpoint struct {
	ID            string
	Name          string
	Kind          string // victorialogs
	Mode          string // shared | dedicated | shadow
	BaseURL       string
	WriteURL      string
	QueryURL      string
	VMUIURL       string
	AuthRef       string
	OwnerTeam     string
	Status        string // active | disabled | failed
	LastProbeAt   *time.Time
	LastProbeError string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
```

规则：

- `AuthRef` 只引用平台 Secret。
- `Mode=shadow` 用于新老链路旁路对账。
- 删除前检查是否存在 `LogRoute` 关联。

### LogSource

```go
type LogSource struct {
	ID              string
	ServiceID       string
	RuntimeTargetID string
	SourceType      string // k8s_stdout | k8s_hostpath | vm_file
	ClusterID       string
	Namespace       string
	WorkloadPattern string
	ContainerNames  []string
	HostPattern     string
	PathPattern      string
	Labels          map[string]string
	Status          string // active | disabled
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
```

说明：

- `LogSource` 表示“从哪里采”。
- K8s 标准输出、K8s hostPath、VM 文件路径都归一到这个模型。
- `ServiceID + RuntimeTargetID + SourceType + Path/Container` 应有唯一约束。

### LogRoute

```go
type LogRoute struct {
	ID              string
	ServiceID       string
	LogSourceID     string
	AgentGroupID    string
	EndpointIDs     []string
	Mode            string // active | shadow | disabled
	ConfigVersion   int
	ConfigHash      string
	LastPublishID   string
	LastPublishState string // pending | applied | drift | failed
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
```

说明：

- `LogRoute` 表示“谁采、采到哪、当前配置状态是什么”。
- 一个 route 可以有多个 endpoint，用于双写和旁路对账。
- 不再用“多个 Pipeline 片段”表达服务日志接入。

### LogAlertBinding

```go
type LogAlertBinding struct {
	ID         string
	ServiceID  string
	RouteID    string
	EndpointID string
	RuleID     string
	Status     string // active | disabled
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
```

说明：

- 告警规则不再只靠 query 字符串反推服务。
- 告警页面可以先选服务，再选 VL 端点，再创建规则。

## 子模块职责

## 闭环流程

第一版按以下顺序闭环，不先做告警：

```text
接入配置 -> 采集 Agent -> 日志分析 -> 日志告警
```

各环节的完成条件：

| 环节 | 产物 | 完成条件 |
| --- | --- | --- |
| 接入配置 | `LogEndpoint`、`LogSource`、`LogRoute` | 路由可预览、可探测、可发布 |
| 采集 Agent | AgentGroup projection、AgentInstance projection | desired/effective config hash 可对比，实例有 last seen 和 output 状态 |
| 日志分析 | VMUI link、explore context | 选择服务后能生成对应 VMUI 链接 |
| 日志告警 | `LogAlertBinding`、规则预览 | 规则能绑定服务和 VL 端点，能预览最近窗口 |

页面顺序也按这个闭环推进：

1. `/logs/onboarding`：先把服务日志接入平台。
2. `/logs/agents`：确认 Agent 已接收配置并采集上来。
3. `/logs/explore`：进入对应 VMUI 做检索。
4. `/logs/alerts`：基于已接入的服务创建日志告警。

### 日志分析

产品入口：`/logs/explore`

职责：

- 通过顶部下拉搜索选择服务。
- 展示服务运行目标、默认 VL 端点、可用 AgentGroup。
- 调后端生成 VMUI 链接。
- iframe 内嵌 VMUI；如果被 CSP 或 X-Frame-Options 阻断，提供新窗口打开。
- 保留最近查询上下文，例如服务、时间范围、namespace、container。

后端能力：

```text
GET /api/v1/logs/explore/context?service_id=...
GET /api/v1/logs/explore/vmui-link?service_id=...&endpoint_id=...&range=15m
GET /api/v1/logs/explore/sample?service_id=...&endpoint_id=...
```

说明：

- Explore 不直接暴露复杂 Pipeline 配置。
- 查询语法由 VMUI 承担，NovaObs 只负责服务上下文和端点选择。
- 日志分析页不放服务列表，不铺 Agent、告警、端点等状态卡；这些状态进入服务详情页或对应子模块。

### 采集 Agent 运维

产品入口：`/logs/agents`

职责：

- 查看 AgentGroup 列表。
- 查看实例在线、健康、配置同步、版本、节点/主机、最近错误。
- 查看当前生效配置摘要和 YAML。
- 查看某个 AgentGroup 影响的服务、日志来源和 VL 端点。
- 后续支持配置发布、回滚和审计。

后端能力：

```text
GET  /api/v1/logs/agents/groups
GET  /api/v1/logs/agents/groups/:id
GET  /api/v1/logs/agents/groups/:id/instances
GET  /api/v1/logs/agents/groups/:id/config
GET  /api/v1/logs/agents/groups/:id/impacts
POST /api/v1/logs/agents/groups/:id/preview-config
POST /api/v1/logs/agents/groups/:id/publish
```

说明：

- Agent 运维可以复用现有 `collectormanagement` 和 OpAMP 数据，但通过 Logs 自己的 projection 输出。
- 用户看到的是 Agent 状态、配置版本、影响范围，而不是旧 Pipeline 片段。

### 日志告警

产品入口：`/logs/alerts`

职责：

- 按服务创建日志告警规则。
- 选择 VL 端点和日志路由。
- 规则预览，展示样例命中和最近窗口状态。
- 规则启停、删除、审计。
- 与服务目录和告警中心互通。

后端能力：

```text
GET    /api/v1/logs/alerts/rules
POST   /api/v1/logs/alerts/rules
GET    /api/v1/logs/alerts/rules/:id
PATCH  /api/v1/logs/alerts/rules/:id
DELETE /api/v1/logs/alerts/rules/:id
POST   /api/v1/logs/alerts/rules/:id/preview
POST   /api/v1/logs/alerts/rules/:id/enable
POST   /api/v1/logs/alerts/rules/:id/disable
```

说明：

- 日志告警不是 Alerting 模块的简单表格复制，而是 Logs 视角下的“服务 + VL + 查询表达式 + 告警动作”。
- 底层可继续复用 `alerting` 规则执行或存储，但必须补齐服务和 VL 端点绑定。

### 接入配置

产品入口：`/logs/onboarding`

职责：

- 登记 VictoriaLogs 端点。
- 探测端点。
- 登记服务日志来源。
- 将日志来源绑定到 AgentGroup 和一个或多个 VL 端点。
- 做接入验证和旁路迁移对账。

后端能力：

```text
GET    /api/v1/logs/endpoints
POST   /api/v1/logs/endpoints
PATCH  /api/v1/logs/endpoints/:id
DELETE /api/v1/logs/endpoints/:id
POST   /api/v1/logs/endpoints/:id/probe

GET    /api/v1/logs/sources
POST   /api/v1/logs/sources
PATCH  /api/v1/logs/sources/:id
DELETE /api/v1/logs/sources/:id

GET    /api/v1/logs/routes
POST   /api/v1/logs/routes
PATCH  /api/v1/logs/routes/:id
DELETE /api/v1/logs/routes/:id
POST   /api/v1/logs/routes/:id/probe
```

说明：

- 接入配置是运维配置视角，不应出现在日志分析主流程里干扰查询。
- VL 端点不单独作为主导航；放在接入配置内部的 `端点` 分区。
- 对业务来说，日常入口应该是日志分析和告警；对 SRE 来说，日常入口是 Agent 运维和接入管理。

## Repository 与端口

```go
type EndpointRepository interface {
	List(ctx context.Context, filter EndpointFilter) ([]LogEndpoint, error)
	Get(ctx context.Context, id string) (LogEndpoint, error)
	Create(ctx context.Context, endpoint LogEndpoint) error
	Update(ctx context.Context, endpoint LogEndpoint) error
	Delete(ctx context.Context, id string) error
}

type SourceRepository interface {
	List(ctx context.Context, filter SourceFilter) ([]LogSource, error)
	Get(ctx context.Context, id string) (LogSource, error)
	Create(ctx context.Context, source LogSource) error
	Update(ctx context.Context, source LogSource) error
	Delete(ctx context.Context, id string) error
}

type RouteRepository interface {
	List(ctx context.Context, filter RouteFilter) ([]LogRoute, error)
	Get(ctx context.Context, id string) (LogRoute, error)
	Create(ctx context.Context, route LogRoute) error
	Update(ctx context.Context, route LogRoute) error
	Delete(ctx context.Context, id string) error
}
```

外部端口：

```go
type AgentInventory interface {
	ListGroups(ctx context.Context, filter AgentGroupFilter) ([]LogAgentGroup, error)
	ListInstances(ctx context.Context, groupID string) ([]LogAgentInstance, error)
	GetDesiredConfig(ctx context.Context, groupID string) (AgentConfigView, error)
}

type EndpointProbe interface {
	Probe(ctx context.Context, endpoint LogEndpoint) ProbeResult
}

type VMUILinkBuilder interface {
	Build(endpoint LogEndpoint, req VMUILinkRequest) (string, error)
}
```

这些端口让 Logs 模块不直接绑死旧 `collectormanagement` 和旧 `pipeline` 实现。

## HTTP API

统一放到 `/api/v1/logs/*` 下，按子模块拆分，避免旧接口散落：

```text
GET    /api/v1/logs/workspace

GET    /api/v1/logs/explore/context
GET    /api/v1/logs/explore/vmui-link
GET    /api/v1/logs/explore/sample

GET    /api/v1/logs/endpoints
POST   /api/v1/logs/endpoints
GET    /api/v1/logs/endpoints/:id
PATCH  /api/v1/logs/endpoints/:id
DELETE /api/v1/logs/endpoints/:id
POST   /api/v1/logs/endpoints/:id/probe

GET    /api/v1/logs/sources
POST   /api/v1/logs/sources
PATCH  /api/v1/logs/sources/:id
DELETE /api/v1/logs/sources/:id

GET    /api/v1/logs/routes
POST   /api/v1/logs/routes
PATCH  /api/v1/logs/routes/:id
DELETE /api/v1/logs/routes/:id
POST   /api/v1/logs/routes/:id/preview-config
POST   /api/v1/logs/routes/:id/publish

GET    /api/v1/logs/agents/groups
GET    /api/v1/logs/agents/groups/:id
GET    /api/v1/logs/agents/groups/:id/instances
GET    /api/v1/logs/agents/groups/:id/config
GET    /api/v1/logs/agents/groups/:id/impacts
POST   /api/v1/logs/agents/groups/:id/preview-config
POST   /api/v1/logs/agents/groups/:id/publish

GET    /api/v1/logs/alerts/rules
POST   /api/v1/logs/alerts/rules
GET    /api/v1/logs/alerts/rules/:id
PATCH  /api/v1/logs/alerts/rules/:id
DELETE /api/v1/logs/alerts/rules/:id
POST   /api/v1/logs/alerts/rules/:id/preview
POST   /api/v1/logs/alerts/rules/:id/enable
POST   /api/v1/logs/alerts/rules/:id/disable
```

兼容策略：

- 旧 `/services/:id/pipeline/*` 标记 deprecated。
- 前端不再从 Logs 主入口调用旧 Pipeline API。
- 后端旧接口可保留一段时间，但不再作为新功能入口。

## 数据库存储

Mongo collections：

```text
log_endpoints
log_sources
log_routes
log_alert_bindings
```

索引：

```text
log_endpoints.name unique
log_sources.service_id
log_sources.runtime_target_id
log_sources.service_id + runtime_target_id + source_type
log_routes.service_id
log_routes.agent_group_id
log_routes.endpoint_ids
log_alert_bindings.service_id
log_alert_bindings.rule_id unique
```

## 前端信息架构

Logs 第一层页面从旧的：

```text
Explorer / Pipelines / Views
```

改成：

```text
接入管理
采集 Agent
日志分析
日志告警
```

这不是普通 tab，而是 Logs 大模块下的二级导航。职责如下：

| 页面 | 主用户 | 核心任务 |
| --- | --- | --- |
| `/logs/onboarding` | SRE | 管理 VL 端点、日志来源、Agent/VL 路由和接入验证 |
| `/logs/agents` | SRE | 查看 AgentGroup、实例、配置 hash、发布状态、最近错误 |
| `/logs/explore` | 业务、SRE | 下拉搜索服务，进入对应 VMUI 做日志检索 |
| `/logs/alerts` | 业务、SRE | 创建和管理服务级日志告警 |

三版预览的保留方式：

- 方案 A 的 VMUI 主工作区进入 `/logs/explore`；服务选择改成顶部下拉搜索，右侧上下文和状态卡移到服务详情页。
- 方案 B 的数据平面拓扑、端点使用情况、迁移对账，进入 `/logs/onboarding`。
- 方案 C 的 AgentGroup、实例状态、配置摘要、影响服务，进入 `/logs/agents`。

### 闭环页面交互

第一版页面要把“接入配置 -> Agent 已采集 -> 日志可分析 -> 可配置告警”做成连续路径，而不是四个孤立入口。

`/logs/onboarding`：

- 主体是接入路由表，每一行表示一条服务日志链路：服务、来源、AgentGroup、VL 端点、探测状态。
- 右侧是当前选中路由的编辑面板，只保留完成链路所需字段：服务、采集方式、范围、AgentGroup、VL 端点、接入验证。
- 发布接入前必须支持配置预览，发布后记录 `route_id`、`config_hash`、`last_publish_id`。
- 发布完成后提供进入 `/logs/agents?agent_group_id=...&route_id=...` 的下一步动作。

`/logs/agents`：

- 主体是 Agent 实例表，展示实例在线、配置同步、版本、输入速率、输出状态和最近错误。
- 右侧是当前 AgentGroup 的配置摘要与影响服务，帮助 SRE 确认刚发布的接入是否已经落到采集侧。
- 当 `desired_config_hash` 与 `effective_config_hash` 不一致时，页面必须显式提示 drift，并保留重新下发入口。
- AgentGroup 达到可接受的应用状态后，提供进入 `/logs/explore?service_id=...&route_id=...` 的下一步动作。

`/logs/explore`：

- 只做服务下拉搜索、时间范围、VL 端点选择和 VMUI 内嵌。
- 不在页面内铺服务卡片、Agent 状态卡或告警状态卡，避免查询场景被运维信息打断。
- 如果从接入或 Agent 页面带入 `route_id`，默认选中对应服务与 VL 端点。

`/logs/alerts`：

- 从服务和 VL 端点开始创建日志告警，而不是让用户从裸查询表达式开始。
- 支持规则预览，展示最近窗口命中样例、预计通知路由和审计信息。
- 从日志分析页进入时，继承当前服务、端点和时间窗口。

## 实施顺序

1. 新分支隔离后端与前端重构。
2. 先实现 `internal/logs` 模型、校验、内存仓储和单测。
3. 增加 Mongo 仓储与索引初始化。
4. 增加 `/api/v1/logs/*` handlers，不改旧接口。
5. 前端新增 Logs 大模块壳层和二级导航。
6. 先实现 `/logs/explore`，打通顶部服务下拉搜索和 VMUI link。
7. 实现 `/logs/agents`，打通 AgentGroup、实例状态、配置摘要、影响服务。
8. 实现 `/logs/onboarding`，打通 VL 端点、日志来源、日志路由。
9. 实现 `/logs/alerts`，打通服务级日志告警绑定、预览和启停。
10. 移除 Logs 页面中的 Pipeline tab。
11. 旧 Pipeline 页面降级为兼容入口或删除主导航。

## 验收标准

- 新服务能登记日志来源。
- 服务能绑定 AgentGroup 和一个或多个 VL 端点。
- 日志分析页面能通过下拉搜索选择服务并加载对应 VMUI。
- Agent 运维页面能查看 AgentGroup、实例、配置 hash、影响服务和最近错误。
- 日志告警页面能基于服务和 VL 端点创建、预览、启停规则。
- 接入配置页面能管理 VL 端点、登记日志来源并绑定 Agent/VL 路由。
- VMUI 能按服务生成链接。
- 删除 VL 端点时能阻止误删仍有关联的端点。
- Logs 主入口不再出现旧 Pipeline 配置心智。
