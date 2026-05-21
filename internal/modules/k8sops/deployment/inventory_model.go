package deployment

import (
	"errors"
	"time"
)

var (
	ErrInvalidInventoryRecord  = errors.New("invalid_k8s_inventory_record")
	ErrInventoryRecordNotFound = errors.New("k8s_inventory_record_not_found")
)

type InventoryRecord struct {
	ID            string
	ClusterID     string
	Namespace     string
	APIVersion    string
	Kind          string
	Name          string
	FieldManager  string
	LastApplyHash string
	LastPreviewID string
	UpdatedAt     time.Time
}

type InventoryFilter struct {
	ClusterID string
	Namespace string
	Kind      string
	Name      string
}
