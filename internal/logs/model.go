package logs

import (
	"time"

	obsruntime "novaapm/internal/observability/runtime"
)

const (
	SourceTypeK8sStdout = "k8s_stdout"
	SourceTypeVMFile    = "vm_file"

	EndpointScopeGlobal     = "global"
	EndpointScopeK8sCluster = "k8s_cluster"
	EndpointScopeVM         = "vm"

	EndpointSinkVL    = "vl"
	EndpointSinkES    = "es"
	EndpointSinkKafka = "kafka"
	EndpointSinkOTel  = "otel"

	EndpointSignalLogs    = "logs"
	EndpointSignalMetrics = "metrics"

	LogTargetSourceExternalVLogs = "external_vlogs"

	LogTargetStatusPendingVerification = "pending_verification"
	LogTargetStatusVerified            = "verified"
	LogTargetStatusDisabled            = "disabled"

	ParseRuleRegex = "regex"
	ParseRuleJSON  = "json"

	VMEndpointStatusPendingProbe = "pending_probe"
	VMEndpointStatusReachable    = "reachable"
	VMEndpointStatusUnreachable  = "unreachable"
)

type LogEndpoint struct {
	ID          string            `json:"id" bson:"_id"`
	Name        string            `json:"name" bson:"name"`
	Description string            `json:"description" bson:"description"`
	Kind        string            `json:"kind,omitempty" bson:"kind,omitempty"`
	SignalTypes []string          `json:"signal_types,omitempty" bson:"signal_types,omitempty"`
	SinkType    string            `json:"sink_type" bson:"sink_type"`
	StreamName  string            `json:"stream_name" bson:"stream_name"`
	WriteURL    string            `json:"write_url" bson:"write_url"`
	QueryURL    string            `json:"query_url" bson:"query_url"`
	VMUIURL     string            `json:"vmui_url" bson:"vmui_url"`
	AccountID   string            `json:"account_id,omitempty" bson:"-"`
	ProjectID   string            `json:"project_id,omitempty" bson:"-"`
	ScopeType   string            `json:"scope_type" bson:"scope_type"`
	ClusterID   string            `json:"cluster_id" bson:"cluster_id"`
	SecretRef   string            `json:"secret_ref,omitempty" bson:"secret_ref,omitempty"`
	Status      string            `json:"status" bson:"status"`
	Health      LogEndpointHealth `json:"health,omitempty" bson:"health,omitempty"`
	CreatedAt   time.Time         `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at" bson:"updated_at"`
}

type LogEndpointHealth struct {
	Status         string    `json:"status" bson:"status"`
	CheckedAt      time.Time `json:"checked_at,omitempty" bson:"checked_at,omitempty"`
	ResponseTimeMS int       `json:"response_time_ms,omitempty" bson:"response_time_ms,omitempty"`
	Message        string    `json:"message,omitempty" bson:"message,omitempty"`
}

type LogSource struct {
	ID                     string            `json:"id" bson:"_id"`
	SourceType             string            `json:"source_type" bson:"source_type"`
	ClusterID              string            `json:"cluster_id" bson:"cluster_id"`
	Namespace              string            `json:"namespace" bson:"namespace"`
	AgentNamespace         string            `json:"agent_namespace" bson:"agent_namespace"`
	WorkloadKind           string            `json:"workload_kind" bson:"workload_kind"`
	WorkloadName           string            `json:"workload_name" bson:"workload_name"`
	HostGroup              string            `json:"host_group" bson:"host_group"`
	HostSelector           map[string]string `json:"host_selector" bson:"host_selector"`
	PathPattern            string            `json:"path_pattern" bson:"path_pattern"`
	ParseRules             []LogParseRule    `json:"parse_rules" bson:"parse_rules"`
	OperatorsYAML          string            `json:"operators_yaml" bson:"operators_yaml"`
	CollectorFragmentYAML  string            `json:"collector_fragment_yaml" bson:"collector_fragment_yaml"`
	CustomCollectorYAML    string            `json:"custom_collector_yaml" bson:"custom_collector_yaml"`
	CollectorConfigHash    string            `json:"collector_config_hash" bson:"collector_config_hash"`
	DeploymentManifestHash string            `json:"deployment_manifest_hash" bson:"deployment_manifest_hash"`
	CreatedAt              time.Time         `json:"created_at" bson:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at" bson:"updated_at"`
}

type LogCollectorConfigVersion struct {
	ID                  string                   `json:"id" bson:"_id"`
	CollectorConfigHash string                   `json:"collector_config_hash" bson:"collector_config_hash"`
	SourceType          string                   `json:"source_type" bson:"source_type"`
	ClusterID           string                   `json:"cluster_id" bson:"cluster_id"`
	AgentNamespace      string                   `json:"agent_namespace" bson:"agent_namespace"`
	CollectorYAML       string                   `json:"collector_yaml" bson:"collector_yaml"`
	ConfigFiles         map[string]string        `json:"config_files" bson:"config_files"`
	ConfigFileRefs      []LogCollectorConfigFile `json:"config_file_refs" bson:"config_file_refs"`
	RouteIDs            []string                 `json:"route_ids" bson:"route_ids"`
	CreatedAt           time.Time                `json:"created_at" bson:"created_at"`
}

type LogCollectorConfigFile struct {
	Path          string `json:"path" bson:"path"`
	ConfigMapName string `json:"config_map_name" bson:"config_map_name"`
	Role          string `json:"role" bson:"role"`
	RouteID       string `json:"route_id,omitempty" bson:"route_id,omitempty"`
	ServiceID     string `json:"service_id,omitempty" bson:"service_id,omitempty"`
}

type LogDeploymentManifestVersion struct {
	ID                     string    `json:"id" bson:"_id"`
	DeploymentManifestHash string    `json:"deployment_manifest_hash" bson:"deployment_manifest_hash"`
	SourceType             string    `json:"source_type" bson:"source_type"`
	ClusterID              string    `json:"cluster_id" bson:"cluster_id"`
	AgentNamespace         string    `json:"agent_namespace" bson:"agent_namespace"`
	ManifestYAML           string    `json:"manifest_yaml" bson:"manifest_yaml"`
	CreatedAt              time.Time `json:"created_at" bson:"created_at"`
}

type LogParseRule struct {
	ID       string `json:"id" bson:"id"`
	Name     string `json:"name" bson:"name"`
	RuleType string `json:"rule_type" bson:"rule_type"`
	Pattern  string `json:"pattern" bson:"pattern"`
	Enabled  bool   `json:"enabled" bson:"enabled"`
}

type ParsePreviewRequest struct {
	Sample     string         `json:"sample"`
	ParseRules []LogParseRule `json:"parse_rules"`
}

type ParsePreviewResult struct {
	Status   string         `json:"status"`
	Fields   map[string]any `json:"fields"`
	Warnings []string       `json:"warnings"`
	Errors   []string       `json:"errors"`
}

type LogRoute struct {
	ID                  string          `json:"id" bson:"_id"`
	Name                string          `json:"name" bson:"name"`
	ServiceID           string          `json:"service_id" bson:"service_id"`
	SourceID            string          `json:"source_id" bson:"source_id"`
	SourceType          string          `json:"source_type" bson:"source_type"`
	K8s                 *K8sRouteConfig `json:"k8s,omitempty" bson:"k8s,omitempty"`
	AgentGroupID        string          `json:"agent_group_id" bson:"agent_group_id"`
	EndpointID          string          `json:"endpoint_id" bson:"endpoint_id"`
	Status              string          `json:"status" bson:"status"`
	CollectorConfigHash string          `json:"collector_config_hash" bson:"collector_config_hash"`
	LastProbeStatus     string          `json:"last_probe_status" bson:"last_probe_status"`
	LastProbeMessage    string          `json:"last_probe_message" bson:"last_probe_message"`
	LastProbeAt         *time.Time      `json:"last_probe_at,omitempty" bson:"last_probe_at,omitempty"`
	LastPublishStatus   string          `json:"last_publish_status" bson:"last_publish_status"`
	LastPublishMessage  string          `json:"last_publish_message" bson:"last_publish_message"`
	LastPublishedAt     *time.Time      `json:"last_published_at,omitempty" bson:"last_published_at,omitempty"`
	LastAuditID         string          `json:"last_audit_id" bson:"last_audit_id"`
	LastPreviewID       string          `json:"last_preview_id" bson:"last_preview_id"`
	CreatedAt           time.Time       `json:"created_at" bson:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" bson:"updated_at"`
}

type LogTarget struct {
	ID               string     `json:"id" bson:"_id"`
	Name             string     `json:"name" bson:"name"`
	ServiceID        string     `json:"service_id" bson:"service_id"`
	EndpointID       string     `json:"endpoint_id" bson:"endpoint_id"`
	SourceKind       string     `json:"source_kind" bson:"source_kind"`
	LogRouteID       string     `json:"log_route_id,omitempty" bson:"log_route_id,omitempty"`
	BaseFilter       string     `json:"base_filter" bson:"base_filter"`
	AccountID        string     `json:"account_id,omitempty" bson:"account_id,omitempty"`
	ProjectID        string     `json:"project_id,omitempty" bson:"project_id,omitempty"`
	Status           string     `json:"status" bson:"status"`
	LastProbeStatus  string     `json:"last_probe_status" bson:"last_probe_status"`
	LastProbeMessage string     `json:"last_probe_message" bson:"last_probe_message"`
	LastProbeAt      *time.Time `json:"last_probe_at,omitempty" bson:"last_probe_at,omitempty"`
	LastSeenLogAt    *time.Time `json:"last_seen_log_at,omitempty" bson:"last_seen_log_at,omitempty"`
	CreatedBy        ActorRef   `json:"created_by" bson:"created_by"`
	UpdatedBy        ActorRef   `json:"updated_by" bson:"updated_by"`
	CreatedAt        time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" bson:"updated_at"`
}

type ActorRef struct {
	ID   string `json:"id" bson:"id"`
	Type string `json:"type" bson:"type"`
	Name string `json:"name" bson:"name"`
}

type VMLogAgentEndpoint struct {
	ID                 string     `json:"id" bson:"_id"`
	RouteID            string     `json:"route_id" bson:"route_id"`
	ServiceID          string     `json:"service_id" bson:"service_id"`
	Name               string     `json:"name" bson:"name"`
	Address            string     `json:"address" bson:"address"`
	Status             string     `json:"status" bson:"status"`
	LastProbeAt        *time.Time `json:"last_probe_at,omitempty" bson:"last_probe_at,omitempty"`
	LastProbeStatus    string     `json:"last_probe_status,omitempty" bson:"last_probe_status,omitempty"`
	LastProbeMessage   string     `json:"last_probe_message,omitempty" bson:"last_probe_message,omitempty"`
	LastProbeLatencyMS int64      `json:"last_probe_latency_ms,omitempty" bson:"last_probe_latency_ms,omitempty"`
	CreatedBy          ActorRef   `json:"created_by" bson:"created_by"`
	UpdatedBy          ActorRef   `json:"updated_by" bson:"updated_by"`
	CreatedAt          time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" bson:"updated_at"`
}

type UpsertVMEndpointRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type VMInstallationArtifact struct {
	RouteID              string   `json:"route_id"`
	ServiceID            string   `json:"service_id"`
	CollectorYAML        string   `json:"collector_yaml"`
	CollectorConfigHash  string   `json:"collector_config_hash"`
	InstallScript        string   `json:"install_script"`
	HealthAddressExample string   `json:"health_address_example"`
	Prerequisites        []string `json:"prerequisites"`
}

type K8sSourceInput struct {
	ClusterID             string         `json:"cluster_id"`
	Namespace             string         `json:"namespace"`
	AgentNamespace        string         `json:"agent_namespace"`
	WorkloadKind          string         `json:"workload_kind"`
	WorkloadName          string         `json:"workload_name"`
	PathPattern           string         `json:"path_pattern"`
	ParseRules            []LogParseRule `json:"parse_rules"`
	OperatorsYAML         string         `json:"operators_yaml"`
	CollectorFragmentYAML string         `json:"collector_fragment_yaml"`
}

type VMSourceInput struct {
	HostGroup     string            `json:"host_group"`
	HostSelector  map[string]string `json:"host_selector"`
	PathPattern   string            `json:"path_pattern"`
	ParseRules    []LogParseRule    `json:"parse_rules"`
	CollectorYAML string            `json:"collector_yaml"`
}

type K8sRouteConfig struct {
	Namespace             string         `json:"namespace" bson:"namespace"`
	WorkloadKind          string         `json:"workload_kind" bson:"workload_kind"`
	WorkloadName          string         `json:"workload_name" bson:"workload_name"`
	PathPattern           string         `json:"path_pattern" bson:"path_pattern"`
	ParseRules            []LogParseRule `json:"parse_rules" bson:"parse_rules"`
	OperatorsYAML         string         `json:"operators_yaml" bson:"operators_yaml"`
	CollectorFragmentYAML string         `json:"collector_fragment_yaml" bson:"collector_fragment_yaml"`
}

type LogCollectorClusterConfig struct {
	ID             string    `json:"id" bson:"_id"`
	ClusterID      string    `json:"cluster_id" bson:"cluster_id"`
	AgentNamespace string    `json:"agent_namespace" bson:"agent_namespace"`
	ProcessorPatch string    `json:"processor_patch" bson:"processor_patch"`
	UpdatedAt      time.Time `json:"updated_at" bson:"updated_at"`
}

type SyncK8sNamespaceRequest struct {
	ProductID     string `json:"product_id"`
	ClusterID     string `json:"cluster_id"`
	Namespace     string `json:"namespace"`
	EnvironmentID string `json:"environment_id"`
	OwnerTeam     string `json:"owner_team"`
	WorkloadKind  string `json:"workload_kind"`
}

type SyncedK8sService struct {
	Service  ServiceSummary `json:"service"`
	Workload Workload       `json:"workload"`
	TargetID string         `json:"target_id"`
	Created  bool           `json:"created"`
}

type SyncK8sNamespaceResult struct {
	Services []SyncedK8sService `json:"services"`
	Total    int                `json:"total"`
}

type UpsertRouteRequest struct {
	RouteID      string         `json:"route_id"`
	Name         string         `json:"name"`
	ServiceID    string         `json:"service_id"`
	SourceType   string         `json:"source_type"`
	AgentGroupID string         `json:"agent_group_id"`
	EndpointID   string         `json:"endpoint_id"`
	K8s          K8sSourceInput `json:"k8s"`
	VM           VMSourceInput  `json:"vm"`
}

type PublishRouteRequest struct {
	PreviewID         string `json:"preview_id"`
	ConfirmationToken string `json:"confirmation_token"`
}

type K8sCollectorRuntimePublishRequest struct {
	ClusterID         string   `json:"cluster_id"`
	Namespace         string   `json:"namespace"`
	TaskType          string   `json:"task_type"`
	RouteIDs          []string `json:"route_ids,omitempty"`
	PreviewID         string   `json:"preview_id,omitempty"`
	ConfirmationToken string   `json:"confirmation_token,omitempty"`
}

type K8sCollectorRuntimeStatusRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
}

type K8sCollectorRuntimeStatus struct {
	ClusterID        string                              `json:"cluster_id"`
	Namespace        string                              `json:"namespace"`
	Ready            bool                                `json:"ready"`
	Status           string                              `json:"status"`
	Message          string                              `json:"message"`
	Runtime          *obsruntime.Runtime                 `json:"runtime,omitempty"`
	Resources        []K8sCollectorRuntimeResourceStatus `json:"resources"`
	MissingResources []K8sCollectorRuntimeResourceStatus `json:"missing_resources"`
}

type K8sCollectorRuntimeResourceStatus struct {
	ClusterID  string `json:"cluster_id"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Exists     bool   `json:"exists"`
}

type K8sCollectorRuntimePublishResult struct {
	Runtime              obsruntime.Runtime `json:"runtime"`
	TaskType             string             `json:"task_type"`
	ManifestYAML         string             `json:"manifest_yaml"`
	CollectorYAML        string             `json:"collector_yaml"`
	CollectorConfigFiles map[string]string  `json:"collector_config_files"`
	CollectorConfigHash  string             `json:"collector_config_hash"`
	ManifestHash         string             `json:"manifest_hash"`
	ChangedConfigMaps    []string           `json:"changed_config_maps"`
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

type CreateLogTargetRequest struct {
	Name       string `json:"name"`
	ServiceID  string `json:"service_id"`
	EndpointID string `json:"endpoint_id"`
	SourceKind string `json:"source_kind"`
	BaseFilter string `json:"base_filter"`
	AccountID  string `json:"account_id"`
	ProjectID  string `json:"project_id"`
}

type UpdateLogTargetRequest struct {
	Name       string  `json:"name"`
	EndpointID string  `json:"endpoint_id"`
	BaseFilter string  `json:"base_filter"`
	AccountID  *string `json:"account_id"`
	ProjectID  *string `json:"project_id"`
	Status     string  `json:"status"`
}

type Workspace struct {
	Services        []ServiceSummary    `json:"services"`
	CollectorGroups []AgentGroupSummary `json:"collector_groups"`
	Clusters        []ClusterSummary    `json:"clusters"`
	Endpoints       []LogEndpoint       `json:"endpoints"`
	Routes          []LogRouteView      `json:"routes"`
	Targets         []LogTargetView     `json:"targets"`
}

type ServiceSummary struct {
	ID            string `json:"id"`
	ProductID     string `json:"product_id"`
	AccountID     string `json:"account_id"`
	ProjectID     string `json:"project_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	EnvironmentID string `json:"environment_id"`
	Cluster       string `json:"cluster"`
	Namespace     string `json:"namespace"`
	OwnerTeam     string `json:"owner_team"`
	IdentityType  string `json:"identity_type"`
	ServiceType   string `json:"service_type"`
	Source        string `json:"source"`
	SyncStatus    string `json:"sync_status"`
}

type AgentGroupSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	Mode            string `json:"mode"`
	EnvironmentID   string `json:"environment_id"`
	Cluster         string `json:"cluster"`
	Namespace       string `json:"namespace"`
	Status          string `json:"status"`
	OnlineInstances int    `json:"online_instances"`
}

type ClusterSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Region     string `json:"region"`
	Status     string `json:"status"`
	AccessMode string `json:"access_mode"`
	ReadOnly   bool   `json:"read_only"`
}

type LogTargetView struct {
	Target   LogTarget       `json:"target"`
	Service  *ServiceSummary `json:"service,omitempty"`
	Endpoint *LogEndpoint    `json:"endpoint,omitempty"`
}

type Workload struct {
	ClusterID       string            `json:"cluster_id"`
	Namespace       string            `json:"namespace"`
	GroupKey        string            `json:"group_key"`
	GroupName       string            `json:"group_name"`
	Key             string            `json:"key"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Selector        map[string]string `json:"selector"`
	TemplateLabels  map[string]string `json:"template_labels"`
	ServiceAccounts []string          `json:"service_accounts"`
	PodsTotal       uint64            `json:"pods_total"`
	PodsRunning     uint64            `json:"pods_running"`
	RestartCount    int32             `json:"restart_count"`
}

type LogRouteView struct {
	Route    LogRoute     `json:"route"`
	Source   *LogSource   `json:"source,omitempty"`
	Endpoint *LogEndpoint `json:"endpoint,omitempty"`
}

type LogRoutePreview struct {
	Source                 LogSource         `json:"source"`
	Endpoint               LogEndpoint       `json:"endpoint"`
	AgentYAML              string            `json:"agent_yaml"`
	CollectorYAML          string            `json:"collector_yaml"`
	CollectorConfigFiles   map[string]string `json:"collector_config_files"`
	ServiceConfigPath      string            `json:"service_config_path"`
	ServiceConfigMapName   string            `json:"service_config_map_name"`
	ServiceConfigYAML      string            `json:"service_config_yaml"`
	CollectorConfigHash    string            `json:"collector_config_hash"`
	DeploymentManifestHash string            `json:"deployment_manifest_hash"`
	Mode                   string            `json:"mode"`
	PublishBlocked         bool              `json:"publish_blocked"`
	PublishBlockedReason   string            `json:"publish_blocked_reason"`
	Warnings               []string          `json:"warnings"`
}

type LogRouteCollectorConfig struct {
	RouteID                string            `json:"route_id"`
	CollectorConfigHash    string            `json:"collector_config_hash"`
	DeploymentManifestHash string            `json:"deployment_manifest_hash"`
	SourceType             string            `json:"source_type"`
	CollectorYAML          string            `json:"collector_yaml"`
	CollectorConfigFiles   map[string]string `json:"collector_config_files"`
	ServiceConfigPath      string            `json:"service_config_path"`
	ServiceConfigMapName   string            `json:"service_config_map_name"`
	ServiceConfigYAML      string            `json:"service_config_yaml"`
}

type PublishRouteResult struct {
	Route                LogRoute `json:"route"`
	Status               string   `json:"status"`
	Message              string   `json:"message"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	PreviewID            string   `json:"preview_id,omitempty"`
	ConfirmationToken    string   `json:"confirmation_token,omitempty"`
	AuditID              string   `json:"audit_id,omitempty"`
	Warnings             []string `json:"warnings"`
}

type ProbeResult struct {
	RouteID   string   `json:"route_id"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	CheckedAt string   `json:"checked_at"`
	Warnings  []string `json:"warnings"`
}
