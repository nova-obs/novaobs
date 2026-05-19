package resource

import "time"

type Identity struct {
	ClusterID  string `json:"cluster_id"`
	Namespace  string `json:"namespace"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type ResourceSummary struct {
	Identity  Identity          `json:"identity"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type ResourceDetail struct {
	Identity  Identity          `json:"identity"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	Spec      map[string]any    `json:"spec"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type ResourceYAML struct {
	Identity Identity `json:"identity"`
	YAML     string   `json:"yaml"`
}

type PodLogResult struct {
	Identity  Identity `json:"identity"`
	Container string   `json:"container"`
	Lines     []string `json:"lines"`
}

type ListFilter struct {
	ClusterID  string
	Namespace  string
	APIVersion string
	Kind       string
	Query      string
	Page       int
	PageSize   int
	Sort       string
	Order      string
}

type DetailQuery struct {
	Identity Identity
}

type PodLogQuery struct {
	ClusterID string
	Namespace string
	Pod       string
	Container string
}
