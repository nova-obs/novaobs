package rbac

import "time"

type Rule struct {
	APIGroups []string `json:"api_groups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

type RoleResource struct {
	ID        string    `json:"id"`
	ClusterID string    `json:"cluster_id"`
	Namespace string    `json:"namespace,omitempty"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	UID       string    `json:"uid"`
	Rules     []Rule    `json:"rules"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Subject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type RoleRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type BindingResource struct {
	ID        string    `json:"id"`
	ClusterID string    `json:"cluster_id"`
	Namespace string    `json:"namespace,omitempty"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	UID       string    `json:"uid"`
	RoleRef   RoleRef   `json:"role_ref"`
	Subjects  []Subject `json:"subjects"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ListFilter struct {
	ClusterID string
	Namespace string
	Page      int
	PageSize  int
}

type RoleRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
	Rules     []Rule `json:"rules"`
}

type BindingRequest struct {
	ClusterID string    `json:"cluster_id"`
	Namespace string    `json:"namespace,omitempty"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	UID       string    `json:"uid,omitempty"`
	RoleRef   RoleRef   `json:"role_ref"`
	Subjects  []Subject `json:"subjects"`
}

type DeleteRequest struct {
	ClusterID string
	Namespace string
	Kind      string
	Name      string
	UID       string
}
