package platformaccess

import (
	"time"

	platformrbac "novaobs/internal/platform/rbac"
)

type PermissionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Resource    string `json:"resource"`
	Action      string `json:"action"`
	ScopeMode   string `json:"scope_mode"`
}

type PermissionProfile struct {
	ID                     string   `json:"id"`
	Label                  string   `json:"label"`
	Description            string   `json:"description"`
	Risk                   string   `json:"risk"`
	ScopeMode              string   `json:"scope_mode"`
	RecommendedSubjectType string   `json:"recommended_subject_type"`
	PermissionIDs          []string `json:"permission_ids"`
}

type Binding struct {
	ID            string                    `json:"id"`
	SubjectID     string                    `json:"subject_id"`
	SubjectType   string                    `json:"subject_type"`
	RoleID        string                    `json:"role_id"`
	RoleName      string                    `json:"role_name"`
	Scope         platformrbac.Scope        `json:"scope"`
	PermissionIDs []string                  `json:"permission_ids"`
	Permissions   []platformrbac.Permission `json:"permissions"`
	CreatedAt     time.Time                 `json:"created_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
}

type SubjectRecord struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	SubjectID   string    `json:"subject_id" bson:"subject_id"`
	SubjectType string    `json:"subject_type" bson:"subject_type"`
	DisplayName string    `json:"display_name" bson:"display_name"`
	Email       string    `json:"email,omitempty" bson:"email,omitempty"`
	Source      string    `json:"source" bson:"source"`
	BindingRefs int       `json:"binding_refs" bson:"-"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

type CreateSubjectRequest struct {
	SubjectID   string `json:"subject_id"`
	SubjectType string `json:"subject_type"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type CreateBindingRequest struct {
	SubjectID     string   `json:"subject_id"`
	SubjectType   string   `json:"subject_type"`
	ClusterID     string   `json:"cluster_id"`
	Namespace     string   `json:"namespace"`
	Namespaces    []string `json:"namespaces"`
	AllNamespaces bool     `json:"all_namespaces"`
	Global        bool     `json:"global"`
	RiskAccepted  bool     `json:"risk_accepted"`
	PermissionIDs []string `json:"permission_ids"`
}

type WriteResult struct {
	Item    *Binding  `json:"item,omitempty"`
	Items   []Binding `json:"items,omitempty"`
	Status  string    `json:"status"`
	AuditID string    `json:"audit_id"`
}

type SubjectWriteResult struct {
	Item    *SubjectRecord `json:"item,omitempty"`
	Status  string         `json:"status"`
	AuditID string         `json:"audit_id"`
}
