package metrics

import (
	"time"

	obsendpoint "novaapm/internal/observability/endpoint"
	obsruntime "novaapm/internal/observability/runtime"
	"novaapm/internal/servicecatalog"
)

const (
	MetricRouteSourceK8sService = "k8s_service"

	MetricRouteStatusActive   = "active"
	MetricRouteStatusDisabled = "disabled"

	RoutePublishStatusPending   = "pending_publish"
	RoutePublishStatusPreviewed = "previewed"
	RoutePublishStatusApplied   = "applied"
	RoutePublishStatusFailed    = "failed"
)

type MetricRoute struct {
	ID                 string            `json:"id" bson:"_id"`
	Name               string            `json:"name" bson:"name"`
	ProductID          string            `json:"product_id" bson:"product_id"`
	ServiceID          string            `json:"service_id" bson:"service_id"`
	EndpointID         string            `json:"endpoint_id" bson:"endpoint_id"`
	SourceKind         string            `json:"source_kind" bson:"source_kind"`
	ClusterID          string            `json:"cluster_id" bson:"cluster_id"`
	Namespace          string            `json:"namespace" bson:"namespace"`
	K8sServiceName     string            `json:"k8s_service_name" bson:"k8s_service_name"`
	Port               string            `json:"port" bson:"port"`
	Scheme             string            `json:"scheme" bson:"scheme"`
	MetricsPath        string            `json:"metrics_path" bson:"metrics_path"`
	ScrapeInterval     string            `json:"scrape_interval" bson:"scrape_interval"`
	ScrapeTimeout      string            `json:"scrape_timeout" bson:"scrape_timeout"`
	LabelMatch         map[string]string `json:"label_match" bson:"label_match"`
	BasePromQL         string            `json:"base_promql" bson:"base_promql"`
	Status             string            `json:"status" bson:"status"`
	DesiredConfigHash  string            `json:"desired_config_hash,omitempty" bson:"desired_config_hash,omitempty"`
	AppliedConfigHash  string            `json:"applied_config_hash,omitempty" bson:"applied_config_hash,omitempty"`
	LastPublishStatus  string            `json:"last_publish_status" bson:"last_publish_status"`
	LastPublishMessage string            `json:"last_publish_message" bson:"last_publish_message"`
	LastPreviewID      string            `json:"last_preview_id,omitempty" bson:"last_preview_id,omitempty"`
	LastAuditID        string            `json:"last_audit_id,omitempty" bson:"last_audit_id,omitempty"`
	LastPublishedAt    *time.Time        `json:"last_published_at,omitempty" bson:"last_published_at,omitempty"`
	CreatedBy          ActorRef          `json:"created_by" bson:"created_by"`
	UpdatedBy          ActorRef          `json:"updated_by" bson:"updated_by"`
	CreatedAt          time.Time         `json:"created_at" bson:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at" bson:"updated_at"`
}

type MetricRouteView struct {
	Route     MetricRoute             `json:"route"`
	Service   *servicecatalog.Service `json:"service,omitempty"`
	Endpoint  *obsendpoint.Endpoint   `json:"endpoint,omitempty"`
	RuntimeID string                  `json:"runtime_id"`
}

type CreateRouteRequest struct {
	ServiceID      string `json:"service_id"`
	Name           string `json:"name"`
	EndpointID     string `json:"endpoint_id"`
	ClusterID      string `json:"cluster_id"`
	Namespace      string `json:"namespace"`
	K8sServiceName string `json:"k8s_service_name"`
	Port           string `json:"port"`
	Scheme         string `json:"scheme"`
	MetricsPath    string `json:"metrics_path"`
	ScrapeInterval string `json:"scrape_interval"`
	ScrapeTimeout  string `json:"scrape_timeout"`
	Status         string `json:"status"`
}

type UpdateRouteRequest struct {
	Name           *string `json:"name"`
	EndpointID     *string `json:"endpoint_id"`
	ClusterID      *string `json:"cluster_id"`
	Namespace      *string `json:"namespace"`
	K8sServiceName *string `json:"k8s_service_name"`
	Port           *string `json:"port"`
	Scheme         *string `json:"scheme"`
	MetricsPath    *string `json:"metrics_path"`
	ScrapeInterval *string `json:"scrape_interval"`
	ScrapeTimeout  *string `json:"scrape_timeout"`
	Status         *string `json:"status"`
}

type CollectorRuntimePublishRequest struct {
	RouteID           string `json:"route_id"`
	Namespace         string `json:"namespace"`
	PreviewID         string `json:"preview_id,omitempty"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
}

type CollectorRuntimePublishResult struct {
	Runtime              obsruntime.Runtime `json:"runtime"`
	RouteIDs             []string           `json:"route_ids"`
	ManifestYAML         string             `json:"manifest_yaml"`
	ConfigYAML           string             `json:"config_yaml"`
	ConfigHash           string             `json:"config_hash"`
	ManifestHash         string             `json:"manifest_hash"`
	Status               string             `json:"status"`
	Message              string             `json:"message"`
	RequiresConfirmation bool               `json:"requires_confirmation"`
	PreviewID            string             `json:"preview_id,omitempty"`
	ConfirmationToken    string             `json:"confirmation_token,omitempty"`
	AuditID              string             `json:"audit_id,omitempty"`
	Resources            any                `json:"resources,omitempty"`
	Diffs                any                `json:"diffs,omitempty"`
	Warnings             []string           `json:"warnings"`
}

type CollectorRuntimeResourceStatus struct {
	ClusterID  string `json:"cluster_id"`
	Namespace  string `json:"namespace,omitempty"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Exists     bool   `json:"exists"`
	Healthy    bool   `json:"healthy"`
}

type CollectorRuntimeStatus struct {
	Runtime          *obsruntime.Runtime              `json:"runtime,omitempty"`
	RuntimeID        string                           `json:"runtime_id"`
	ClusterID        string                           `json:"cluster_id"`
	Namespace        string                           `json:"namespace"`
	RouteIDs         []string                         `json:"route_ids"`
	Ready            bool                             `json:"ready"`
	Status           string                           `json:"status"`
	Message          string                           `json:"message"`
	Resources        []CollectorRuntimeResourceStatus `json:"resources"`
	MissingResources []CollectorRuntimeResourceStatus `json:"missing_resources"`
}
