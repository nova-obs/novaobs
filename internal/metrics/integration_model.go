package metrics

import "time"

const (
	EnvironmentIdentityLabel = "novaapm_environment_id"

	DesiredStateConnected    = "connected"
	DesiredStateDisconnected = "disconnected"

	SourceKindKubernetesInfra = "kubernetes_infra"
	SourceKindHostInfra       = "host_infra"
	SourceKindLogDerived      = "log_derived"

	CollectionModeExternal = "external_collector"
	CollectionModeManaged  = "managed_collector"
)

type Integration struct {
	ID               string    `json:"id" bson:"_id"`
	EnvironmentID    string    `json:"environment_id" bson:"environment_id"`
	DestinationRef   string    `json:"destination_ref" bson:"destination_ref"`
	DashboardRef     string    `json:"dashboard_ref,omitempty" bson:"dashboard_ref,omitempty"`
	DesiredState     string    `json:"desired_state" bson:"desired_state"`
	IdentityLabelKey string    `json:"identity_label_key" bson:"identity_label_key"`
	CreatedBy        string    `json:"created_by" bson:"created_by"`
	UpdatedBy        string    `json:"updated_by" bson:"updated_by"`
	CreatedAt        time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" bson:"updated_at"`
}

type SourceAccess struct {
	ID                string    `json:"id" bson:"_id"`
	IntegrationID     string    `json:"integration_id" bson:"integration_id"`
	ResourceBindingID string    `json:"resource_binding_id" bson:"resource_binding_id"`
	SourceKind        string    `json:"source_kind" bson:"source_kind"`
	CollectionMode    string    `json:"collection_mode" bson:"collection_mode"`
	DesiredState      string    `json:"desired_state" bson:"desired_state"`
	CreatedBy         string    `json:"created_by" bson:"created_by"`
	UpdatedBy         string    `json:"updated_by" bson:"updated_by"`
	CreatedAt         time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" bson:"updated_at"`
}

type IntegrationView struct {
	Integration       Integration        `json:"integration"`
	SourceAccesses    []SourceAccess     `json:"source_accesses"`
	CollectorReleases []CollectorRelease `json:"collector_releases"`
}

type CreateIntegrationRequest struct {
	EnvironmentID  string `json:"environment_id"`
	DestinationRef string `json:"destination_ref"`
	DashboardRef   string `json:"dashboard_ref"`
}

type UpdateIntegrationRequest struct {
	DestinationRef *string `json:"destination_ref"`
	DashboardRef   *string `json:"dashboard_ref"`
	DesiredState   *string `json:"desired_state"`
}

type UpdateSourceAccessRequest struct {
	CollectionMode string `json:"collection_mode"`
	DesiredState   string `json:"desired_state"`
}

type WriteDestinationOption struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RemoteWriteURL string `json:"remote_write_url"`
	QueryURL       string `json:"query_url"`
	UIURL          string `json:"ui_url"`
}

type DashboardOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	UIURL string `json:"ui_url"`
}

type HandoffArtifact struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Note    string `json:"note"`
}

type SourceHandoff struct {
	SourceAccessID string            `json:"source_access_id"`
	EnvironmentID  string            `json:"environment_id"`
	ResourceRef    string            `json:"resource_ref"`
	DestinationRef string            `json:"destination_ref"`
	Artifacts      []HandoffArtifact `json:"artifacts"`
}

const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthFailed   = "failed"
	HealthUnknown  = "unknown"
)

type HealthLayer struct {
	Status     string    `json:"status" bson:"status"`
	Message    string    `json:"message" bson:"message"`
	ObservedAt time.Time `json:"observed_at" bson:"observed_at"`
}

type SourceHealth struct {
	SourceAccessID string `json:"source_access_id" bson:"source_access_id"`
	SourceKind     string `json:"source_kind" bson:"source_kind"`
	Status         string `json:"status" bson:"status"`
	Message        string `json:"message" bson:"message"`
}

type HealthSnapshot struct {
	ID            string              `json:"id" bson:"_id"`
	IntegrationID string              `json:"integration_id" bson:"integration_id"`
	EnvironmentID string              `json:"environment_id" bson:"environment_id"`
	Configuration HealthLayer         `json:"configuration" bson:"configuration"`
	Destination   HealthLayer         `json:"destination" bson:"destination"`
	DataFlow      HealthLayer         `json:"data_flow" bson:"data_flow"`
	Sources       []SourceHealth      `json:"sources" bson:"sources"`
	Signals       []EnvironmentSignal `json:"signals" bson:"signals"`
	CreatedAt     time.Time           `json:"created_at" bson:"created_at"`
}

type EnvironmentSignal struct {
	Key    string  `json:"key" bson:"key"`
	Label  string  `json:"label" bson:"label"`
	Value  float64 `json:"value" bson:"value"`
	Unit   string  `json:"unit" bson:"unit"`
	Status string  `json:"status" bson:"status"`
}

type OverviewItem struct {
	Integration    Integration     `json:"integration"`
	SourceAccesses []SourceAccess  `json:"source_accesses"`
	LatestSnapshot *HealthSnapshot `json:"latest_snapshot"`
	GrafanaURL     string          `json:"grafana_url,omitempty"`
}

const (
	ReleasePreviewed = "previewed"
	ReleaseApplied   = "applied"
	ReleaseFailed    = "failed"
)

type CollectorRelease struct {
	ID                string            `json:"id" bson:"_id"`
	SourceAccessID    string            `json:"source_access_id" bson:"source_access_id"`
	Generation        int64             `json:"generation" bson:"generation"`
	ClusterID         string            `json:"cluster_id" bson:"cluster_id"`
	Namespace         string            `json:"namespace" bson:"namespace"`
	Image             string            `json:"image" bson:"image"`
	ManifestHash      string            `json:"manifest_hash" bson:"manifest_hash"`
	Status            string            `json:"status" bson:"status"`
	PreviewID         string            `json:"preview_id,omitempty" bson:"preview_id,omitempty"`
	ConfirmationToken string            `json:"confirmation_token,omitempty" bson:"confirmation_token,omitempty"`
	Resources         []ManagedResource `json:"resources" bson:"resources"`
	Message           string            `json:"message" bson:"message"`
	CreatedBy         string            `json:"created_by" bson:"created_by"`
	CreatedAt         time.Time         `json:"created_at" bson:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at" bson:"updated_at"`
}

type ManagedResource struct {
	APIVersion string `json:"api_version" bson:"api_version"`
	Kind       string `json:"kind" bson:"kind"`
	Namespace  string `json:"namespace" bson:"namespace"`
	Name       string `json:"name" bson:"name"`
}

type PreviewCollectorReleaseRequest struct {
	Namespace string `json:"namespace"`
}

type SourceAssessment struct {
	SourceAccessID    string    `json:"source_access_id"`
	ResourceRef       string    `json:"resource_ref"`
	Status            string    `json:"status"`
	DetectedCollector string    `json:"detected_collector"`
	DetectedSignals   []string  `json:"detected_signals"`
	RecommendedMode   string    `json:"recommended_mode"`
	Message           string    `json:"message"`
	AssessedAt        time.Time `json:"assessed_at"`
}
