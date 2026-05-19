package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateRoleRequiresPermission(t *testing.T) {
	router, _ := newRBACTestRouter(t, testPlatformRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/rbac/roles", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","kind":"Role","name":"orders-reader","rules":[{"api_groups":[""],"resources":["pods"],"verbs":["get"]}]}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateRoleRecordsAudit(t *testing.T) {
	router, auditStore := newRBACTestRouter(t, rbacWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/rbac/roles", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","kind":"Role","name":"orders-reader","rules":[{"api_groups":[""],"resources":["pods"],"verbs":["get"]}]}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"audit_id"`)
	require.Contains(t, recorder.Body.String(), `"orders-reader"`)

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.rbac", events[0].ResourceType)
	require.Equal(t, "role.create", events[0].Action)
	require.Equal(t, "success", events[0].Result)
}

func TestDeleteBindingRequiresPermission(t *testing.T) {
	router, _ := newRBACTestRouter(t, testPlatformRBACRepo{}, platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/rbac/bindings?cluster_id=prod&namespace=orders&kind=RoleBinding&name=orders-reader-binding&uid=uid-binding", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateBindingRecordsAudit(t *testing.T) {
	router, auditStore := newRBACTestRouter(t, rbacWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/rbac/bindings", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","kind":"RoleBinding","name":"orders-reader-binding","role_ref":{"kind":"Role","name":"orders-reader"},"subjects":[{"kind":"ServiceAccount","name":"orders-reader","namespace":"orders"}]}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"audit_id"`)
	require.Contains(t, recorder.Body.String(), `"orders-reader-binding"`)

	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.rbac", events[0].ResourceType)
	require.Equal(t, "binding.create", events[0].Action)
}

func newRBACTestRouter(t *testing.T, rbacRepo testPlatformRBACRepo, subjects ...platformrbac.Subject) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auditStore := audit.NewMemoryStore()
	service := NewService(NewMemoryRepository(nil, nil), platformrbac.NewService(rbacRepo), audit.NewService(auditStore))
	router := gin.New()
	if len(subjects) > 0 {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subjects[0]))
		})
	}
	api := router.Group("/api/v1")
	api.GET("/k8s/rbac/roles", ListRolesHandler(service))
	api.POST("/k8s/rbac/roles", CreateRoleHandler(service))
	api.PUT("/k8s/rbac/roles", UpdateRoleHandler(service))
	api.DELETE("/k8s/rbac/roles", DeleteRoleHandler(service))
	api.GET("/k8s/rbac/bindings", ListBindingsHandler(service))
	api.POST("/k8s/rbac/bindings", CreateBindingHandler(service))
	api.DELETE("/k8s/rbac/bindings", DeleteBindingHandler(service))
	return router, auditStore
}

func rbacWriterRepo() testPlatformRBACRepo {
	return testPlatformRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-rbac-writer": {
				ID: "role-rbac-writer",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.rbac", Action: "create", ScopeMode: "namespace"},
					{Resource: "k8s.rbac", Action: "update", ScopeMode: "namespace"},
					{Resource: "k8s.rbac", Action: "delete", ScopeMode: "namespace"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-rbac-writer", Scope: platformrbac.Scope{ClusterID: "prod", Namespace: "orders"}},
		},
	}
}

type testPlatformRBACRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

func (r testPlatformRBACRepo) SaveRole(role platformrbac.Role) error {
	return nil
}

func (r testPlatformRBACRepo) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r testPlatformRBACRepo) SaveBinding(binding platformrbac.Binding) error {
	return nil
}

func (r testPlatformRBACRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}
