package metrics

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntegrationAPIRequiresAuthenticatedSubject(t *testing.T) {
	fixture := newIntegrationFixture(t)
	router := integrationTestRouter(fixture.service, nil)

	response := performIntegrationRequest(router, http.MethodGet, "/api/v1/metrics/integrations", "")

	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestIntegrationAPICreatesEnvironmentIntegrationAndUpdatesSourceMode(t *testing.T) {
	fixture := newIntegrationFixture(t)
	subject := integrationSubject()
	router := integrationTestRouter(fixture.service, &subject)

	created := performIntegrationRequest(router, http.MethodPost, "/api/v1/metrics/integrations", `{"environment_id":"env-prod","destination_ref":"vm-prod"}`)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), `"identity_label_key":"novaapm_environment_id"`)
	require.Contains(t, created.Body.String(), `"source_kind":"kubernetes_infra"`)

	list := performIntegrationRequest(router, http.MethodGet, "/api/v1/metrics/integrations", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), `"environment_id":"env-prod"`)

	integrations, err := fixture.repository.ListIntegrations(context.Background())
	require.NoError(t, err)
	sources, err := fixture.repository.ListSourceAccesses(context.Background(), integrations[0].ID)
	require.NoError(t, err)
	sourceID := sources[0].ID
	handoff := performIntegrationRequest(router, http.MethodGet, "/api/v1/metrics/source-accesses/"+sourceID+"/handoff", "")
	require.Equal(t, http.StatusOK, handoff.Code, handoff.Body.String())
	require.Contains(t, handoff.Body.String(), `"kind":"vmoperator_patch"`)
}

func integrationTestRouter(service IntegrationService, subject *platformrbac.Subject) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if subject != nil {
		router.Use(func(ctx *gin.Context) {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), *subject))
			ctx.Next()
		})
	}
	RegisterIntegrationRoutes(router.Group("/api/v1"), service)
	return router
}

func performIntegrationRequest(router http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	return response
}
