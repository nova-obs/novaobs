package iam

import (
	"time"

	platformrbac "novaapm/internal/platform/rbac"
)

const (
	SubjectTypeUser           = "user"
	SubjectTypeGroup          = "group"
	SubjectTypeServiceAccount = "service-account"
)

type User struct {
	ID           string    `json:"id" bson:"_id,omitempty"`
	Username     string    `json:"username" bson:"username"`
	DisplayName  string    `json:"display_name" bson:"display_name"`
	Email        string    `json:"email,omitempty" bson:"email,omitempty"`
	PasswordHash string    `json:"-" bson:"password_hash,omitempty"`
	PasswordSet  bool      `json:"password_set" bson:"-"`
	Status       string    `json:"status" bson:"status"`
	Source       string    `json:"source" bson:"source"`
	CreatedAt    time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" bson:"updated_at"`
}

type Group struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	Name        string    `json:"name" bson:"name"`
	DisplayName string    `json:"display_name" bson:"display_name"`
	Description string    `json:"description,omitempty" bson:"description,omitempty"`
	Status      string    `json:"status" bson:"status"`
	Source      string    `json:"source" bson:"source"`
	MemberCount int       `json:"member_count" bson:"-"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

type Membership struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	GroupID     string    `json:"group_id" bson:"group_id"`
	SubjectID   string    `json:"subject_id" bson:"subject_id"`
	SubjectType string    `json:"subject_type" bson:"subject_type"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

type MembershipView struct {
	ID                 string    `json:"id"`
	GroupID            string    `json:"group_id"`
	GroupName          string    `json:"group_name,omitempty"`
	SubjectID          string    `json:"subject_id"`
	SubjectType        string    `json:"subject_type"`
	SubjectDisplayName string    `json:"subject_display_name,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ServiceAccount struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	Name        string    `json:"name" bson:"name"`
	DisplayName string    `json:"display_name" bson:"display_name"`
	Description string    `json:"description,omitempty" bson:"description,omitempty"`
	Status      string    `json:"status" bson:"status"`
	Owner       string    `json:"owner,omitempty" bson:"owner,omitempty"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

type SubjectView struct {
	ID          string    `json:"id"`
	SubjectID   string    `json:"subject_id"`
	SubjectType string    `json:"subject_type"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email,omitempty"`
	Status      string    `json:"status"`
	Source      string    `json:"source"`
	BindingRefs int       `json:"binding_refs"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Status      string `json:"status"`
}

type CreateGroupRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type CreateMembershipRequest struct {
	GroupID     string `json:"group_id"`
	SubjectID   string `json:"subject_id"`
	SubjectType string `json:"subject_type"`
}

type CreateServiceAccountRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Owner       string `json:"owner"`
	Status      string `json:"status"`
}

type CreateRoleRequest struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Permissions []platformrbac.Permission `json:"permissions"`
}

type CreateBindingRequest struct {
	SubjectID   string             `json:"subject_id"`
	SubjectType string             `json:"subject_type"`
	RoleID      string             `json:"role_id"`
	Scope       platformrbac.Scope `json:"scope"`
}

type BindingView struct {
	ID          string             `json:"id"`
	SubjectID   string             `json:"subject_id"`
	SubjectType string             `json:"subject_type"`
	RoleID      string             `json:"role_id"`
	RoleName    string             `json:"role_name"`
	Scope       platformrbac.Scope `json:"scope"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type EffectivePermissionView struct {
	BindingID          string                    `json:"binding_id"`
	RoleID             string                    `json:"role_id"`
	RoleName           string                    `json:"role_name"`
	GrantedToSubjectID string                    `json:"granted_to_subject_id"`
	GrantedToType      string                    `json:"granted_to_type"`
	GrantedVia         string                    `json:"granted_via"`
	Permissions        []platformrbac.Permission `json:"permissions"`
	Scope              platformrbac.Scope        `json:"scope"`
	CreatedAt          time.Time                 `json:"created_at"`
}

type WriteResult[T any] struct {
	Item   T      `json:"item"`
	Status string `json:"status"`
}
