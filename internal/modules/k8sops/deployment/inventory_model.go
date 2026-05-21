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
	ID            string    `json:"id" bson:"_id,omitempty"`
	ClusterID     string    `json:"cluster_id" bson:"cluster_id"`
	Namespace     string    `json:"namespace" bson:"namespace"`
	APIVersion    string    `json:"api_version" bson:"api_version"`
	Kind          string    `json:"kind" bson:"kind"`
	Name          string    `json:"name" bson:"name"`
	FieldManager  string    `json:"field_manager" bson:"field_manager"`
	LastApplyHash string    `json:"last_apply_hash" bson:"last_apply_hash"`
	LastPreviewID string    `json:"last_preview_id" bson:"last_preview_id"`
	UpdatedAt     time.Time `json:"updated_at" bson:"updated_at"`
}

type InventoryFilter struct {
	ClusterID string
	Namespace string
	Kind      string
	Name      string
}
