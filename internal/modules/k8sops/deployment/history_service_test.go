package deployment

import (
	"context"
	"testing"

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
