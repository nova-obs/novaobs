package terminal

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

func TestExecRequiresPermission(t *testing.T) {
	router, _ := newTerminalTestRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/terminal/exec", strings.NewReader(execBody("get pods")))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestExecAcceptsReadOnlyCommandAndRecordsAudit(t *testing.T) {
	router, auditStore := newTerminalTestRouter(t, terminalWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/terminal/exec", strings.NewReader(execBody("kubectl get pods -n orders")))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"accepted"`)
	require.Contains(t, recorder.Body.String(), `"mode":"dry_run"`)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.terminal", events[0].ResourceType)
	require.Equal(t, "exec", events[0].Action)
	require.Equal(t, "accepted", events[0].Result)
	require.Equal(t, "get", events[0].RequestSummary["verb"])
}

func TestExecBlocksDangerousCommandAndRecordsAudit(t *testing.T) {
	router, auditStore := newTerminalTestRouter(t, terminalWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/terminal/exec", strings.NewReader(execBody("delete pod orders-api")))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "k8s_terminal_command_blocked")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "blocked", events[0].Result)
	require.Equal(t, "delete", events[0].RequestSummary["verb"])
}

func TestExecBlocksShellMetaCharacters(t *testing.T) {
	router, auditStore := newTerminalTestRouter(t, terminalWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/terminal/exec", strings.NewReader(execBody("get pods | cat")))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "shell")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "blocked", events[0].Result)
}

func newTerminalTestRouter(t *testing.T, rbacRepo testRBACRepo, subjects ...platformrbac.Subject) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	subject := platformrbac.Subject{}
	if len(subjects) > 0 {
		subject = subjects[0]
	}
	auditStore := audit.NewMemoryStore()
	service := NewService(platformrbac.NewService(rbacRepo), audit.NewService(auditStore))
	router := gin.New()
	if subject.ID != "" {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
			ctx.Next()
		})
	}
	api := router.Group("/api/v1")
	api.POST("/k8s/terminal/exec", ExecHandler(service))
	return router, auditStore
}

func terminalWriterRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-terminal-writer": {
				ID: "role-terminal-writer",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-terminal-writer", Scope: platformrbac.Scope{ClusterID: "prod", Namespace: "orders"}},
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

func execBody(command string) string {
	return `{"cluster_id":"prod","namespace":"orders","command":"` + command + `"}`
}
