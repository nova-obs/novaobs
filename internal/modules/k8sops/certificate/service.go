package certificate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/platform/secret"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_certificate_request")
	ErrNotFound         = errors.New("certificate_not_found")
	ErrAlreadyExists    = errors.New("certificate_already_exists")
	ErrWriteUnavailable = errors.New("certificate_write_unavailable")
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Certificate, error)
	Create(ctx context.Context, item Certificate) (Certificate, error)
	Delete(ctx context.Context, req DeleteRequest) (Certificate, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type SecretService interface {
	Create(ctx context.Context, req secret.CreateRequest) (secret.Secret, error)
}

type writeUnavailableRepository interface {
	WritesUnavailable() bool
}

type Service struct {
	repo       Repository
	authorizer Authorizer
	auditor    Auditor
	secrets    SecretService
	policy     cluster.ReadOnlyPolicy
}

func NewService(repo Repository, dependencies ...any) Service {
	service := Service{repo: repo, authorizer: denyAuthorizer{}, auditor: noopAuditor{}}
	for _, item := range dependencies {
		switch value := item.(type) {
		case Authorizer:
			if value != nil {
				service.authorizer = value
			}
		case Auditor:
			if value != nil {
				service.auditor = value
			}
		case SecretService:
			if value != nil {
				service.secrets = value
			}
		case cluster.ReadOnlyPolicy:
			if value != nil {
				service.policy = value
			}
		}
	}
	return service
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Certificate, error) {
	return s.repo.List(ctx, filter)
}

func (s Service) Create(ctx context.Context, subject platformrbac.Subject, req CreateRequest) (Certificate, audit.Event, error) {
	req = normalizeCreateRequest(req)
	if req.ClusterID == "" || req.Namespace == "" || req.Name == "" || req.CommonName == "" || req.CertificatePEM == "" || req.PrivateKeyPEM == "" {
		return Certificate{}, audit.Event{}, ErrInvalidRequest
	}
	if !s.allowed(subject, "create", req.ClusterID, req.Namespace) {
		return Certificate{}, audit.Event{}, ErrPermissionDenied
	}
	if err := s.ensureWritable(ctx, req.ClusterID); err != nil {
		return Certificate{}, audit.Event{}, err
	}
	if guard, ok := s.repo.(writeUnavailableRepository); ok && guard.WritesUnavailable() {
		return Certificate{}, audit.Event{}, ErrWriteUnavailable
	}
	notAfter, err := parseNotAfter(req.NotAfter)
	if err != nil {
		return Certificate{}, audit.Event{}, ErrInvalidRequest
	}
	secretID := ""
	if s.secrets != nil {
		metadata, err := s.secrets.Create(ctx, secret.CreateRequest{
			Name:      fmt.Sprintf("%s-%s-private-key", req.Namespace, req.Name),
			Type:      "certificate_private_key",
			Scope:     secret.Scope{ClusterID: req.ClusterID, Namespace: req.Namespace},
			Plaintext: []byte(req.PrivateKeyPEM),
			CreatedBy: subject.ID,
			ExpiresAt: notAfter,
		})
		if err != nil {
			return Certificate{}, audit.Event{}, err
		}
		secretID = metadata.ID
	}
	item := Certificate{
		ID:          primitive.NewObjectID().Hex(),
		ClusterID:   req.ClusterID,
		Namespace:   req.Namespace,
		Name:        req.Name,
		CommonName:  req.CommonName,
		Fingerprint: fingerprint(req.CertificatePEM),
		SecretID:    secretID,
		NotAfter:    notAfter,
		Status:      certificateStatus(notAfter),
		Source:      "novaobs",
		Certificate: req.CertificatePEM,
		PrivateKey:  req.PrivateKeyPEM,
	}
	created, err := s.repo.Create(ctx, item)
	if err != nil {
		return Certificate{}, audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "create", created, map[string]any{
		"cluster_id":        created.ClusterID,
		"namespace":         created.Namespace,
		"name":              created.Name,
		"common_name":       created.CommonName,
		"fingerprint":       created.Fingerprint,
		"secret_id":         created.SecretID,
		"private_key_pem":   req.PrivateKeyPEM,
		"certificate_bytes": len(req.CertificatePEM),
	})
	if err != nil {
		_, _ = s.repo.Delete(ctx, DeleteRequest{ID: created.ID, ClusterID: created.ClusterID, Namespace: created.Namespace, Name: created.Name})
		return Certificate{}, audit.Event{}, err
	}
	return created, event, nil
}

func (s Service) Delete(ctx context.Context, subject platformrbac.Subject, req DeleteRequest) (audit.Event, error) {
	req = normalizeDeleteRequest(req)
	if req.ID == "" && (req.ClusterID == "" || req.Namespace == "" || req.Name == "") {
		return audit.Event{}, ErrInvalidRequest
	}
	existing, err := s.findByDeleteRequest(ctx, req)
	if err != nil {
		return audit.Event{}, err
	}
	if !s.allowed(subject, "delete", existing.ClusterID, existing.Namespace) {
		return audit.Event{}, ErrPermissionDenied
	}
	if err := s.ensureWritable(ctx, existing.ClusterID); err != nil {
		return audit.Event{}, err
	}
	deleteReq := DeleteRequest{ID: existing.ID, ClusterID: existing.ClusterID, Namespace: existing.Namespace, Name: existing.Name}
	deleted, err := s.repo.Delete(ctx, deleteReq)
	if err != nil {
		return audit.Event{}, err
	}
	event, err := s.record(ctx, subject, "delete", deleted, map[string]any{
		"cluster_id":  deleted.ClusterID,
		"namespace":   deleted.Namespace,
		"name":        deleted.Name,
		"common_name": deleted.CommonName,
		"fingerprint": deleted.Fingerprint,
		"secret_id":   deleted.SecretID,
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
	items []Certificate
}

func NewMemoryRepository(items []Certificate) *MemoryRepository {
	copied := make([]Certificate, len(items))
	copy(copied, items)
	return &MemoryRepository{items: copied}
}

func (r *MemoryRepository) List(_ context.Context, filter ListFilter) ([]Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Certificate, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), query) && !strings.Contains(strings.ToLower(item.CommonName), query) {
			continue
		}
		item.PrivateKey = ""
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		less := out[left].NotAfter.Before(out[right].NotAfter)
		if strings.EqualFold(filter.Order, "desc") {
			return !less
		}
		return less
	})
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r *MemoryRepository) Create(_ context.Context, item Certificate) (Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.items {
		if existing.ID == item.ID || (existing.ClusterID == item.ClusterID && existing.Namespace == item.Namespace && existing.Name == item.Name) {
			return Certificate{}, ErrAlreadyExists
		}
	}
	r.items = append(r.items, item)
	return item, nil
}

func (r *MemoryRepository) Delete(_ context.Context, req DeleteRequest) (Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req = normalizeDeleteRequest(req)
	next := r.items[:0]
	deleted := Certificate{}
	for _, item := range r.items {
		if deleteRequestMatches(item, req) {
			deleted = item
			continue
		}
		next = append(next, item)
	}
	if deleted.ID == "" {
		return Certificate{}, ErrNotFound
	}
	r.items = next
	return deleted, nil
}

func paginate(items []Certificate, page int, pageSize int) []Certificate {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []Certificate{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func (s Service) findByDeleteRequest(ctx context.Context, req DeleteRequest) (Certificate, error) {
	items, err := s.repo.List(ctx, ListFilter{ClusterID: req.ClusterID, Namespace: req.Namespace})
	if err != nil {
		return Certificate{}, err
	}
	for _, item := range items {
		if deleteRequestMatches(item, req) {
			return item, nil
		}
	}
	return Certificate{}, ErrNotFound
}

func normalizeDeleteRequest(req DeleteRequest) DeleteRequest {
	req.ID = strings.TrimSpace(req.ID)
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Name = strings.TrimSpace(req.Name)
	return req
}

func deleteRequestMatches(item Certificate, req DeleteRequest) bool {
	if req.ID != "" && item.ID != req.ID {
		return false
	}
	if req.ClusterID != "" && item.ClusterID != req.ClusterID {
		return false
	}
	if req.Namespace != "" && item.Namespace != req.Namespace {
		return false
	}
	if req.Name != "" && item.Name != req.Name {
		return false
	}
	return req.ID != "" || (req.ClusterID != "" && req.Namespace != "" && req.Name != "")
}

func (s Service) allowed(subject platformrbac.Subject, action string, clusterID string, namespace string) bool {
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.certificate",
		Action:   action,
		Scope:    platformrbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, action string, item Certificate, summary map[string]any) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.certificate", Name: item.Name},
		ResourceType:   "k8s.certificate",
		ResourceName:   item.Name,
		Action:         action,
		Scope:          fmt.Sprintf("cluster=%s namespace=%s", item.ClusterID, item.Namespace),
		Result:         "success",
		RequestSummary: summary,
	})
}

func normalizeCreateRequest(req CreateRequest) CreateRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Name = strings.TrimSpace(req.Name)
	req.CommonName = strings.TrimSpace(req.CommonName)
	req.CertificatePEM = strings.TrimSpace(req.CertificatePEM)
	req.PrivateKeyPEM = strings.TrimSpace(req.PrivateKeyPEM)
	req.NotAfter = strings.TrimSpace(req.NotAfter)
	return req
}

func parseNotAfter(value string) (time.Time, error) {
	if value == "" {
		return time.Now().UTC().Add(90 * 24 * time.Hour), nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

func certificateStatus(notAfter time.Time) string {
	daysRemaining := int64(notAfter.Sub(time.Now().UTC()).Hours() / 24)
	if daysRemaining <= 0 {
		return "expired"
	}
	if daysRemaining <= 30 {
		return "warning"
	}
	return "valid"
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
