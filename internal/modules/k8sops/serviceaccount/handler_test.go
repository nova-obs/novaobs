package serviceaccount

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateServiceAccountRequiresPermission(t *testing.T) {
	router, _ := newServiceAccountTestRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/service-accounts", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","name":"orders-reader"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-NovaObs-User", "user-1")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateServiceAccountRecordsAuditAndHidesSecrets(t *testing.T) {
	router, auditStore := newServiceAccountTestRouter(t, serviceAccountWriterRepo())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/service-accounts", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","name":"orders-reader","token":"must-not-leak"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-NovaObs-User", "user-1")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"orders-reader"`)
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
	require.NotContains(t, recorder.Body.String(), "token")

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.service-account", events[0].ResourceType)
	require.Equal(t, "create", events[0].Action)
	require.Equal(t, "success", events[0].Result)
	require.Equal(t, "[redacted]", events[0].RequestSummary["token"])
}

func TestDeleteServiceAccountRequiresPermissionAndRecordsAudit(t *testing.T) {
	router, auditStore := newServiceAccountTestRouter(t, serviceAccountWriterRepo(), ServiceAccount{
		ID:        "sa-prod-orders-reader",
		ClusterID: "prod",
		Namespace: "orders",
		Name:      "orders-reader",
		UID:       "uid-orders-reader",
		Status:    "active",
		Source:    "startorch",
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/service-accounts?cluster_id=prod&namespace=orders&name=orders-reader&uid=uid-orders-reader", nil)
	request.Header.Set("X-NovaObs-User", "user-1")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"deleted"`)

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.service-account", events[0].ResourceType)
	require.Equal(t, "delete", events[0].Action)
	require.Equal(t, "success", events[0].Result)
}

func newServiceAccountTestRouter(t *testing.T, rbacRepo testRBACRepo, seed ...ServiceAccount) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auditStore := audit.NewMemoryStore()
	service := NewService(NewMemoryRepository(seed), rbac.NewService(rbacRepo), audit.NewService(auditStore))

	router := gin.New()
	api := router.Group("/api/v1")
	api.POST("/k8s/service-accounts", CreateHandler(service))
	api.DELETE("/k8s/service-accounts", DeleteHandler(service))
	return router, auditStore
}

func serviceAccountWriterRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]rbac.Role{
			"role-sa-writer": {
				ID: "role-sa-writer",
				Permissions: []rbac.Permission{
					{Resource: "k8s.service-account", Action: "create", ScopeMode: "namespace"},
					{Resource: "k8s.service-account", Action: "delete", ScopeMode: "namespace"},
				},
			},
		},
		bindings: []rbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-sa-writer", Scope: rbac.Scope{ClusterID: "prod", Namespace: "orders"}},
		},
	}
}

type testRBACRepo struct {
	roles    map[string]rbac.Role
	bindings []rbac.Binding
}

func (r testRBACRepo) SaveRole(role rbac.Role) error {
	return nil
}

func (r testRBACRepo) GetRole(id string) (rbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return rbac.Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r testRBACRepo) SaveBinding(binding rbac.Binding) error {
	return nil
}

func (r testRBACRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]rbac.Binding, error) {
	out := make([]rbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}
