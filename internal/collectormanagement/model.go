package collectormanagement

import "time"

type CollectorGroup struct {
	ID                           string     `json:"id" bson:"_id"`
	Name                         string     `json:"name" bson:"name"`
	DisplayName                  string     `json:"display_name" bson:"display_name"`
	Description                  string     `json:"description" bson:"description"`
	Mode                         string     `json:"mode" bson:"mode"`
	Environment                  string     `json:"environment" bson:"environment"`
	Cluster                      string     `json:"cluster" bson:"cluster"`
	Namespace                    string     `json:"namespace" bson:"namespace"`
	TenantID                     string     `json:"tenant_id" bson:"tenant_id"`
	OwnerTeam                    string     `json:"owner_team" bson:"owner_team"`
	IsolationLevel               string     `json:"isolation_level" bson:"isolation_level"`
	PlatformTemplateID           string     `json:"platform_template_id" bson:"platform_template_id"`
	Status                       string     `json:"status" bson:"status"`
	ReceiverProfile              string     `json:"receiver_profile" bson:"receiver_profile"`
	ExporterProfile              string     `json:"exporter_profile" bson:"exporter_profile"`
	IngestEndpoint               string     `json:"ingest_endpoint" bson:"ingest_endpoint"`
	DesiredReplicas              int        `json:"desired_replicas" bson:"desired_replicas"`
	MaxServices                  int        `json:"max_services" bson:"max_services"`
	ConfigVersion                int        `json:"config_version" bson:"config_version"`
	DesiredConfigHash            string     `json:"desired_config_hash" bson:"desired_config_hash"`
	LastAppliedConfigHash        string     `json:"last_applied_config_hash" bson:"last_applied_config_hash"`
	LastPublishStatus            string     `json:"last_publish_status" bson:"last_publish_status"`
	LastPublishMessage           string     `json:"last_publish_message" bson:"last_publish_message"`
	LastPublishedAt              *time.Time `json:"last_published_at,omitempty" bson:"last_published_at,omitempty"`
	InstanceCount                int        `json:"instance_count" bson:"-"`
	OnlineInstances              int        `json:"online_instances" bson:"-"`
	HealthyInstances             int        `json:"healthy_instances" bson:"-"`
	RemoteConfigCapableInstances int        `json:"remote_config_capable_instances" bson:"-"`
	CreatedAt                    time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt                    time.Time  `json:"updated_at" bson:"updated_at"`
}

type CollectorInstance struct {
	ID                  string    `json:"id" bson:"_id"`
	InstanceUID         string    `json:"instance_uid" bson:"instance_uid"`
	CollectorGroupID    string    `json:"collector_group_id,omitempty" bson:"collector_group_id"`
	ServiceID           string    `json:"service_id" bson:"service_id"`
	Hostname            string    `json:"hostname" bson:"hostname"`
	PodName             string    `json:"pod_name" bson:"pod_name"`
	NodeName            string    `json:"node_name" bson:"node_name"`
	IP                  string    `json:"ip" bson:"ip"`
	Version             string    `json:"version" bson:"version"`
	Online              bool      `json:"online" bson:"online"`
	Healthy             bool      `json:"healthy" bson:"healthy"`
	Capabilities        uint64    `json:"capabilities" bson:"capabilities"`
	RemoteConfigCapable bool      `json:"remote_config_capable" bson:"remote_config_capable"`
	EffectiveConfigHash string    `json:"effective_config_hash" bson:"effective_config_hash"`
	RemoteConfigStatus  string    `json:"remote_config_status" bson:"remote_config_status"`
	RuntimeStatus       string    `json:"runtime_status" bson:"-"`
	LastConfigHash      string    `json:"last_config_hash" bson:"last_config_hash"`
	LastError           string    `json:"last_error" bson:"last_error"`
	LastSeenAt          time.Time `json:"last_seen_at" bson:"last_seen_at"`
	CreatedAt           time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt           time.Time `json:"updated_at" bson:"updated_at"`
}

type CollectorConfigVersion struct {
	ID               string     `json:"id" bson:"_id"`
	CollectorGroupID string     `json:"collector_group_id" bson:"collector_group_id"`
	Version          int        `json:"version" bson:"version"`
	ConfigHash       string     `json:"config_hash" bson:"config_hash"`
	CollectorYAML    string     `json:"collector_yaml" bson:"collector_yaml"`
	ServiceIDs       []string   `json:"service_ids" bson:"service_ids"`
	Status           string     `json:"status" bson:"status"`
	CreatedBy        string     `json:"created_by" bson:"created_by"`
	CreatedAt        time.Time  `json:"created_at" bson:"created_at"`
	AppliedAt        *time.Time `json:"applied_at,omitempty" bson:"applied_at,omitempty"`
	Message          string     `json:"message" bson:"message"`
}

type InstanceStatus struct {
	ServiceID           string
	Online              bool
	Healthy             bool
	HealthSet           bool
	Capabilities        uint64
	RemoteConfigCapable bool
	EffectiveConfigHash string
	RemoteConfigStatus  string
	LastConfigHash      string
	LastError           string
	LastSeenAt          time.Time
}

type ListGroupFilter struct {
	Query           string
	Environment     string
	Cluster         string
	Namespace       string
	Mode            string
	Status          string
	ReceiverProfile string
}

type DeleteGroupDependencies struct {
	OnlineInstances  int
	OnboardingRefs   int
	ConfigRefs       int
	PendingPublishes int
}

type UpdateGroupRequest struct {
	Name                  *string `json:"name"`
	DisplayName           *string `json:"display_name"`
	Description           *string `json:"description"`
	Mode                  *string `json:"mode"`
	Environment           *string `json:"environment"`
	Cluster               *string `json:"cluster"`
	Namespace             *string `json:"namespace"`
	TenantID              *string `json:"tenant_id"`
	OwnerTeam             *string `json:"owner_team"`
	IsolationLevel        *string `json:"isolation_level"`
	PlatformTemplateID    *string `json:"platform_template_id"`
	Status                *string `json:"status"`
	ReceiverProfile       *string `json:"receiver_profile"`
	ExporterProfile       *string `json:"exporter_profile"`
	IngestEndpoint        *string `json:"ingest_endpoint"`
	DesiredReplicas       *int    `json:"desired_replicas"`
	MaxServices           *int    `json:"max_services"`
	ConfigVersion         *int    `json:"config_version"`
	DesiredConfigHash     *string `json:"desired_config_hash"`
	LastAppliedConfigHash *string `json:"last_applied_config_hash"`
	LastPublishStatus     *string `json:"last_publish_status"`
	LastPublishMessage    *string `json:"last_publish_message"`
}
