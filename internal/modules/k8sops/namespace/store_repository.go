package namespace

import (
	"context"

	"novaobs/internal/database"
)

type StoreRepository struct {
	store database.K8sNamespaceStore
}

func NewStoreRepository(store database.K8sNamespaceStore) *StoreRepository {
	return &StoreRepository{store: store}
}

func (r *StoreRepository) List(ctx context.Context, filter ListFilter) ([]Namespace, error) {
	var items []Namespace
	var err error
	if filter.ClusterID == "" {
		err = r.store.FindAll(ctx, &items)
	} else {
		err = r.store.FindByCluster(ctx, filter.ClusterID, &items)
	}
	if err != nil {
		return nil, err
	}
	return NewMemoryRepository(items).List(ctx, filter)
}

func (r *StoreRepository) Upsert(ctx context.Context, item Namespace) (Namespace, error) {
	if item.ID == "" {
		item.ID = item.ClusterID + "/" + item.Name
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.Phase == "" {
		item.Phase = "Active"
	}
	if err := r.store.Upsert(ctx, item.ID, item); err != nil {
		return Namespace{}, err
	}
	return item, nil
}
