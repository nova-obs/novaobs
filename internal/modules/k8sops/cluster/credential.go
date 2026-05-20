package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/platform/secret"
)

const ClusterCredentialSecretType = "k8s.cluster-credential"

var (
	ErrCredentialPermissionDenied = errors.New("permission_denied")
	ErrInvalidCredentialRequest   = errors.New("invalid_cluster_credential_request")
)

type CredentialSecretService interface {
	Create(ctx context.Context, req secret.CreateRequest) (secret.Secret, error)
	PlaintextByTypeAndScope(ctx context.Context, typ string, scope secret.Scope) ([]byte, secret.Secret, error)
	ListByType(ctx context.Context, typ string) ([]secret.Secret, error)
}

type CredentialAuthorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type CredentialAuditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type CredentialService struct {
	secrets    CredentialSecretService
	authorizer CredentialAuthorizer
	auditor    CredentialAuditor
}

type CredentialListFilter struct {
	ClusterID string
}

type UpsertCredentialRequest struct {
	ClusterID  string `json:"cluster_id"`
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
	ExpiresAt  time.Time
}

type CredentialMetadata struct {
	SecretID    string     `json:"secret_id"`
	ClusterID   string     `json:"cluster_id"`
	Name        string     `json:"name"`
	Fingerprint string     `json:"fingerprint"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	RotatedAt   *time.Time `json:"rotated_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type CredentialWriteResult struct {
	Item    CredentialMetadata `json:"item"`
	AuditID string             `json:"audit_id"`
}

func NewCredentialService(secrets CredentialSecretService, authorizer CredentialAuthorizer, auditor CredentialAuditor) CredentialService {
	if authorizer == nil {
		authorizer = denyCredentialAuthorizer{}
	}
	if auditor == nil {
		auditor = noopCredentialAuditor{}
	}
	return CredentialService{secrets: secrets, authorizer: authorizer, auditor: auditor}
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
		out = append(out, metadataFromSecret(item, "active"))
	}
	return out, nil
}

func (s CredentialService) Create(ctx context.Context, subject platformrbac.Subject, req UpsertCredentialRequest) (CredentialWriteResult, error) {
	return s.upsert(ctx, subject, req, "create")
}

func (s CredentialService) Rotate(ctx context.Context, subject platformrbac.Subject, req UpsertCredentialRequest) (CredentialWriteResult, error) {
	return s.upsert(ctx, subject, req, "rotate")
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
		return nil, err
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
	item, err := s.secrets.Create(ctx, secret.CreateRequest{
		Name:      req.Name,
		Type:      ClusterCredentialSecretType,
		Scope:     secret.Scope{ClusterID: req.ClusterID},
		Plaintext: []byte(req.Kubeconfig),
		CreatedBy: subject.ID,
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		return CredentialWriteResult{}, err
	}
	if action == "rotate" {
		item.RotatedAt = time.Now().UTC()
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
	return CredentialWriteResult{Item: metadataFromSecret(item, "active"), AuditID: event.ID}, nil
}

func (s CredentialService) allowed(subject platformrbac.Subject, clusterID string, action string) bool {
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.cluster-credential",
		Action:   action,
		Scope:    platformrbac.Scope{ClusterID: clusterID},
	})
	return decision.Allowed
}

func (s CredentialService) record(ctx context.Context, subject platformrbac.Subject, action string, clusterID string, item secret.Secret, summary map[string]any) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:        audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:     audit.Resource{Type: "k8s.cluster-credential", Name: clusterID},
		ResourceType: "k8s.cluster-credential",
		ResourceName: clusterID,
		Action:       action,
		Scope:        fmt.Sprintf("cluster=%s", clusterID),
		Result:       "success",
		RequestSummary: map[string]any{
			"cluster_id":  summary["cluster_id"],
			"secret_id":   summary["secret_id"],
			"fingerprint": summary["fingerprint"],
			"name":        summary["name"],
		},
		CreatedAt: time.Now().UTC(),
	})
}

func normalizeUpsertCredentialRequest(req UpsertCredentialRequest) UpsertCredentialRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Name = strings.TrimSpace(req.Name)
	req.Kubeconfig = strings.TrimSpace(req.Kubeconfig)
	return req
}

func looksLikeKubeconfig(value string) bool {
	return strings.Contains(value, "apiVersion:") && strings.Contains(value, "kind: Config") && strings.Contains(value, "clusters:")
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
