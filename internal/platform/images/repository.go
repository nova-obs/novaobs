package images

import (
	"context"
	"errors"

	"novaapm/internal/database"
)

var ErrUnavailable = errors.New("platform image store unavailable")

type Repository interface {
	Upsert(ctx context.Context, item Image) error
	List(ctx context.Context) ([]Image, error)
}

type StoreRepository struct {
	store database.PlatformImageStore
}

func NewStoreRepository(store database.PlatformImageStore) StoreRepository {
	return StoreRepository{store: store}
}

func (r StoreRepository) Upsert(ctx context.Context, item Image) error {
	if r.store == nil {
		return ErrUnavailable
	}
	return r.store.Upsert(ctx, item.Key, item)
}

func (r StoreRepository) List(ctx context.Context) ([]Image, error) {
	if r.store == nil {
		return nil, ErrUnavailable
	}
	var items []Image
	if err := r.store.FindAll(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}
