package cluster

import (
	"encoding/json"
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

func TestClusterCredentialHandlersCreateRotateAndListMetadata(t *testing.T) {
	router := newCredentialRouter(t, clusterCredentialAdminRepo(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})
	body := `{"cluster_id":"prod","name":"prod-readonly","kubeconfig":"apiVersion: v1\nkind: Config\nclusters: []\n"}`

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/cluster-credentials", strings.NewReader(body))
	createRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRecorder, createRequest)

	rotateRecorder := httptest.NewRecorder()
	rotateRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/cluster-credentials/rotate", strings.NewReader(body))
	rotateRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rotateRecorder, rotateRequest)

	var createdBody struct {
		Data struct {
			Item CredentialMetadata `json:"item"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &createdBody))

	rollbackRecorder := httptest.NewRecorder()
	rollbackRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/cluster-credentials/rollback", strings.NewReader(`{"cluster_id":"prod","secret_id":"`+createdBody.Data.Item.SecretID+`"}`))
	rollbackRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rollbackRecorder, rollbackRequest)

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/cluster-credentials?cluster_id=prod", nil)
	router.ServeHTTP(listRecorder, listRequest)

	require.Equal(t, http.StatusCreated, createRecorder.Code)
	require.Equal(t, http.StatusOK, rotateRecorder.Code)
	require.Equal(t, http.StatusOK, rollbackRecorder.Code)
	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.Contains(t, createRecorder.Body.String(), `"audit_id"`)
	require.Contains(t, rotateRecorder.Body.String(), `"audit_id"`)
	require.Contains(t, rollbackRecorder.Body.String(), `"audit_id"`)
	require.Contains(t, rollbackRecorder.Body.String(), `"probe"`)
	require.Contains(t, listRecorder.Body.String(), `"fingerprint"`)
	require.Contains(t, listRecorder.Body.String(), `"active"`)
	require.Contains(t, listRecorder.Body.String(), `"superseded"`)
	require.NotContains(t, createRecorder.Body.String(), "apiVersion")
	require.NotContains(t, rotateRecorder.Body.String(), "apiVersion")
	require.NotContains(t, rollbackRecorder.Body.String(), "apiVersion")
	require.NotContains(t, listRecorder.Body.String(), "apiVersion")
}

func TestClusterCredentialCreateRequiresPermission(t *testing.T) {
	router := newCredentialRouter(t, testRBACRepo{}, platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/cluster-credentials", strings.NewReader(`{"cluster_id":"prod","name":"prod-readonly","kubeconfig":"apiVersion: v1\nkind: Config\nclusters: []\n"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "permission_denied")
}

func newCredentialRouter(t *testing.T, rbacRepo testRBACRepo, subject platformrbac.Subject) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	service := NewCredentialService(secretSvc, platformrbac.NewService(rbacRepo), audit.NewService(audit.NewMemoryStore()), &credentialValidationStub{serverVersion: "v1.30.2", resourceCount: 2})
	router := gin.New()
	if subject.ID != "" {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
			ctx.Next()
		})
	}
	api := router.Group("/api/v1")
	api.GET("/k8s/cluster-credentials", ListCredentialHandler(service))
	api.POST("/k8s/cluster-credentials", CreateCredentialHandler(service))
	api.POST("/k8s/cluster-credentials/rotate", RotateCredentialHandler(service))
	api.POST("/k8s/cluster-credentials/rollback", RollbackCredentialHandler(service))
	return router
}
