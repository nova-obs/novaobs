package cluster

import (
	"context"

	"novaapm/internal/database"
)

type StoreRepository struct {
	store database.K8sClusterStore
}

func NewStoreRepository(store database.K8sClusterStore) *StoreRepository {
	return &StoreRepository{store: store}
}

func (r *StoreRepository) List(ctx context.Context, filter ListFilter) ([]Cluster, error) {
	var items []Cluster
	if err := r.store.FindAll(ctx, &items); err != nil {
		return nil, err
	}
	return NewMemoryRepository(items).List(ctx, filter)
}

func (r *StoreRepository) Upsert(ctx context.Context, item Cluster) (Cluster, error) {
	item = normalizeCluster(item)
	if err := r.store.Upsert(ctx, item.ID, item); err != nil {
		return Cluster{}, err
	}
	return item, nil
}

func (r *StoreRepository) Delete(ctx context.Context, id string) error {
	return r.store.Delete(ctx, id)
}
