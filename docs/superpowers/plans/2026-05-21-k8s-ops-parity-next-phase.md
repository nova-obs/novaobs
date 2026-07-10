# K8s Ops Parity Next Phase Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the next phase after startorch runtime topology parity: make the migrated K8s module reachable in real API runtime, close the remaining resource-management gaps, and prepare NovaAPM-native RBAC/user authorization surfaces.

**Architecture:** Keep NovaAPM as the unified frontend and backend API surface. K8s cluster access continues through NovaAPM credential/provider/RBAC boundaries; startorch behavior is used as functional baseline, but old startorch permission code is not carried forward.

**Tech Stack:** Go/Gin backend, Kubernetes client-go typed + dynamic clients, React/Vite frontend, TanStack Query, NovaAPM platform RBAC/audit modules.

---

## Current Baseline

- Runtime topology API and page exist.
- Resource reader now covers startorch core set: Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet, Service, ConfigMap, PersistentVolumeClaim, PersistentVolume, HorizontalPodAutoscaler, Ingress, Gateway, VirtualService, DestinationRule, EnvoyFilter.
- Frontend resource Kind selector and template type selector expose the expanded set.
- Verified commands:
  - `go test ./internal/modules/k8sops/... ./internal/httpapi`
  - `npm run build`

## Known Gaps To Close

1. Local real API runtime still needs backend restart/recheck: previous screenshot showed `/api/v1/k8s/runtime-groups` returning 404 from port `7890`, likely because the running backend was stale.
2. Resource detail/YAML for cluster-scoped resources such as `PersistentVolume` needs explicit real-cluster validation because frontend still requires namespace context while dynamic reader can return empty namespace.
3. NovaAPM-native user/RBAC management UI is not implemented; existing K8s users page is still not the final platform authorization surface.
4. Apply/Delete execution path exists in plan history, but K8s resource pages are still mostly read-only and do not expose a reviewed operation flow consistently.
5. Real cluster read-only validation is pending.

---

### Task 1: Real Backend Runtime Smoke Test

**Files:**
- Verify: `/Users/user/Documents/NovaAPM/novaapm/cmd/server/main.go`
- Verify: `/Users/user/Documents/NovaAPM/novaapm/configs`
- Verify: `/Users/user/Documents/NovaAPM/novaapm-fe/vite.config.ts`
- Modify only if needed: `/Users/user/Documents/NovaAPM/novaapm/README.md`

- [ ] **Step 1: Start the current backend build**

Run:

```bash
export NOVAAPM_SECRET_KEY="12345678901234567890123456789012"
go run ./cmd/server
```

Expected: backend listens on configured host/port, usually `localhost:7890`.

- [ ] **Step 2: Confirm route exists**

Run:

```bash
curl --silent --location "http://localhost:7890/api/v1/k8s/runtime-groups?cluster_id=test03-02&namespace=default"
```

Expected: not `404`. Acceptable results are:

- `200` with runtime topology data.
- `403 permission_denied` if current default subject lacks read permission.
- `409 k8s_cluster_credential_required` if credential state is missing.
- Kubernetes/API error if cluster is unreachable.

- [ ] **Step 3: Confirm frontend proxy points at the same backend**

Check `/Users/user/Documents/NovaAPM/novaapm-fe/vite.config.ts`:

```ts
proxy: {
  '/api': 'http://localhost:7890',
}
```

Expected: frontend dev server proxies `/api` to the backend just started.

- [ ] **Step 4: Capture smoke result**

Update run notes in final response and, if Notion is available, create `阶段复盘：K8s 运行时拓扑真实 API smoke test`.

---

### Task 2: Resource Detail/YAML Real Coverage For Expanded Kinds

**Files:**
- Modify: `/Users/user/Documents/NovaAPM/novaapm/internal/modules/k8sops/resource/kubernetes_reader.go`
- Modify: `/Users/user/Documents/NovaAPM/novaapm/internal/modules/k8sops/resource/kubernetes_reader_test.go`
- Modify: `/Users/user/Documents/NovaAPM/novaapm-fe/src/pages/k8s/ResourcePage.tsx`

- [ ] **Step 1: Add tests for cluster-scoped detail/YAML**

Add a test next to `TestKubernetesReaderListsStartorchResourceKindsWithResolvedVersion`:

```go
func TestKubernetesReaderReadsClusterScopedPersistentVolumeYAML(t *testing.T) {
	pvGVR := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata": map[string]any{
			"name": "pv-orders",
			"uid":  "uid-pv",
		},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{pvGVR: "PersistentVolumeList"},
		object,
	)
	reader := NewKubernetesReader(staticResourceBundleProvider{bundle: kubeclient.Bundle{
		Clientset: fake.NewSimpleClientset(),
		Dynamic:   dynamicClient,
		Discovery: discoveryForResources("v1", metav1.APIResource{Name: "persistentvolumes", Kind: "PersistentVolume", Namespaced: false}),
	}})

	rendered, err := reader.GetYAML(context.Background(), DetailQuery{Identity: Identity{
		ClusterID:  "prod",
		Namespace:  "",
		APIVersion: "v1",
		Kind:       "PersistentVolume",
		Name:       "pv-orders",
		UID:        "uid-pv",
	}})

	require.NoError(t, err)
	require.Contains(t, rendered.YAML, "kind: PersistentVolume")
	require.Equal(t, "", rendered.Identity.Namespace)
}
```

- [ ] **Step 2: Run the resource test**

Run:

```bash
go test ./internal/modules/k8sops/resource
```

Expected: pass. If it fails due namespace authorization, adjust `allowed` handling for cluster-scoped dynamic resources with a deliberate RBAC scope decision.

- [ ] **Step 3: Frontend cluster-scoped display guard**

In `ResourcePage.tsx`, ensure namespace display uses `cluster-scoped` for empty namespace:

```tsx
<td className="font-mono text-xs">{item.identity.namespace || 'cluster-scoped'}</td>
```

Expected: PV rows do not look broken in resource table.

---

### Task 3: NovaAPM-Native RBAC/User Authorization Surface

**Files:**
- Inspect: `/Users/user/Documents/NovaAPM/novaapm/internal/platform/rbac`
- Inspect: `/Users/user/Documents/NovaAPM/novaapm/internal/modules/k8sops/rbac`
- Create or modify backend module under: `/Users/user/Documents/NovaAPM/novaapm/internal/platform`
- Modify frontend route/nav:
  - `/Users/user/Documents/NovaAPM/novaapm-fe/src/app/routes.tsx`
  - `/Users/user/Documents/NovaAPM/novaapm-fe/src/pages/k8s/navigation.ts`
- Create frontend page if needed:
  - `/Users/user/Documents/NovaAPM/novaapm-fe/src/pages/k8s/PlatformAccessPage.tsx`

- [ ] **Step 1: Inventory current platform RBAC model**

Run:

```bash
rg -n "type .*Binding|type .*Role|Authorize|Subject|Scope|k8s\\." internal/platform internal/modules/k8sops
```

Expected: identify existing `Subject`, `Request`, `Scope`, repository/binding types.

- [ ] **Step 2: Define API contract**

Target endpoints:

```text
GET    /api/v1/k8s/platform-access/bindings
POST   /api/v1/k8s/platform-access/bindings
DELETE /api/v1/k8s/platform-access/bindings/:id
GET    /api/v1/k8s/platform-access/permissions
```

Expected permission examples:

```text
k8s.resource:read
k8s.credential:manage
k8s.deploy:apply
k8s.deploy:delete
k8s.terminal:exec
k8s.rbac:manage
```

- [ ] **Step 3: Add backend handler tests before implementation**

Tests must cover:

- default reader cannot manage access.
- authorized admin can create a namespace-scoped binding.
- terminal permission can be granted separately from resource read.

- [ ] **Step 4: Implement minimal backend service**

Use existing platform RBAC repository if present. If missing, add a small repository interface and memory implementation consistent with current code style.

- [ ] **Step 5: Implement frontend access page**

Use compact operational UI:

- subject selector/input
- cluster/namespace scope inputs
- permission checklist
- binding table
- delete action with audit result

No marketing copy or decorative hero.

---

### Task 4: Resource Operation Entry Points

**Files:**
- Inspect: `/Users/user/Documents/NovaAPM/novaapm/internal/modules/k8sops/deployment`
- Modify frontend:
  - `/Users/user/Documents/NovaAPM/novaapm-fe/src/pages/k8s/ResourcePage.tsx`
  - `/Users/user/Documents/NovaAPM/novaapm-fe/src/pages/k8s/api.ts`

- [ ] **Step 1: Verify existing backend operation routes**

Run:

```bash
rg -n "Preview|Apply|Delete|Rollback|dry|audit|/k8s/.+apply|/k8s/.+delete" internal/modules/k8sops internal/httpapi
```

Expected: locate preview/apply/delete endpoints already backed by typed-first dynamic fallback.

- [ ] **Step 2: Add frontend API wrappers**

Add wrappers such as:

```ts
previewResourceApply(input)
applyResource(input)
previewResourceDelete(input)
deleteResource(input)
```

Expected: wrappers use existing `/api/v1/k8s/...` routes and return audit/plan payload.

- [ ] **Step 3: Add ResourcePage guarded operation panel**

For selected resource:

- YAML preview tab remains read-only.
- Add `Dry-run Apply` and `Dry-run Delete` controls.
- Actual Apply/Delete require explicit confirmation and permission error display.

Expected: read-only users see disabled operations with reason.

---

### Task 5: Real Cluster Read-Only Validation

**Files:**
- No code change unless validation finds bugs.

- [ ] **Step 1: Ask user before using kubeconfig for real cluster**

Ask for a read-only kubeconfig or confirm `test03-02` credential can be used.

- [ ] **Step 2: Validate safe read-only endpoints**

Run via backend only:

```bash
curl --silent --location "http://localhost:7890/api/v1/k8s/resources?cluster_id=test03-02&namespace=default&kind=Pod"
curl --silent --location "http://localhost:7890/api/v1/k8s/resources?cluster_id=test03-02&namespace=default&kind=VirtualService"
curl --silent --location "http://localhost:7890/api/v1/k8s/runtime-groups?cluster_id=test03-02&namespace=default"
```

Expected:

- no write operation is executed.
- unsupported CRDs return controlled errors only when explicitly requested.
- runtime topology returns data or empty groups, not 500/404.

- [ ] **Step 3: Capture UI screenshots**

Use Playwright against:

```text
http://127.0.0.1:3001/k8s/resource-view
http://127.0.0.1:3001/k8s/runtime-topology
```

Expected: screenshots show real API state or clear permission/empty-state messages.

---

## Recommended Execution Order

1. Task 1: Real backend route smoke test.
2. Task 2: Expanded resource detail/YAML cluster-scoped validation.
3. Task 5: Real read-only cluster validation.
4. Task 3: NovaAPM-native RBAC/user authorization surface.
5. Task 4: Resource operation entry points.

Reason: route/runtime correctness should be verified before adding more UI and permission workflows. RBAC should precede actual operation controls, because apply/delete/terminal behavior depends on clean permission semantics.

## Verification Gate

Before claiming completion for this phase, run:

```bash
go test ./internal/modules/k8sops/... ./internal/httpapi
npm run build
```

If frontend UI was changed, also capture screenshots for the changed pages.
