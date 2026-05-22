package template

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

func TestCreateTemplateRequiresPermission(t *testing.T) {
	router, _ := newTemplateTestRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/templates", strings.NewReader(`{"name":"orders-deploy","type":"Deployment","yaml_content":"kind: Deployment"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestCreateTemplateRecordsSanitizedAudit(t *testing.T) {
	router, auditStore := newTemplateTestRouter(t, templateWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/templates", strings.NewReader(`{"name":"orders-deploy","type":"Deployment","yaml_content":"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: orders-api","variables":[{"name":"image","required":true}]}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), "audit_id")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.template", events[0].ResourceType)
	require.Equal(t, "create", events[0].Action)
	require.NotContains(t, events[0].RequestSummary, "yaml_content")
	require.Equal(t, []string{"image"}, events[0].RequestSummary["variables"])
}

func TestRenderTemplateRequiresRequiredVariablesAndRecordsAudit(t *testing.T) {
	seed := Template{
		ID:          "tpl-1",
		Name:        "orders-deploy",
		Type:        "Deployment",
		YAMLContent: "image: <<image>>\nreplicas: <<replicas>>",
		Variables:   []Variable{{Name: "image", Required: true}, {Name: "replicas", Required: false}},
	}
	router, auditStore := newTemplateTestRouter(t, templateWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, seed)

	missingRecorder := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/templates/render", strings.NewReader(`{"id":"tpl-1","variables":{"replicas":"2"}}`))
	missingRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(missingRecorder, missingRequest)
	require.Equal(t, http.StatusBadRequest, missingRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/templates/render", strings.NewReader(`{"id":"tpl-1","variables":{"image":"orders:v2","replicas":"2"}}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "orders:v2")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "render", events[0].Action)
	require.Equal(t, []string{"image", "replicas"}, events[0].RequestSummary["variable_keys"])
}

func TestBaseTemplateHandlerReturnsBuiltInTemplateWithoutAudit(t *testing.T) {
	router, auditStore := newTemplateTestRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/templates/base?type=Gateway", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Gateway")
	require.Contains(t, recorder.Body.String(), "novaobs-base")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestUpdateTemplateRollsBackOnAuditFailure(t *testing.T) {
	repo := NewMemoryRepository([]Template{{ID: "tpl-1", Name: "orders-deploy", Type: "Deployment", YAMLContent: "kind: Deployment"}})
	service := NewService(repo, platformrbac.NewService(templateWriterRepo()), failingAuditor{})

	_, _, err := service.Update(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, UpsertRequest{
		ID:          "tpl-1",
		Name:        "orders-deploy-v2",
		Type:        "Deployment",
		YAMLContent: "kind: Service",
	})

	require.Error(t, err)
	item, getErr := repo.Get(context.Background(), "tpl-1")
	require.NoError(t, getErr)
	require.Equal(t, "orders-deploy", item.Name)
	require.Equal(t, "kind: Deployment", item.YAMLContent)
}

func newTemplateTestRouter(t *testing.T, rbacRepo testRBACRepo, subjectsAndSeeds ...any) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	subject := platformrbac.Subject{}
	seeds := []Template{}
	for _, item := range subjectsAndSeeds {
		switch value := item.(type) {
		case platformrbac.Subject:
			subject = value
		case Template:
			seeds = append(seeds, value)
		}
	}
	auditStore := audit.NewMemoryStore()
	service := NewService(NewMemoryRepository(seeds), platformrbac.NewService(rbacRepo), audit.NewService(auditStore))
	router := gin.New()
	if subject.ID != "" {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
			ctx.Next()
		})
	}
	api := router.Group("/api/v1")
	api.GET("/k8s/templates", ListHandler(service))
	api.POST("/k8s/templates", CreateHandler(service))
	api.PUT("/k8s/templates", UpdateHandler(service))
	api.DELETE("/k8s/templates/:id", DeleteHandler(service))
	api.POST("/k8s/templates/render", RenderHandler(service))
	api.GET("/k8s/templates/base", BaseTemplateHandler())
	return router, auditStore
}

func templateWriterRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-template-writer": {
				ID: "role-template-writer",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.template", Action: "create", ScopeMode: "global"},
					{Resource: "k8s.template", Action: "update", ScopeMode: "global"},
					{Resource: "k8s.template", Action: "delete", ScopeMode: "global"},
					{Resource: "k8s.template", Action: "render", ScopeMode: "global"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-template-writer", Scope: platformrbac.Scope{Global: true}},
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

type failingAuditor struct{}

func (failingAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return audit.Event{}, errors.New("audit down")
}
