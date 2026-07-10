package audit

import (
	"context"

	"novaapm/internal/database"
)

type StoreRepository struct {
	store database.AuditEventStore
}

func NewStoreRepository(store database.AuditEventStore) StoreRepository {
	return StoreRepository{store: store}
}

func (r StoreRepository) Insert(ctx context.Context, event Event) error {
	return r.store.Insert(ctx, event)
}

func (r StoreRepository) List(ctx context.Context) ([]Event, error) {
	var events []Event
	if err := r.store.FindAll(ctx, &events); err != nil {
		return nil, err
	}
	return events, nil
}
