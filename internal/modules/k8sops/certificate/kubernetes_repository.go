package certificate

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"sort"
	"strings"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func (r KubernetesRepository) Create(ctx context.Context, item Certificate) (Certificate, error) {
	item.ClusterID = strings.TrimSpace(item.ClusterID)
	item.Namespace = strings.TrimSpace(item.Namespace)
	item.Name = strings.TrimSpace(item.Name)
	if item.ClusterID == "" || item.Namespace == "" || item.Name == "" || strings.TrimSpace(item.Certificate) == "" || strings.TrimSpace(item.PrivateKey) == "" {
		return Certificate{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, item.ClusterID)
	if err != nil {
		return Certificate{}, err
	}
	created, err := client.CoreV1().Secrets(item.Namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: item.Name, Namespace: item.Namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte(item.Certificate),
			corev1.TLSPrivateKeyKey: []byte(item.PrivateKey),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return Certificate{}, ErrAlreadyExists
		}
		return Certificate{}, err
	}
	return mapKubernetesTLSSecret(item.ClusterID, *created), nil
}

func (r KubernetesRepository) Delete(ctx context.Context, req DeleteRequest) (Certificate, error) {
	req = normalizeDeleteRequest(req)
	if req.ID == "" || req.ClusterID == "" || req.Namespace == "" || req.Name == "" {
		return Certificate{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, req.ClusterID)
	if err != nil {
		return Certificate{}, err
	}
	existing, err := client.CoreV1().Secrets(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return Certificate{}, ErrNotFound
		}
		return Certificate{}, err
	}
	if existing.Type != corev1.SecretTypeTLS || string(existing.UID) != req.ID {
		return Certificate{}, ErrNotFound
	}
	deleted := mapKubernetesTLSSecret(req.ClusterID, *existing)
	if err := client.CoreV1().Secrets(req.Namespace).Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return Certificate{}, ErrNotFound
		}
		return Certificate{}, err
	}
	return deleted, nil
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
