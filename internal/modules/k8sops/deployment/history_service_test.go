package deployment

import (
	"context"
	"testing"

	"novaapm/internal/database/memstore"
	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestServiceListsDeploymentHistory(t *testing.T) {
	svc := NewService(NewMemoryReader([]HistoryRecord{
		{ID: "deploy-1", ClusterID: "prod", Namespace: "orders", Workload: "orders-api", Action: "rollout", Status: "warning"},
	}))

	items, err := svc.ListHistory(context.Background(), ListFilter{ClusterID: "prod"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders-api", items[0].Workload)
}

func TestServiceListsAuditEvents(t *testing.T) {
	svc := NewService(NewMemoryReader(nil, []AuditEvent{
		{ID: "audit-1", ClusterID: "prod", Namespace: "orders", ResourceKind: "Deployment", ResourceName: "orders-api", Action: "rollout.pause", Actor: "platform-admin"},
	}))

	items, err := svc.ListAuditEvents(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "rollout.pause", items[0].Action)
}

func TestServiceListsPlatformAuditEvents(t *testing.T) {
	auditSvc := audit.NewService(audit.NewMemoryStore())
	_, err := auditSvc.Record(context.Background(), audit.Event{
		Actor:        audit.Actor{ID: "user-1", Name: "alice"},
		Resource:     audit.Resource{Type: "k8s.terminal", Name: "orders"},
		ResourceType: "k8s.terminal",
		ResourceName: "orders",
		Action:       "exec",
		Scope:        "cluster=prod namespace=orders",
		RequestSummary: map[string]any{
			"cluster_id": "prod",
			"namespace":  "orders",
			"verb":       "delete",
		},
		Result: "blocked",
		Trace:  "trace-terminal-1",
	})
	require.NoError(t, err)

	svc := NewService(NewMemoryReader(nil), auditSvc)
	items, err := svc.ListAuditEvents(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Query: "exec"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "exec", items[0].Action)
	require.Equal(t, "alice", items[0].Actor)
	require.Equal(t, "blocked", items[0].Status)
	require.Equal(t, "trace-terminal-1", items[0].TraceID)
	require.Equal(t, "terminal", items[0].ResourceKind)
}

func TestServicePreviewResolvesResourceVersionFromClusterCapabilities(t *testing.T) {
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), staticDeploymentCapabilityProvider{
		snapshot: kubeclient.CapabilitySnapshot{
			Resources: []kubeclient.APIResource{
				{Group: "extensions", Version: "v1beta1", GroupVersion: "extensions/v1beta1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
			},
		},
	})

	result, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: orders-ingress
  namespace: orders`,
	})

	require.NoError(t, err)
	require.Len(t, result.Resources, 1)
	require.Equal(t, "extensions/v1beta1", result.Resources[0].APIVersion)
	require.Equal(t, "Ingress", result.Resources[0].Kind)
}

func TestServicePreviewRunsServerSideDryRunAfterAuthorization(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
			Operation:  "create",
			AfterHash:  "deployment-after",
		}},
	}}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner)

	result, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	})

	require.NoError(t, err)
	require.Equal(t, 1, dryRunner.calls)
	require.Equal(t, "prod", dryRunner.request.ClusterID)
	require.Len(t, result.Resources, 1)
	require.Equal(t, "apps/v1", result.Resources[0].APIVersion)
	require.Equal(t, "Deployment", result.Resources[0].Kind)
	require.Equal(t, "orders", result.Resources[0].Namespace)
	require.Len(t, result.Diffs, 1)
	require.Equal(t, "create", result.Diffs[0].Operation)
	require.Equal(t, "deployment-after", result.Diffs[0].AfterHash)
}

func TestServicePreviewAcceptsClusterScopedResourcesWithoutNamespace(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{
			{APIVersion: "v1", Kind: "Namespace", Name: "novaapm-system"},
			{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: "novaapm-logs-agent"},
			{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: "novaapm-logs-agent"},
			{APIVersion: "apps/v1", Kind: "DaemonSet", Namespace: "novaapm-system", Name: "novaapm-logs-agent"},
		},
	}}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner)

	result, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: v1
kind: Namespace
metadata:
  name: novaapm-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: novaapm-logs-agent
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: novaapm-logs-agent
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: novaapm-logs-agent
  namespace: novaapm-system`,
	})

	require.NoError(t, err)
	require.Len(t, result.Resources, 4)
	namespacesByKind := map[string]string{}
	for _, resource := range result.Resources {
		namespacesByKind[resource.Kind] = resource.Namespace
	}
	require.Equal(t, "", namespacesByKind["Namespace"])
	require.Equal(t, "", namespacesByKind["ClusterRole"])
	require.Equal(t, "", namespacesByKind["ClusterRoleBinding"])
	require.Equal(t, "novaapm-system", namespacesByKind["DaemonSet"])
	require.Equal(t, 1, dryRunner.calls)
}

func TestServiceApplyPersistsClusterScopedResourcesInInventory(t *testing.T) {
	objects := []kubeclient.OperationObject{
		{APIVersion: "v1", Kind: "Namespace", Name: "novaapm-system", AfterHash: "namespace-hash"},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: "novaapm-logs-agent", AfterHash: "clusterrole-hash"},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: "novaapm-logs-agent", AfterHash: "clusterrolebinding-hash"},
		{APIVersion: "apps/v1", Kind: "DaemonSet", Namespace: "novaapm-system", Name: "novaapm-logs-agent", AfterHash: "daemonset-hash"},
	}
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{Objects: objects}}
	applier := &recordingDeploymentApplier{result: kubeclient.ResourceOperationResult{Objects: objects}}
	inventory := NewMemoryInventoryRepository(nil)
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner, applier, inventory)
	req := OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: v1
kind: Namespace
metadata:
  name: novaapm-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: novaapm-logs-agent
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: novaapm-logs-agent
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: novaapm-logs-agent
  namespace: novaapm-system`,
	}
	preview, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, req)
	require.NoError(t, err)

	applied, err := svc.Apply(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID:         req.ClusterID,
		YAMLContent:       req.YAMLContent,
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})

	require.NoError(t, err)
	require.Equal(t, "applied", applied.Status)
	clusterRole, err := inventory.Find(context.Background(), ResourceIdentity{
		ClusterID:  "prod",
		APIVersion: "rbac.authorization.k8s.io/v1",
		Kind:       "ClusterRole",
		Name:       "novaapm-logs-agent",
	})
	require.NoError(t, err)
	require.Equal(t, "", clusterRole.Namespace)
	daemonSet, err := inventory.Find(context.Background(), ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "novaapm-system",
		APIVersion: "apps/v1",
		Kind:       "DaemonSet",
		Name:       "novaapm-logs-agent",
	})
	require.NoError(t, err)
	require.Equal(t, "novaapm-system", daemonSet.Namespace)
}

func TestServiceBlocksPreviewForReadOnlyCluster(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner, staticClusterPolicy{readOnly: true})

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "test03",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrClusterReadOnly)
	require.Equal(t, 0, dryRunner.calls)
}

func TestServiceBlocksDeleteForReadOnlyCluster(t *testing.T) {
	deleter := &recordingDeploymentDeleter{}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), deleter, staticClusterPolicy{readOnly: true})

	_, err := svc.Delete(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, DeleteRequest{
		Identity: ResourceIdentity{ClusterID: "test03", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
	})

	require.ErrorIs(t, err, ErrClusterReadOnly)
	require.Equal(t, 0, deleter.calls)
}

func TestServiceBlocksRollbackForReadOnlyCluster(t *testing.T) {
	svc := NewService(
		NewMemoryReader([]HistoryRecord{{ID: "history-1", ClusterID: "test03", Namespace: "orders", Workload: "orders-api"}}),
		allowDeploymentAuthorizer{},
		audit.NewService(audit.NewMemoryStore()),
		staticClusterPolicy{readOnly: true},
	)

	_, err := svc.Rollback(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, RollbackRequest{
		HistoryID: "history-1",
		Identity:  ResourceIdentity{ClusterID: "test03", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
	})

	require.ErrorIs(t, err, ErrClusterReadOnly)
}

func TestServicePreviewReturnsConfirmationPlan(t *testing.T) {
	auditStore := audit.NewMemoryStore()
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{
			{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  "orders",
				Name:       "orders-api",
			},
			{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Namespace:  "orders",
				Name:       "orders-config",
			},
		},
		Warnings: []string{"partial discovery warning"},
	}}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(auditStore), dryRunner)
	req := OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: v1
kind: ConfigMap
metadata:
  name: orders-config
  namespace: orders
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	}

	first, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, req)
	require.NoError(t, err)
	second, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, req)
	require.NoError(t, err)

	require.NotEmpty(t, first.PreviewID)
	require.Equal(t, first.PreviewID, second.PreviewID)
	require.NotEmpty(t, first.ConfirmationToken)
	require.Equal(t, first.ConfirmationToken, second.ConfirmationToken)
	require.Equal(t, []string{"partial discovery warning"}, first.Warnings)
	require.Len(t, first.Diffs, 2)
	require.Equal(t, "apply", first.Diffs[0].Operation)
	require.Equal(t, "ConfigMap", first.Diffs[0].Kind)
	require.Equal(t, "orders-config", first.Diffs[0].Name)
	require.NotEmpty(t, first.Diffs[0].AfterHash)
	require.Equal(t, first.Diffs, second.Diffs)
	require.NotContains(t, first.ConfirmationToken, "orders-api")
	require.NotContains(t, first.ConfirmationToken, "apiVersion")

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, first.PreviewID, events[0].RequestSummary["preview_id"])
	require.NotContains(t, events[0].RequestSummary, "yaml_content")
	require.NotContains(t, events[0].RequestSummary, "confirmation_token")
	require.Equal(t, 2, events[0].RequestSummary["diff_count"])
}

func TestServiceApplyRequiresMatchingPreviewConfirmation(t *testing.T) {
	auditStore := audit.NewMemoryStore()
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
			AfterHash:  "after-hash",
		}},
	}}
	applier := &recordingDeploymentApplier{result: kubeclient.ResourceOperationResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
		}},
	}}
	inventory := NewMemoryInventoryRepository(nil)
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(auditStore), dryRunner, applier, inventory)
	req := OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	}

	_, err := svc.Apply(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, req)
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.Equal(t, 0, applier.calls)

	_, err = svc.Apply(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID:         req.ClusterID,
		YAMLContent:       req.YAMLContent,
		PreviewID:         "wrong-preview",
		ConfirmationToken: "wrong-token",
	})
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.Equal(t, 0, applier.calls)

	preview, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, req)
	require.NoError(t, err)
	applied, err := svc.Apply(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID:         req.ClusterID,
		YAMLContent:       req.YAMLContent,
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})

	require.NoError(t, err)
	require.Equal(t, "applied", applied.Status)
	require.Equal(t, 1, applier.calls)
	require.Equal(t, kubeclient.OperationModeApply, applier.request.Mode)
	require.Equal(t, "prod", applier.request.ClusterID)
	require.False(t, dryRunner.request.ForceConflicts)
	require.False(t, applier.request.ForceConflicts)
	found, err := inventory.Find(context.Background(), ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
	})
	require.NoError(t, err)
	require.Equal(t, preview.PreviewID, found.LastPreviewID)
	require.NotEmpty(t, found.LastApplyHash)

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "deploy", events[1].Action)
	require.Equal(t, preview.PreviewID, events[1].RequestSummary["preview_id"])
	require.NotContains(t, events[1].RequestSummary, "confirmation_token")
	require.NotContains(t, events[1].RequestSummary, "yaml_content")
}

func TestServiceApplyPropagatesInternalForceConflicts(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Namespace:  "novaapm-system",
			Name:       "novaapm-logs-agent-config",
			AfterHash:  "after-hash",
		}},
	}}
	applier := &recordingDeploymentApplier{result: kubeclient.ResourceOperationResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Namespace:  "novaapm-system",
			Name:       "novaapm-logs-agent-config",
		}},
	}}
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner, applier, nil)
	req := OperationRequest{
		ClusterID:      "test03",
		ForceConflicts: true,
		YAMLContent: `apiVersion: v1
kind: ConfigMap
metadata:
  name: novaapm-logs-agent-config
  namespace: novaapm-system`,
	}

	preview, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, req)
	require.NoError(t, err)
	require.True(t, dryRunner.request.ForceConflicts)

	_, err = svc.Apply(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID:         req.ClusterID,
		YAMLContent:       req.YAMLContent,
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
		ForceConflicts:    true,
	})
	require.NoError(t, err)
	require.True(t, dryRunner.request.ForceConflicts)
	require.True(t, applier.request.ForceConflicts)
}

func TestServiceApplyPersistsDeploymentHistorySnapshot(t *testing.T) {
	store := memstore.NewStore()
	historyRepo := NewStoreHistoryRepository(store.K8sDeploymentHistory())
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
			AfterHash:  "after-hash",
		}},
	}}
	applier := &recordingDeploymentApplier{result: kubeclient.ResourceOperationResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
		}},
	}}
	svc := NewService(historyRepo, allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), dryRunner, applier, historyRepo)
	req := OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	}
	preview, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, req)
	require.NoError(t, err)

	applied, err := svc.Apply(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, OperationRequest{
		ClusterID:         req.ClusterID,
		YAMLContent:       req.YAMLContent,
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})
	require.NoError(t, err)

	items, err := svc.ListHistory(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders-api", items[0].Workload)
	require.Equal(t, "deploy", items[0].Action)
	require.Equal(t, "applied", items[0].Status)
	require.Equal(t, applied.AuditID, items[0].Revision)
	require.Equal(t, "alice", items[0].Actor)
}

func TestServiceDeleteRequiresMatchingConfirmationBeforeExecution(t *testing.T) {
	deleter := &recordingDeploymentDeleter{result: kubeclient.ResourceOperationResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
		}},
	}}
	auditStore := audit.NewMemoryStore()
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(auditStore), deleter)
	identity := ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
		UID:        "uid-orders-api",
	}

	_, err := svc.Delete(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, DeleteRequest{Identity: identity})
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.Equal(t, 0, deleter.calls)

	plan := buildDeletePlan(identity)
	result, err := svc.Delete(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, DeleteRequest{
		Identity:          identity,
		PreviewID:         plan.ID,
		ConfirmationToken: plan.ConfirmationToken,
	})

	require.NoError(t, err)
	require.Equal(t, "deleted", result.Status)
	require.Equal(t, plan.ID, result.PreviewID)
	require.Equal(t, 1, deleter.calls)
	require.Equal(t, kubeclient.OperationModeDelete, deleter.request.Mode)
	require.Equal(t, "orders-api", deleter.request.Identity.Name)

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "delete", events[0].Action)
	require.Equal(t, plan.ID, events[0].RequestSummary["preview_id"])
	require.NotContains(t, events[0].RequestSummary, "confirmation_token")
}

func TestServicePreviewDeleteReturnsConfirmationPlan(t *testing.T) {
	auditStore := audit.NewMemoryStore()
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(auditStore))
	identity := ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
		UID:        "uid-orders-api",
	}

	result, err := svc.PreviewDelete(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, DeleteRequest{Identity: identity})

	require.NoError(t, err)
	require.Equal(t, "preview", result.Status)
	require.NotEmpty(t, result.PreviewID)
	require.NotEmpty(t, result.ConfirmationToken)
	require.Len(t, result.Diffs, 1)
	require.Equal(t, "delete", result.Diffs[0].Operation)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "delete_preview", events[0].Action)
	require.NotContains(t, events[0].RequestSummary, "confirmation_token")
}

func TestServiceRollbackRequiresExistingHistoryRecord(t *testing.T) {
	identity := ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
		UID:        "uid-orders-api",
	}
	svc := NewService(NewMemoryReader([]HistoryRecord{
		{ID: "deploy-1", ClusterID: "prod", Namespace: "orders", Workload: "orders-api", Action: "deploy", Status: "applied"},
	}), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()))

	_, err := svc.Rollback(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, RollbackRequest{HistoryID: "missing", Identity: identity})
	require.ErrorIs(t, err, ErrInvalidRequest)

	result, err := svc.Rollback(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, RollbackRequest{HistoryID: "deploy-1", Identity: identity})
	require.NoError(t, err)
	require.Equal(t, "rollback_requested", result.Status)
}

func TestServicePreviewRechecksPermissionForDryRunResultIdentities(t *testing.T) {
	dryRunner := &recordingDeploymentDryRunner{result: kubeclient.DryRunApplyResult{
		Objects: []kubeclient.OperationObject{{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "payments",
			Name:       "orders-api",
		}},
	}}
	svc := NewService(NewMemoryReader(nil), namespaceDeploymentAuthorizer{allowedNamespace: "orders"}, audit.NewService(audit.NewMemoryStore()), dryRunner)

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrPermissionDenied)
	require.Equal(t, 1, dryRunner.calls)
}

func TestServicePreviewRejectsUnsupportedResourceVersion(t *testing.T) {
	svc := NewService(NewMemoryReader(nil), allowDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), staticDeploymentCapabilityProvider{})

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: orders-vs
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestServicePreviewChecksPermissionBeforeResolvingClusterCapabilities(t *testing.T) {
	provider := &countingDeploymentCapabilityProvider{}
	dryRunner := &recordingDeploymentDryRunner{}
	svc := NewService(NewMemoryReader(nil), denyDeploymentAuthorizer{}, audit.NewService(audit.NewMemoryStore()), provider, dryRunner)

	_, err := svc.Preview(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, OperationRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: orders-vs
  namespace: orders`,
	})

	require.ErrorIs(t, err, ErrPermissionDenied)
	require.Equal(t, 0, provider.calls)
	require.Equal(t, 0, dryRunner.calls)
}

type staticDeploymentCapabilityProvider struct {
	snapshot kubeclient.CapabilitySnapshot
}

func (p staticDeploymentCapabilityProvider) Capabilities(_ context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error) {
	p.snapshot.ClusterID = clusterID
	return p.snapshot, nil
}

type allowDeploymentAuthorizer struct{}

func (allowDeploymentAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type denyDeploymentAuthorizer struct{}

func (denyDeploymentAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false}
}

type namespaceDeploymentAuthorizer struct {
	allowedNamespace string
}

func (a namespaceDeploymentAuthorizer) Authorize(_ platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: req.Scope.Namespace == a.allowedNamespace}
}

type countingDeploymentCapabilityProvider struct {
	calls int
}

func (p *countingDeploymentCapabilityProvider) Capabilities(context.Context, string) (kubeclient.CapabilitySnapshot, error) {
	p.calls++
	return kubeclient.CapabilitySnapshot{}, nil
}

type recordingDeploymentDryRunner struct {
	result  kubeclient.DryRunApplyResult
	request kubeclient.ClusterDryRunApplyRequest
	calls   int
}

func (r *recordingDeploymentDryRunner) DryRunApply(_ context.Context, req kubeclient.ClusterDryRunApplyRequest) (kubeclient.DryRunApplyResult, error) {
	r.calls++
	r.request = req
	return r.result, nil
}

type recordingDeploymentApplier struct {
	result  kubeclient.ResourceOperationResult
	request kubeclient.ClusterApplyRequest
	calls   int
}

func (a *recordingDeploymentApplier) Apply(_ context.Context, req kubeclient.ClusterApplyRequest) (kubeclient.ResourceOperationResult, error) {
	a.calls++
	a.request = req
	return a.result, nil
}

type recordingDeploymentDeleter struct {
	result  kubeclient.ResourceOperationResult
	request kubeclient.ClusterDeleteRequest
	calls   int
}

func (d *recordingDeploymentDeleter) Delete(_ context.Context, req kubeclient.ClusterDeleteRequest) (kubeclient.ResourceOperationResult, error) {
	d.calls++
	d.request = req
	return d.result, nil
}

type staticClusterPolicy struct {
	readOnly bool
}

func (p staticClusterPolicy) IsReadOnly(context.Context, string) (bool, error) {
	return p.readOnly, nil
}
