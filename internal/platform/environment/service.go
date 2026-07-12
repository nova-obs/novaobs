package environment

import (
	"context"
	"errors"
	"strings"
	"time"

	platformaudit "novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event platformaudit.Event) (platformaudit.Event, error)
}

type ResourceValidator interface {
	Validate(ctx context.Context, resourceKind string, resourceRef string) error
}

type Service struct {
	repo       Repository
	authorizer Authorizer
	auditor    Auditor
	resources  ResourceValidator
	now        func() time.Time
}

type ServiceOption func(*Service)

func WithAuthorizer(authorizer Authorizer) ServiceOption {
	return func(service *Service) { service.authorizer = authorizer }
}

func WithAuditor(auditor Auditor) ServiceOption {
	return func(service *Service) { service.auditor = auditor }
}

func NewService(repo Repository, resources ResourceValidator, options ...ServiceOption) Service {
	service := Service{repo: repo, resources: resources, now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(&service)
	}
	return service
}

func (s Service) List(ctx context.Context, subject platformrbac.Subject) ([]Environment, error) {
	if !s.allowed(subject, "read") {
		return nil, ErrPermissionDenied
	}
	return s.repo.ListEnvironments(ctx)
}

func (s Service) Get(ctx context.Context, subject platformrbac.Subject, id string) (EnvironmentView, error) {
	if !s.allowed(subject, "read") {
		return EnvironmentView{}, ErrPermissionDenied
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return EnvironmentView{}, apperr.InvalidRequest("environment_id 不能为空")
	}
	item, err := s.repo.GetEnvironment(ctx, id)
	if err != nil {
		return EnvironmentView{}, err
	}
	bindings, err := s.repo.ListResourceBindings(ctx, id)
	if err != nil {
		return EnvironmentView{}, err
	}
	return EnvironmentView{Environment: item, ResourceBindings: bindings}, nil
}

func (s Service) Create(ctx context.Context, subject platformrbac.Subject, req CreateRequest) (Environment, error) {
	if !s.allowed(subject, "manage") {
		return Environment{}, ErrPermissionDenied
	}
	now := s.now()
	item := Environment{
		ID:          primitive.NewObjectID().Hex(),
		Name:        strings.TrimSpace(req.Name),
		Stage:       strings.TrimSpace(req.Stage),
		Description: strings.TrimSpace(req.Description),
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   subject.ID,
		UpdatedBy:   subject.ID,
	}
	if err := validateEnvironment(item); err != nil {
		return Environment{}, err
	}
	if err := s.repo.CreateEnvironment(ctx, item); err != nil {
		return Environment{}, err
	}
	if err := s.record(ctx, subject, "environment", item.ID, "create", map[string]any{"name": item.Name, "stage": item.Stage}); err != nil {
		return Environment{}, err
	}
	return item, nil
}

func (s Service) Update(ctx context.Context, subject platformrbac.Subject, id string, req UpdateRequest) (Environment, error) {
	if !s.allowed(subject, "manage") {
		return Environment{}, ErrPermissionDenied
	}
	item, err := s.repo.GetEnvironment(ctx, strings.TrimSpace(id))
	if err != nil {
		return Environment{}, err
	}
	if req.Name != nil {
		item.Name = strings.TrimSpace(*req.Name)
	}
	if req.Stage != nil {
		item.Stage = strings.TrimSpace(*req.Stage)
	}
	if req.Description != nil {
		item.Description = strings.TrimSpace(*req.Description)
	}
	if req.Status != nil {
		item.Status = strings.TrimSpace(*req.Status)
	}
	item.UpdatedAt = s.now()
	item.UpdatedBy = subject.ID
	if err := validateEnvironment(item); err != nil {
		return Environment{}, err
	}
	if err := s.repo.UpdateEnvironment(ctx, item); err != nil {
		return Environment{}, err
	}
	if err := s.record(ctx, subject, "environment", item.ID, "update", map[string]any{"name": item.Name, "stage": item.Stage, "status": item.Status}); err != nil {
		return Environment{}, err
	}
	return item, nil
}

func (s Service) BindResource(ctx context.Context, subject platformrbac.Subject, environmentID string, req BindResourceRequest) (ResourceBinding, error) {
	if !s.allowed(subject, "manage") {
		return ResourceBinding{}, ErrPermissionDenied
	}
	environmentID = strings.TrimSpace(environmentID)
	item, err := s.repo.GetEnvironment(ctx, environmentID)
	if err != nil {
		return ResourceBinding{}, err
	}
	if item.Status == StatusArchived {
		return ResourceBinding{}, ErrEnvironmentArchived
	}
	binding := ResourceBinding{
		ID:            primitive.NewObjectID().Hex(),
		EnvironmentID: environmentID,
		ResourceKind:  strings.TrimSpace(req.ResourceKind),
		ResourceRef:   strings.TrimSpace(req.ResourceRef),
		CreatedAt:     s.now(),
		CreatedBy:     subject.ID,
	}
	if err := validateResourceBinding(binding); err != nil {
		return ResourceBinding{}, err
	}
	if s.resources == nil {
		return ResourceBinding{}, apperr.InvalidRequest("环境资源目录不可用")
	}
	if err := s.resources.Validate(ctx, binding.ResourceKind, binding.ResourceRef); err != nil {
		return ResourceBinding{}, err
	}
	if _, err := s.repo.FindResourceBinding(ctx, binding.ResourceKind, binding.ResourceRef); err == nil {
		return ResourceBinding{}, ErrResourceAlreadyBound
	} else if !errors.Is(err, ErrBindingNotFound) {
		return ResourceBinding{}, err
	}
	if err := s.repo.CreateResourceBinding(ctx, binding); err != nil {
		return ResourceBinding{}, err
	}
	if err := s.record(ctx, subject, "environment_resource_binding", binding.ID, "create", map[string]any{"environment_id": environmentID, "resource_kind": binding.ResourceKind, "resource_ref": binding.ResourceRef}); err != nil {
		return ResourceBinding{}, err
	}
	return binding, nil
}

func (s Service) UnbindResource(ctx context.Context, subject platformrbac.Subject, environmentID string, bindingID string) error {
	if !s.allowed(subject, "manage") {
		return ErrPermissionDenied
	}
	bindings, err := s.repo.ListResourceBindings(ctx, strings.TrimSpace(environmentID))
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.ID == strings.TrimSpace(bindingID) {
			if err := s.repo.DeleteResourceBinding(ctx, binding.ID); err != nil {
				return err
			}
			return s.record(ctx, subject, "environment_resource_binding", binding.ID, "delete", map[string]any{"environment_id": environmentID, "resource_kind": binding.ResourceKind, "resource_ref": binding.ResourceRef})
		}
	}
	return ErrBindingNotFound
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, resourceType string, resourceName string, action string, summary map[string]any) error {
	if s.auditor == nil {
		return nil
	}
	_, err := s.auditor.Record(ctx, platformaudit.Event{
		Actor:          platformaudit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       platformaudit.Resource{Type: resourceType, Name: resourceName},
		Action:         action,
		Scope:          "global",
		RequestSummary: summary,
		Result:         "success",
	})
	return err
}

func (s Service) allowed(subject platformrbac.Subject, action string) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "platform.environment",
		Action:   action,
		Scope:    platformrbac.Scope{Global: true},
	}).Allowed
}

func validateEnvironment(item Environment) error {
	if item.Name == "" {
		return apperr.InvalidRequest("name 不能为空")
	}
	switch item.Stage {
	case StageProduction, StageStaging, StageTest, StageDevelopment:
	default:
		return apperr.InvalidRequest("stage 必须是 production、staging、test 或 development")
	}
	if item.Status != StatusActive && item.Status != StatusArchived {
		return apperr.InvalidRequest("status 必须是 active 或 archived")
	}
	return nil
}

func validateResourceBinding(item ResourceBinding) error {
	if item.EnvironmentID == "" {
		return apperr.InvalidRequest("environment_id 不能为空")
	}
	if item.ResourceKind != ResourceKindK8sCluster && item.ResourceKind != ResourceKindHostGroup {
		return apperr.InvalidRequest("resource_kind 必须是 k8s_cluster 或 host_group")
	}
	if item.ResourceRef == "" {
		return apperr.InvalidRequest("resource_ref 不能为空")
	}
	return nil
}
