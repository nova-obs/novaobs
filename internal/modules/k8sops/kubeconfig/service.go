package kubeconfig

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

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_kubeconfig_request")
)

type SecretService interface {
	Create(ctx context.Context, req secret.CreateRequest) (secret.Secret, error)
	Plaintext(ctx context.Context, id string) ([]byte, secret.Secret, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Service struct {
	secrets    SecretService
	authorizer Authorizer
	auditor    Auditor
}

func NewService(secrets SecretService, authorizer Authorizer, auditor Auditor) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	if auditor == nil {
		auditor = noopAuditor{}
	}
	return Service{secrets: secrets, authorizer: authorizer, auditor: auditor}
}

func (s Service) Create(ctx context.Context, subject platformrbac.Subject, req CreateRequest) (CreateResult, error) {
	req = normalizeCreateRequest(req)
	if req.ClusterID == "" || req.Namespace == "" || req.ServiceAccount == "" {
		return CreateResult{}, ErrInvalidRequest
	}
	if !s.allowed(subject, req.ClusterID, req.Namespace) {
		return CreateResult{}, ErrPermissionDenied
	}
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	metadata, err := s.secrets.Create(ctx, secret.CreateRequest{
		Name:      fmt.Sprintf("%s-%s-kubeconfig", req.Namespace, req.ServiceAccount),
		Type:      "kubeconfig",
		Scope:     secret.Scope{ClusterID: req.ClusterID, Namespace: req.Namespace},
		Plaintext: []byte(renderKubeconfig(req)),
		CreatedBy: subject.ID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return CreateResult{}, err
	}
	event, err := s.record(ctx, subject, "export", metadata.ID, req.ClusterID, req.Namespace, map[string]any{
		"cluster_id":      req.ClusterID,
		"namespace":       req.Namespace,
		"service_account": req.ServiceAccount,
		"token":           req.Token,
		"secret_id":       metadata.ID,
	})
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{SecretID: metadata.ID, Fingerprint: metadata.Fingerprint, ExpiresAt: expiresAt, AuditID: event.ID}, nil
}

func (s Service) Export(ctx context.Context, subject platformrbac.Subject, req ExportRequest) (ExportResult, error) {
	req.SecretID = strings.TrimSpace(req.SecretID)
	if req.SecretID == "" {
		return ExportResult{}, ErrInvalidRequest
	}
	plaintext, metadata, err := s.secrets.Plaintext(ctx, req.SecretID)
	if err != nil {
		return ExportResult{}, err
	}
	if !s.allowed(subject, metadata.Scope.ClusterID, metadata.Scope.Namespace) {
		return ExportResult{}, ErrPermissionDenied
	}
	event, err := s.record(ctx, subject, "export.plaintext", metadata.ID, metadata.Scope.ClusterID, metadata.Scope.Namespace, map[string]any{
		"secret_id":  metadata.ID,
		"cluster_id": metadata.Scope.ClusterID,
		"namespace":  metadata.Scope.Namespace,
	})
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Kubeconfig: string(plaintext), AuditID: event.ID}, nil
}

func (s Service) allowed(subject platformrbac.Subject, clusterID string, namespace string) bool {
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.kubeconfig",
		Action:   "export",
		Scope:    platformrbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, action string, name string, clusterID string, namespace string, summary map[string]any) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.kubeconfig", Name: name},
		ResourceType:   "k8s.kubeconfig",
		ResourceName:   name,
		Action:         action,
		Scope:          fmt.Sprintf("cluster=%s namespace=%s", clusterID, namespace),
		Result:         "success",
		RequestSummary: summary,
	})
}

func normalizeCreateRequest(req CreateRequest) CreateRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.ServiceAccount = strings.TrimSpace(req.ServiceAccount)
	return req
}

func renderKubeconfig(req CreateRequest) string {
	return fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: %s-context\nclusters:\n- name: %s\nusers:\n- name: %s\n", req.ServiceAccount, req.ClusterID, req.ServiceAccount)
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
