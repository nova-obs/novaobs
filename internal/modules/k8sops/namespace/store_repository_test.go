package namespace

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestStoreRepositoryPersistsAndListsNamespaces(t *testing.T) {
	store := memstore.NewStore()
	repo := NewStoreRepository(store.K8sNamespaces())
	ctx := context.Background()

	_, err := repo.Upsert(ctx, Namespace{
		ID:        "prod/orders",
		ClusterID: "prod",
		Name:      "orders",
		Status:    "active",
		Owner:     "orders-team",
		Phase:     "Active",
		UpdatedAt: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	items, err := repo.List(ctx, ListFilter{ClusterID: "prod", Query: "orders"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders", items[0].Name)
	require.Equal(t, "orders-team", items[0].Owner)
}
