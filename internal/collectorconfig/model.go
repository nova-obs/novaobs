package collectorconfig

import "time"

type CollectorPlatformTemplate struct {
	ID             string    `json:"id" bson:"_id"`
	Name           string    `json:"name" bson:"name"`
	Description    string    `json:"description" bson:"description"`
	Source         string    `json:"source" bson:"source"`
	SourceAgentUID string    `json:"source_agent_uid" bson:"source_agent_uid"`
	BaseYAML       string    `json:"base_yaml" bson:"base_yaml"`
	ConfigHash     string    `json:"config_hash" bson:"config_hash"`
	Status         string    `json:"status" bson:"status"`
	Version        int       `json:"version" bson:"version"`
	CreatedAt      time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" bson:"updated_at"`
}

type CollectorGroupOverride struct {
	ID               string    `json:"id" bson:"_id"`
	CollectorGroupID string    `json:"collector_group_id" bson:"collector_group_id"`
	OverrideYAML     string    `json:"override_yaml" bson:"override_yaml"`
	UpdatedAt        time.Time `json:"updated_at" bson:"updated_at"`
}

type ServiceEnrichmentPatch struct {
	ID               string    `json:"id" bson:"_id"`
	ServiceID        string    `json:"service_id" bson:"service_id"`
	CollectorGroupID string    `json:"collector_group_id" bson:"collector_group_id"`
	PatchYAML        string    `json:"patch_yaml" bson:"patch_yaml"`
	ConfigHash       string    `json:"config_hash" bson:"config_hash"`
	Warnings         []string  `json:"warnings" bson:"warnings"`
	Status           string    `json:"status" bson:"status"`
	CreatedAt        time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" bson:"updated_at"`
}

type ServiceParserRule struct {
	ID                string            `json:"id" bson:"_id"`
	ServiceID         string            `json:"service_id" bson:"service_id"`
	CollectorGroupID  string            `json:"collector_group_id" bson:"collector_group_id"`
	ParseMode         string            `json:"parse_mode" bson:"parse_mode"`
	ParseFrom         string            `json:"parse_from" bson:"parse_from"`
	RegexPattern      string            `json:"regex_pattern" bson:"regex_pattern"`
	JSONMappings      map[string]string `json:"json_mappings" bson:"json_mappings"`
	AttributeMappings map[string]string `json:"attribute_mappings" bson:"attribute_mappings"`
	ResourceMappings  map[string]string `json:"resource_mappings" bson:"resource_mappings"`
	OTTLStatements    []string          `json:"ottl_statements" bson:"ottl_statements"`
	SampleLog         string            `json:"sample_log" bson:"sample_log"`
	Enabled           bool              `json:"enabled" bson:"enabled"`
	Status            string            `json:"status" bson:"status"`
	Version           int               `json:"version" bson:"version"`
	CreatedAt         time.Time         `json:"created_at" bson:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at" bson:"updated_at"`
}

type ServicePipelinePatch struct {
	ID               string    `json:"id" bson:"_id"`
	ServiceID        string    `json:"service_id" bson:"service_id"`
	CollectorGroupID string    `json:"collector_group_id" bson:"collector_group_id"`
	ParserRuleID     string    `json:"parser_rule_id" bson:"parser_rule_id"`
	PatchYAML        string    `json:"patch_yaml" bson:"patch_yaml"`
	ConfigHash       string    `json:"config_hash" bson:"config_hash"`
	Status           string    `json:"status" bson:"status"`
	Enabled          bool      `json:"enabled" bson:"enabled"`
	Version          int       `json:"version" bson:"version"`
	CreatedAt        time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" bson:"updated_at"`
}

type CollectorAdditionalConfig struct {
	ID                   string    `json:"id" bson:"_id"`
	Scope                string    `json:"scope" bson:"scope"`
	TargetID             string    `json:"target_id" bson:"target_id"`
	CollectorGroupID     string    `json:"collector_group_id" bson:"collector_group_id"`
	ConfigMapKey         string    `json:"config_map_key" bson:"config_map_key"`
	YAMLPatch            string    `json:"yaml_patch" bson:"yaml_patch"`
	ConfigHash           string    `json:"config_hash" bson:"config_hash"`
	LastRemoteConfigHash string    `json:"last_remote_config_hash" bson:"last_remote_config_hash"`
	Status               string    `json:"status" bson:"status"`
	Version              int       `json:"version" bson:"version"`
	CreatedAt            time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt            time.Time `json:"updated_at" bson:"updated_at"`
}

type SourceBreakdown struct {
	Type     string   `json:"type"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Hash     string   `json:"hash"`
	Warnings []string `json:"warnings"`
}

type ConfigSources struct {
	PlatformTemplate         *CollectorPlatformTemplate `json:"platform_template"`
	GroupOverride            *CollectorGroupOverride    `json:"group_override"`
	ServiceEnrichmentPatches []ServiceEnrichmentPatch   `json:"service_enrichment_patches"`
	ServicePipelinePatches   []ServicePipelinePatch     `json:"service_pipeline_patches"`
	RenderedYAML             string                     `json:"rendered_yaml"`
	ConfigHash               string                     `json:"config_hash"`
	Warnings                 []string                   `json:"warnings"`
	Errors                   []string                   `json:"errors"`
	SourceBreakdown          []SourceBreakdown          `json:"source_breakdown"`
}

type ValidationResult struct {
	Valid           bool              `json:"valid"`
	RenderedYAML    string            `json:"rendered_yaml"`
	ConfigHash      string            `json:"config_hash"`
	SourceBreakdown []SourceBreakdown `json:"source_breakdown"`
	Warnings        []string          `json:"warnings"`
	Errors          []string          `json:"errors"`
}

type ParserPreviewRequest struct {
	ParseMode         string            `json:"parse_mode"`
	ParseFrom         string            `json:"parse_from"`
	RegexPattern      string            `json:"regex_pattern"`
	JSONMappings      map[string]string `json:"json_mappings"`
	AttributeMappings map[string]string `json:"attribute_mappings"`
	ResourceMappings  map[string]string `json:"resource_mappings"`
	OTTLStatements    []string          `json:"ottl_statements"`
	SampleLog         string            `json:"sample_log"`
}

type ParserPreviewResult struct {
	Valid            bool           `json:"valid"`
	ParseMode        string         `json:"parse_mode"`
	SampleLog        string         `json:"sample_log"`
	ParsedFields     map[string]any `json:"parsed_fields"`
	MappedAttributes map[string]any `json:"mapped_attributes"`
	MappedResources  map[string]any `json:"mapped_resources"`
	UnmappedFields   []string       `json:"unmapped_fields"`
	Warnings         []string       `json:"warnings"`
	Errors           []string       `json:"errors"`
}

type RemoteConfigDeployment struct {
	ID                   string
	CollectorInstanceUID string
	CollectorGroupID     string
	Version              int
	ConfigHash           string
	CollectorYAML        string
	ConfigFiles          map[string]string
	Status               string
}
