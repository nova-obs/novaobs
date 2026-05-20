package certificate

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"sort"
	"strings"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type KubernetesRepository struct {
	clients    ClientsetProvider
	authorizer Authorizer
}

func NewKubernetesRepository(clients ClientsetProvider, dependencies ...any) KubernetesRepository {
	repo := KubernetesRepository{clients: clients, authorizer: denyAuthorizer{}}
	for _, dependency := range dependencies {
		if value, ok := dependency.(Authorizer); ok && value != nil {
			repo.authorizer = value
		}
	}
	return repo
}

func (r KubernetesRepository) List(ctx context.Context, filter ListFilter) ([]Certificate, error) {
	filter = normalizeListFilter(filter)
	if filter.ClusterID == "" || filter.Namespace == "" {
		return nil, ErrInvalidRequest
	}
	if !r.allowed(ctx, filter.ClusterID, filter.Namespace) {
		return nil, ErrPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	result, err := client.CoreV1().Secrets(filter.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "type=" + string(corev1.SecretTypeTLS),
	})
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	items := make([]Certificate, 0, len(result.Items))
	for _, item := range result.Items {
		if item.Type != corev1.SecretTypeTLS {
			continue
		}
		certificate := mapKubernetesTLSSecret(filter.ClusterID, item)
		if query != "" && !strings.Contains(strings.ToLower(certificate.Name), query) && !strings.Contains(strings.ToLower(certificate.CommonName), query) {
			continue
		}
		items = append(items, certificate)
	}
	sort.SliceStable(items, func(left, right int) bool {
		less := items[left].NotAfter.Before(items[right].NotAfter)
		if strings.EqualFold(filter.Order, "desc") {
			return !less
		}
		return less
	})
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) Create(context.Context, Certificate) (Certificate, error) {
	return Certificate{}, ErrWriteUnavailable
}

func (r KubernetesRepository) Delete(context.Context, string) (Certificate, error) {
	return Certificate{}, ErrWriteUnavailable
}

func (r KubernetesRepository) WritesUnavailable() bool {
	return true
}

func (r KubernetesRepository) allowed(ctx context.Context, clusterID string, namespace string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	decision := r.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.certificate",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}

func normalizeListFilter(filter ListFilter) ListFilter {
	filter.ClusterID = strings.TrimSpace(filter.ClusterID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	filter.Query = strings.TrimSpace(filter.Query)
	return filter
}

func mapKubernetesTLSSecret(clusterID string, item corev1.Secret) Certificate {
	certPEM := item.Data[corev1.TLSCertKey]
	parsed, fingerprintValue, ok := parseCertificateMetadata(certPEM)
	commonName := ""
	notAfter := item.CreationTimestamp.Time
	status := "unknown"
	if ok {
		commonName = parsed.Subject.CommonName
		if commonName == "" && len(parsed.DNSNames) > 0 {
			commonName = parsed.DNSNames[0]
		}
		notAfter = parsed.NotAfter.UTC()
		status = certificateStatus(notAfter)
	}
	return Certificate{
		ID:          string(item.UID),
		ClusterID:   clusterID,
		Namespace:   item.Namespace,
		Name:        item.Name,
		CommonName:  commonName,
		Fingerprint: fingerprintValue,
		SecretID:    item.Name,
		NotAfter:    notAfter,
		Status:      status,
		Source:      "Kubernetes API",
	}
}

func parseCertificateMetadata(certPEM []byte) (*x509.Certificate, string, bool) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, "", false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, "", false
	}
	sum := sha256.Sum256(block.Bytes)
	return cert, "sha256:" + hex.EncodeToString(sum[:])[:12], true
}
