package certificate

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesRepositoryListsTLSSecretsAsCertificateMetadata(t *testing.T) {
	notAfter := time.Now().UTC().Add(60 * 24 * time.Hour)
	certPEM := testCertificatePEM(t, "*.example.internal", notAfter)
	client := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "wildcard-example", Namespace: "ingress", UID: "uid-cert"},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: []byte("hidden")},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "opaque-secret", Namespace: "ingress", UID: "uid-opaque"},
			Type:       corev1.SecretTypeOpaque,
		},
	)
	repo := NewKubernetesRepository(staticCertificateClientsetProvider{client: client}, allowCertificateReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "dev-admin", Type: "user"})

	items, err := repo.List(ctx, ListFilter{ClusterID: "test03-02", Namespace: "ingress"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "uid-cert", items[0].ID)
	require.Equal(t, "test03-02", items[0].ClusterID)
	require.Equal(t, "ingress", items[0].Namespace)
	require.Equal(t, "wildcard-example", items[0].Name)
	require.Equal(t, "*.example.internal", items[0].CommonName)
	require.NotEmpty(t, items[0].Fingerprint)
	require.Equal(t, "wildcard-example", items[0].SecretID)
	require.Equal(t, notAfter.Unix(), items[0].NotAfter.Unix())
	require.Equal(t, "valid", items[0].Status)
	require.Equal(t, "Kubernetes API", items[0].Source)
	require.Empty(t, items[0].PrivateKey)
}

func TestKubernetesRepositoryRequiresNamespaceAndReadPermission(t *testing.T) {
	repo := NewKubernetesRepository(staticCertificateClientsetProvider{client: fake.NewSimpleClientset()}, denyCertificateReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"})

	_, missingNamespaceErr := repo.List(ctx, ListFilter{ClusterID: "test03-02"})
	_, deniedErr := repo.List(ctx, ListFilter{ClusterID: "test03-02", Namespace: "ingress"})

	require.ErrorIs(t, missingNamespaceErr, ErrInvalidRequest)
	require.ErrorIs(t, deniedErr, ErrPermissionDenied)
}

func TestKubernetesRepositoryDisablesWrites(t *testing.T) {
	repo := NewKubernetesRepository(staticCertificateClientsetProvider{client: fake.NewSimpleClientset()}, allowCertificateReadAuthorizer{})

	_, createErr := repo.Create(context.Background(), Certificate{})
	_, deleteErr := repo.Delete(context.Background(), "uid-cert")

	require.ErrorIs(t, createErr, ErrWriteUnavailable)
	require.ErrorIs(t, deleteErr, ErrWriteUnavailable)
	require.True(t, repo.WritesUnavailable())
}

func testCertificatePEM(t *testing.T, commonName string, notAfter time.Time) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().UTC().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		DNSNames:     []string{commonName},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

type staticCertificateClientsetProvider struct {
	client kubernetes.Interface
}

func (p staticCertificateClientsetProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.client, nil
}

type allowCertificateReadAuthorizer struct{}

func (allowCertificateReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type denyCertificateReadAuthorizer struct{}

func (denyCertificateReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}
