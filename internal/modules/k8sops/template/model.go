package template

import "time"

type Template struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	YAMLContent string     `json:"yaml_content"`
	Variables   []Variable `json:"variables"`
	Description string     `json:"description"`
	Source      string     `json:"source"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type Variable struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	DefaultValue string `json:"default_value,omitempty"`
	Required     bool   `json:"required"`
}

type ListFilter struct {
	Type     string
	Query    string
	Page     int
	PageSize int
}

type UpsertRequest struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	YAMLContent string     `json:"yaml_content"`
	Variables   []Variable `json:"variables"`
	Description string     `json:"description"`
}

type DeleteRequest struct {
	ID string
}

type RenderRequest struct {
	ID        string            `json:"id"`
	Variables map[string]string `json:"variables"`
}

type RenderResult struct {
	RenderedYAML string `json:"rendered_yaml"`
	AuditID      string `json:"audit_id"`
}

type BaseTemplateResult struct {
	Type        string     `json:"type"`
	YAMLContent string     `json:"yaml_content"`
	Variables   []Variable `json:"variables"`
	Description string     `json:"description"`
	Source      string     `json:"source"`
}
