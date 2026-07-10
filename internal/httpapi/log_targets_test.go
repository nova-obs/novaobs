package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"novaapm/internal/collectormanagement"
	"novaapm/internal/database/memstore"
	"novaapm/internal/logs"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLogsTargetAPICreatesExternalVLogsTarget(t *testing.T) {
	env := newTestRouter(t)
	ctx := context.Background()
	require.NoError(t, env.store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:        "vl-external",
		Name:      "vl-external",
		SinkType:  logs.EndpointSinkVL,
		WriteURL:  "http://vl.external:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.external:9428/select/logsql/query",
		VMUIURL:   "http://vl.external:9428/select/vmui",
		AccountID: "1001",
		ProjectID: "2001",
		Status:    "active",
	}))

	body := `{"name":"orders 自建 VL","service_id":"` + env.service.ID + `","endpoint_id":"vl-external","base_filter":"\"stream\":\"orders\"","account_id":"1001","project_id":"2001"}`
	created := performJSON(t, env.router, http.MethodPost, "/api/v1/logs/targets", body)

	require.Equal(t, "external_vlogs", nestedString(t, created, "data", "target", "source_kind"))
	require.Equal(t, env.service.ID, nestedString(t, created, "data", "target", "service_id"))
	require.Equal(t, "vl-external", nestedString(t, created, "data", "target", "endpoint_id"))
	require.Equal(t, `"stream":"orders"`, nestedString(t, created, "data", "target", "base_filter"))
	require.Equal(t, "1001", nestedString(t, created, "data", "target", "account_id"))
	require.Equal(t, "2001", nestedString(t, created, "data", "target", "project_id"))
	require.Equal(t, "vl-external", nestedString(t, created, "data", "endpoint", "id"))
	require.Equal(t, "1001", nestedString(t, created, "data", "endpoint", "account_id"))
	require.Equal(t, "2001", nestedString(t, created, "data", "endpoint", "project_id"))

	workspace := performJSON(t, env.router, http.MethodGet, "/api/v1/products/"+env.product.ID+"/services/"+env.service.ID+"/logs/workspace", "")
	require.Len(t, nestedValue(t, workspace, "data", "targets").([]any), 1)
	require.Empty(t, nestedValue(t, workspace, "data", "routes").([]any))
}

func TestLogsTargetAPIRequiresAuthenticatedSubject(t *testing.T) {
	router := gin.New()
	router.POST("/api/v1/logs/targets", createLogsTargetHandler(logs.Service{}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/logs/targets", bytes.NewBufferString(`{"service_id":"svc","endpoint_id":"ep","base_filter":"error"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"unauthorized"`)
}

func TestLogsTargetAPIRejectsSubjectWithoutManagePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	store := memstore.NewStore()
	serviceRepo := servicecatalog.NewRepository(store.Services())
	service, err := serviceRepo.Create(ctx, servicecatalog.Service{
		Name:        "orders-api",
		DisplayName: "orders-api",
		Environment: "production",
		OwnerTeam:   "orders-team",
		Status:      "active",
	})
	require.NoError(t, err)
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:       "vl-external",
		Name:     "vl-external",
		SinkType: logs.EndpointSinkVL,
		WriteURL: "http://vl.external:9428/insert/opentelemetry/v1/logs",
		QueryURL: "http://vl.external:9428/select/logsql/query",
		Status:   "active",
	}))
	logsService := logs.NewService(
		store.LogEndpoints(),
		store.LogSources(),
		store.LogRoutes(),
		store.LogCollectorConfigVersions(),
		store.LogDeploymentManifestVersions(),
		store.LogCollectorClusterConfigs(),
		serviceRepo,
		servicecatalog.NewTargetRepository(store.ServiceTargets()),
		collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances()),
		nil,
		nil,
		nil,
		logs.WithLogTargets(store.LogTargets()),
		logs.WithObservabilityRuntimes(store.ObservabilityRuntimes()),
		logs.WithAuthorizer(platformrbac.NewService(platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings()))),
	)
	router := gin.New()
	router.Use(func(ginCtx *gin.Context) {
		ginCtx.Request = ginCtx.Request.WithContext(authctx.WithSubject(ginCtx.Request.Context(), platformrbac.Subject{ID: "viewer", Type: "user"}))
	})
	router.POST("/api/v1/logs/targets", createLogsTargetHandler(logsService))

	body := `{"name":"orders 自建 VL","service_id":"` + service.ID + `","endpoint_id":"vl-external","base_filter":"\"stream\":\"orders\""}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/logs/targets", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"permission_denied"`)
}
