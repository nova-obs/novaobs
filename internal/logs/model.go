package logs

import "time"

const (
	SourceTypeK8sStdout = "k8s_stdout"
	SourceTypeVMFile    = "vm_file"

	EndpointScopeGlobal     = "global"
	EndpointScopeK8sCluster = "k8s_cluster"
	EndpointScopeVM         = "vm"

	EndpointSinkVL    = "vl"
	EndpointSinkES    = "es"
	EndpointSinkKafka = "kafka"

	ParseRuleRegex = "regex"
	ParseRuleJSON  = "json"
)

type LogEndpoint struct {
	ID          string    `json:"id" bson:"_id"`
	Name        string    `json:"name" bson:"name"`
	Description string    `json:"description" bson:"description"`
	SinkType    string    `json:"sink_type" bson:"sink_type"`
	StreamName  string    `json:"stream_name" bson:"stream_name"`
	WriteURL    string    `json:"write_url" bson:"write_url"`
	QueryURL    string    `json:"query_url" bson:"query_url"`
	VMUIURL     string    `json:"vmui_url" bson:"vmui_url"`
	SecretRef   string    `json:"secret_ref" bson:"secret_ref"`
	ScopeType   string    `json:"scope_type" bson:"scope_type"`
	ClusterID   string    `json:"cluster_id" bson:"cluster_id"`
	Status      string    `json:"status" bson:"status"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

type LogSource struct {
	ID               string            `json:"id" bson:"_id"`
	SourceType       string            `json:"source_type" bson:"source_type"`
	ClusterID        string            `json:"cluster_id" bson:"cluster_id"`
	Namespace        string            `json:"namespace" bson:"namespace"`
	AgentNamespace   string            `json:"agent_namespace" bson:"agent_namespace"`
	WorkloadKind     string            `json:"workload_kind" bson:"workload_kind"`
	WorkloadName     string            `json:"workload_name" bson:"workload_name"`
	Container        string            `json:"container" bson:"container"`
	WorkloadSelector map[string]string `json:"workload_selector" bson:"workload_selector"`
	RuntimeLogPaths  []string          `json:"runtime_log_paths" bson:"runtime_log_paths"`
	HostGroup        string            `json:"host_group" bson:"host_group"`
	HostSelector     map[string]string `json:"host_selector" bson:"host_selector"`
	PathPattern      string            `json:"path_pattern" bson:"path_pattern"`
	ParseRules       []LogParseRule    `json:"parse_rules" bson:"parse_rules"`
	CollectorYAML    string            `json:"collector_yaml" bson:"collector_yaml"`
	CreatedAt        time.Time         `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at" bson:"updated_at"`
}

type LogParseRule struct {
	ID       string            `json:"id" bson:"id"`
	Name     string            `json:"name" bson:"name"`
	RuleType string            `json:"rule_type" bson:"rule_type"`
	Pattern  string            `json:"pattern" bson:"pattern"`
	Fields   map[string]string `json:"fields" bson:"fields"`
	Enabled  bool              `json:"enabled" bson:"enabled"`
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
	ID                 string     `json:"id" bson:"_id"`
	Name               string     `json:"name" bson:"name"`
	ServiceID          string     `json:"service_id" bson:"service_id"`
	SourceID           string     `json:"source_id" bson:"source_id"`
	SourceType         string     `json:"source_type" bson:"source_type"`
	AgentGroupID       string     `json:"agent_group_id" bson:"agent_group_id"`
	EndpointID         string     `json:"endpoint_id" bson:"endpoint_id"`
	Status             string     `json:"status" bson:"status"`
	ConfigHash         string     `json:"config_hash" bson:"config_hash"`
	LastProbeStatus    string     `json:"last_probe_status" bson:"last_probe_status"`
	LastProbeMessage   string     `json:"last_probe_message" bson:"last_probe_message"`
	LastProbeAt        *time.Time `json:"last_probe_at,omitempty" bson:"last_probe_at,omitempty"`
	LastPublishStatus  string     `json:"last_publish_status" bson:"last_publish_status"`
	LastPublishMessage string     `json:"last_publish_message" bson:"last_publish_message"`
	LastPublishedAt    *time.Time `json:"last_published_at,omitempty" bson:"last_published_at,omitempty"`
	LastAuditID        string     `json:"last_audit_id" bson:"last_audit_id"`
	LastPreviewID      string     `json:"last_preview_id" bson:"last_preview_id"`
	CreatedAt          time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" bson:"updated_at"`
}

type LogAgentPlan struct {
	ID                string    `json:"id" bson:"_id"`
	RouteID           string    `json:"route_id" bson:"route_id"`
	AgentGroupID      string    `json:"agent_group_id" bson:"agent_group_id"`
	SourceType        string    `json:"source_type" bson:"source_type"`
	ClusterID         string    `json:"cluster_id" bson:"cluster_id"`
	Namespace         string    `json:"namespace" bson:"namespace"`
	ConfigHash        string    `json:"config_hash" bson:"config_hash"`
	RenderedYAML      string    `json:"rendered_yaml" bson:"rendered_yaml"`
	Status            string    `json:"status" bson:"status"`
	PreviewID         string    `json:"preview_id" bson:"preview_id"`
	ConfirmationToken string    `json:"confirmation_token" bson:"confirmation_token"`
	AuditID           string    `json:"audit_id" bson:"audit_id"`
	Message           string    `json:"message" bson:"message"`
	CreatedAt         time.Time `json:"created_at" bson:"created_at"`
}

type K8sSourceInput struct {
	ClusterID        string            `json:"cluster_id"`
	Namespace        string            `json:"namespace"`
	AgentNamespace   string            `json:"agent_namespace"`
	WorkloadKind     string            `json:"workload_kind"`
	WorkloadName     string            `json:"workload_name"`
	Container        string            `json:"container"`
	WorkloadSelector map[string]string `json:"workload_selector"`
	RuntimeLogPaths  []string          `json:"runtime_log_paths"`
	PathPattern      string            `json:"path_pattern"`
	ParseRules       []LogParseRule    `json:"parse_rules"`
	CollectorYAML    string            `json:"collector_yaml"`
}

type VMSourceInput struct {
	HostGroup     string            `json:"host_group"`
	HostSelector  map[string]string `json:"host_selector"`
	PathPattern   string            `json:"path_pattern"`
	ParseRules    []LogParseRule    `json:"parse_rules"`
	CollectorYAML string            `json:"collector_yaml"`
}

type SyncK8sNamespaceRequest struct {
	ClusterID    string `json:"cluster_id"`
	Namespace    string `json:"namespace"`
	Environment  string `json:"environment"`
	OwnerTeam    string `json:"owner_team"`
	WorkloadKind string `json:"workload_kind"`
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

type Workspace struct {
	Services        []ServiceSummary    `json:"services"`
	CollectorGroups []AgentGroupSummary `json:"collector_groups"`
	Clusters        []ClusterSummary    `json:"clusters"`
	Endpoints       []LogEndpoint       `json:"endpoints"`
	Routes          []LogRouteView      `json:"routes"`
}

type ServiceSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Environment  string `json:"environment"`
	Cluster      string `json:"cluster"`
	Namespace    string `json:"namespace"`
	OwnerTeam    string `json:"owner_team"`
	IdentityType string `json:"identity_type"`
	ServiceType  string `json:"service_type"`
	Source       string `json:"source"`
	SyncStatus   string `json:"sync_status"`
}

type AgentGroupSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	Mode            string `json:"mode"`
	Environment     string `json:"environment"`
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
	Source               LogSource   `json:"source"`
	Endpoint             LogEndpoint `json:"endpoint"`
	AgentYAML            string      `json:"agent_yaml"`
	ConfigHash           string      `json:"config_hash"`
	Mode                 string      `json:"mode"`
	PublishBlocked       bool        `json:"publish_blocked"`
	PublishBlockedReason string      `json:"publish_blocked_reason"`
	Warnings             []string    `json:"warnings"`
}

type PublishRouteResult struct {
	Route                LogRoute     `json:"route"`
	Plan                 LogAgentPlan `json:"plan"`
	Status               string       `json:"status"`
	Message              string       `json:"message"`
	RequiresConfirmation bool         `json:"requires_confirmation"`
	PreviewID            string       `json:"preview_id,omitempty"`
	ConfirmationToken    string       `json:"confirmation_token,omitempty"`
	AuditID              string       `json:"audit_id,omitempty"`
	Resources            any          `json:"resources,omitempty"`
	Diffs                any          `json:"diffs,omitempty"`
	Warnings             []string     `json:"warnings"`
}

type ProbeResult struct {
	RouteID   string   `json:"route_id"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	CheckedAt string   `json:"checked_at"`
	Warnings  []string `json:"warnings"`
}
