package deployment

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type InventoryRepository interface {
	Upsert(ctx context.Context, record InventoryRecord) (InventoryRecord, error)
	Find(ctx context.Context, identity ResourceIdentity) (InventoryRecord, error)
	List(ctx context.Context, filter InventoryFilter) ([]InventoryRecord, error)
}

type MemoryInventoryRepository struct {
	mu      sync.RWMutex
	records map[string]InventoryRecord
}

func NewMemoryInventoryRepository(records []InventoryRecord) *MemoryInventoryRepository {
	repo := &MemoryInventoryRepository{records: map[string]InventoryRecord{}}
	for _, record := range records {
		record = normalizeInventoryRecord(record)
		if record.ID == "" {
			record.ID = primitive.NewObjectID().Hex()
		}
		repo.records[inventoryRecordKey(record)] = record
	}
	return repo
}

func (r *MemoryInventoryRepository) Upsert(_ context.Context, record InventoryRecord) (InventoryRecord, error) {
	record = normalizeInventoryRecord(record)
	if !completeInventoryRecord(record) {
		return InventoryRecord{}, ErrInvalidInventoryRecord
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := inventoryRecordKey(record)
	if existing, ok := r.records[key]; ok {
		record.ID = existing.ID
	} else if record.ID == "" {
		record.ID = primitive.NewObjectID().Hex()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	r.records[key] = record
	return record, nil
}

func (r *MemoryInventoryRepository) Find(_ context.Context, identity ResourceIdentity) (InventoryRecord, error) {
	record := inventoryRecordFromIdentity(identity)
	if !completeInventoryRecord(record) {
		return InventoryRecord{}, ErrInvalidInventoryRecord
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	found, ok := r.records[inventoryRecordKey(record)]
	if !ok {
		return InventoryRecord{}, ErrInventoryRecordNotFound
	}
	return found, nil
}

func (r *MemoryInventoryRepository) List(_ context.Context, filter InventoryFilter) ([]InventoryRecord, error) {
	filter = normalizeInventoryFilter(filter)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]InventoryRecord, 0, len(r.records))
	for _, record := range r.records {
		if filter.ClusterID != "" && record.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && record.Namespace != filter.Namespace {
			continue
		}
		if filter.Kind != "" && !strings.EqualFold(record.Kind, filter.Kind) {
			continue
		}
		if filter.Name != "" && record.Name != filter.Name {
			continue
		}
		out = append(out, record)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return inventoryRecordKey(out[left]) < inventoryRecordKey(out[right])
	})
	return out, nil
}

func normalizeInventoryRecord(record InventoryRecord) InventoryRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.ClusterID = strings.TrimSpace(record.ClusterID)
	record.Namespace = strings.TrimSpace(record.Namespace)
	record.APIVersion = strings.TrimSpace(record.APIVersion)
	record.Kind = strings.TrimSpace(record.Kind)
	record.Name = strings.TrimSpace(record.Name)
	record.FieldManager = strings.TrimSpace(record.FieldManager)
	record.LastApplyHash = strings.TrimSpace(record.LastApplyHash)
	record.LastPreviewID = strings.TrimSpace(record.LastPreviewID)
	return record
}

func normalizeInventoryFilter(filter InventoryFilter) InventoryFilter {
	filter.ClusterID = strings.TrimSpace(filter.ClusterID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	filter.Kind = strings.TrimSpace(filter.Kind)
	filter.Name = strings.TrimSpace(filter.Name)
	return filter
}

func inventoryRecordFromIdentity(identity ResourceIdentity) InventoryRecord {
	identity = normalizeIdentity(identity)
	return InventoryRecord{
		ClusterID:  identity.ClusterID,
		Namespace:  identity.Namespace,
		APIVersion: identity.APIVersion,
		Kind:       identity.Kind,
		Name:       identity.Name,
	}
}

func completeInventoryRecord(record InventoryRecord) bool {
	return record.ClusterID != "" && record.Namespace != "" && record.APIVersion != "" && record.Kind != "" && record.Name != ""
}

func inventoryRecordKey(record InventoryRecord) string {
	return strings.Join([]string{record.ClusterID, record.Namespace, record.APIVersion, strings.ToLower(record.Kind), record.Name}, "\x00")
}
