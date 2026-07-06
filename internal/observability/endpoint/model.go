package endpoint

import (
	"errors"
	"time"
)

const (
	SignalTypeLogs       = "logs"
	SignalTypeMetrics    = "metrics"
	SignalTypeDashboards = "dashboards"

	KindVictoriaLogs    = "victorialogs"
	KindVictoriaMetrics = "victoriametrics"
	KindGrafana         = "grafana"
	KindOTel            = "otel"
	KindKafka           = "kafka"
	KindElasticsearch   = "elasticsearch"

	HealthStatusUnknown = "unknown"
	HealthStatusPending = "pending"
	HealthStatusFailed  = "failed"
)

var ErrPermissionDenied = errors.New("permission_denied")

type Endpoint struct {
	ID          string         `json:"id" bson:"_id"`
	Name        string         `json:"name" bson:"name"`
	Description string         `json:"description" bson:"description"`
	Kind        string         `json:"kind" bson:"kind"`
	SignalTypes []string       `json:"signal_types" bson:"signal_types"`
	Scope       EndpointScope  `json:"scope" bson:"scope"`
	URLs        EndpointURLs   `json:"urls" bson:"urls"`
	Tenant      EndpointTenant `json:"tenant" bson:"tenant"`
	SecretRef   string         `json:"secret_ref" bson:"secret_ref"`
	Status      string         `json:"status" bson:"status"`
	Health      EndpointHealth `json:"health" bson:"health"`
	CreatedAt   time.Time      `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at" bson:"updated_at"`
}

type EndpointScope struct {
	Type      string `json:"type" bson:"type"`
	ClusterID string `json:"cluster_id,omitempty" bson:"cluster_id,omitempty"`
	Namespace string `json:"namespace,omitempty" bson:"namespace,omitempty"`
}

type EndpointURLs struct {
	WriteURL       string `json:"write_url" bson:"write_url"`
	QueryURL       string `json:"query_url" bson:"query_url"`
	UIURL          string `json:"ui_url" bson:"ui_url"`
	RemoteWriteURL string `json:"remote_write_url" bson:"remote_write_url"`
	BaseURL        string `json:"base_url" bson:"base_url"`
}

type EndpointTenant struct {
	AccountID string `json:"account_id" bson:"account_id"`
	ProjectID string `json:"project_id" bson:"project_id"`
}

type EndpointHealth struct {
	Status         string    `json:"status" bson:"status"`
	CheckedAt      time.Time `json:"checked_at,omitempty" bson:"checked_at,omitempty"`
	ResponseTimeMS int       `json:"response_time_ms,omitempty" bson:"response_time_ms,omitempty"`
	Message        string    `json:"message,omitempty" bson:"message,omitempty"`
}

type ListFilter struct {
	SignalType string
	Kind       string
}

type TestResult struct {
	Status         string    `json:"status"`
	Message        string    `json:"message"`
	ResponseTimeMS int       `json:"response_time_ms"`
	CheckedAt      time.Time `json:"checked_at"`
}
