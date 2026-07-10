package cluster

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/platform/secret"
)

const ClusterCredentialSecretType = "k8s.cluster-credential"

var (
	ErrCredentialPermissionDenied = errors.New("permission_denied")
	ErrCredentialNotFound         = errors.New("cluster_credential_not_found")
	ErrInvalidCredentialRequest   = errors.New("invalid_cluster_credential_request")
	ErrCredentialValidationFailed = errors.New("cluster_credential_validation_failed")
)

type CredentialSecretService interface {
	Create(ctx context.Context, req secret.CreateRequest) (secret.Secret, error)
	Plaintext(ctx context.Context, id string) ([]byte, secret.Secret, error)
	PlaintextByTypeAndScope(ctx context.Context, typ string, scope secret.Scope) ([]byte, secret.Secret, error)
	ListByType(ctx context.Context, typ string) ([]secret.Secret, error)
}

type CredentialAuthorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type CredentialAuditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type CredentialValidator interface {
	ValidateCredential(ctx context.Context, clusterID string, kubeconfig []byte) (kubeclient.CapabilitySnapshot, error)
}

type CredentialService struct {
	secrets    CredentialSecretService
	authorizer CredentialAuthorizer
	auditor    CredentialAuditor
	validator  CredentialValidator
}

type CredentialListFilter struct {
	ClusterID string
}

type UpsertCredentialRequest struct {
	ClusterID  string    `json:"cluster_id"`
	Name       string    `json:"name"`
	Kubeconfig string    `json:"kubeconfig"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type RollbackCredentialRequest struct {
	ClusterID string `json:"cluster_id"`
	SecretID  string `json:"secret_id"`
}

type CredentialMetadata struct {
	SecretID    string     `json:"secret_id"`
	ClusterID   string     `json:"cluster_id"`
	Name        string     `json:"name"`
	Fingerprint string     `json:"fingerprint"`
	Status      string     `json:"status"`
	Active      bool       `json:"active"`
	Version     int        `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	RotatedAt   *time.Time `json:"rotated_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Expired     bool       `json:"expired"`
	ExpiresSoon bool       `json:"expires_soon"`
}

type CredentialWriteResult struct {
	Item    CredentialMetadata `json:"item"`
	AuditID string             `json:"audit_id"`
	Probe   *ProbeResult       `json:"probe,omitempty"`
}

func NewCredentialService(secrets CredentialSecretService, authorizer CredentialAuthorizer, auditor CredentialAuditor, dependencies ...any) CredentialService {
	if authorizer == nil {
		authorizer = denyCredentialAuthorizer{}
	}
	if auditor == nil {
		auditor = noopCredentialAuditor{}
	}
	service := CredentialService{secrets: secrets, authorizer: authorizer, auditor: auditor}
	for _, dependency := range dependencies {
		if validator, ok := dependency.(CredentialValidator); ok && validator != nil {
			service.validator = validator
		}
	}
	return service
}

func (s CredentialService) List(ctx context.Context, filter CredentialListFilter) ([]CredentialMetadata, error) {
	filter.ClusterID = strings.TrimSpace(filter.ClusterID)
	items, err := s.secrets.ListByType(ctx, ClusterCredentialSecretType)
	if err != nil {
		return nil, err
	}
	out := make([]CredentialMetadata, 0, len(items))
	for _, item := range items {
		if filter.ClusterID != "" && item.Scope.ClusterID != filter.ClusterID {
			continue
		}
		out = append(out, metadataFromSecret(item, "unknown"))
	}
	return credentialHistory(out, time.Now().UTC()), nil
}

func (s CredentialService) Create(ctx context.Context, subject platformrbac.Subject, req UpsertCredentialRequest) (CredentialWriteResult, error) {
	return s.upsert(ctx, subject, req, "create")
}

func (s CredentialService) Rotate(ctx context.Context, subject platformrbac.Subject, req UpsertCredentialRequest) (CredentialWriteResult, error) {
	return s.upsert(ctx, subject, req, "rotate")
}

func (s CredentialService) Rollback(ctx context.Context, subject platformrbac.Subject, req RollbackCredentialRequest) (CredentialWriteResult, error) {
	req = normalizeRollbackCredentialRequest(req)
	if req.ClusterID == "" || req.SecretID == "" {
		return CredentialWriteResult{}, ErrInvalidCredentialRequest
	}
	if !s.allowed(subject, req.ClusterID, "rollback") {
		return CredentialWriteResult{}, ErrCredentialPermissionDenied
	}
	plaintext, source, err := s.secrets.Plaintext(ctx, req.SecretID)
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return CredentialWriteResult{}, ErrCredentialNotFound
		}
		return CredentialWriteResult{}, err
	}
	if source.Type != ClusterCredentialSecretType || source.Scope.ClusterID != req.ClusterID {
		return CredentialWriteResult{}, ErrCredentialNotFound
	}
	if !looksLikeKubeconfig(string(plaintext)) {
		return CredentialWriteResult{}, ErrInvalidCredentialRequest
	}
	probe, err := s.validateCredential(ctx, req.ClusterID, plaintext)
	if err != nil {
		return CredentialWriteResult{}, err
	}
	item, err := s.secrets.Create(ctx, secret.CreateRequest{
		Name:      source.Name,
		Type:      ClusterCredentialSecretType,
		Scope:     secret.Scope{ClusterID: req.ClusterID},
		Plaintext: plaintext,
		CreatedBy: subject.ID,
		RotatedAt: time.Now().UTC(),
		ExpiresAt: source.ExpiresAt,
	})
	if err != nil {
		return CredentialWriteResult{}, err
	}
	event, err := s.record(ctx, subject, "rollback", req.ClusterID, item, map[string]any{
		"cluster_id":         req.ClusterID,
		"secret_id":          item.ID,
		"fingerprint":        item.Fingerprint,
		"name":               item.Name,
		"source_secret_id":   source.ID,
		"source_fingerprint": source.Fingerprint,
	})
	if err != nil {
		return CredentialWriteResult{}, err
	}
	metadata := metadataFromSecret(item, "active")
	metadata.Active = true
	return CredentialWriteResult{Item: metadata, AuditID: event.ID, Probe: probe}, nil
}

func (s CredentialService) Kubeconfig(ctx context.Context, subject platformrbac.Subject, clusterID string) ([]byte, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return nil, ErrInvalidCredentialRequest
	}
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.cluster-credential",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: clusterID},
	})
	if !decision.Allowed {
		return nil, ErrCredentialPermissionDenied
	}
	plaintext, metadata, err := s.secrets.PlaintextByTypeAndScope(ctx, ClusterCredentialSecretType, secret.Scope{ClusterID: clusterID})
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}
	if !looksLikeKubeconfig(string(plaintext)) {
		return nil, ErrInvalidCredentialRequest
	}
	_, err = s.auditor.Record(ctx, audit.Event{
		Actor:        audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:     audit.Resource{Type: "k8s.cluster-credential", Name: clusterID},
		ResourceType: "k8s.cluster-credential",
		ResourceName: clusterID,
		Action:       "read",
		Scope:        fmt.Sprintf("cluster=%s", clusterID),
		Result:       "success",
		RequestSummary: map[string]any{
			"cluster_id":  clusterID,
			"secret_id":   metadata.ID,
			"fingerprint": metadata.Fingerprint,
		},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func (s CredentialService) upsert(ctx context.Context, subject platformrbac.Subject, req UpsertCredentialRequest, action string) (CredentialWriteResult, error) {
	req = normalizeUpsertCredentialRequest(req)
	if req.ClusterID == "" || req.Name == "" || !looksLikeKubeconfig(req.Kubeconfig) {
		return CredentialWriteResult{}, ErrInvalidCredentialRequest
	}
	if !s.allowed(subject, req.ClusterID, action) {
		return CredentialWriteResult{}, ErrCredentialPermissionDenied
	}
	probe, err := s.validateCredential(ctx, req.ClusterID, []byte(req.Kubeconfig))
	if err != nil {
		return CredentialWriteResult{}, err
	}
	item, err := s.secrets.Create(ctx, secret.CreateRequest{
		Name:      req.Name,
		Type:      ClusterCredentialSecretType,
		Scope:     secret.Scope{ClusterID: req.ClusterID},
		Plaintext: []byte(req.Kubeconfig),
		CreatedBy: subject.ID,
		RotatedAt: rotatedAtForAction(action),
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		return CredentialWriteResult{}, err
	}
	event, err := s.record(ctx, subject, action, req.ClusterID, item, map[string]any{
		"cluster_id":  req.ClusterID,
		"secret_id":   item.ID,
		"fingerprint": item.Fingerprint,
		"name":        item.Name,
	})
	if err != nil {
		return CredentialWriteResult{}, err
	}
	metadata := metadataFromSecret(item, "active")
	metadata.Active = true
	return CredentialWriteResult{Item: metadata, AuditID: event.ID, Probe: probe}, nil
}

func rotatedAtForAction(action string) time.Time {
	if action == "rotate" {
		return time.Now().UTC()
	}
	return time.Time{}
}

func (s CredentialService) allowed(subject platformrbac.Subject, clusterID string, action string) bool {
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.cluster-credential",
		Action:   action,
		Scope:    platformrbac.Scope{ClusterID: clusterID},
	})
	return decision.Allowed
}

func (s CredentialService) validateCredential(ctx context.Context, clusterID string, kubeconfig []byte) (*ProbeResult, error) {
	if s.validator == nil {
		return nil, nil
	}
	snapshot, err := s.validator.ValidateCredential(ctx, clusterID, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialValidationFailed, err)
	}
	return &ProbeResult{
		ClusterID:     clusterID,
		Status:        "connected",
		ServerVersion: snapshot.ServerVersion,
		ResourceCount: len(snapshot.Resources),
		Warnings:      append([]string{}, snapshot.Warnings...),
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s CredentialService) record(ctx context.Context, subject platformrbac.Subject, action string, clusterID string, item secret.Secret, summary map[string]any) (audit.Event, error) {
	requestSummary := map[string]any{
		"cluster_id":  summary["cluster_id"],
		"secret_id":   summary["secret_id"],
		"fingerprint": summary["fingerprint"],
		"name":        summary["name"],
	}
	if summary["source_secret_id"] != nil {
		requestSummary["source_secret_id"] = summary["source_secret_id"]
	}
	if summary["source_fingerprint"] != nil {
		requestSummary["source_fingerprint"] = summary["source_fingerprint"]
	}
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.cluster-credential", Name: clusterID},
		ResourceType:   "k8s.cluster-credential",
		ResourceName:   clusterID,
		Action:         action,
		Scope:          fmt.Sprintf("cluster=%s", clusterID),
		Result:         "success",
		RequestSummary: requestSummary,
		CreatedAt:      time.Now().UTC(),
	})
}

func normalizeUpsertCredentialRequest(req UpsertCredentialRequest) UpsertCredentialRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Name = strings.TrimSpace(req.Name)
	req.Kubeconfig = strings.TrimSpace(req.Kubeconfig)
	return req
}

func normalizeRollbackCredentialRequest(req RollbackCredentialRequest) RollbackCredentialRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.SecretID = strings.TrimSpace(req.SecretID)
	return req
}

func looksLikeKubeconfig(value string) bool {
	return strings.Contains(value, "apiVersion:") && strings.Contains(value, "kind: Config") && strings.Contains(value, "clusters:")
}

func credentialHistory(items []CredentialMetadata, now time.Time) []CredentialMetadata {
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].ClusterID != items[right].ClusterID {
			return items[left].ClusterID < items[right].ClusterID
		}
		return credentialFreshness(items[left]).After(credentialFreshness(items[right]))
	})
	groupSizes := map[string]int{}
	for _, item := range items {
		groupSizes[item.ClusterID]++
	}
	groupIndex := map[string]int{}
	for index := range items {
		item := &items[index]
		groupIndex[item.ClusterID]++
		active := groupIndex[item.ClusterID] == 1
		item.Active = active
		item.Version = groupSizes[item.ClusterID] - groupIndex[item.ClusterID] + 1
		if item.ExpiresAt != nil {
			item.Expired = !item.ExpiresAt.After(now)
			item.ExpiresSoon = !item.Expired && item.ExpiresAt.Sub(now) <= 7*24*time.Hour
		}
		switch {
		case item.Expired:
			item.Status = "expired"
		case active:
			item.Status = "active"
		default:
			item.Status = "superseded"
		}
	}
	return items
}

func credentialFreshness(item CredentialMetadata) time.Time {
	if item.RotatedAt != nil {
		return *item.RotatedAt
	}
	return item.CreatedAt
}

func metadataFromSecret(item secret.Secret, status string) CredentialMetadata {
	var rotatedAt *time.Time
	if !item.RotatedAt.IsZero() {
		rotatedAt = &item.RotatedAt
	}
	var expiresAt *time.Time
	if !item.ExpiresAt.IsZero() {
		expiresAt = &item.ExpiresAt
	}
	return CredentialMetadata{
		SecretID:    item.ID,
		ClusterID:   item.Scope.ClusterID,
		Name:        item.Name,
		Fingerprint: item.Fingerprint,
		Status:      status,
		CreatedAt:   item.CreatedAt,
		RotatedAt:   rotatedAt,
		ExpiresAt:   expiresAt,
	}
}

type denyCredentialAuthorizer struct{}

func (denyCredentialAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopCredentialAuditor struct{}

func (noopCredentialAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
