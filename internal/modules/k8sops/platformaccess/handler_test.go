package platformaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateBindingRequiresPlatformAccessManagePermission(t *testing.T) {
	router, _ := newPlatformAccessRouter(t, platformAccessReaderRepo(), platformrbac.Subject{ID: "reader-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-1",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateBindingGrantsNamespaceScopedResourceRead(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, auditStore := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-1",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.resource:read"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	decision := platformrbac.NewService(repo).Authorize(platformrbac.Subject{ID: "operator-1", Type: "user"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.True(t, decision.Allowed)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.platform-access", events[0].ResourceType)
}

func TestCreateBindingCanGrantTerminalExecSeparately(t *testing.T) {
	repo := platformAccessAdminRepo()
	router, _ := newPlatformAccessRouter(t, repo, platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	request := newJSONRequest(http.MethodPost, "/bindings", CreateBindingRequest{
		SubjectID:     "operator-2",
		SubjectType:   "user",
		ClusterID:     "prod",
		Namespace:     "orders",
		PermissionIDs: []string{"k8s.terminal:exec"},
	})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	rbacSvc := platformrbac.NewService(repo)
	terminal := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-2", Type: "user"}, platformrbac.Request{
		Resource: "k8s.terminal",
		Action:   "exec",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	read := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-2", Type: "user"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.True(t, terminal.Allowed)
	require.False(t, read.Allowed)
}

func TestListSubjectsIncludesSubjectsFromExistingBindings(t *testing.T) {
	router, _ := newPlatformAccessRouter(t, platformAccessAdminRepo(), platformrbac.Subject{ID: "admin-1", Type: "user"})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subjects", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "admin-1")
	require.Contains(t, recorder.Body.String(), "binding_refs")
}

func newPlatformAccessRouter(t *testing.T, repo *testPlatformAccessRepo, subject platformrbac.Subject) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auditStore := audit.NewMemoryStore()
	service := NewService(repo, platformrbac.NewService(repo), audit.NewService(auditStore))
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
		ctx.Next()
	})
	router.GET("/bindings", ListBindingsHandler(service))
	router.POST("/bindings", CreateBindingHandler(service))
	router.DELETE("/bindings/:id", DeleteBindingHandler(service))
	router.GET("/permissions", PermissionsHandler(service))
	router.GET("/subjects", ListSubjectsHandler(service))
	return router, auditStore
}

func newJSONRequest(method string, path string, body any) *http.Request {
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func platformAccessAdminRepo() *testPlatformAccessRepo {
	now := time.Now().UTC()
	repo := &testPlatformAccessRepo{roles: map[string]platformrbac.Role{}}
	_ = repo.SaveRole(platformrbac.Role{
		ID:   "role-platform-access-admin",
		Name: "平台 K8s 授权管理员",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.platform-access", Action: "manage", ScopeMode: "global"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = repo.SaveBinding(platformrbac.Binding{
		ID:          "binding-platform-access-admin",
		SubjectID:   "admin-1",
		SubjectType: "user",
		RoleID:      "role-platform-access-admin",
		Scope:       platformrbac.Scope{Global: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	return repo
}

func platformAccessReaderRepo() *testPlatformAccessRepo {
	now := time.Now().UTC()
	repo := &testPlatformAccessRepo{roles: map[string]platformrbac.Role{}}
	_ = repo.SaveRole(platformrbac.Role{
		ID:   "role-reader",
		Name: "资源只读",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = repo.SaveBinding(platformrbac.Binding{
		ID:          "binding-reader",
		SubjectID:   "reader-1",
		SubjectType: "user",
		RoleID:      "role-reader",
		Scope:       platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	return repo
}

type testPlatformAccessRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

func (r *testPlatformAccessRepo) SaveRole(role platformrbac.Role) error {
	r.roles[role.ID] = role
	return nil
}

func (r *testPlatformAccessRepo) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, ErrBindingNotFound
	}
	return role, nil
}

func (r *testPlatformAccessRepo) SaveBinding(binding platformrbac.Binding) error {
	for index, item := range r.bindings {
		if item.ID == binding.ID {
			r.bindings[index] = binding
			return nil
		}
	}
	r.bindings = append(r.bindings, binding)
	return nil
}

func (r *testPlatformAccessRepo) ListBindings() ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, len(r.bindings))
	copy(out, r.bindings)
	return out, nil
}

func (r *testPlatformAccessRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (r *testPlatformAccessRepo) DeleteBinding(id string) error {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.ID != id {
			out = append(out, binding)
		}
	}
	r.bindings = out
	return nil
}
