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

func TestCreateRoleDoesNotOverwriteExistingRole(t *testing.T) {
	repo := NewMemoryRepository([]RoleResource{
		{ID: "role-prod-orders-orders-reader", ClusterID: "prod", Namespace: "orders", Kind: "Role", Name: "orders-reader", UID: "uid-existing", Rules: []Rule{{Resources: []string{"secrets"}, Verbs: []string{"get"}}}},
	}, nil)
	service := NewService(repo, platformrbac.NewService(rbacWriterRepo()), audit.NewService(audit.NewMemoryStore()))

	_, _, err := service.CreateRole(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, RoleRequest{
		ClusterID: "prod",
		Namespace: "orders",
		Kind:      "Role",
		Name:      "orders-reader",
		Rules:     []Rule{{Resources: []string{"pods"}, Verbs: []string{"list"}}},
	})

	require.ErrorIs(t, err, ErrAlreadyExists)
	items, listErr := repo.ListRoles(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders"})
	require.NoError(t, listErr)
	require.Len(t, items, 1)
	require.Equal(t, []string{"secrets"}, items[0].Rules[0].Resources)
	require.Equal(t, "uid-existing", items[0].UID)
}

func TestUpdateRoleRestoresExistingRoleWhenAuditFails(t *testing.T) {
	repo := NewMemoryRepository([]RoleResource{
		{ID: "role-prod-orders-orders-reader", ClusterID: "prod", Namespace: "orders", Kind: "Role", Name: "orders-reader", UID: "uid-existing", Rules: []Rule{{Resources: []string{"secrets"}, Verbs: []string{"get"}}}},
	}, nil)
	service := NewService(repo, platformrbac.NewService(rbacWriterRepo()), failingAuditor{})

	_, _, err := service.UpdateRole(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, RoleRequest{
		ClusterID: "prod",
		Namespace: "orders",
		Kind:      "Role",
		Name:      "orders-reader",
		UID:       "uid-existing",
		Rules:     []Rule{{Resources: []string{"pods"}, Verbs: []string{"list"}}},
	})

	require.Error(t, err)
	items, listErr := repo.ListRoles(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders"})
	require.NoError(t, listErr)
	require.Len(t, items, 1)
	require.Equal(t, []string{"secrets"}, items[0].Rules[0].Resources)
	require.Equal(t, "uid-existing", items[0].UID)
}

func TestCreateBindingDoesNotOverwriteExistingBinding(t *testing.T) {
	repo := NewMemoryRepository(nil, []BindingResource{
		{ID: "binding-prod-orders-orders-reader-binding", ClusterID: "prod", Namespace: "orders", Kind: "RoleBinding", Name: "orders-reader-binding", UID: "uid-binding", RoleRef: RoleRef{Kind: "Role", Name: "existing-role"}},
	})
	service := NewService(repo, platformrbac.NewService(rbacWriterRepo()), audit.NewService(audit.NewMemoryStore()))

	_, _, err := service.CreateBinding(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, BindingRequest{
		ClusterID: "prod",
		Namespace: "orders",
		Kind:      "RoleBinding",
		Name:      "orders-reader-binding",
		RoleRef:   RoleRef{Kind: "Role", Name: "new-role"},
		Subjects:  []Subject{{Kind: "ServiceAccount", Name: "orders-reader", Namespace: "orders"}},
	})

	require.ErrorIs(t, err, ErrAlreadyExists)
	items, listErr := repo.ListBindings(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders"})
	require.NoError(t, listErr)
	require.Len(t, items, 1)
	require.Equal(t, "existing-role", items[0].RoleRef.Name)
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

type failingAuditor struct{}

func (failingAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return audit.Event{}, errors.New("audit failed")
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
