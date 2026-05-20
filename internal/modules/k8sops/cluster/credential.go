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
	PlaintextByTypeAndScope(ctx context.Context, typ string, scope secret.Scope) ([]byte, secret.Secret, error)
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

func NewCredentialService(secrets CredentialSecretService, authorizer CredentialAuthorizer, auditor CredentialAuditor) CredentialService {
	if authorizer == nil {
		authorizer = denyCredentialAuthorizer{}
	}
	if auditor == nil {
		auditor = noopCredentialAuditor{}
	}
	return CredentialService{secrets: secrets, authorizer: authorizer, auditor: auditor}
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

type denyCredentialAuthorizer struct{}

func (denyCredentialAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopCredentialAuditor struct{}

func (noopCredentialAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
