package kubeconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"novaapm/internal/modules/k8sops/cluster"
	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/platform/secret"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	ErrPermissionDenied   = errors.New("permission_denied")
	ErrInvalidRequest     = errors.New("invalid_kubeconfig_request")
	ErrTokenUnavailable   = errors.New("kubeconfig_token_unavailable")
	ErrCredentialRequired = errors.New("k8s_cluster_credential_required")
)

type SecretService interface {
	Create(ctx context.Context, req secret.CreateRequest) (secret.Secret, error)
	Plaintext(ctx context.Context, id string) ([]byte, secret.Secret, error)
	ListByType(ctx context.Context, typ string) ([]secret.Secret, error)
	PlaintextByTypeAndScope(ctx context.Context, typ string, scope secret.Scope) ([]byte, secret.Secret, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type TokenRequester interface {
	Token(ctx context.Context, req TokenRequest) (TokenResult, error)
}

type TokenRequest struct {
	ClusterID      string
	Namespace      string
	ServiceAccount string
	Duration       time.Duration
}

type TokenResult struct {
	Token     string
	ExpiresAt time.Time
}

type Service struct {
	secrets    SecretService
	authorizer Authorizer
	auditor    Auditor
	tokens     TokenRequester
}

func NewService(secrets SecretService, authorizer Authorizer, auditor Auditor, dependencies ...any) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	if auditor == nil {
		auditor = noopAuditor{}
	}
	service := Service{secrets: secrets, authorizer: authorizer, auditor: auditor}
	for _, dependency := range dependencies {
		if value, ok := dependency.(TokenRequester); ok && value != nil {
			service.tokens = value
		}
	}
	return service
}

func (s Service) Create(ctx context.Context, subject platformrbac.Subject, req CreateRequest) (CreateResult, error) {
	req = normalizeCreateRequest(req)
	if req.ClusterID == "" || req.Namespace == "" || req.ServiceAccount == "" {
		return CreateResult{}, ErrInvalidRequest
	}
	if !s.allowed(subject, req.ClusterID, req.Namespace) {
		return CreateResult{}, ErrPermissionDenied
	}
	tokenResult, err := s.serviceAccountToken(ctx, req)
	if err != nil {
		return CreateResult{}, err
	}
	kubeconfig, err := s.renderKubeconfig(ctx, req, tokenResult.Token)
	if err != nil {
		return CreateResult{}, err
	}
	expiresAt := tokenResult.ExpiresAt
	metadata, err := s.secrets.Create(ctx, secret.CreateRequest{
		Name:      fmt.Sprintf("%s-%s-kubeconfig", req.Namespace, req.ServiceAccount),
		Type:      "kubeconfig",
		Scope:     secret.Scope{ClusterID: req.ClusterID, Namespace: req.Namespace},
		Plaintext: kubeconfig,
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

func (s Service) serviceAccountToken(ctx context.Context, req CreateRequest) (TokenResult, error) {
	if req.Token != "" {
		return TokenResult{Token: req.Token, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}, nil
	}
	if s.tokens == nil {
		return TokenResult{}, ErrTokenUnavailable
	}
	return s.tokens.Token(ctx, TokenRequest{
		ClusterID:      req.ClusterID,
		Namespace:      req.Namespace,
		ServiceAccount: req.ServiceAccount,
		Duration:       24 * time.Hour,
	})
}

func (s Service) renderKubeconfig(ctx context.Context, req CreateRequest, token string) ([]byte, error) {
	if s.secrets == nil {
		return nil, ErrCredentialRequired
	}
	source, _, err := s.secrets.PlaintextByTypeAndScope(ctx, cluster.ClusterCredentialSecretType, secret.Scope{ClusterID: req.ClusterID})
	if err != nil {
		return nil, ErrCredentialRequired
	}
	sourceConfig, err := clientcmd.Load(source)
	if err != nil {
		return nil, err
	}
	sourceCluster := currentCluster(sourceConfig)
	if sourceCluster == nil || strings.TrimSpace(sourceCluster.Server) == "" {
		return nil, ErrCredentialRequired
	}
	clusterName := req.ClusterID
	userName := req.ServiceAccount
	contextName := fmt.Sprintf("%s/%s/%s", req.ClusterID, req.Namespace, req.ServiceAccount)
	return clientcmd.Write(clientcmdapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		CurrentContext: contextName,
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: {
				Server:                   sourceCluster.Server,
				CertificateAuthorityData: append([]byte{}, sourceCluster.CertificateAuthorityData...),
				InsecureSkipTLSVerify:    sourceCluster.InsecureSkipTLSVerify,
				TLSServerName:            sourceCluster.TLSServerName,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			userName: {Token: token},
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: {
				Cluster:   clusterName,
				AuthInfo:  userName,
				Namespace: req.Namespace,
			},
		},
	})
}

func currentCluster(config *clientcmdapi.Config) *clientcmdapi.Cluster {
	if config == nil {
		return nil
	}
	if config.CurrentContext != "" {
		contextValue := config.Contexts[config.CurrentContext]
		if contextValue != nil {
			if clusterValue := config.Clusters[contextValue.Cluster]; clusterValue != nil {
				return clusterValue
			}
		}
	}
	for _, clusterValue := range config.Clusters {
		return clusterValue
	}
	return nil
}

type KubernetesTokenRequester struct {
	clients kubeclient.ClientsetProvider
}

func NewKubernetesTokenRequester(clients kubeclient.ClientsetProvider) KubernetesTokenRequester {
	return KubernetesTokenRequester{clients: clients}
}

func (r KubernetesTokenRequester) Token(ctx context.Context, req TokenRequest) (TokenResult, error) {
	if r.clients == nil {
		return TokenResult{}, ErrTokenUnavailable
	}
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.ServiceAccount = strings.TrimSpace(req.ServiceAccount)
	if req.ClusterID == "" || req.Namespace == "" || req.ServiceAccount == "" {
		return TokenResult{}, ErrInvalidRequest
	}
	duration := req.Duration
	if duration <= 0 {
		duration = 24 * time.Hour
	}
	seconds := int64(duration.Seconds())
	client, err := r.clients.Clientset(ctx, req.ClusterID)
	if err != nil {
		return TokenResult{}, err
	}
	token, err := client.CoreV1().ServiceAccounts(req.Namespace).CreateToken(ctx, req.ServiceAccount, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{ExpirationSeconds: &seconds},
	}, metav1.CreateOptions{})
	if err != nil {
		return TokenResult{}, err
	}
	expiresAt := token.Status.ExpirationTimestamp.Time
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(duration)
	}
	return TokenResult{Token: token.Status.Token, ExpiresAt: expiresAt.UTC()}, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
