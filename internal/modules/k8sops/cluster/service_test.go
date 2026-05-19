package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceListsClusters(t *testing.T) {
	repo := NewMemoryRepository([]Cluster{
		{ID: "prod", Name: "prod", Region: "cn-shanghai", Status: "active"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod", items[0].ID)
	require.Equal(t, "active", items[0].Status)
}

func TestMemoryRepositoryFiltersClustersByQuery(t *testing.T) {
	repo := NewMemoryRepository([]Cluster{
		{ID: "prod", Name: "prod-core", Region: "cn-shanghai", Status: "active"},
		{ID: "staging", Name: "staging-lab", Region: "cn-beijing", Status: "paused"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{Query: "core"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod", items[0].ID)
}
