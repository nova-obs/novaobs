package environment

import (
	"errors"
	"time"
)

const (
	StageProduction  = "production"
	StageStaging     = "staging"
	StageTest        = "test"
	StageDevelopment = "development"

	StatusActive   = "active"
	StatusArchived = "archived"

	ResourceKindK8sCluster = "k8s_cluster"
	ResourceKindHostGroup  = "host_group"
)

var (
	ErrPermissionDenied     = errors.New("permission_denied")
	ErrEnvironmentNotFound  = errors.New("environment_not_found")
	ErrEnvironmentArchived  = errors.New("environment_archived")
	ErrBindingNotFound      = errors.New("environment_resource_binding_not_found")
	ErrResourceAlreadyBound = errors.New("environment_resource_already_bound")
)

type Environment struct {
	ID          string    `json:"id" bson:"_id"`
	Name        string    `json:"name" bson:"name"`
	Stage       string    `json:"stage" bson:"stage"`
	Description string    `json:"description" bson:"description"`
	Status      string    `json:"status" bson:"status"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
	CreatedBy   string    `json:"created_by" bson:"created_by"`
	UpdatedBy   string    `json:"updated_by" bson:"updated_by"`
}

type ResourceBinding struct {
	ID            string    `json:"id" bson:"_id"`
	EnvironmentID string    `json:"environment_id" bson:"environment_id"`
	ResourceKind  string    `json:"resource_kind" bson:"resource_kind"`
	ResourceRef   string    `json:"resource_ref" bson:"resource_ref"`
	CreatedAt     time.Time `json:"created_at" bson:"created_at"`
	CreatedBy     string    `json:"created_by" bson:"created_by"`
}

type EnvironmentView struct {
	Environment      Environment       `json:"environment"`
	ResourceBindings []ResourceBinding `json:"resource_bindings"`
}

type CreateRequest struct {
	Name        string `json:"name"`
	Stage       string `json:"stage"`
	Description string `json:"description"`
}

type UpdateRequest struct {
	Name        *string `json:"name"`
	Stage       *string `json:"stage"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
}

type BindResourceRequest struct {
	ResourceKind string `json:"resource_kind"`
	ResourceRef  string `json:"resource_ref"`
}
