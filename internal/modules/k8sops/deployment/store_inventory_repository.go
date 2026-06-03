package deployment

import (
	"context"
	"errors"
	"strings"
	"time"

	"novaobs/internal/database"

	"go.mongodb.org/mongo-driver/mongo"
)

type StoreInventoryRepository struct {
	store database.K8sDeploymentInventoryStore
}

func NewStoreInventoryRepository(store database.K8sDeploymentInventoryStore) *StoreInventoryRepository {
	return &StoreInventoryRepository{store: store}
}

func (r *StoreInventoryRepository) Upsert(ctx context.Context, record InventoryRecord) (InventoryRecord, error) {
	record = normalizeInventoryRecord(record)
	if !completeInventoryRecord(record) {
		return InventoryRecord{}, ErrInvalidInventoryRecord
	}
	if record.ID == "" {
		record.ID = inventoryStableID(record)
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	if err := r.store.Upsert(ctx, record.ID, record); err != nil {
		return InventoryRecord{}, err
	}
	return record, nil
}

func (r *StoreInventoryRepository) Find(ctx context.Context, identity ResourceIdentity) (InventoryRecord, error) {
	identity = normalizeIdentity(identity)
	record := inventoryRecordFromIdentity(identity)
	if !completeInventoryRecord(record) {
		return InventoryRecord{}, ErrInvalidInventoryRecord
	}
	var found InventoryRecord
	err := r.store.FindByIdentity(ctx, identity.ClusterID, identity.Namespace, identity.APIVersion, identity.Kind, identity.Name, &found)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return InventoryRecord{}, ErrInventoryRecordNotFound
	}
	if err != nil {
		return InventoryRecord{}, err
	}
	return normalizeInventoryRecord(found), nil
}

func (r *StoreInventoryRepository) List(ctx context.Context, filter InventoryFilter) ([]InventoryRecord, error) {
	var items []InventoryRecord
	if err := r.store.FindAll(ctx, &items); err != nil {
		return nil, err
	}
	return NewMemoryInventoryRepository(items).List(ctx, filter)
}

func (r *StoreInventoryRepository) Remove(ctx context.Context, identity ResourceIdentity) error {
	record := inventoryRecordFromIdentity(identity)
	if !completeInventoryRecord(record) {
		return ErrInvalidInventoryRecord
	}
	err := r.store.Delete(ctx, inventoryStableID(record))
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrInventoryRecordNotFound
	}
	return err
}

func inventoryStableID(record InventoryRecord) string {
	record = normalizeInventoryRecord(record)
	parts := []string{record.ClusterID, record.Namespace, record.APIVersion, strings.ToLower(record.Kind), record.Name}
	return digest("k8s-inventory:" + strings.Join(parts, "\x00"))
}
