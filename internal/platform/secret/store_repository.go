package secret

import (
	"context"

	"novaobs/internal/database"
)

type StoreRepository struct {
	store database.SecretStore
}

func NewStoreRepository(store database.SecretStore) StoreRepository {
	return StoreRepository{store: store}
}

func (r StoreRepository) Save(ctx context.Context, item Secret) error {
	return r.store.Upsert(ctx, item.ID, item)
}

func (r StoreRepository) Get(ctx context.Context, id string) (Secret, error) {
	var item Secret
	err := r.store.FindByID(ctx, id, &item)
	return item, err
}

func (r StoreRepository) FindByTypeAndScope(ctx context.Context, typ string, scope Scope) (Secret, error) {
	var item Secret
	err := r.store.FindByTypeAndScope(ctx, typ, scope, &item)
	return item, err
}
