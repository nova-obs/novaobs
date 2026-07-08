package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"novaobs/internal/alerting"
	"novaobs/internal/database/memstore"
	"novaobs/internal/logs"
	"novaobs/internal/metrics"
	"novaobs/internal/modules/k8sops"
	obsendpoint "novaobs/internal/observability/endpoint"
	platformauth "novaobs/internal/platform/auth"
	"novaobs/internal/platform/iam"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/servicecatalog"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMetricsAPIRequiresAuthenticatedSubject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	router := NewRouter(Dependencies{
		Store:               store,
		K8sOpsModule:        k8sops.NewModule(),
		PlatformAuthService: platformauth.NewService(iam.NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts()), []byte("12345678901234567890123456789012")),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/workspace?service_id=svc-orders", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"unauthorized"`)
}

func TestMetricsAPIServesEndpointsBindingsAndWorkspace(t *testing.T) {
	env := newMetricsHTTPTestEnv(t)

	endpoints := performJSON(t, env.router, http.MethodGet, "/api/v1/observability/endpoints?signal_type=metrics", "")
	require.Len(t, nestedValue(t, endpoints, "data").([]any), 1)
	require.Equal(t, "victoriametrics", nestedString(t, endpoints, "data", "0", "kind"))

	metricsEndpoints := performJSON(t, env.router, http.MethodGet, "/api/v1/metrics/endpoints", "")
	require.Len(t, nestedValue(t, metricsEndpoints, "data").([]any), 1)

	testResult := performJSON(t, env.router, http.MethodPost, "/api/v1/observability/endpoints/vm-prod/test", `{}`)
	require.Equal(t, "pending", nestedString(t, testResult, "data", "status"))

	body := `{"service_id":"` + env.service.ID + `","endpoint_id":"vm-prod","label_match":{"service.name":"orders-api"}}`
	created := performJSON(t, env.router, http.MethodPost, "/api/v1/metrics/service-bindings", body)
	require.Equal(t, metrics.BindingStatusActive, nestedString(t, created, "data", "binding", "status"))
	bindingID := nestedString(t, created, "data", "binding", "id")

	patch := performJSON(t, env.router, http.MethodPatch, "/api/v1/metrics/service-bindings/"+bindingID, `{"label_match":{"service.name":"orders-api","namespace":"orders"}}`)
	require.Equal(t, "orders", nestedString(t, patch, "data", "binding", "label_match", "namespace"))

	probed := performJSON(t, env.router, http.MethodPost, "/api/v1/metrics/service-bindings/"+bindingID+"/probe", `{}`)
	require.Equal(t, metrics.ProbeStatusVerified, nestedString(t, probed, "data", "binding", "last_probe_status"))

	workspace := performJSON(t, env.router, http.MethodGet, "/api/v1/metrics/workspace?service_id="+env.service.ID, "")
	require.Equal(t, env.service.ID, nestedString(t, workspace, "data", "active_service_id"))
	require.Equal(t, "vm-prod", nestedString(t, workspace, "data", "binding", "binding", "endpoint_id"))
	require.Len(t, nestedValue(t, workspace, "data", "endpoints").([]any), 1)
}

func TestMetricsAPIRejectsDuplicateActiveBinding(t *testing.T) {
	env := newMetricsHTTPTestEnv(t)
	body := `{"service_id":"` + env.service.ID + `","endpoint_id":"vm-prod","label_match":{"service.name":"orders-api"}}`
	_ = performJSON(t, env.router, http.MethodPost, "/api/v1/metrics/service-bindings", body)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/metrics/service-bindings", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"conflict"`)
}

func TestMetricsAPIRejectsMissingEndpoint(t *testing.T) {
	env := newMetricsHTTPTestEnv(t)
	body := `{"service_id":"` + env.service.ID + `","endpoint_id":"missing","label_match":{"service.name":"orders-api"}}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/metrics/service-bindings", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"not_found"`)
}

func TestMetricsAlertRuleAPITestAndCreateForceMetricsSignal(t *testing.T) {
	env := newMetricsHTTPTestEnv(t)
	binding := performJSON(t, env.router, http.MethodPost, "/api/v1/metrics/service-bindings", `{"service_id":"`+env.service.ID+`","endpoint_id":"vm-prod","tenant":{"account_id":"1001","project_id":"2001"},"label_match":{"service.name":"orders-api"},"base_promql":"service:requests:rate5m{service=\"orders-api\"}"}`)
	bindingID := nestedString(t, binding, "data", "binding", "id")
	spec := metricsAlertRuleSpecJSON(bindingID)

	tested := performJSON(t, env.router, http.MethodPost, "/api/v1/metrics/alert-rules/test", `{"spec":`+spec+`,"range_start":"2026-06-22T07:55:00Z","range_end":"2026-06-22T08:00:00Z"}`)
	require.Contains(t, nestedString(t, tested, "data", "compiled_query"), "service:requests:rate5m")
	token := nestedString(t, tested, "data", "test_token")
	require.NotEmpty(t, token)

	created := performJSON(t, env.router, http.MethodPost, "/api/v1/metrics/alert-rules", `{"spec":`+spec+`,"test_token":"`+token+`"}`)
	require.Equal(t, alerting.SignalTypeMetrics, nestedString(t, created, "data", "rule", "spec", "signal_type"))
	require.Equal(t, bindingID, nestedString(t, created, "data", "rule", "spec", "scope", "metrics_binding_id"))
	require.Equal(t, "vm-prod", nestedString(t, created, "data", "rule", "spec", "scope", "endpoint_id"))

	metricsRules := performJSON(t, env.router, http.MethodGet, "/api/v1/metrics/alert-rules", "")
	require.Len(t, nestedValue(t, metricsRules, "data").([]any), 1)
	logRules := performJSON(t, env.router, http.MethodGet, "/api/v1/alerts/rules", "")
	require.Len(t, nestedValue(t, logRules, "data").([]any), 0)
	filtered := performJSON(t, env.router, http.MethodGet, "/api/v1/alerts/rules?signal_type=metrics", "")
	require.Len(t, nestedValue(t, filtered, "data").([]any), 1)
}

func metricsAlertRuleSpecJSON(bindingID string) string {
	return `{"signal_type":"logs","name":"orders-5xx-rate","scope":{"metrics_binding_id":"` + bindingID + `"},"query":{"mode":"promql","expression":"sum(rate(http_requests_total{status=~\"5..\"}[5m]))"},"trigger":{"mode":"window","aggregation":"count","operator":"gte","threshold":10,"window":"5m","evaluation_interval":"1m"},"grouping":{"max_instances":20},"notification":{"policy_id":"orders-oncall","severity":"warning","owner_team":"orders-team"}}`
}

type metricsHTTPTestEnv struct {
	router  http.Handler
	service servicecatalog.Service
}

func newMetricsHTTPTestEnv(t *testing.T) metricsHTTPTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	store := memstore.NewStore()
	serviceRepo := servicecatalog.NewRepository(store.Services())
	service, err := serviceRepo.Create(ctx, servicecatalog.Service{
		Name:        "orders-api",
		DisplayName: "订单服务",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "orders",
		OwnerTeam:   "orders-team",
		Status:      "active",
	})
	require.NoError(t, err)
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:          "vm-prod",
		Name:        "vm-prod",
		Kind:        obsendpoint.KindVictoriaMetrics,
		SignalTypes: []string{obsendpoint.SignalTypeMetrics},
		QueryURL:    "http://victoriametrics:8428/api/v1/query",
		VMUIURL:     "http://victoriametrics:8428/vmui",
		Status:      "active",
	}))
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	rbacService := platformrbac.NewService(rbacRepo, platformrbac.WithSuperSubjects(platformrbac.DevAdminSubject()))
	metricsService := metrics.NewService(metrics.Dependencies{
		Bindings:   store.MetricsServiceBindings(),
		Endpoints:  obsendpoint.NewLogEndpointFacade(store.LogEndpoints()),
		Services:   serviceRepo,
		Authorizer: rbacService,
	})
	endpointService := obsendpoint.NewLogEndpointFacade(store.LogEndpoints(), obsendpoint.WithAuthorizer(rbacService))
	alertRepository := alerting.NewStoreRepository(store.Alerting())
	alertScopeResolver := alerting.NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsServiceBindings())
	alertService := alerting.NewService(alerting.Dependencies{
		Repository:    alertRepository,
		Authorizer:    rbacService,
		ScopeResolver: alertScopeResolver,
		Tester: alerting.NewSignalAwareTester(
			alerting.NewVictoriaLogsTester(alertScopeResolver, nil),
			alerting.MetricsCompileOnlyTester{},
		),
		ReceiptSigner: alerting.NewHMACTestReceiptSigner([]byte("12345678901234567890123456789012")),
	})
	router := NewRouter(Dependencies{
		Store:                  store,
		ServiceRepo:            serviceRepo,
		K8sOpsModule:           k8sops.NewModule(),
		ObservabilityEndpoints: endpointService,
		MetricsService:         metricsService,
		AlertService:           alertService,
		DefaultSubject:         platformrbac.DevAdminSubject(),
	})
	return metricsHTTPTestEnv{router: router, service: service}
}

func TestNestedValueSupportsArrayIndexes(t *testing.T) {
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"data":[{"kind":"victoriametrics"}]}`), &result))
	require.Equal(t, "victoriametrics", nestedString(t, result, "data", "0", "kind"))
}
