package namespace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"novaapm/internal/modules/k8sops/cluster"
	"novaapm/internal/platform/audit"
	"novaapm/internal/platform/rbac"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_namespace_request")
	ErrNotFound         = errors.New("namespace_not_found")
	ErrWriteUnavailable = errors.New("namespace_write_unavailable")
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Namespace, error)
}

type WriteRepository interface {
	Create(ctx context.Context, item Namespace) (Namespace, error)
	Delete(ctx context.Context, req DeleteRequest) (Namespace, error)
}

type Authorizer interface {
	Authorize(subject rbac.Subject, req rbac.Request) rbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Service struct {
	repo       Repository
	writeRepo  WriteRepository
	authorizer Authorizer
	auditor    Auditor
	policy     cluster.ReadOnlyPolicy
}

func NewService(repo Repository, dependencies ...any) Service {
	service := Service{
		repo:       repo,
		authorizer: denyAuthorizer{},
		auditor:    noopAuditor{},
	}
	if value, ok := repo.(WriteRepository); ok && value != nil {
		service.writeRepo = value
	}
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case Authorizer:
			if value != nil {
				service.authorizer = value
			}
		case Auditor:
			if value != nil {
				service.auditor = value
			}
		case cluster.ReadOnlyPolicy:
			if value != nil {
				service.policy = value
			}
		case WriteRepository:
			if value != nil {
				service.writeRepo = value
			}
		}
	}
	return service
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Namespace, error) {
	return s.repo.List(ctx, filter)
}

func (s Service) Create(ctx context.Context, subject rbac.Subject, req CreateRequest) (Namespace, audit.Event, error) {
	req = normalizeCreateRequest(req)
	if req.ClusterID == "" || req.Name == "" {
		return Namespace{}, audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, req.ClusterID, "create") {
		return Namespace{}, audit.Event{}, ErrPermissionDenied
	}
	if err := s.ensureWritable(ctx, req.ClusterID); err != nil {
		return Namespace{}, audit.Event{}, err
	}
	if s.writeRepo == nil {
		return Namespace{}, audit.Event{}, ErrWriteUnavailable
	}
	now := time.Now().UTC()
	created, err := s.writeRepo.Create(ctx, Namespace{
		ID:        fmt.Sprintf("namespace-%s-%s", req.ClusterID, req.Name),
		ClusterID: req.ClusterID,
		Name:      req.Name,
		Status:    "active",
		Owner:     req.Owner,
		Phase:     "Active",
		UpdatedAt: now,
	})
	if err != nil {
		return Namespace{}, audit.Event{}, err
	}
	event, err := s.auditor.Record(ctx, audit.Event{
		Actor:    audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource: audit.Resource{Type: "k8s.namespace", Name: created.Name},
		Action:   "create",
		Scope:    namespaceScope(created.ClusterID),
		Result:   "success",
		RequestSummary: map[string]any{
			"cluster_id": created.ClusterID,
			"name":       created.Name,
			"owner":      created.Owner,
		},
	})
	if err != nil {
		_, _ = s.writeRepo.Delete(ctx, DeleteRequest{ClusterID: created.ClusterID, Name: created.Name, UID: created.ID})
		return Namespace{}, audit.Event{}, err
	}
	return created, event, nil
}

func (s Service) Delete(ctx context.Context, subject rbac.Subject, req DeleteRequest) (audit.Event, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Name == "" || req.UID == "" {
		return audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, req.ClusterID, "delete") {
		return audit.Event{}, ErrPermissionDenied
	}
	if err := s.ensureWritable(ctx, req.ClusterID); err != nil {
		return audit.Event{}, err
	}
	if s.writeRepo == nil {
		return audit.Event{}, ErrWriteUnavailable
	}
	deleted, err := s.writeRepo.Delete(ctx, req)
	if err != nil {
		return audit.Event{}, err
	}
	event, err := s.auditor.Record(ctx, audit.Event{
		Actor:    audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource: audit.Resource{Type: "k8s.namespace", Name: req.Name},
		Action:   "delete",
		Scope:    namespaceScope(req.ClusterID),
		Result:   "success",
		RequestSummary: map[string]any{
			"cluster_id": req.ClusterID,
			"name":       req.Name,
			"uid":        req.UID,
		},
	})
	if err != nil {
		_, _ = s.writeRepo.Create(ctx, deleted)
		return audit.Event{}, err
	}
	return event, nil
}

func (s Service) allowed(subject rbac.Subject, clusterID string, action string) bool {
	decision := s.authorizer.Authorize(subject, rbac.Request{
		Resource: "k8s.namespace",
		Action:   action,
		Scope:    rbac.Scope{ClusterID: clusterID},
	})
	return decision.Allowed
}

func (s Service) ensureWritable(ctx context.Context, clusterID string) error {
	if s.policy == nil {
		return nil
	}
	readOnly, err := s.policy.IsReadOnly(ctx, clusterID)
	if err != nil {
		return err
	}
	if readOnly {
		return cluster.ErrClusterReadOnly
	}
	return nil
}

type MemoryRepository struct {
	mu    sync.Mutex
	items []Namespace
}

func NewMemoryRepository(items []Namespace) *MemoryRepository {
	copied := make([]Namespace, len(items))
	copy(copied, items)
	return &MemoryRepository{items: copied}
}

func (r *MemoryRepository) List(_ context.Context, filter ListFilter) ([]Namespace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Namespace, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), query) && !strings.Contains(strings.ToLower(item.Owner), query) {
			continue
		}
		out = append(out, item)
	}
	sortNamespaces(out, filter.Sort, filter.Order)
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r *MemoryRepository) Create(_ context.Context, item Namespace) (Namespace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item = normalizeNamespace(item)
	if item.ClusterID == "" || item.Name == "" {
		return Namespace{}, ErrInvalidRequest
	}
	for _, existing := range r.items {
		if existing.ClusterID == item.ClusterID && existing.Name == item.Name {
			return Namespace{}, errors.New("namespace already exists")
		}
	}
	r.items = append(r.items, item)
	return item, nil
}

func (r *MemoryRepository) Delete(_ context.Context, req DeleteRequest) (Namespace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req = normalizeDeleteRequest(req)
	next := make([]Namespace, 0, len(r.items))
	deleted := Namespace{}
	for _, item := range r.items {
		if item.ClusterID == req.ClusterID && item.Name == req.Name && item.ID == req.UID {
			deleted = item
			continue
		}
		next = append(next, item)
	}
	if deleted.ID == "" {
		return Namespace{}, ErrNotFound
	}
	r.items = next
	return deleted, nil
}

func sortNamespaces(items []Namespace, field string, order string) {
	desc := strings.EqualFold(order, "desc")
	sort.SliceStable(items, func(left, right int) bool {
		var less bool
		switch field {
		case "status":
			less = items[left].Status < items[right].Status
		case "updated_at":
			less = items[left].UpdatedAt.Before(items[right].UpdatedAt)
		default:
			less = items[left].Name < items[right].Name
		}
		if desc {
			return !less
		}
		return less
	})
}

func normalizeCreateRequest(req CreateRequest) CreateRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Name = strings.TrimSpace(req.Name)
	req.Owner = strings.TrimSpace(req.Owner)
	return req
}

func normalizeDeleteRequest(req DeleteRequest) DeleteRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Name = strings.TrimSpace(req.Name)
	req.UID = strings.TrimSpace(req.UID)
	return req
}

func normalizeNamespace(item Namespace) Namespace {
	item.ClusterID = strings.TrimSpace(item.ClusterID)
	item.Name = strings.TrimSpace(item.Name)
	item.Owner = strings.TrimSpace(item.Owner)
	if item.ID == "" && item.ClusterID != "" && item.Name != "" {
		item.ID = fmt.Sprintf("namespace-%s-%s", item.ClusterID, item.Name)
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.Phase == "" {
		item.Phase = "Active"
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return item
}

func namespaceScope(clusterID string) string {
	return "cluster=" + clusterID
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(rbac.Subject, rbac.Request) rbac.Decision {
	return rbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(_ context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}

func paginate(items []Namespace, page int, pageSize int) []Namespace {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []Namespace{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
