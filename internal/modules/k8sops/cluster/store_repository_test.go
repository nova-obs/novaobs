package cluster

import (
	"context"
	"testing"

	"novaobs/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestStoreRepositoryPersistsAndListsClusters(t *testing.T) {
	store := memstore.NewStore()
	repo := NewStoreRepository(store.K8sClusters())
	ctx := context.Background()

	created, err := repo.Upsert(ctx, Cluster{
		ID:          "prod",
		Name:        "prod-core",
		Version:     "v1.30.1",
		Region:      "cn-shanghai",
		Description: "生产集群",
		Status:      "active",
	})
	require.NoError(t, err)
	require.Equal(t, "prod", created.ID)

	items, err := repo.List(ctx, ListFilter{Query: "core"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod-core", items[0].Name)
	require.Equal(t, "active", items[0].Status)
}
