package dashboard

import "time"

type HealthStatus string

const (
	HealthHealthy HealthStatus = "healthy"
	HealthWarning HealthStatus = "warning"
	HealthUnknown HealthStatus = "unknown"
	HealthFailed  HealthStatus = "failed"
)

type SyncStatus string

const (
	SyncApplied SyncStatus = "applied"
	SyncPending SyncStatus = "pending"
	SyncUnknown SyncStatus = "unknown"
	SyncFailed  SyncStatus = "failed"
)

type Query struct {
	ClusterID string
}

type Snapshot struct {
	Stats   Stats     `json:"stats"`
	Signals []Signal  `json:"signals"`
	Sync    SyncState `json:"sync"`
}

type Stats struct {
	ClusterID  string       `json:"cluster_id"`
	Health     HealthStatus `json:"health"`
	Namespaces int          `json:"namespaces"`
	Workloads  int          `json:"workloads"`
	Pods       PodStats     `json:"pods"`
}

type PodStats struct {
	Total   int `json:"total"`
	Ready   int `json:"ready"`
	Warning int `json:"warning"`
}

type Signal struct {
	Key       string       `json:"key"`
	Label     string       `json:"label"`
	Status    HealthStatus `json:"status"`
	Source    string       `json:"source"`
	CheckedAt time.Time    `json:"checked_at"`
}

type SyncState struct {
	Status       SyncStatus `json:"status"`
	Source       string     `json:"source"`
	TimeWindow   string     `json:"time_window"`
	LastSyncedAt time.Time  `json:"last_synced_at"`
}
