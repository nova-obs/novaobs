package deployment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryInventoryRepositoryUpsertsFindsAndListsRecords(t *testing.T) {
	repo := NewMemoryInventoryRepository(nil)
	ctx := context.Background()
	updatedAt := time.Date(2026, 5, 21, 9, 30, 0, 0, time.UTC)

	created, err := repo.Upsert(ctx, InventoryRecord{
		ClusterID:     "prod",
		Namespace:     "orders",
		APIVersion:    "apps/v1",
		Kind:          "Deployment",
		Name:          "orders-api",
		FieldManager:  "novaobs-k8sops",
		LastApplyHash: "hash-v1",
		LastPreviewID: "preview-v1",
		UpdatedAt:     updatedAt,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	found, err := repo.Find(ctx, ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, found.ID)
	require.Equal(t, "hash-v1", found.LastApplyHash)

	updated, err := repo.Upsert(ctx, InventoryRecord{
		ClusterID:     "prod",
		Namespace:     "orders",
		APIVersion:    "apps/v1",
		Kind:          "Deployment",
		Name:          "orders-api",
		FieldManager:  "novaobs-k8sops",
		LastApplyHash: "hash-v2",
		LastPreviewID: "preview-v2",
		UpdatedAt:     updatedAt.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, "hash-v2", updated.LastApplyHash)

	_, err = repo.Upsert(ctx, InventoryRecord{
		ClusterID:     "prod",
		Namespace:     "payments",
		APIVersion:    "v1",
		Kind:          "ConfigMap",
		Name:          "payment-config",
		FieldManager:  "novaobs-k8sops",
		LastApplyHash: "hash-cm",
		LastPreviewID: "preview-cm",
		UpdatedAt:     updatedAt,
	})
	require.NoError(t, err)

	items, err := repo.List(ctx, InventoryFilter{ClusterID: "prod", Namespace: "orders"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "orders-api", items[0].Name)

	items[0].LastApplyHash = "mutated-by-caller"
	foundAgain, err := repo.Find(ctx, ResourceIdentity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
	})
	require.NoError(t, err)
	require.Equal(t, "hash-v2", foundAgain.LastApplyHash)
}

func TestMemoryInventoryRepositoryRejectsIncompleteRecords(t *testing.T) {
	repo := NewMemoryInventoryRepository(nil)

	_, err := repo.Upsert(context.Background(), InventoryRecord{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
	})

	require.ErrorIs(t, err, ErrInvalidInventoryRecord)
}
