# K8s Startorch Runtime Topology Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 startorch 已实现的 K8s 资源运行时拓扑能力迁移到 NovaAPM，并在 NovaAPM 的统一权限、统一 API、多版本 discovery、审计和前端设计体系下增强。

**Architecture:** 后端新增 `k8sops/resource` runtime topology 读模型和接口，复用现有 `kubeclient.BundleProvider`、`DiscoverCapabilities`、`ResourceVersionResolver` 和 dynamic client。前端在 K8s 运维模块内新增运行时拓扑视图，先完成 startorch 等价展示，再按 NovaAPM UI skill 优化为更专业的拓扑工作台。

**Tech Stack:** Go、Gin、client-go typed/dynamic/discovery、React、TypeScript/JavaScript、现有 NovaAPM response envelope、现有 K8s RBAC authorizer。

---

## 基线对齐结论

startorch 已实现但 NovaAPM 当前缺失或不完整的基线能力：

- 资源运行时拓扑：`/api/k8s/runtime-groups`，按单 namespace 聚合 Service、Deployment、StatefulSet、DaemonSet、Pod、PVC、HPA、Ingress、Istio Gateway、VirtualService、DestinationRule、安全策略。
- 服务与工作负载关系：Service selector 匹配 workload template labels，Pod 通过 workload selector 归属到更具体的 workload。
- 暴露与治理关系：Ingress / VirtualService / Gateway / DestinationRule 与 Service / Workload 回挂。
- 配置与运行状态摘要：ServiceAccount、ConfigMap、PVC、Pod 状态、容器 ready/restart、HPA target。
- Istio 安全策略归一：PeerAuthentication、AuthorizationPolicy、RequestAuthentication、legacy Policy、MeshPolicy、ServiceRoleBinding、ClusterRbacConfig。
- 前端运行时视图：namespace 详情中的 Runtime tab、拓扑关系图、治理面板、资源浏览、YAML/日志/历史弹窗联动。

NovaAPM 已具备可复用基础：

- 集群凭据、clientset/dynamic/discovery provider。
- 资源读取权限 `k8s.resource:read`。
- HPA / Ingress / Istio networking 多版本 resolver。
- typed first + dynamic fallback 的 apply/delete 执行器。
- 资源列表/详情/YAML 的 dynamic 读取基础。

## 文件结构

- 修改：`internal/modules/k8sops/resource/model.go`
  - 增加 runtime topology request/response/node 模型。
- 修改：`internal/modules/k8sops/resource/service.go`
  - Reader 接口增加 `ListRuntimeGroups`。
  - Service 增加 `ListRuntimeGroups`，包含权限校验、参数校验和后续缓存入口。
- 修改：`internal/modules/k8sops/resource/kubernetes_reader.go`
  - 实现 typed + dynamic 的 runtime topology 聚合。
  - 复用 `kubeclient.ResourceVersionResolver` 处理 HPA、Ingress、Istio 多版本资源。
- 新增：`internal/modules/k8sops/resource/runtime_topology.go`
  - 放置拓扑聚合 helper：selector 匹配、Pod 汇总、Service/Workload 分组、Istio route/destination/security 解析。
- 修改：`internal/modules/k8sops/resource/handler.go`
  - 新增 `RuntimeGroupsHandler`。
- 修改：`internal/httpapi/router.go`
  - 新增 `GET /api/v1/k8s/runtime-groups`。
- 修改：`internal/modules/k8sops/resource/*_test.go`
  - 覆盖 service 权限、handler 路由、reader 拓扑聚合、多版本 Istio/HPA/Ingress。
- 修改：`novaapm-fe/src/pages/k8s/*`
  - 新增运行时拓扑页面或 tab。
- 修改：`novaapm-fe/src/services/*`
  - 新增 runtime-groups API。

## Task 1: 后端 API 模型与服务入口

**Files:**
- Modify: `internal/modules/k8sops/resource/model.go`
- Modify: `internal/modules/k8sops/resource/service.go`
- Test: `internal/modules/k8sops/resource/service_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/modules/k8sops/resource/service_test.go` 增加：

```go
func TestServiceListRuntimeGroupsRequiresNamespace(t *testing.T) {
	service := NewService(runtimeGroupsReaderStub{})

	_, err := service.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{
		ClusterID: "prod",
	})

	require.ErrorIs(t, err, ErrNamespaceRequired)
}

func TestServiceListRuntimeGroupsDelegatesToReader(t *testing.T) {
	reader := runtimeGroupsReaderStub{
		result: RuntimeGroupsResponse{
			ClusterID: "prod",
			Namespace: "orders",
			Summary: RuntimeGroupsSummary{GroupCount: 1},
			Groups: []RuntimeGroup{{
				Key: "orders",
				DisplayName: "orders",
				Summary: RuntimeGroupSummary{ServicesTotal: 1},
			}},
		},
	}
	service := NewService(reader)

	result, err := service.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{
		ClusterID: "prod",
		Namespace: "orders",
	})

	require.NoError(t, err)
	require.Equal(t, uint64(1), result.Summary.GroupCount)
	require.Len(t, result.Groups, 1)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/modules/k8sops/resource`

Expected: 编译失败，提示 `RuntimeGroupsQuery` 或 `ListRuntimeGroups` 未定义。

- [ ] **Step 3: 增加模型和服务方法**

在 `model.go` 增加 startorch 等价 runtime 类型；命名保留 NovaAPM 语义：

```go
type RuntimeGroupsQuery struct {
	ClusterID string
	Namespace string
}

type RuntimeGroupsResponse struct {
	ClusterID string               `json:"cluster_id"`
	Namespace string               `json:"namespace"`
	Groups    []RuntimeGroup       `json:"groups"`
	Summary   RuntimeGroupsSummary `json:"summary"`
}
```

在 `service.go` 的 `Reader` 接口增加：

```go
ListRuntimeGroups(ctx context.Context, query RuntimeGroupsQuery) (RuntimeGroupsResponse, error)
```

并在 `Service` 上增加同名方法，要求：

- `cluster_id` 必填，否则返回 `k8s_cluster_required` 或既有错误。
- `namespace` 必填且不能为 `*`，否则返回 `ErrNamespaceRequired`。
- 调用 reader 前复用 `k8s.resource:read` 访问语义。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/modules/k8sops/resource`

Expected: PASS。

## Task 2: 后端 Handler 与路由

**Files:**
- Modify: `internal/modules/k8sops/resource/handler.go`
- Modify: `internal/httpapi/router.go`
- Test: `internal/httpapi/router_test.go`

- [ ] **Step 1: 写失败测试**

新增路由测试：

```go
request := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/runtime-groups?cluster_id=prod&namespace=orders", nil)
```

断言响应 envelope 成功，`data.summary.group_count == 1`。

- [ ] **Step 2: 实现 Handler**

新增：

```go
func RuntimeGroupsHandler(service Service) gin.HandlerFunc
```

查询参数：

- `cluster_id`
- `namespace`

错误映射：

- 权限不足：403 `permission_denied`
- 未录入 kubeconfig：409 `k8s_cluster_credential_required`
- 参数错误：400 `invalid_request`
- 其他失败：500 `k8s_runtime_groups_failed`

- [ ] **Step 3: 注册路由**

在 `internal/httpapi/router.go` 注册：

```go
api.GET("/k8s/runtime-groups", k8sopsresource.RuntimeGroupsHandler(deps.K8sOpsModule.Resource))
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/httpapi ./internal/modules/k8sops/resource`

Expected: PASS。

## Task 3: Runtime Topology Reader 第一版聚合

**Files:**
- Create: `internal/modules/k8sops/resource/runtime_topology.go`
- Modify: `internal/modules/k8sops/resource/kubernetes_reader.go`
- Test: `internal/modules/k8sops/resource/kubernetes_reader_test.go`

- [ ] **Step 1: 写失败测试**

构造 fake clientset：

- Service `orders` selector: `app=orders`
- Deployment `orders-api` template labels: `app=orders`
- Pod `orders-api-1` labels: `app=orders`，Running，container ready。

断言：

- 返回一个 RuntimeGroup。
- group 下包含 Service 和 Deployment。
- workload pod summary total/running/ready containers 正确。

- [ ] **Step 2: 实现 typed 聚合**

实现：

- list Services
- list Deployments
- list StatefulSets
- list DaemonSets
- list Pods
- buildRuntimeServiceNode
- buildRuntimeWorkloadFromDeployment/StatefulSet/DaemonSet
- selectorMatches
- bestMatchedWorkloadKey
- groupByAppLabels
- connected components grouping

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/modules/k8sops/resource`

Expected: PASS。

## Task 4: HPA / Ingress / Istio 多版本拓扑挂接

**Files:**
- Modify: `internal/modules/k8sops/resource/runtime_topology.go`
- Modify: `internal/modules/k8sops/resource/kubernetes_reader.go`
- Test: `internal/modules/k8sops/resource/kubernetes_reader_test.go`

- [ ] **Step 1: 写失败测试**

用 fake dynamic client 和 discovery 构造：

- HPA 仅支持 `autoscaling/v1`
- Ingress 仅支持 `extensions/v1beta1`
- VirtualService 仅支持 `networking.istio.io/v1beta1`
- DestinationRule 仅支持 `networking.istio.io/v1alpha3`

断言 resolver 能选中真实版本，并且 topology summary 计数正确。

- [ ] **Step 2: 实现 dynamic list helper**

复用：

```go
kubeclient.NewResourceVersionResolver(snapshot).Resolve(...)
```

不要在 runtime topology 内重新维护另一份版本表。

- [ ] **Step 3: 实现暴露与治理挂接**

迁移并适配 startorch 逻辑：

- Ingress -> ServiceRefs
- VirtualService -> Hosts / Gateways / RouteTargets / RouteRules
- DestinationRule -> Host / Subsets / TrafficPolicy / ExportTo
- Gateway -> hosts
- 回挂到 Service 和 RuntimeGroup summary

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/modules/k8sops/resource ./internal/modules/k8sops/kubeclient`

Expected: PASS。

## Task 5: Istio 安全策略归一

**Files:**
- Modify: `internal/modules/k8sops/resource/runtime_topology.go`
- Test: `internal/modules/k8sops/resource/kubernetes_reader_test.go`

- [ ] **Step 1: 写失败测试**

构造 dynamic resources：

- `security.istio.io/v1` PeerAuthentication
- `security.istio.io/v1beta1` AuthorizationPolicy
- `security.istio.io/v1beta1` RequestAuthentication
- legacy `authentication.istio.io/v1alpha1` Policy / MeshPolicy
- legacy `rbac.istio.io/v1alpha1` ServiceRoleBinding / ClusterRbacConfig

断言：

- security policy count 正确。
- workload.security_policies 归一为 `RuntimeSecurityPolicyNode`。

- [ ] **Step 2: 迁移安全策略 binding 逻辑**

从 startorch 的 `runtimeSecurityPolicyBinding` 思路迁移，但适配 NovaAPM 命名和错误处理：

- namespace scoped dynamic list 失败不阻塞主拓扑。
- cluster scoped legacy resource list 失败不阻塞主拓扑。
- 输出稳定排序和去重。

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/modules/k8sops/resource`

Expected: PASS。

## Task 6: 前端 Runtime Topology API 与页面

**Files:**
- Modify: `novaapm-fe/src/services/*`
- Modify/Create: `novaapm-fe/src/pages/k8s/*`
- Modify/Create: `novaapm-fe/src/components/k8s/runtime/*`

- [ ] **Step 1: 新增 API 客户端**

新增 `GET /api/v1/k8s/runtime-groups` 调用，参数：

- `cluster_id`
- `namespace`

- [ ] **Step 2: 新增运行时拓扑 Tab**

在 K8s 运维模块命名空间视图中新增：

- 运行时拓扑
- 服务/工作负载关系
- Istio 治理关系
- 配置与运行状态

- [ ] **Step 3: 迁移 startorch UI 能力但按 NovaAPM 设计约束重做**

必须遵守 `novaapm-ui-design-system`：

- 不照搬强线条和纯色大块。
- 用柔和分层、专业密度、可扫描结构。
- 拓扑图优先展示真实关系，避免装饰性图形。
- 支持空态、加载态、错误态、无 Istio CRD 的降级态。

- [ ] **Step 4: 浏览器截图验收**

启动前后端，打开页面，截图检查：

- 数据加载成功。
- 宽屏和移动宽度无重叠。
- 空态和异常态文案正确。

## Task 7: 基线审计与提交

**Files:**
- Modify: `docs/superpowers/plans/2026-05-21-k8s-startorch-runtime-topology-parity.md`

- [ ] **Step 1: 跑后端测试**

Run: `go test ./internal/modules/k8sops/...`

Expected: PASS。

- [ ] **Step 2: 跑前端测试/构建**

Run: `npm run build`

Expected: PASS。

- [ ] **Step 3: 对照 startorch 基线清单**

确认：

- runtime groups API 有等价 NovaAPM endpoint。
- typed resources 已聚合。
- HPA / Ingress / Istio networking 多版本已聚合。
- Istio security legacy 资源已归一。
- 前端有 Runtime tab 和治理拓扑视图。

- [ ] **Step 4: Git 提交**

建议拆成：

```bash
git add internal/modules/k8sops docs/superpowers/plans/2026-05-21-k8s-startorch-runtime-topology-parity.md
git commit -m "feat: add k8s runtime topology backend"
```

```bash
git add ../novaapm-fe
git commit -m "feat: add k8s runtime topology view"
```

## 风险与原则

- 不迁移 startorch 的旧权限模型；全部纳入 NovaAPM 当前/未来 RBAC。
- 不新增 demo seed 或静态兜底数据。
- 不复制第二套多版本候选表；统一走 `ResourceVersionResolver`。
- 运行时拓扑失败不应影响普通资源列表。
- Istio CRD 不存在时应返回空治理关系，而不是 500。
