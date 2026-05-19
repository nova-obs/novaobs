package rbac

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_k8s_rbac_request")
	ErrNotFound         = errors.New("k8s_rbac_resource_not_found")
)

type Repository interface {
	ListRoles(ctx context.Context, filter ListFilter) ([]RoleResource, error)
	UpsertRole(ctx context.Context, item RoleResource) (RoleResource, error)
	DeleteRole(ctx context.Context, req DeleteRequest) (RoleResource, error)
	ListBindings(ctx context.Context, filter ListFilter) ([]BindingResource, error)
	UpsertBinding(ctx context.Context, item BindingResource) (BindingResource, error)
	DeleteBinding(ctx context.Context, req DeleteRequest) (BindingResource, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Service struct {
	repo       Repository
	authorizer Authorizer
	auditor    Auditor
}

func NewService(repo Repository, authorizer Authorizer, auditor Auditor) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	if auditor == nil {
		auditor = noopAuditor{}
	}
	return Service{repo: repo, authorizer: authorizer, auditor: auditor}
}

func (s Service) ListRoles(ctx context.Context, filter ListFilter) ([]RoleResource, error) {
	return s.repo.ListRoles(ctx, filter)
}

func (s Service) ListBindings(ctx context.Context, filter ListFilter) ([]BindingResource, error) {
	return s.repo.ListBindings(ctx, filter)
}

func (s Service) CreateRole(ctx context.Context, subject platformrbac.Subject, req RoleRequest) (RoleResource, audit.Event, error) {
	return s.upsertRole(ctx, subject, req, "create")
}

func (s Service) UpdateRole(ctx context.Context, subject platformrbac.Subject, req RoleRequest) (RoleResource, audit.Event, error) {
	return s.upsertRole(ctx, subject, req, "update")
}

func (s Service) DeleteRole(ctx context.Context, subject platformrbac.Subject, req DeleteRequest) (audit.Event, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Kind == "" || req.Name == "" || req.UID == "" {
		return audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "delete", req.ClusterID, req.Namespace) {
		return audit.Event{}, ErrPermissionDenied
	}
	deleted, err := s.repo.DeleteRole(ctx, req)
	if err != nil {
		return audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "role.delete", deleted.Name, deleted.ClusterID, deleted.Namespace, map[string]any{
		"cluster_id": deleted.ClusterID,
		"namespace":  deleted.Namespace,
		"kind":       deleted.Kind,
		"name":       deleted.Name,
		"uid":        deleted.UID,
	})
	if err != nil {
		_, _ = s.repo.UpsertRole(ctx, deleted)
		return audit.Event{}, err
	}
	return event, nil
}

func (s Service) CreateBinding(ctx context.Context, subject platformrbac.Subject, req BindingRequest) (BindingResource, audit.Event, error) {
	req = normalizeBindingRequest(req)
	if req.ClusterID == "" || req.Kind == "" || req.Name == "" || req.RoleRef.Name == "" {
		return BindingResource{}, audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "create", req.ClusterID, req.Namespace) {
		return BindingResource{}, audit.Event{}, ErrPermissionDenied
	}
	item := BindingResource{
		ID:        resourceID("binding", req.ClusterID, req.Namespace, req.Name),
		ClusterID: req.ClusterID,
		Namespace: req.Namespace,
		Kind:      req.Kind,
		Name:      req.Name,
		UID:       defaultUID(req.UID),
		RoleRef:   req.RoleRef,
		Subjects:  append([]Subject(nil), req.Subjects...),
		Source:    "novaobs",
		UpdatedAt: time.Now().UTC(),
	}
	created, err := s.repo.UpsertBinding(ctx, item)
	if err != nil {
		return BindingResource{}, audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "binding.create", created.Name, created.ClusterID, created.Namespace, map[string]any{
		"cluster_id": created.ClusterID,
		"namespace":  created.Namespace,
		"kind":       created.Kind,
		"name":       created.Name,
		"role_ref":   created.RoleRef.Name,
	})
	if err != nil {
		_, _ = s.repo.DeleteBinding(ctx, DeleteRequest{ClusterID: created.ClusterID, Namespace: created.Namespace, Kind: created.Kind, Name: created.Name, UID: created.UID})
		return BindingResource{}, audit.Event{}, err
	}
	return created, event, nil
}

func (s Service) DeleteBinding(ctx context.Context, subject platformrbac.Subject, req DeleteRequest) (audit.Event, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Kind == "" || req.Name == "" || req.UID == "" {
		return audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "delete", req.ClusterID, req.Namespace) {
		return audit.Event{}, ErrPermissionDenied
	}
	deleted, err := s.repo.DeleteBinding(ctx, req)
	if err != nil {
		return audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "binding.delete", deleted.Name, deleted.ClusterID, deleted.Namespace, map[string]any{
		"cluster_id": deleted.ClusterID,
		"namespace":  deleted.Namespace,
		"kind":       deleted.Kind,
		"name":       deleted.Name,
		"uid":        deleted.UID,
	})
	if err != nil {
		_, _ = s.repo.UpsertBinding(ctx, deleted)
		return audit.Event{}, err
	}
	return event, nil
}

func (s Service) upsertRole(ctx context.Context, subject platformrbac.Subject, req RoleRequest, action string) (RoleResource, audit.Event, error) {
	req = normalizeRoleRequest(req)
	if req.ClusterID == "" || req.Kind == "" || req.Name == "" || len(req.Rules) == 0 {
		return RoleResource{}, audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, action, req.ClusterID, req.Namespace) {
		return RoleResource{}, audit.Event{}, ErrPermissionDenied
	}
	item := RoleResource{
		ID:        resourceID("role", req.ClusterID, req.Namespace, req.Name),
		ClusterID: req.ClusterID,
		Namespace: req.Namespace,
		Kind:      req.Kind,
		Name:      req.Name,
		UID:       defaultUID(req.UID),
		Rules:     cloneRules(req.Rules),
		Source:    "novaobs",
		UpdatedAt: time.Now().UTC(),
	}
	saved, err := s.repo.UpsertRole(ctx, item)
	if err != nil {
		return RoleResource{}, audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "role."+action, saved.Name, saved.ClusterID, saved.Namespace, map[string]any{
		"cluster_id": saved.ClusterID,
		"namespace":  saved.Namespace,
		"kind":       saved.Kind,
		"name":       saved.Name,
		"rules":      len(saved.Rules),
	})
	if err != nil {
		_, _ = s.repo.DeleteRole(ctx, DeleteRequest{ClusterID: saved.ClusterID, Namespace: saved.Namespace, Kind: saved.Kind, Name: saved.Name, UID: saved.UID})
		return RoleResource{}, audit.Event{}, err
	}
	return saved, event, nil
}

func (s Service) allowed(subject platformrbac.Subject, action string, clusterID string, namespace string) bool {
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.rbac",
		Action:   action,
		Scope:    platformrbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, action string, name string, clusterID string, namespace string, summary map[string]any) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.rbac", Name: name},
		ResourceType:   "k8s.rbac",
		ResourceName:   name,
		Action:         action,
		Scope:          scopeString(clusterID, namespace),
		Result:         "success",
		RequestSummary: summary,
	})
}

type MemoryRepository struct {
	mu       sync.Mutex
	roles    []RoleResource
	bindings []BindingResource
}

func NewMemoryRepository(roles []RoleResource, bindings []BindingResource) *MemoryRepository {
	roleCopy := append([]RoleResource(nil), roles...)
	bindingCopy := append([]BindingResource(nil), bindings...)
	return &MemoryRepository{roles: roleCopy, bindings: bindingCopy}
}

func (r *MemoryRepository) ListRoles(_ context.Context, filter ListFilter) ([]RoleResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RoleResource, 0, len(r.roles))
	for _, item := range r.roles {
		if matchesFilter(item.ClusterID, item.Namespace, filter) {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(left, right int) bool { return out[left].Name < out[right].Name })
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r *MemoryRepository) UpsertRole(_ context.Context, item RoleResource) (RoleResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for idx, existing := range r.roles {
		if existing.ID == item.ID {
			r.roles[idx] = item
			return item, nil
		}
	}
	r.roles = append(r.roles, item)
	return item, nil
}

func (r *MemoryRepository) DeleteRole(_ context.Context, req DeleteRequest) (RoleResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.roles[:0]
	deleted := RoleResource{}
	for _, item := range r.roles {
		if item.ClusterID == req.ClusterID && item.Namespace == req.Namespace && item.Kind == req.Kind && item.Name == req.Name && item.UID == req.UID {
			deleted = item
			continue
		}
		next = append(next, item)
	}
	if deleted.ID == "" {
		return RoleResource{}, ErrNotFound
	}
	r.roles = next
	return deleted, nil
}

func (r *MemoryRepository) ListBindings(_ context.Context, filter ListFilter) ([]BindingResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]BindingResource, 0, len(r.bindings))
	for _, item := range r.bindings {
		if matchesFilter(item.ClusterID, item.Namespace, filter) {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(left, right int) bool { return out[left].Name < out[right].Name })
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r *MemoryRepository) UpsertBinding(_ context.Context, item BindingResource) (BindingResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for idx, existing := range r.bindings {
		if existing.ID == item.ID {
			r.bindings[idx] = item
			return item, nil
		}
	}
	r.bindings = append(r.bindings, item)
	return item, nil
}

func (r *MemoryRepository) DeleteBinding(_ context.Context, req DeleteRequest) (BindingResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.bindings[:0]
	deleted := BindingResource{}
	for _, item := range r.bindings {
		if item.ClusterID == req.ClusterID && item.Namespace == req.Namespace && item.Kind == req.Kind && item.Name == req.Name && item.UID == req.UID {
			deleted = item
			continue
		}
		next = append(next, item)
	}
	if deleted.ID == "" {
		return BindingResource{}, ErrNotFound
	}
	r.bindings = next
	return deleted, nil
}

func matchesFilter(clusterID string, namespace string, filter ListFilter) bool {
	if filter.ClusterID != "" && clusterID != filter.ClusterID {
		return false
	}
	return filter.Namespace == "" || namespace == filter.Namespace
}

func normalizeRoleRequest(req RoleRequest) RoleRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Kind = strings.TrimSpace(req.Kind)
	req.Name = strings.TrimSpace(req.Name)
	req.UID = strings.TrimSpace(req.UID)
	return req
}

func normalizeBindingRequest(req BindingRequest) BindingRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Kind = strings.TrimSpace(req.Kind)
	req.Name = strings.TrimSpace(req.Name)
	req.UID = strings.TrimSpace(req.UID)
	req.RoleRef.Name = strings.TrimSpace(req.RoleRef.Name)
	req.RoleRef.Kind = strings.TrimSpace(req.RoleRef.Kind)
	return req
}

func normalizeDeleteRequest(req DeleteRequest) DeleteRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Kind = strings.TrimSpace(req.Kind)
	req.Name = strings.TrimSpace(req.Name)
	req.UID = strings.TrimSpace(req.UID)
	return req
}

func resourceID(prefix string, clusterID string, namespace string, name string) string {
	if namespace == "" {
		namespace = "cluster"
	}
	return fmt.Sprintf("%s-%s-%s-%s", prefix, clusterID, namespace, name)
}

func defaultUID(value string) string {
	if value != "" {
		return value
	}
	return primitive.NewObjectID().Hex()
}

func scopeString(clusterID string, namespace string) string {
	if namespace == "" {
		return fmt.Sprintf("cluster=%s", clusterID)
	}
	return fmt.Sprintf("cluster=%s namespace=%s", clusterID, namespace)
}

func cloneRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	return out
}

func paginate[T any](items []T, page int, pageSize int) []T {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []T{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
