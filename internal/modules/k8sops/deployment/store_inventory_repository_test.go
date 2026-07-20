package deployment

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestStoreInventoryRepositoryPersistsRecordsAcrossInstances(t *testing.T) {
	store := memstore.NewStore()
	ctx := context.Background()
	first := NewStoreInventoryRepository(store.K8sDeploymentInventory())
	second := NewStoreInventoryRepository(store.K8sDeploymentInventory())
	record := InventoryRecord{
		ClusterID:     "prod",
		Namespace:     "orders",
		APIVersion:    "apps/v1",
		Kind:          "Deployment",
		Name:          "orders-api",
		FieldManager:  "novaapm-k8sops",
		LastApplyHash: "hash-v1",
		LastPreviewID: "preview-v1",
		UpdatedAt:     time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	}

	created, err := first.Upsert(ctx, record)
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	found, err := second.Find(ctx, ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, found.ID)
	require.Equal(t, "hash-v1", found.LastApplyHash)

	items, err := second.List(ctx, InventoryFilter{ClusterID: "prod", Namespace: "orders"})
	require.NoError(t, err)
	require.Len(t, items, 1)

	require.NoError(t, second.Remove(ctx, ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
	}))
	_, err = first.Find(ctx, ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
	})
	require.ErrorIs(t, err, ErrInventoryRecordNotFound)
}
