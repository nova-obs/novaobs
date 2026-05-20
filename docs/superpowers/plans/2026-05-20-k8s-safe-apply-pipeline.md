# K8s Typed-First Operation Executor And Safe Apply Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将部署 Apply/Delete 从“校验 + 审计”推进到 typed 优先、dynamic fallback 的 dry-run/apply/delete 执行器，并补齐真实写入前后的 diff、确认、资源归属 inventory、发布审计和回滚快照能力。

**Architecture:** 第一层先在 `kubeclient` 收敛统一资源操作执行器：内置核心资源走 typed client，未知/CRD/多版本资源走 dynamic client fallback，所有路径共享解析、授权后执行、错误归一和审计摘要。第二层再在 `deployment` 服务层实现 Preview diff、Apply confirmation、inventory 和 history，确保真实写入具备可确认、可追踪、可回滚的 NovaObs 语义。

**Tech Stack:** Go、client-go dynamic client、Mongo/memstore repository、Gin handler、NovaObs RBAC/audit envelope、React 前端后续接入。

---

## File Structure

- Modify: `internal/modules/k8sops/kubeclient/resource_operation_engine.go`
	- 扩展现有 dry-run 引擎，新增 typed-first/dynamic-fallback 的 preview/apply/delete 能力。
- Modify: `internal/modules/k8sops/kubeclient/resource_operation_engine_test.go`
	- 覆盖 typed apply/delete、dynamic fallback、live object 存在、不存在、版本回退、cluster-scoped/namespaced 资源。
- Create: `internal/modules/k8sops/kubeclient/typed_operation_executor.go`
	- 封装 Deployment、Service、ConfigMap 等核心资源 typed client 操作。
- Create: `internal/modules/k8sops/kubeclient/dynamic_operation_executor.go`
	- 封装 CRD、多版本资源和 typed 未覆盖资源的 dynamic fallback 操作。
- Create: `internal/modules/k8sops/kubeclient/resource_operation_model.go`
	- 定义 `OperationMode`、`ApplyRequest`、`DeleteRequest`、`OperationResult` 等通用模型。
- Create: `internal/modules/k8sops/deployment/inventory_model.go`
	- 定义 NovaObs 管理的 K8s 资源归属记录。
- Create: `internal/modules/k8sops/deployment/inventory_repository.go`
	- 定义 inventory repository interface 与内存实现。
- Create: `internal/modules/k8sops/deployment/apply_plan.go`
	- 定义 preview plan、resource diff、apply confirmation fingerprint。
- Modify: `internal/modules/k8sops/deployment/history_service.go`
	- Preview 返回 diff 和 confirmation，Apply 校验 confirmation 后执行真实 server-side apply。
- Modify: `internal/modules/k8sops/deployment/operation_model.go`
	- 扩展 OperationRequest/OperationResult，增加 `preview_id`、`confirmation_token`、`diffs`、`warnings`。
- Modify: `internal/modules/k8sops/deployment/history_handler.go`
	- 为校验失败、确认失败、冲突失败返回明确错误码。
- Modify: `internal/modules/k8sops/module.go`
	- 注入 inventory repository 和 apply executor。
- Test: `internal/modules/k8sops/deployment/history_service_test.go`
	- 覆盖权限、确认、inventory、history、错误映射。
- Test: `internal/modules/k8sops/deployment/operation_handler_test.go`
	- 覆盖 HTTP envelope 和错误码。

---

### Task 0: Typed-First Dynamic-Fallback Operation Executor

**Files:**
- Create: `internal/modules/k8sops/kubeclient/resource_operation_model.go`
- Create: `internal/modules/k8sops/kubeclient/typed_operation_executor.go`
- Create: `internal/modules/k8sops/kubeclient/dynamic_operation_executor.go`
- Modify: `internal/modules/k8sops/kubeclient/resource_operation_engine.go`
- Test: `internal/modules/k8sops/kubeclient/resource_operation_engine_test.go`

- [ ] **Step 1: Write failing tests for typed-first apply/delete**

Add tests that verify `Deployment apps/v1` uses typed client for dry-run/apply/delete when typed client is available, and that unknown `VirtualService networking.istio.io/v1beta1` uses dynamic fallback.

Run: `go test ./internal/modules/k8sops/kubeclient -run TestResourceOperationEngineTypedFirst -count=1`

Expected: FAIL because typed operation executor does not exist.

- [ ] **Step 2: Define shared operation model**

Create `resource_operation_model.go` with:

```go
type OperationMode string

const (
	OperationModeDryRun OperationMode = "dry_run"
	OperationModeApply  OperationMode = "apply"
	OperationModeDelete OperationMode = "delete"
)

type ApplyRequest struct {
	Mode        OperationMode
	YAMLContent string
}

type DeleteRequest struct {
	Mode     OperationMode
	Identity OperationObject
}

type ResourceOperationResult struct {
	Objects  []OperationObject `json:"objects"`
	Warnings []string          `json:"warnings"`
}
```

- [ ] **Step 3: Implement typed executor for core resources**

Support typed paths for at least:
- `apps/v1 Deployment`
- `v1 Service`
- `v1 ConfigMap`

Typed apply should use server-side apply with `FieldManager=DefaultFieldManager`; dry-run must set `DryRun=All`; delete dry-run should use Kubernetes delete options dry-run if supported by client-go for the typed resource.

- [ ] **Step 4: Implement dynamic fallback**

For resources not covered by typed executor, use the existing resolved GVR dynamic path. Apply uses `Patch(..., types.ApplyPatchType, ...)`; delete uses dynamic `Delete` with `DeleteOptions{DryRun: []string{metav1.DryRunAll}}` for dry-run mode and normal delete for apply mode.

- [ ] **Step 5: Route engine operations**

`ResourceOperationEngine` should choose typed executor first. If typed executor reports unsupported resource, continue to dynamic fallback. Unsupported API versions should still fail before any write operation.

Run: `go test ./internal/modules/k8sops/kubeclient -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/modules/k8sops/kubeclient/resource_operation_model.go internal/modules/k8sops/kubeclient/typed_operation_executor.go internal/modules/k8sops/kubeclient/dynamic_operation_executor.go internal/modules/k8sops/kubeclient/resource_operation_engine.go internal/modules/k8sops/kubeclient/resource_operation_engine_test.go
git commit -m "feat: add k8s typed-first operation executor"
```

---

### Task 1: Preview Diff Model And Confirmation Fingerprint

**Files:**
- Create: `internal/modules/k8sops/deployment/apply_plan.go`
- Modify: `internal/modules/k8sops/deployment/operation_model.go`
- Test: `internal/modules/k8sops/deployment/history_service_test.go`

- [ ] **Step 1: Write failing tests for preview result shape**

Add tests that assert Preview returns a stable `PreviewID`, `ConfirmationToken`, `Diffs`, and no raw YAML in audit summary.

Run: `go test ./internal/modules/k8sops/deployment -run TestServicePreviewReturnsConfirmationPlan -count=1`

Expected: FAIL because the fields and plan builder do not exist.

- [ ] **Step 2: Implement plan types**

Add these types:

```go
type PreviewPlan struct {
	ID                string
	ConfirmationToken string
	Resources         []ResourceIdentity
	Diffs             []ResourceDiff
	Warnings          []string
}

type ResourceDiff struct {
	ClusterID  string `json:"cluster_id"`
	Namespace  string `json:"namespace,omitempty"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Operation  string `json:"operation"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash"`
}
```

- [ ] **Step 3: Generate deterministic confirmation token**

Use sorted resource identity + after hash + cluster ID. Do not include raw YAML in the returned token source.

Run: `go test ./internal/modules/k8sops/deployment -run TestServicePreviewReturnsConfirmationPlan -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/modules/k8sops/deployment/apply_plan.go internal/modules/k8sops/deployment/operation_model.go internal/modules/k8sops/deployment/history_service_test.go
git commit -m "feat: add k8s deployment preview plan"
```

---

### Task 2: Resource Inventory Repository

**Files:**
- Create: `internal/modules/k8sops/deployment/inventory_model.go`
- Create: `internal/modules/k8sops/deployment/inventory_repository.go`
- Test: `internal/modules/k8sops/deployment/inventory_repository_test.go`

- [ ] **Step 1: Write failing repository tests**

Test upsert, lookup by resource identity, and list by cluster/namespace.

Run: `go test ./internal/modules/k8sops/deployment -run TestMemoryInventoryRepository -count=1`

Expected: FAIL because repository does not exist.

- [ ] **Step 2: Implement inventory model**

Use a focused model:

```go
type InventoryRecord struct {
	ID           string
	ClusterID    string
	Namespace    string
	APIVersion   string
	Kind         string
	Name         string
	FieldManager string
	LastApplyHash string
	LastPreviewID string
	UpdatedAt    time.Time
}
```

- [ ] **Step 3: Implement memory repository**

Repository methods:

```go
type InventoryRepository interface {
	Upsert(ctx context.Context, record InventoryRecord) (InventoryRecord, error)
	Find(ctx context.Context, identity ResourceIdentity) (InventoryRecord, error)
	List(ctx context.Context, filter InventoryFilter) ([]InventoryRecord, error)
}
```

Run: `go test ./internal/modules/k8sops/deployment -run TestMemoryInventoryRepository -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/modules/k8sops/deployment/inventory_model.go internal/modules/k8sops/deployment/inventory_repository.go internal/modules/k8sops/deployment/inventory_repository_test.go
git commit -m "feat: add k8s deployment inventory repository"
```

---

### Task 3: Live Object Read And Diff Inputs

**Files:**
- Modify: `internal/modules/k8sops/kubeclient/resource_operation_engine.go`
- Modify: `internal/modules/k8sops/kubeclient/resource_operation_engine_test.go`

- [ ] **Step 1: Write failing tests for live object reads**

Test that the engine attempts `Get` before dry-run patch and marks operation as `create` when live object is not found, `update` when found.

Run: `go test ./internal/modules/k8sops/kubeclient -run TestResourceOperationEnginePreviewApplyDiffInputs -count=1`

Expected: FAIL because only dry-run patch exists.

- [ ] **Step 2: Add preview operation method**

Add a method that returns object identity, resolved version, operation type, before hash and after hash:

```go
func (e ResourceOperationEngine) PreviewApply(ctx context.Context, req DryRunApplyRequest) (PreviewApplyResult, error)
```

Do not persist anything. Treat Kubernetes `NotFound` as `create`.

- [ ] **Step 3: Keep DryRunApply as compatibility wrapper**

`DryRunApply` should call the new preview method and project the old result shape so current callers do not break.

Run: `go test ./internal/modules/k8sops/kubeclient -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/modules/k8sops/kubeclient/resource_operation_engine.go internal/modules/k8sops/kubeclient/resource_operation_engine_test.go
git commit -m "feat: add k8s preview diff inputs"
```

---

### Task 4: Apply And Delete Confirmation With Real Execution

**Files:**
- Modify: `internal/modules/k8sops/deployment/history_service.go`
- Modify: `internal/modules/k8sops/deployment/operation_model.go`
- Modify: `internal/modules/k8sops/deployment/history_handler.go`
- Test: `internal/modules/k8sops/deployment/history_service_test.go`
- Test: `internal/modules/k8sops/deployment/operation_handler_test.go`

- [ ] **Step 1: Write failing Apply confirmation tests**

Test that Apply rejects requests without confirmation token, rejects mismatched token, and only then calls executor.

Run: `go test ./internal/modules/k8sops/deployment -run TestServiceApplyRequiresMatchingPreviewConfirmation -count=1`

Expected: FAIL.

- [ ] **Step 2: Extend OperationRequest**

Add:

```go
PreviewID         string `json:"preview_id,omitempty"`
ConfirmationToken string `json:"confirmation_token,omitempty"`
```

- [ ] **Step 3: Implement confirmation check**

Apply should recompute the preview plan from submitted YAML and compare `ConfirmationToken`. If mismatch, return `ErrInvalidRequest` with a specific confirmation failure message.

- [ ] **Step 4: Add real apply/delete executor interface**

Define:

```go
type OperationApplier interface {
	Apply(ctx context.Context, req kubeclient.ClusterApplyRequest) (kubeclient.ApplyResult, error)
}

type OperationDeleter interface {
	Delete(ctx context.Context, req kubeclient.ClusterDeleteRequest) (kubeclient.DeleteResult, error)
}
```

The implementation should delegate to the typed-first/dynamic-fallback executor from Task 0.

- [ ] **Step 5: Persist inventory and audit after successful apply/delete**

For each resource returned by apply, upsert inventory with field manager, preview ID, and apply hash. For delete, mark or remove inventory only after the executor succeeds. Keep raw YAML out of audit summary.

Run: `go test ./internal/modules/k8sops/deployment -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/modules/k8sops/deployment/history_service.go internal/modules/k8sops/deployment/operation_model.go internal/modules/k8sops/deployment/history_handler.go internal/modules/k8sops/deployment/history_service_test.go internal/modules/k8sops/deployment/operation_handler_test.go
git commit -m "feat: require confirmed k8s apply and delete"
```

---

### Task 5: Frontend Preview Confirmation Flow

**Files:**
- Modify: `novaobs-fe` K8s deployment/template apply page files after locating exact routes.
- Test: frontend unit or Playwright smoke test for preview -> confirm -> apply.

- [ ] **Step 1: Locate current K8s deployment UI**

Run in frontend repo:

```bash
rg "deployments/preview|yaml_content|confirmation_token|K8s" src
```

Expected: Find the existing deployment or template operation entry.

- [ ] **Step 2: Add preview result rendering**

Render resource diffs with operation badges `create/update`, resolved API version, namespace, name and API Server validation warnings.

- [ ] **Step 3: Add explicit confirmation action**

Apply button must be disabled until preview succeeds and must submit `preview_id` + `confirmation_token`.

- [ ] **Step 4: Run frontend verification**

Run:

```bash
npm run build
```

If a dev server is used for visual QA, capture a screenshot of the updated flow before finalizing.

- [ ] **Step 5: Commit**

Run:

```bash
git add <frontend files>
git commit -m "feat: add k8s apply confirmation flow"
```

---

## Self-Review

- Spec coverage: 覆盖 typed 优先、dynamic fallback、dry-run/apply/delete 执行器，以及真实写入前的 diff、确认、inventory、审计、回滚前置数据和前端确认入口。
- Known gap: 真实 rollback 执行不放在本计划第一轮实现；本计划先保存可回滚所需的 inventory/history 基础，后续再做 rollback executor。
- Risk control: Apply 之前必须有 token 校验；所有集群调用必须在 RBAC 之后；审计不得保存 raw YAML 或 kubeconfig。
