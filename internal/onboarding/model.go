package onboarding

import "time"

type IngestionIdentity struct {
	ID           string     `json:"id" bson:"_id"`
	ServiceID    string     `json:"service_id" bson:"service_id"`
	TenantID     string     `json:"tenant_id" bson:"tenant_id"`
	Environment  string     `json:"environment" bson:"environment"`
	IdentityType string     `json:"identity_type" bson:"identity_type"`
	TokenHash    string     `json:"-" bson:"token_hash"`
	K8sNamespace string     `json:"k8s_namespace" bson:"k8s_namespace"`
	K8sWorkload  string     `json:"k8s_workload" bson:"k8s_workload"`
	Enabled      bool       `json:"enabled" bson:"enabled"`
	ExpiresAt    *time.Time `json:"expires_at" bson:"expires_at"`
	CreatedAt    time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" bson:"updated_at"`
}

type ServiceOnboarding struct {
	ID                   string     `json:"id" bson:"_id"`
	ServiceID            string     `json:"service_id" bson:"service_id"`
	Mode                 string     `json:"mode" bson:"mode"`
	CollectorGroupID     string     `json:"collector_group_id" bson:"collector_group_id"`
	IdentityID           string     `json:"identity_id" bson:"identity_id"`
	Status               string     `json:"status" bson:"status"`
	Endpoint             string     `json:"endpoint" bson:"endpoint"`
	ResourceAttributes   string     `json:"resource_attributes" bson:"resource_attributes"`
	KubernetesLabels     string     `json:"kubernetes_labels" bson:"kubernetes_labels"`
	LastCheckStatus      string     `json:"last_check_status" bson:"last_check_status"`
	LastCheckMessage     string     `json:"last_check_message" bson:"last_check_message"`
	LastSeenLogAt        *time.Time `json:"last_seen_log_at" bson:"last_seen_log_at"`
	VerificationAttempts int        `json:"verification_attempts" bson:"verification_attempts"`
	LastVerifiedAt       *time.Time `json:"last_verified_at" bson:"last_verified_at"`
	LastCheckAt          *time.Time `json:"last_check_at" bson:"last_check_at"`
	LastCheckDetails     string     `json:"last_check_details" bson:"last_check_details"`
	CreatedAt            time.Time  `json:"created_at" bson:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" bson:"updated_at"`
}

type UpsertRequest struct {
	Mode             string `json:"mode"`
	CollectorGroupID string `json:"collector_group_id"`
	IdentityType     string `json:"identity_type"`
	K8sNamespace     string `json:"k8s_namespace"`
	K8sWorkload      string `json:"k8s_workload"`
}

type Workspace struct {
	Service          ServiceSummary    `json:"service"`
	Onboarding       ServiceOnboarding `json:"onboarding"`
	Identity         IdentitySummary   `json:"identity"`
	CollectorTarget  CollectorTarget   `json:"collector_target"`
	GeneratedConfig  GeneratedConfig   `json:"generated_config"`
	Checklist        []ChecklistItem   `json:"checklist"`
	LastCheck        CheckResult       `json:"last_check"`
	AvailableActions []string          `json:"available_actions"`
}

type ServiceSummary struct {
	ID            string `json:"id"`
	CMDBServiceID string `json:"cmdb_service_id"`
	BusinessID    string `json:"business_id"`
	ApplicationID string `json:"application_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	IdentityType  string `json:"identity_type"`
	Environment   string `json:"environment"`
	Cluster       string `json:"cluster"`
	Namespace     string `json:"namespace"`
	OwnerTeam     string `json:"owner_team"`
	Owner         string `json:"owner"`
	AlertRoute    string `json:"alert_route"`
	Status        string `json:"status"`
}

type IdentitySummary struct {
	ID           string     `json:"id"`
	IdentityType string     `json:"identity_type"`
	Enabled      bool       `json:"enabled"`
	TenantID     string     `json:"tenant_id"`
	Environment  string     `json:"environment"`
	K8sNamespace string     `json:"k8s_namespace"`
	K8sWorkload  string     `json:"k8s_workload"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	TokenPresent bool       `json:"token_present"`
}

type CollectorTarget struct {
	GroupID                      string `json:"group_id"`
	Name                         string `json:"name"`
	Mode                         string `json:"mode"`
	Environment                  string `json:"environment"`
	Cluster                      string `json:"cluster"`
	Namespace                    string `json:"namespace"`
	Status                       string `json:"status"`
	ReceiverProfile              string `json:"receiver_profile"`
	ExporterProfile              string `json:"exporter_profile"`
	Endpoint                     string `json:"endpoint"`
	OnlineInstances              int    `json:"online_instances"`
	HealthyInstances             int    `json:"healthy_instances"`
	RemoteConfigCapableInstances int    `json:"remote_config_capable_instances"`
}

type GeneratedConfig struct {
	Endpoint               string            `json:"endpoint"`
	ResourceAttributes     map[string]string `json:"resource_attributes"`
	ResourceAttributesText string            `json:"resource_attributes_text"`
	KubernetesLabels       map[string]string `json:"kubernetes_labels"`
	EnvironmentVariables   map[string]string `json:"environment_variables"`
	EnvBlock               string            `json:"env_block"`
	OTelCollectorHint      string            `json:"otel_collector_hint"`
	CodeSamples            map[string]string `json:"code_samples"`
}
