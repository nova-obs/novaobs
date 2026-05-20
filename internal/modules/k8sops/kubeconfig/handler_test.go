package kubeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/platform/secret"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateKubeconfigRequiresExportPermission(t *testing.T) {
	router, _ := newKubeconfigTestRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/kubeconfigs", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","service_account":"orders-reader"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateKubeconfigStoresSecretAndRecordsAudit(t *testing.T) {
	router, auditStore := newKubeconfigTestRouter(t, kubeconfigExporterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/kubeconfigs", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","service_account":"orders-reader","token":"must-not-leak"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"secret_id"`)
	require.Contains(t, recorder.Body.String(), `"fingerprint"`)
	require.Contains(t, recorder.Body.String(), `"audit_id"`)
	require.NotContains(t, recorder.Body.String(), "apiVersion")
	require.NotContains(t, recorder.Body.String(), "must-not-leak")

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.kubeconfig", events[0].ResourceType)
	require.Equal(t, "export", events[0].Action)
	require.Equal(t, "[redacted]", events[0].RequestSummary["token"])
}

func TestExportKubeconfigRequiresPermissionAndRecordsAudit(t *testing.T) {
	router, auditStore := newKubeconfigTestRouter(t, kubeconfigExporterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/kubeconfigs", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","service_account":"orders-reader"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRecorder, createRequest)
	require.Equal(t, http.StatusCreated, createRecorder.Code)

	secretID := extractCreatedSecretID(t, createRecorder.Body.String())
	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/kubeconfigs/export", strings.NewReader(`{"secret_id":"`+secretID+`"}`))
	exportRequest.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(exportRecorder, exportRequest)

	require.Equal(t, http.StatusOK, exportRecorder.Code)
	require.Contains(t, exportRecorder.Body.String(), "apiVersion")
	require.Contains(t, exportRecorder.Body.String(), `"audit_id"`)

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "export.plaintext", events[1].Action)
}

func newKubeconfigTestRouter(t *testing.T, rbacRepo testRBACRepo, subjects ...platformrbac.Subject) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auditStore := audit.NewMemoryStore()
	secretSvc := secret.NewService(newTestSecretRepo(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	service := NewService(secretSvc, platformrbac.NewService(rbacRepo), audit.NewService(auditStore))
	router := gin.New()
	if len(subjects) > 0 {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subjects[0]))
		})
	}
	api := router.Group("/api/v1")
	api.POST("/k8s/kubeconfigs", CreateHandler(service))
	api.POST("/k8s/kubeconfigs/export", ExportHandler(service))
	return router, auditStore
}

func extractCreatedSecretID(t *testing.T, raw string) string {
	t.Helper()
	var body struct {
		Data struct {
			SecretID string `json:"secret_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	require.NotEmpty(t, body.Data.SecretID)
	return body.Data.SecretID
}

type testSecretRepo struct {
	items map[string]secret.Secret
}

func newTestSecretRepo() *testSecretRepo {
	return &testSecretRepo{items: map[string]secret.Secret{}}
}

func (r *testSecretRepo) Save(ctx context.Context, item secret.Secret) error {
	r.items[item.ID] = item
	return nil
}

func (r *testSecretRepo) Get(ctx context.Context, id string) (secret.Secret, error) {
	item, ok := r.items[id]
	if !ok {
		return secret.Secret{}, errors.New("secret not found")
	}
	return item, nil
}

func (r *testSecretRepo) FindByTypeAndScope(ctx context.Context, typ string, scope secret.Scope) (secret.Secret, error) {
	for _, item := range r.items {
		if item.Type == typ && item.Scope.ClusterID == scope.ClusterID && item.Scope.Namespace == scope.Namespace && item.Scope.ServiceID == scope.ServiceID {
			return item, nil
		}
	}
	return secret.Secret{}, errors.New("secret not found")
}

func (r *testSecretRepo) ListByType(ctx context.Context, typ string) ([]secret.Secret, error) {
	out := make([]secret.Secret, 0, len(r.items))
	for _, item := range r.items {
		if item.Type == typ {
			out = append(out, item)
		}
	}
	return out, nil
}

func kubeconfigExporterRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-kubeconfig-exporter": {
				ID:          "role-kubeconfig-exporter",
				Permissions: []platformrbac.Permission{{Resource: "k8s.kubeconfig", Action: "export", ScopeMode: "namespace"}},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-kubeconfig-exporter", Scope: platformrbac.Scope{ClusterID: "prod", Namespace: "orders"}},
		},
	}
}

type testRBACRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
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
