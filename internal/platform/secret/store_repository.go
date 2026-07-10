package secret

import (
	"context"
	"errors"

	"novaapm/internal/database"

	"go.mongodb.org/mongo-driver/mongo"
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
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = ErrNotFound
	}
	return item, err
}

func (r StoreRepository) FindByTypeAndScope(ctx context.Context, typ string, scope Scope) (Secret, error) {
	var item Secret
	err := r.store.FindByTypeAndScope(ctx, typ, scope, &item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = ErrNotFound
	}
	return item, err
}

func (r StoreRepository) ListByType(ctx context.Context, typ string) ([]Secret, error) {
	var items []Secret
	if err := r.store.FindByType(ctx, typ, &items); err != nil {
		return nil, err
	}
	return items, nil
}
