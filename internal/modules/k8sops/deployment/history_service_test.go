package deployment

import (
	"context"
	"testing"

	"novaobs/internal/platform/audit"

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
