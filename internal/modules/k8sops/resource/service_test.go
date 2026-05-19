package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceListsResourcesByIdentityScope(t *testing.T) {
	svc := NewService(NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
		{
			Identity: Identity{ClusterID: "prod", Namespace: "payment", APIVersion: "apps/v1", Kind: "Deployment", Name: "payment-gateway", UID: "uid-payment"},
			Status:   "healthy",
		},
	}))

	items, err := svc.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: "Deployment"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "uid-orders", items[0].Identity.UID)
	require.Equal(t, "orders-api", items[0].Identity.Name)
}

func TestServiceReturnsDetailAndYAMLByIdentity(t *testing.T) {
	reader := NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
	})
	svc := NewService(reader)

	detail, err := svc.GetDetail(context.Background(), DetailQuery{Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"}})
	require.NoError(t, err)
	require.Equal(t, "orders-api", detail.Identity.Name)

	yaml, err := svc.GetYAML(context.Background(), DetailQuery{Identity: detail.Identity})
	require.NoError(t, err)
	require.Contains(t, yaml.YAML, "kind: Deployment")
	require.Contains(t, yaml.YAML, "name: orders-api")
}

func TestServiceRequiresAPIVersionForDetailIdentity(t *testing.T) {
	reader := NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
	})
	svc := NewService(reader)

	_, err := svc.GetDetail(context.Background(), DetailQuery{Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"}})

	require.Error(t, err)
}
