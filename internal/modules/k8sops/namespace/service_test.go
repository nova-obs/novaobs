package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceListsNamespaces(t *testing.T) {
	repo := NewMemoryRepository([]Namespace{{ID: "orders", Name: "orders", ClusterID: "prod"}})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{ClusterID: "prod"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders", items[0].Name)
}

func TestMemoryRepositoryFiltersSortsAndPaginatesNamespaces(t *testing.T) {
	repo := NewMemoryRepository([]Namespace{
		{ID: "orders", Name: "orders", ClusterID: "prod", Status: "active"},
		{ID: "payment", Name: "payment", ClusterID: "prod", Status: "active"},
		{ID: "sandbox", Name: "sandbox", ClusterID: "dev", Status: "paused"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{
		ClusterID: "prod",
		Sort:      "name",
		Order:     "desc",
		Page:      2,
		PageSize:  1,
	})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders", items[0].Name)
}
