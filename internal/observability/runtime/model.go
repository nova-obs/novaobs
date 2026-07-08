package obsruntime

import "time"

const (
	KindLogsCollector  = "logs_collector"
	KindLogsVmalert    = "logs_vmalert"
	KindMetricsVmalert = "metrics_vmalert"

	SignalLogs    = "logs"
	SignalMetrics = "metrics"

	StatusPendingPublish = "pending_publish"
	StatusPreviewed      = "previewed"
	StatusReady          = "ready"
	StatusFailed         = "failed"
)

type Runtime struct {
	ID                  string        `json:"id" bson:"_id"`
	Kind                string        `json:"kind" bson:"kind"`
	SignalType          string        `json:"signal_type" bson:"signal_type"`
	ClusterID           string        `json:"cluster_id" bson:"cluster_id"`
	Namespace           string        `json:"namespace" bson:"namespace"`
	EndpointID          string        `json:"endpoint_id,omitempty" bson:"endpoint_id,omitempty"`
	CollectorConfigHash string        `json:"collector_config_hash,omitempty" bson:"collector_config_hash,omitempty"`
	ArtifactHash        string        `json:"artifact_hash,omitempty" bson:"artifact_hash,omitempty"`
	ManifestHash        string        `json:"manifest_hash,omitempty" bson:"manifest_hash,omitempty"`
	Status              string        `json:"status" bson:"status"`
	LastPreviewID       string        `json:"last_preview_id,omitempty" bson:"last_preview_id,omitempty"`
	LastAuditID         string        `json:"last_audit_id,omitempty" bson:"last_audit_id,omitempty"`
	LastError           string        `json:"last_error,omitempty" bson:"last_error,omitempty"`
	LastPublishedAt     *time.Time    `json:"last_published_at,omitempty" bson:"last_published_at,omitempty"`
	Resources           []ResourceRef `json:"resources,omitempty" bson:"resources,omitempty"`
	CreatedAt           time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at" bson:"updated_at"`
}

type ResourceRef struct {
	APIVersion string `json:"api_version" bson:"api_version"`
	Kind       string `json:"kind" bson:"kind"`
	Namespace  string `json:"namespace,omitempty" bson:"namespace,omitempty"`
	Name       string `json:"name" bson:"name"`
}
