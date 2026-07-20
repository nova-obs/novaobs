package environment

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentAPIRequiresAuthenticatedSubject(t *testing.T) {
	router := environmentTestRouter(NewService(NewMemoryRepository(), allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{})), nil)

	response := performEnvironmentRequest(router, http.MethodGet, "/api/v1/platform/environments", "")

	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Contains(t, response.Body.String(), `"code":"unauthorized"`)
}

func TestEnvironmentAPIEnforcesPlatformEnvironmentPermission(t *testing.T) {
	subject := testSubject()
	router := environmentTestRouter(NewService(NewMemoryRepository(), allowAllResourceValidator{}, WithAuthorizer(denyAllAuthorizer{})), &subject)

	response := performEnvironmentRequest(router, http.MethodPost, "/api/v1/platform/environments", `{"name":"生产环境","stage":"production"}`)

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), `"code":"permission_denied"`)
}

func TestEnvironmentAPICreatesEnvironmentAndBindsMixedResources(t *testing.T) {
	subject := testSubject()
	router := environmentTestRouter(NewService(NewMemoryRepository(), allowAllResourceValidator{}, WithAuthorizer(allowAllAuthorizer{})), &subject)

	created := performEnvironmentRequest(router, http.MethodPost, "/api/v1/platform/environments", `{"name":"生产环境","stage":"production"}`)
	require.Equal(t, http.StatusOK, created.Code)
	environmentID := responseDataID(t, created.Body.Bytes())
	require.NotEmpty(t, environmentID)

	k8s := performEnvironmentRequest(router, http.MethodPost, "/api/v1/platform/environments/"+environmentID+"/resource-bindings", `{"resource_kind":"k8s_cluster","resource_ref":"cluster-prod"}`)
	require.Equal(t, http.StatusOK, k8s.Code)
	host := performEnvironmentRequest(router, http.MethodPost, "/api/v1/platform/environments/"+environmentID+"/resource-bindings", `{"resource_kind":"host_group","resource_ref":"prod-vms"}`)
	require.Equal(t, http.StatusOK, host.Code)

	detail := performEnvironmentRequest(router, http.MethodGet, "/api/v1/platform/environments/"+environmentID, "")
	require.Equal(t, http.StatusOK, detail.Code)
	require.Contains(t, detail.Body.String(), `"resource_kind":"k8s_cluster"`)
	require.Contains(t, detail.Body.String(), `"resource_kind":"host_group"`)
}

func environmentTestRouter(service Service, subject *platformrbac.Subject) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if subject != nil {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), *subject))
			ctx.Next()
		})
	}
	api := router.Group("/api/v1")
	RegisterRoutes(api, service)
	return router
}

func performEnvironmentRequest(router http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	return response
}

func responseDataID(t *testing.T, body []byte) string {
	t.Helper()
	text := string(body)
	marker := `"data":{"id":"`
	start := bytes.Index(body, []byte(marker))
	require.NotEqual(t, -1, start, text)
	remaining := body[start+len(marker):]
	end := bytes.IndexByte(remaining, '"')
	require.NotEqual(t, -1, end, text)
	return string(remaining[:end])
}
