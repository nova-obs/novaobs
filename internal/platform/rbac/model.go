package rbac

import "time"

type Subject struct {
	ID          string `json:"id" bson:"_id,omitempty"`
	Type        string `json:"type" bson:"type"`
	DisplayName string `json:"display_name" bson:"display_name"`
}

type Scope struct {
	Global        bool     `json:"global" bson:"global"`
	ClusterID     string   `json:"cluster_id,omitempty" bson:"cluster_id,omitempty"`
	Namespace     string   `json:"namespace,omitempty" bson:"namespace,omitempty"`
	Namespaces    []string `json:"namespaces,omitempty" bson:"namespaces,omitempty"`
	AllNamespaces bool     `json:"all_namespaces,omitempty" bson:"all_namespaces,omitempty"`
	Environment   string   `json:"environment,omitempty" bson:"environment,omitempty"`
	ServiceID     string   `json:"service_id,omitempty" bson:"service_id,omitempty"`
}

type Permission struct {
	Resource  string `json:"resource" bson:"resource"`
	Action    string `json:"action" bson:"action"`
	ScopeMode string `json:"scope_mode" bson:"scope_mode"`
}

type Role struct {
	ID          string       `json:"id" bson:"_id,omitempty"`
	Name        string       `json:"name" bson:"name"`
	Description string       `json:"description" bson:"description"`
	Permissions []Permission `json:"permissions" bson:"permissions"`
	CreatedAt   time.Time    `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at" bson:"updated_at"`
}

type Binding struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	SubjectID   string    `json:"subject_id" bson:"subject_id"`
	SubjectType string    `json:"subject_type" bson:"subject_type"`
	RoleID      string    `json:"role_id" bson:"role_id"`
	Scope       Scope     `json:"scope" bson:"scope"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

type Request struct {
	Resource string
	Action   string
	Scope    Scope
}

type Decision struct {
	Allowed bool
	Reason  string
}
