package certificate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaapm/internal/modules/k8sops/cluster"
	"novaapm/internal/platform/audit"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/platform/secret"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateCertificateRequiresPermission(t *testing.T) {
	router, _ := newCertificateTestRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/certificates", strings.NewReader(certificateCreateBody()))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateCertificateStoresPrivateKeyAsSecretAndSanitizesAudit(t *testing.T) {
	router, auditStore := newCertificateTestRouter(t, certificateWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/certificates", strings.NewReader(certificateCreateBody()))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), "secret_id")
	require.NotContains(t, recorder.Body.String(), "PRIVATE KEY")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.certificate", events[0].ResourceType)
	require.Equal(t, "create", events[0].Action)
	require.Equal(t, "[redacted]", events[0].RequestSummary["private_key_pem"])
}

func TestCreateCertificateBlocksReadOnlyClusterBeforeSecretWrite(t *testing.T) {
	auditStore := audit.NewMemoryStore()
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	service := NewService(NewMemoryRepository(nil), platformrbac.NewService(certificateWriterRepo()), audit.NewService(auditStore), secretSvc, staticClusterPolicy{readOnly: true})

	_, _, err := service.Create(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, CreateRequest{
		ClusterID:      "prod",
		Namespace:      "ingress",
		Name:           "wildcard-prod",
		CommonName:     "*.prod.example.com",
		CertificatePEM: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		PrivateKeyPEM:  "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
		NotAfter:       "2030-01-01T00:00:00Z",
	})

	require.ErrorIs(t, err, cluster.ErrClusterReadOnly)
	events, listErr := auditStore.List(context.Background())
	require.NoError(t, listErr)
	require.Empty(t, events)
}

func TestDeleteCertificateRecordsAudit(t *testing.T) {
	router, auditStore := newCertificateTestRouter(t, certificateWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, Certificate{
		ID: "cert-1", ClusterID: "prod", Namespace: "ingress", Name: "wildcard-prod", CommonName: "*.prod.example.com", Fingerprint: "sha256:abc",
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/certificates/cert-1", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "audit_id")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "delete", events[0].Action)
}

func newCertificateTestRouter(t *testing.T, rbacRepo testRBACRepo, subjectsAndSeeds ...any) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	subject := platformrbac.Subject{}
	seeds := []Certificate{}
	for _, item := range subjectsAndSeeds {
		switch value := item.(type) {
		case platformrbac.Subject:
			subject = value
		case Certificate:
			seeds = append(seeds, value)
		}
	}
	auditStore := audit.NewMemoryStore()
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	service := NewService(NewMemoryRepository(seeds), platformrbac.NewService(rbacRepo), audit.NewService(auditStore), secretSvc)
	router := gin.New()
	if subject.ID != "" {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
			ctx.Next()
		})
	}
	api := router.Group("/api/v1")
	api.POST("/k8s/certificates", CreateHandler(service))
	api.DELETE("/k8s/certificates/:id", DeleteHandler(service))
	return router, auditStore
}

func certificateWriterRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-certificate-writer": {
				ID: "role-certificate-writer",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.certificate", Action: "create", ScopeMode: "namespace"},
					{Resource: "k8s.certificate", Action: "delete", ScopeMode: "namespace"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-certificate-writer", Scope: platformrbac.Scope{ClusterID: "prod", Namespace: "ingress"}},
		},
	}
}

type testRBACRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

type staticClusterPolicy struct {
	readOnly bool
}

func (p staticClusterPolicy) IsReadOnly(context.Context, string) (bool, error) {
	return p.readOnly, nil
}

func (r testRBACRepo) SaveRole(role platformrbac.Role) error { return nil }

func (r testRBACRepo) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r testRBACRepo) SaveBinding(binding platformrbac.Binding) error { return nil }

func (r testRBACRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}

func certificateCreateBody() string {
	return `{"cluster_id":"prod","namespace":"ingress","name":"wildcard-prod","common_name":"*.prod.example.com","certificate_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----","private_key_pem":"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----","not_after":"2026-08-19T00:00:00Z"}`
}
