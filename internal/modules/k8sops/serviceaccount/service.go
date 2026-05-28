package serviceaccount

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_service_account_request")
	ErrNotFound         = errors.New("service_account_not_found")
	ErrWriteUnavailable = errors.New("service_account_write_unavailable")
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]ServiceAccount, error)
	Create(ctx context.Context, item ServiceAccount) (ServiceAccount, error)
	Delete(ctx context.Context, req DeleteRequest) (ServiceAccount, error)
}

type Authorizer interface {
	Authorize(subject rbac.Subject, req rbac.Request) rbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Service struct {
	repo       Repository
	authorizer Authorizer
	auditor    Auditor
	policy     cluster.ReadOnlyPolicy
}

func NewService(repo Repository, authorizer Authorizer, auditor Auditor, policies ...cluster.ReadOnlyPolicy) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	if auditor == nil {
		auditor = noopAuditor{}
	}
	service := Service{repo: repo, authorizer: authorizer, auditor: auditor}
	for _, policy := range policies {
		if policy != nil {
			service.policy = policy
		}
	}
	return service
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]ServiceAccount, error) {
	return s.repo.List(ctx, filter)
}

func (s Service) Create(ctx context.Context, subject rbac.Subject, req CreateRequest) (ServiceAccount, audit.Event, error) {
	req = normalizeCreateRequest(req)
	if req.ClusterID == "" || req.Namespace == "" || req.Name == "" {
		return ServiceAccount{}, audit.Event{}, ErrInvalidRequest
	}
	decision := s.authorizer.Authorize(subject, rbac.Request{
		Resource: "k8s.service-account",
		Action:   "create",
		Scope:    rbac.Scope{ClusterID: req.ClusterID, Namespace: req.Namespace},
	})
	if !decision.Allowed {
		return ServiceAccount{}, audit.Event{}, ErrPermissionDenied
	}
	if err := s.ensureWritable(ctx, req.ClusterID); err != nil {
		return ServiceAccount{}, audit.Event{}, err
	}
	item := ServiceAccount{
		ID:        fmt.Sprintf("sa-%s-%s-%s", req.ClusterID, req.Namespace, req.Name),
		ClusterID: req.ClusterID,
		Namespace: req.Namespace,
		Name:      req.Name,
		UID:       primitive.NewObjectID().Hex(),
		Status:    "active",
		Source:    "novaobs",
		CreatedAt: time.Now().UTC(),
	}
	created, err := s.repo.Create(ctx, item)
	if err != nil {
		return ServiceAccount{}, audit.Event{}, err
	}
	event, err := s.auditor.Record(ctx, audit.Event{
		Actor:    audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource: audit.Resource{Type: "k8s.service-account", Name: created.Name},
		Action:   "create",
		Scope:    scopeString(created.ClusterID, created.Namespace),
		Result:   "success",
		RequestSummary: map[string]any{
			"cluster_id": created.ClusterID,
			"namespace":  created.Namespace,
			"name":       created.Name,
			"token":      req.Token,
		},
	})
	if err != nil {
		_, _ = s.repo.Delete(ctx, DeleteRequest{ClusterID: created.ClusterID, Namespace: created.Namespace, Name: created.Name, UID: created.UID})
		return ServiceAccount{}, audit.Event{}, err
	}
	return created, event, nil
}

func (s Service) Delete(ctx context.Context, subject rbac.Subject, req DeleteRequest) (audit.Event, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Namespace == "" || req.Name == "" || req.UID == "" {
		return audit.Event{}, ErrInvalidRequest
	}
	decision := s.authorizer.Authorize(subject, rbac.Request{
		Resource: "k8s.service-account",
		Action:   "delete",
		Scope:    rbac.Scope{ClusterID: req.ClusterID, Namespace: req.Namespace},
	})
	if !decision.Allowed {
		return audit.Event{}, ErrPermissionDenied
	}
	if err := s.ensureWritable(ctx, req.ClusterID); err != nil {
		return audit.Event{}, err
	}
	deleted, err := s.repo.Delete(ctx, req)
	if err != nil {
		return audit.Event{}, err
	}
	event, err := s.auditor.Record(ctx, audit.Event{
		Actor:    audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource: audit.Resource{Type: "k8s.service-account", Name: req.Name},
		Action:   "delete",
		Scope:    scopeString(req.ClusterID, req.Namespace),
		Result:   "success",
		RequestSummary: map[string]any{
			"cluster_id": req.ClusterID,
			"namespace":  req.Namespace,
			"name":       req.Name,
			"uid":        req.UID,
		},
	})
	if err != nil {
		_, _ = s.repo.Create(ctx, deleted)
		return audit.Event{}, err
	}
	return event, nil
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
	items []ServiceAccount
}

func NewMemoryRepository(items []ServiceAccount) *MemoryRepository {
	copied := make([]ServiceAccount, len(items))
	copy(copied, items)
	return &MemoryRepository{items: copied}
}

func (r *MemoryRepository) List(_ context.Context, filter ListFilter) ([]ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ServiceAccount, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), query) && !strings.Contains(strings.ToLower(item.UID), query) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].Name < out[right].Name
	})
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r *MemoryRepository) Create(_ context.Context, item ServiceAccount) (ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.items {
		if existing.ClusterID == item.ClusterID && existing.Namespace == item.Namespace && existing.Name == item.Name {
			return ServiceAccount{}, errors.New("service account already exists")
		}
	}
	r.items = append(r.items, item)
	return item, nil
}

func (r *MemoryRepository) Delete(_ context.Context, req DeleteRequest) (ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.items[:0]
	deleted := ServiceAccount{}
	for _, item := range r.items {
		match := item.ClusterID == req.ClusterID && item.Namespace == req.Namespace && item.Name == req.Name && item.UID == req.UID
		if match {
			deleted = item
			continue
		}
		next = append(next, item)
	}
	if deleted.ID == "" {
		return ServiceAccount{}, ErrNotFound
	}
	r.items = next
	return deleted, nil
}

func paginate(items []ServiceAccount, page int, pageSize int) []ServiceAccount {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []ServiceAccount{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func normalizeCreateRequest(req CreateRequest) CreateRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Name = strings.TrimSpace(req.Name)
	return req
}

func normalizeDeleteRequest(req DeleteRequest) DeleteRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Name = strings.TrimSpace(req.Name)
	req.UID = strings.TrimSpace(req.UID)
	return req
}

func scopeString(clusterID string, namespace string) string {
	return fmt.Sprintf("cluster=%s namespace=%s", clusterID, namespace)
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject rbac.Subject, req rbac.Request) rbac.Decision {
	return rbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
