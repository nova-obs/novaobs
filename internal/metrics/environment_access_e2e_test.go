package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"novaapm/internal/platform/authctx"
	platformenvironment "novaapm/internal/platform/environment"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentMetricsAccessEndToEnd(t *testing.T) {
	environments := platformenvironment.NewMemoryRepository()
	integrationRepository := NewMemoryIntegrationRepository()
	authorizer := integrationAllowAuthorizer{}
	environmentService := platformenvironment.NewService(environments, e2eResourceValidator{}, platformenvironment.WithAuthorizer(authorizer))
	integrationService := NewIntegrationService(IntegrationDependencies{
		Repository: integrationRepository, Environments: environments,
		Destinations: staticDestinationReader{ids: map[string]bool{"vm-prod": true}},
		Authorizer:   authorizer, Verifier: staticHealthVerifier{},
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), integrationSubject()))
		ctx.Next()
	})
	api := router.Group("/api/v1")
	platformenvironment.RegisterRoutes(api, environmentService)
	RegisterIntegrationRoutes(api, integrationService)

	createdEnvironment := e2eRequest(t, router, http.MethodPost, "/api/v1/platform/environments", `{"name":"生产环境","stage":"production"}`)
	environmentID := createdEnvironment["id"].(string)
	require.NotEmpty(t, environmentID)
	e2eRequest(t, router, http.MethodPost, "/api/v1/platform/environments/"+environmentID+"/resource-bindings", `{"resource_kind":"k8s_cluster","resource_ref":"cluster-prod"}`)
	e2eRequest(t, router, http.MethodPost, "/api/v1/platform/environments/"+environmentID+"/resource-bindings", `{"resource_kind":"host_group","resource_ref":"hosts-prod"}`)

	createdIntegration := e2eRequest(t, router, http.MethodPost, "/api/v1/metrics/integrations", `{"environment_id":"`+environmentID+`","destination_ref":"vm-prod"}`)
	integration := createdIntegration["integration"].(map[string]any)
	integrationID := integration["id"].(string)
	require.Equal(t, environmentID, integration["environment_id"])
	require.Len(t, createdIntegration["source_accesses"], 2)

	e2eRequest(t, router, http.MethodPost, "/api/v1/metrics/integrations/"+integrationID+"/log-derived-source", "")
	verified := e2eRequest(t, router, http.MethodPost, "/api/v1/metrics/integrations/"+integrationID+"/verify", "")
	require.Equal(t, HealthHealthy, verified["configuration"].(map[string]any)["status"])
	require.Len(t, verified["sources"], 3)
}

type e2eResourceValidator struct{}

func (e2eResourceValidator) Validate(context.Context, string, string) error { return nil }

func e2eRequest(t *testing.T, router http.Handler, method string, path string, body string) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return envelope.Data
}
