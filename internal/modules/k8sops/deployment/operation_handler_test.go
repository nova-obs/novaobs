package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/platform/audit"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const deploymentYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`

func TestPreviewDeploymentRequiresPermission(t *testing.T) {
	router, _ := newDeploymentOperationRouter(t, testRBACRepo{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/deployments/preview", strings.NewReader(`{"cluster_id":"prod","yaml_content":"`+escapeJSON(deploymentYAML)+`"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func TestPreviewDeploymentRecordsAuditWithoutYAMLContent(t *testing.T) {
	router, auditStore := newDeploymentOperationRouter(t, deploymentWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/deployments/preview", strings.NewReader(`{"cluster_id":"prod","yaml_content":"`+escapeJSON(deploymentYAML)+`"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "orders-api")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.deployment", events[0].ResourceType)
	require.Equal(t, "preview", events[0].Action)
	require.NotContains(t, events[0].RequestSummary, "yaml_content")
	require.Equal(t, len(deploymentYAML), events[0].RequestSummary["yaml_bytes"])
}

func TestDeleteDeploymentRequiresFullResourceIdentity(t *testing.T) {
	router, _ := newDeploymentOperationRouter(t, deploymentWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/deployments", strings.NewReader(`{"identity":{"cluster_id":"prod","namespace":"orders","api_version":"apps/v1","kind":"Deployment","name":"orders-api"}}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")
}

func TestApplyDeploymentUsesDeployPermission(t *testing.T) {
	router, auditStore := newDeploymentOperationRouter(t, deploymentWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/deployments", strings.NewReader(`{"cluster_id":"prod","yaml_content":"`+escapeJSON(deploymentYAML)+`"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), "audit_id")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "deploy", events[0].Action)
}

func TestApplyDeploymentConfirmationMismatchReturnsSpecificErrorCode(t *testing.T) {
	router, _ := newDeploymentOperationRouter(t, deploymentWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, &recordingDeploymentApplier{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/deployments", strings.NewReader(`{"cluster_id":"prod","yaml_content":"`+escapeJSON(deploymentYAML)+`","preview_id":"wrong","confirmation_token":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "confirmation_mismatch", body.Error.Code)
	require.Contains(t, body.Error.Message, "重新预览")
}

func TestPreviewDeleteDeploymentReturnsConfirmationPlan(t *testing.T) {
	router, _ := newDeploymentOperationRouter(t, deploymentWriterRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/deployments/delete-preview", strings.NewReader(`{"identity":{"cluster_id":"prod","namespace":"orders","api_version":"apps/v1","kind":"Deployment","name":"orders-api","uid":"uid-orders-api"}}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "confirmation_token")
	require.Contains(t, recorder.Body.String(), "delete")
}

func TestInvalidDeploymentRequestMessageIncludesAPIServerValidationDetail(t *testing.T) {
	err := fmt.Errorf("%w: %w", ErrInvalidRequest, fmt.Errorf("%w: Deployment.apps \"orders-api\" is invalid: spec.selector: Required value", kubeclient.ErrResourceOperationInvalid))

	message := invalidDeploymentRequestMessage(err)

	require.Contains(t, message, "资源清单未通过 Kubernetes API Server 校验")
	require.Contains(t, message, "spec.selector")
	require.NotContains(t, message, "invalid_k8s_deployment_request")
	require.NotContains(t, message, "k8s_resource_operation_invalid")
}

func newDeploymentOperationRouter(t *testing.T, rbacRepo testRBACRepo, subjectsAndDependencies ...any) (*gin.Engine, *audit.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auditStore := audit.NewMemoryStore()
	subject := platformrbac.Subject{}
	dependencies := []any{platformrbac.NewService(rbacRepo), audit.NewService(auditStore)}
	for _, item := range subjectsAndDependencies {
		switch value := item.(type) {
		case platformrbac.Subject:
			subject = value
		default:
			dependencies = append(dependencies, item)
		}
	}
	service := NewService(NewMemoryReader(nil), dependencies...)
	router := gin.New()
	if subject.ID != "" {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
			ctx.Next()
		})
	}
	api := router.Group("/api/v1")
	api.POST("/k8s/deployments/preview", PreviewHandler(service))
	api.POST("/k8s/deployments/delete-preview", PreviewDeleteHandler(service))
	api.POST("/k8s/deployments", ApplyHandler(service))
	api.DELETE("/k8s/deployments", DeleteHandler(service))
	api.POST("/k8s/deployments/rollback", RollbackHandler(service))
	return router, auditStore
}

func deploymentWriterRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-deployment-writer": {
				ID: "role-deployment-writer",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.deployment", Action: "preview", ScopeMode: "namespace"},
					{Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"},
					{Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"},
					{Resource: "k8s.deployment", Action: "rollback", ScopeMode: "namespace"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-deployment-writer", Scope: platformrbac.Scope{ClusterID: "prod", Namespace: "orders"}},
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

func escapeJSON(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", `\n`), `"`, `\"`)
}
