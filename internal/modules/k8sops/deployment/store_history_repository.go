package deployment

import (
	"context"
	"time"

	"novaapm/internal/database"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type HistoryWriter interface {
	CreateHistory(ctx context.Context, record HistoryRecord) (HistoryRecord, error)
}

type StoreHistoryRepository struct {
	store database.K8sDeploymentHistoryStore
}

func NewStoreHistoryRepository(store database.K8sDeploymentHistoryStore) *StoreHistoryRepository {
	return &StoreHistoryRepository{store: store}
}

func (r *StoreHistoryRepository) ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error) {
	var items []HistoryRecord
	if err := r.store.FindAll(ctx, &items); err != nil {
		return nil, err
	}
	return NewMemoryReader(items).ListHistory(ctx, filter)
}

func (r *StoreHistoryRepository) ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	return NewMemoryReader(nil).ListAuditEvents(ctx, filter)
}

func (r *StoreHistoryRepository) CreateHistory(ctx context.Context, record HistoryRecord) (HistoryRecord, error) {
	if record.ID == "" {
		record.ID = primitive.NewObjectID().Hex()
	}
	now := time.Now().UTC()
	if record.StartedAt.IsZero() {
		record.StartedAt = now
	}
	if record.FinishedAt.IsZero() {
		record.FinishedAt = now
	}
	if err := r.store.Insert(ctx, record); err != nil {
		return HistoryRecord{}, err
	}
	return record, nil
}
