package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"novaapm/internal/alerting"
	"novaapm/internal/collectorconfig"
	"novaapm/internal/collectormanagement"
	"novaapm/internal/database/memstore"
	"novaapm/internal/logs"
	"novaapm/internal/modules/k8sops"
	k8sopscluster "novaapm/internal/modules/k8sops/cluster"
	k8sopsdeployment "novaapm/internal/modules/k8sops/deployment"
	k8sopsresource "novaapm/internal/modules/k8sops/resource"
	obsruntime "novaapm/internal/observability/runtime"
	"novaapm/internal/onboarding"
	"novaapm/internal/opamp"
	"novaapm/internal/platform/audit"
	platformauth "novaapm/internal/platform/auth"
	platformenvironment "novaapm/internal/platform/environment"
	"novaapm/internal/platform/iam"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	router  http.Handler
	store   *memstore.Store
	product servicecatalog.Product
	service servicecatalog.Service
	group   collectormanagement.CollectorGroup
	manager *opamp.Manager
}

type testRuntimeDeploymentService struct{}

func (testRuntimeDeploymentService) Preview(_ context.Context, _ platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	return k8sopsdeployment.OperationResult{
		Status:            "previewed",
		Message:           "previewed",
		PreviewID:         "preview-runtime",
		ConfirmationToken: "confirm-runtime",
		AuditID:           "audit-runtime-preview",
		Resources: []k8sopsdeployment.ResourceIdentity{{
			ClusterID: req.ClusterID, APIVersion: "v1", Kind: "Namespace", Name: "novaapm-system",
		}},
	}, nil
}

func (testRuntimeDeploymentService) Apply(_ context.Context, _ platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	return k8sopsdeployment.OperationResult{
		Status:    "applied",
		Message:   "applied",
		PreviewID: req.PreviewID,
		AuditID:   "audit-runtime-apply",
		Resources: []k8sopsdeployment.ResourceIdentity{{
			ClusterID: req.ClusterID, APIVersion: "v1", Kind: "Namespace", Name: "novaapm-system",
		}},
	}, nil
}

func testLogsCollectorRuntimeResources(clusterID string, namespace string) []k8sopsresource.ResourceSummary {
	now := time.Now().UTC()
	refs := []k8sopsresource.Identity{
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "v1", Kind: "Namespace", Name: namespace, UID: "logs-runtime-namespace"},
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: "novaapm-logs-agent", UID: "logs-runtime-cluster-role"},
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: "novaapm-logs-agent", UID: "logs-runtime-cluster-role-binding"},
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "v1", Kind: "ServiceAccount", Name: "novaapm-logs-agent", UID: "logs-runtime-service-account"},
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "v1", Kind: "ConfigMap", Name: "novaapm-logs-agent-base-config", UID: "logs-runtime-base-config"},
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "v1", Kind: "Service", Name: "novaapm-logs-agent", UID: "logs-runtime-service"},
		{ClusterID: clusterID, Namespace: namespace, APIVersion: "apps/v1", Kind: "DaemonSet", Name: "novaapm-logs-agent", UID: "logs-runtime-daemonset"},
	}
	items := make([]k8sopsresource.ResourceSummary, 0, len(refs))
	for _, ref := range refs {
		items = append(items, k8sopsresource.ResourceSummary{Identity: ref, Status: "active", UpdatedAt: now})
	}
	return items
}

func TestErrorLogMiddlewareLogsServerErrorsWithCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	output := captureSlog(t, slog.LevelInfo, func() {
		router := gin.New()
		router.Use(errorLogMiddleware())
		router.GET("/boom", func(ctx *gin.Context) {
			_ = ctx.Error(errors.New("mongo connection timeout"))
			response.Error(ctx, http.StatusInternalServerError, "internal_error", "服务处理失败")
		})

		req := httptest.NewRequest(http.MethodGet, "/boom?cluster_id=test03&token=secret-token", nil)
		req.Header.Set("X-Request-ID", "req-001")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusInternalServerError, recorder.Code)
	})

	require.Contains(t, output, "level=ERROR")
	require.Contains(t, output, "msg=\"HTTP 请求处理失败\"")
	require.Contains(t, output, "path=/boom")
	require.Contains(t, output, "code=internal_error")
	require.Contains(t, output, "request_id=req-001")
	require.Contains(t, output, "cluster_id")
	require.Contains(t, output, "token")
	require.Contains(t, output, "mongo connection timeout")
	require.NotContains(t, output, "secret-token")
}

func TestErrorLogMiddlewareWarnsForPolicyErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	output := captureSlog(t, slog.LevelInfo, func() {
		router := gin.New()
		router.Use(errorLogMiddleware())
		router.GET("/deny", func(ctx *gin.Context) {
			response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行操作")
		})

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/deny", nil))
		require.Equal(t, http.StatusForbidden, recorder.Code)
	})

	require.Contains(t, output, "level=WARN")
	require.Contains(t, output, "msg=\"HTTP 请求被业务策略阻断\"")
	require.Contains(t, output, "code=permission_denied")
}

func TestErrorLogMiddlewareDoesNotEmitBadRequestAtDefaultLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	output := captureSlog(t, slog.LevelInfo, func() {
		router := gin.New()
		router.Use(errorLogMiddleware())
		router.GET("/bad-request", func(ctx *gin.Context) {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求参数无效")
		})

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/bad-request", nil))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	require.Empty(t, strings.TrimSpace(output))
}

func captureSlog(t *testing.T, level slog.Leveler, fn func()) string {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: level})))
	defer slog.SetDefault(previous)
	fn()
	return buffer.String()
}

func testRouterLoginCredential() string {
	return strings.Join([]string{"test", "login", "credential"}, "-")
}

func newTestRouter(t *testing.T) testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	ctx := context.Background()
	require.NoError(t, store.Environments().Insert(ctx, platformenvironment.Environment{ID: "production", Name: "生产环境", Stage: platformenvironment.StageProduction, Status: platformenvironment.StatusActive}))
	require.NoError(t, store.Environments().Insert(ctx, platformenvironment.Environment{ID: "prod", Name: "生产环境别名", Stage: platformenvironment.StageProduction, Status: platformenvironment.StatusActive}))
	productRepo := servicecatalog.NewProductRepository(store.Products())
	product, err := productRepo.Create(ctx, servicecatalog.Product{Name: "commerce", DisplayName: "交易产品"})
	require.NoError(t, err)
	svcRepo := servicecatalog.NewRepository(store.Services(), store.Environments(), store.Products())
	svc, err := svcRepo.Create(ctx, servicecatalog.Service{
		ProductID:     product.ID,
		Name:          "orders-api",
		DisplayName:   "订单服务",
		EnvironmentID: "production",
		Cluster:       "prod-1",
		Namespace:     "orders",
		OwnerTeam:     "orders-team",
		AlertRoute:    "orders-alerts",
		Status:        "active",
	})
	require.NoError(t, err)
	collectorSvc := collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances(), collectormanagement.WithConfigVersionStore(store.CollectorConfigVersions()))
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:          "logs-gateway",
		Mode:          "shared_gateway",
		EnvironmentID: "production",
		Cluster:       "prod-1",
		Status:        "active",
	})
	require.NoError(t, err)
	configSvc := collectorconfig.NewService(
		store.CollectorPlatformTemplates(),
		store.CollectorGroupOverrides(),
		store.ServiceEnrichmentPatches(),
		store.ServiceParserRules(),
		store.ServicePipelinePatches(),
		collectorSvc,
		svcRepo,
	)
	manager := opamp.NewManager()
	admin := platformrbac.DevAdminSubject()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	rbacSvc := platformrbac.NewService(rbacRepo, platformrbac.WithSuperSubjects(admin))
	iamSvc := iam.NewService(
		iam.NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts()),
		iam.NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		rbacSvc,
	)
	alertRepository := alerting.NewStoreRepository(store.Alerting())
	k8sModule := k8sops.NewModuleWithSecurity(nil, nil, nil,
		k8sopscluster.NewMemoryRepository([]k8sopscluster.Cluster{
			{ID: "prod-1", Name: "prod-1", Status: "active"},
		}),
		k8sopsresource.NewMemoryReader(testLogsCollectorRuntimeResources("prod-1", "novaapm-system")),
	)
	logsSvc := logs.NewService(
		store.LogEndpoints(),
		store.LogSources(),
		store.LogRoutes(),
		store.LogCollectorConfigVersions(),
		store.LogDeploymentManifestVersions(),
		store.LogCollectorClusterConfigs(),
		svcRepo,
		servicecatalog.NewTargetRepository(store.ServiceTargets()),
		collectorSvc,
		k8sModule.Cluster,
		k8sModule.Resource,
		k8sModule.Deploy,
		logs.WithLogTargets(store.LogTargets()),
		logs.WithVMLogAgentEndpoints(store.VMLogAgentEndpoints()),
		logs.WithObservabilityRuntimes(store.ObservabilityRuntimes()),
		logs.WithAuthorizer(rbacSvc),
	)
	alertRuntimeSvc := alerting.NewLogRuntimeService(alerting.LogRuntimeDependencies{
		Endpoints:      store.LogEndpoints(),
		Runtimes:       store.ObservabilityRuntimes(),
		Repository:     alertRepository,
		ScopeResolver:  alerting.NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsIntegrations(), store.Environments(), store.Products()),
		K8sDeployments: testRuntimeDeploymentService{},
	})
	metricsRuntimeSvc := alerting.NewMetricsRuntimeService(alerting.MetricsRuntimeDependencies{
		Endpoints:      store.LogEndpoints(),
		Runtimes:       store.ObservabilityRuntimes(),
		Repository:     alertRepository,
		K8sDeployments: testRuntimeDeploymentService{},
	})
	router := NewRouter(Dependencies{
		Store:                  store,
		ProductRepo:            productRepo,
		ServiceRepo:            svcRepo,
		ServiceTargetRepo:      servicecatalog.NewTargetRepository(store.ServiceTargets()),
		CollectorConfigService: configSvc,
		CollectorService:       collectorSvc,
		OnboardingService:      onboarding.NewService(store.Onboardings(), store.IngestionIdentities(), svcRepo, collectorSvc),
		LogsService:            logsSvc,
		AlertRuntimeService:    alertRuntimeSvc,
		MetricsRuntimeService:  metricsRuntimeSvc,
		AlertService: alerting.NewService(alerting.Dependencies{
			Repository: alertRepository,
			Authorizer: rbacSvc,
		}),
		AlertPolicyService: alerting.NewPolicyService(alerting.PolicyDependencies{
			Repository: alertRepository,
			Authorizer: rbacSvc,
		}),
		PlatformIAMService: iamSvc,
		K8sOpsModule:       k8sModule,
		OpAMPManager:       manager,
		DefaultSubject:     admin,
	})
	return testEnv{router: router, store: store, product: product, service: svc, group: group, manager: manager}
}

func TestRouterInjectsDefaultSubjectForK8sOpsWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	require.NoError(t, platformrbac.EnsureK8sOpsDefaults(rbacRepo, platformrbac.DevAdminSubject(), platformrbac.DevK8sOpsScope()))
	rbacSvc := platformrbac.NewService(rbacRepo)
	auditSvc := audit.NewService(audit.NewMemoryStore())
	router := NewRouter(Dependencies{
		Store:          store,
		K8sOpsModule:   k8sops.NewModuleWithSecurity(rbacSvc, auditSvc, nil),
		DefaultSubject: platformrbac.DevAdminSubject(),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/terminal/exec", strings.NewReader(`{"cluster_id":"prod","namespace":"orders","command":"get pods -n orders"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"accepted"`)
	require.Contains(t, recorder.Body.String(), `"audit_id"`)
}

func TestRouterLogoutClearsSessionCookie(t *testing.T) {
	env := newTestRouter(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"logged_out"`)
	require.Contains(t, recorder.Header().Get("Set-Cookie"), "novaapm_session=")
	require.Contains(t, recorder.Header().Get("Set-Cookie"), "Max-Age=0")
}

func TestRouterUsesPlatformAuthSessionForProtectedAPIs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memstore.NewStore()
	ctx := context.Background()
	admin := platformrbac.DevAdminSubject()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	iamRepo := iam.NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	iamSvc := iam.NewService(
		iamRepo,
		iam.NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		platformrbac.NewService(rbacRepo),
	)
	_, err := iamSvc.CreateUser(ctx, admin, iam.CreateUserRequest{
		Username:    "operator",
		DisplayName: "一线运维",
		Password:    testRouterLoginCredential(),
	})
	require.NoError(t, err)
	router := NewRouter(Dependencies{
		Store:               store,
		PlatformIAMService:  iamSvc,
		K8sOpsModule:        k8sops.NewModule(),
		PlatformAuthService: platformauth.NewService(iamRepo, []byte("12345678901234567890123456789012"), platformauth.WithPasswordlessLocalUsers(true)),
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/platform/me", nil))
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	require.Contains(t, unauthorized.Body.String(), `"code":"unauthorized"`)

	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"operator"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(login, loginRequest)
	require.Equal(t, http.StatusOK, login.Code)
	require.Contains(t, login.Header().Get("Set-Cookie"), platformauth.SessionCookieName+"=")

	me := httptest.NewRecorder()
	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/platform/me", nil)
	meRequest.Header.Set("Cookie", login.Header().Get("Set-Cookie"))
	router.ServeHTTP(me, meRequest)
	require.Equal(t, http.StatusOK, me.Code)
	require.Contains(t, me.Body.String(), `"subject_id":"operator"`)
}

func TestRouterServesCoreAPIs(t *testing.T) {
	env := newTestRouter(t)

	for _, path := range []string{
		"/api/v1/health",
		"/api/v1/services",
		"/api/v1/services/" + env.service.ID,
		"/api/v1/services/" + env.service.ID + "/observability-graph",
		"/api/v1/services/" + env.service.ID + "/onboarding",
		"/api/v1/products/" + env.product.ID + "/services/" + env.service.ID + "/logs/workspace",
		"/api/v1/logs/endpoints",
		"/api/v1/k8sops/dashboard?cluster_id=prod",
		"/api/v1/k8s/clusters?q=prod",
		"/api/v1/k8s/namespaces?cluster_id=prod",
		"/api/v1/k8s/resources?cluster_id=prod&namespace=orders",
		"/api/v1/k8s/runtime-groups?cluster_id=prod&namespace=orders",
		"/api/v1/k8s/deployment-history?cluster_id=prod",
		"/api/v1/k8s/audit-events?cluster_id=prod",
		"/api/v1/k8s/certificates?cluster_id=prod",
		"/api/v1/k8s/service-accounts?cluster_id=prod&namespace=orders",
		"/api/v1/k8s/rbac/roles?cluster_id=prod&namespace=orders",
		"/api/v1/k8s/rbac/bindings?cluster_id=prod&namespace=orders",
		"/api/v1/opamp/agents",
		"/api/v1/alerts/rules",
		"/api/v1/platform/me",
		"/api/v1/platform/subjects",
		"/api/v1/platform/users",
		"/api/v1/platform/groups",
		"/api/v1/platform/service-accounts",
		"/api/v1/platform/roles",
		"/api/v1/platform/bindings",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		env.router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, path)
		require.Contains(t, recorder.Body.String(), `"success":true`, path)
	}

	for _, path := range []string{
		"/api/v1/k8s/resources/detail?cluster_id=prod&namespace=orders&api_version=apps/v1&kind=Deployment&name=orders-api&uid=uid-orders-api",
		"/api/v1/k8s/resources/yaml?cluster_id=prod&namespace=orders&api_version=apps/v1&kind=Deployment&name=orders-api&uid=uid-orders-api",
		"/api/v1/k8s/pod-logs?cluster_id=prod&namespace=orders&pod=orders-api-6f7d&container=app",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		env.router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNotFound, recorder.Code, path)
		require.Contains(t, recorder.Body.String(), `"success":false`, path)
	}
}

func TestRouterReturnsServiceObservabilityGraph(t *testing.T) {
	env := newTestRouter(t)
	ctx := context.Background()
	targetRepo := servicecatalog.NewTargetRepository(env.store.ServiceTargets())
	_, err := targetRepo.Create(ctx, servicecatalog.ObservedTarget{
		ServiceID:     env.service.ID,
		TargetType:    "host_process",
		EnvironmentID: "production",
		DisplayName:   "orders-api on vm-01",
		IdentityAttributes: map[string]string{
			"host.name":               "vm-01",
			"process.executable.name": "orders-api",
		},
	})
	require.NoError(t, err)

	collectorSvc := collectormanagement.NewService(env.store.CollectorGroups(), env.store.CollectorInstances())
	_, err = collectorSvc.UpsertInstance(ctx, "collector-orders", env.group.ID, collectormanagement.InstanceStatus{
		ServiceID:           env.service.ID,
		Online:              true,
		Healthy:             true,
		RemoteConfigCapable: true,
		LastSeenAt:          time.Now().UTC(),
	})
	require.NoError(t, err)

	alertSvc := alerting.NewService(alerting.Dependencies{
		Repository: alerting.NewStoreRepository(env.store.Alerting()),
		Authorizer: platformrbac.NewService(platformrbac.NewStoreRepository(env.store.RBACRoles(), env.store.RBACBindings()), platformrbac.WithSuperSubjects(platformrbac.DevAdminSubject())),
	})
	_, err = alertSvc.Enable(ctx, platformrbac.DevAdminSubject(), alerting.EnableRequest{Spec: alerting.RuleSpec{
		Name:         "orders-error-count",
		Scope:        alerting.RuleScope{ServiceID: env.service.ID, ServiceName: "orders-api", LogRouteID: "route-orders", EndpointID: "vl-prod", AccountID: "1", ProjectID: "1"},
		Query:        alerting.QuerySpec{Mode: alerting.QueryModeContains, Expression: "level=error"},
		Trigger:      alerting.TriggerSpec{Mode: alerting.TriggerModeWindow, Aggregation: alerting.AggregationCount, Operator: alerting.OperatorGTE, Threshold: 1, Window: "1m", EvaluationInterval: "30s"},
		Grouping:     alerting.GroupingSpec{MaxInstances: 100},
		Notification: alerting.NotificationSpec{PolicyID: "orders-oncall", Severity: alerting.SeverityCritical, OwnerTeam: "orders-team"},
	}})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/services/"+env.service.ID+"/observability-graph", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"targets"`)
	require.Contains(t, recorder.Body.String(), `"target_type":"host_process"`)
	require.Contains(t, recorder.Body.String(), `"agents"`)
	require.Contains(t, recorder.Body.String(), `"instance_uid":"collector-orders"`)
	require.Contains(t, recorder.Body.String(), `"log_routes"`)
	require.Contains(t, recorder.Body.String(), `"alert_rules"`)
	require.Contains(t, recorder.Body.String(), `"orders-error-count"`)
	require.NotContains(t, recorder.Body.String(), `"dashboard_panels"`)
}

func TestRouterCreatesServiceTarget(t *testing.T) {
	env := newTestRouter(t)

	body := `{"target_type":"physical_or_network_device","environment_id":"production","display_name":"edge-fw-01","identity_attributes":{"device.name":"edge-fw-01","net.host.ip":"10.0.0.8"},"match_rules":{"syslog.hostname":"edge-fw-01"}}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/targets", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"target_type":"physical_or_network_device"`)
	require.Contains(t, recorder.Body.String(), `"device.name":"edge-fw-01"`)

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/services/"+env.service.ID+"/targets", nil)
	env.router.ServeHTTP(listRecorder, listRequest)

	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.Contains(t, listRecorder.Body.String(), `"display_name":"edge-fw-01"`)
}

func TestRouterImportsCollectorTemplate(t *testing.T) {
	env := newTestRouter(t)

	env.manager.RegisterInstanceGroup("collector-a", env.group.ID)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		Health:       &protobufs.ComponentHealth{Healthy: true, StartTimeUnixNano: uint64(time.Now().UnixNano())},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\nprocessors:\n  memory_limiter:\n  batch:\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      processors: [memory_limiter, batch]\n      exporters: [debug]\n")},
		}}},
	})

	importBody := `{"name":"platform-prod","source_agent_uid":"collector-a","collector_group_id":"` + env.group.ID + `"}`
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collector-platform-templates/import-from-agent", bytes.NewBufferString(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(importRecorder, importRequest)
	require.Equal(t, http.StatusCreated, importRecorder.Code)
	require.Contains(t, importRecorder.Body.String(), `"name":"platform-prod"`)
}

func TestRouterRejectsCollectorGroupActivationWithoutConfig(t *testing.T) {
	env := newTestRouter(t)
	draft := "draft"
	err := env.store.CollectorGroups().Update(context.Background(), env.group.ID, collectormanagement.CollectorGroup{
		ID:            env.group.ID,
		Name:          env.group.Name,
		Mode:          env.group.Mode,
		EnvironmentID: env.group.EnvironmentID,
		Cluster:       env.group.Cluster,
		Status:        draft,
	})
	require.NoError(t, err)
	env.manager.RegisterInstanceGroup("collector-a", env.group.ID)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid: []byte("collector-a"),
		Health:      &protobufs.ComponentHealth{Healthy: true, StartTimeUnixNano: uint64(time.Now().UnixNano())},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\nservice:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      exporters: [debug]\n")},
		}}},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/collector-groups/"+env.group.ID+"/activate", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestRouterReturnsOpAMPAgentDetailWithConfigSources(t *testing.T) {
	env := newTestRouter(t)
	env.manager.RegisterInstanceGroup("collector-a", env.group.ID)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		AgentDescription: &protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol-contrib"}}},
			},
		},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\n")},
		}}},
	})

	importBody := `{"name":"platform-prod","source_agent_uid":"collector-a","collector_group_id":"` + env.group.ID + `"}`
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/collector-platform-templates/import-from-agent", bytes.NewBufferString(importBody))
	importRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(importRecorder, importRequest)
	require.Equal(t, http.StatusCreated, importRecorder.Code)

	require.NoError(t, env.store.Onboardings().Upsert(context.Background(), env.service.ID, onboarding.ServiceOnboarding{
		ServiceID:        env.service.ID,
		Mode:             "shared_gateway",
		CollectorGroupID: env.group.ID,
		Status:           "pending_verification",
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/opamp/agents/collector-a", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"instance_uid":"collector-a"`)
	require.Contains(t, recorder.Body.String(), `"config_sources"`)
	require.Contains(t, recorder.Body.String(), `"name":"orders-api"`)
}

func TestRouterRemovesServiceParserPreviewEndpoint(t *testing.T) {
	env := newTestRouter(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/services/"+env.service.ID+"/parser-rule/preview", bytes.NewBufferString(`{"parse_mode":"regex","regex_pattern":"[","sample_log":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestRouterCreatesResources(t *testing.T) {
	env := newTestRouter(t)

	for _, item := range []struct {
		path string
		body string
	}{
		{path: "/api/v1/products/" + env.product.ID + "/services", body: `{"name":"inventory-api","environment_id":"prod","owner_team":"inventory-team","alert_route":"inventory-prod"}`},
		{path: "/api/v1/alerts/rules", body: `{"spec":{"name":"inventory-error-count","scope":{"service_id":"service-inventory","service_name":"inventory-api","log_route_id":"route-inventory","endpoint_id":"vl-prod","account_id":"1","project_id":"1"},"query":{"mode":"contains","expression":"level=error"},"trigger":{"mode":"window","aggregation":"count","operator":"gte","threshold":1,"window":"1m","evaluation_interval":"30s"},"grouping":{"max_instances":100},"notification":{"policy_id":"inventory-oncall","severity":"critical","owner_team":"inventory-team"}}}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, item.path, bytes.NewBufferString(item.body))
		request.Header.Set("Content-Type", "application/json")
		env.router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusCreated, recorder.Code, item.path)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), item.path)
		require.Equal(t, true, body["success"], item.path)
	}
}

func TestRouterCreatesProductThenDerivesServiceTenant(t *testing.T) {
	env := newTestRouter(t)

	productResponse := performJSON(t, env.router, http.MethodPost, "/api/v1/products", `{"name":"payments","display_name":"支付产品","project_id":"0"}`)
	productData := productResponse["data"].(map[string]any)
	productID := productData["id"].(string)
	projectID := productData["project_id"].(string)
	require.NotEmpty(t, productID)
	require.NotEqual(t, "", projectID)
	require.NotEqual(t, "0", projectID)

	serviceResponse := performJSON(t, env.router, http.MethodPost, "/api/v1/products/"+productID+"/services", `{"name":"payment-api","environment_id":"prod","account_id":"9527","project_id":"9528"}`)
	serviceData := serviceResponse["data"].(map[string]any)
	require.Equal(t, productID, serviceData["product_id"])
	require.Equal(t, "0", serviceData["account_id"])
	require.Equal(t, projectID, serviceData["project_id"])
}

func TestRouterCreatesVMLogRouteAndProvidesManualInstallation(t *testing.T) {
	env := newTestRouter(t)

	routeBody := createVMLogRouteRequestBody(t, env)
	previewRecorder := httptest.NewRecorder()
	previewRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes/preview", bytes.NewBufferString(routeBody))
	previewRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(previewRecorder, previewRequest)
	require.Equal(t, http.StatusOK, previewRecorder.Code)
	require.Contains(t, previewRecorder.Body.String(), `"source_type":"vm_file"`)
	require.Contains(t, previewRecorder.Body.String(), "file_log/vm")

	routeRecorder := httptest.NewRecorder()
	routeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes", bytes.NewBufferString(routeBody))
	routeRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(routeRecorder, routeRequest)
	require.Equal(t, http.StatusCreated, routeRecorder.Code)
	var routeEnvelope struct {
		Data logs.LogRouteView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(routeRecorder.Body.Bytes(), &routeEnvelope))
	require.Equal(t, "awaiting_nodes", routeEnvelope.Data.Route.Status)
	require.Empty(t, routeEnvelope.Data.Route.AgentGroupID)

	probeRecorder := httptest.NewRecorder()
	probeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes/"+routeEnvelope.Data.Route.ID+"/probe", nil)
	env.router.ServeHTTP(probeRecorder, probeRequest)
	require.Equal(t, http.StatusBadRequest, probeRecorder.Code)
	require.Contains(t, probeRecorder.Body.String(), "请逐节点校验 VM 地址")

	publishRecorder := httptest.NewRecorder()
	publishRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes/"+routeEnvelope.Data.Route.ID+"/publish", bytes.NewBufferString(`{}`))
	publishRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(publishRecorder, publishRequest)
	require.Equal(t, http.StatusBadRequest, publishRecorder.Code)
	require.Contains(t, publishRecorder.Body.String(), "没有平台发布语义")

	installationRecorder := httptest.NewRecorder()
	installationRequest := httptest.NewRequest(http.MethodGet, "/api/v1/logs/routes/"+routeEnvelope.Data.Route.ID+"/vm-installation", nil)
	env.router.ServeHTTP(installationRecorder, installationRequest)
	require.Equal(t, http.StatusOK, installationRecorder.Code)
	require.Contains(t, installationRecorder.Body.String(), `"collector_config_hash":"`+routeEnvelope.Data.Route.CollectorConfigHash+`"`)
	require.NotContains(t, installationRecorder.Body.String(), "OTELCOL_PACKAGE_URL")
	require.Contains(t, installationRecorder.Body.String(), "平台批准版本的 otelcol-contrib")

	nodeRecorder := httptest.NewRecorder()
	nodeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes/"+routeEnvelope.Data.Route.ID+"/vm-agent-endpoints", bytes.NewBufferString(`{"name":"billing-01","address":"10.0.0.8:13133"}`))
	nodeRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(nodeRecorder, nodeRequest)
	require.Equal(t, http.StatusCreated, nodeRecorder.Code)
	require.Contains(t, nodeRecorder.Body.String(), `"status":"pending_probe"`)

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/logs/routes/"+routeEnvelope.Data.Route.ID+"/vm-agent-endpoints", nil)
	env.router.ServeHTTP(listRecorder, listRequest)
	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.Contains(t, listRecorder.Body.String(), `"address":"10.0.0.8:13133"`)
}

func TestRouterGetsLogRouteCollectorConfigYAML(t *testing.T) {
	env := newTestRouter(t)
	route := createK8sLogRoute(t, env)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/routes/"+route.Route.ID+"/collector-config", nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), `"config_hash"`)
	require.Contains(t, recorder.Body.String(), `"deployment_manifest_hash":"`+route.Source.DeploymentManifestHash+`"`)
	require.Contains(t, recorder.Body.String(), `"collector_config_hash":"`+route.Route.CollectorConfigHash+`"`)
	require.Contains(t, recorder.Body.String(), `"collector_yaml"`)
	require.Contains(t, recorder.Body.String(), `"collector_config_files"`)
	require.Contains(t, recorder.Body.String(), `"service_config_path":"services/svc-orders-orders-api-`)
	require.Contains(t, recorder.Body.String(), `"service_config_yaml"`)
	require.Contains(t, recorder.Body.String(), "receivers:")
	require.Contains(t, recorder.Body.String(), "file_log/orders-orders-api")
	require.Contains(t, recorder.Body.String(), "base.yaml")
	require.NotContains(t, recorder.Body.String(), "kind: DaemonSet")
	require.NotContains(t, recorder.Body.String(), "collector.yaml: |")
}

func TestRouterUpdatesLogsEndpoint(t *testing.T) {
	env := newTestRouter(t)
	endpointBody := `{"name":"vl-prod","sink_type":"vl","write_url":"http://victorialogs:9428/insert/opentelemetry/v1/logs","query_url":"http://victorialogs:9428/select/logsql/query","vmui_url":"http://victorialogs:9428/select/vmui","account_id":"9527","project_id":"9527","secret_ref":"secret://vl/prod","scope_type":"global"}`
	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/endpoints", bytes.NewBufferString(endpointBody))
	createRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(createRecorder, createRequest)
	require.Equal(t, http.StatusCreated, createRecorder.Code)

	var created struct {
		Data logs.LogEndpoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &created))
	require.Empty(t, created.Data.AccountID)
	require.Empty(t, created.Data.ProjectID)
	require.NotContains(t, createRecorder.Body.String(), "secret_ref")

	updateBody := `{"name":"vl-prod-fixed","sink_type":"vl","write_url":"http://victorialogs-fixed:9428/insert/opentelemetry/v1/logs","query_url":"http://victorialogs-fixed:9428/select/logsql/query","vmui_url":"http://victorialogs-fixed:9428/select/vmui","account_id":"9528","project_id":"9529","secret_ref":"secret://vl/prod","scope_type":"global"}`
	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/logs/endpoints/"+created.Data.ID, bytes.NewBufferString(updateBody))
	updateRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(updateRecorder, updateRequest)

	require.Equal(t, http.StatusOK, updateRecorder.Code)
	require.Contains(t, updateRecorder.Body.String(), `"id":"`+created.Data.ID+`"`)
	require.Contains(t, updateRecorder.Body.String(), `"name":"vl-prod-fixed"`)
	require.Contains(t, updateRecorder.Body.String(), `"write_url":"http://victorialogs-fixed:9428/insert/opentelemetry/v1/logs"`)
	require.NotContains(t, updateRecorder.Body.String(), "account_id")
	require.NotContains(t, updateRecorder.Body.String(), "project_id")
	require.NotContains(t, updateRecorder.Body.String(), "secret_ref")
}

func TestRouterPublishesEndpointVmalertRuntimePreview(t *testing.T) {
	env := newTestRouter(t)
	endpointBody := `{"name":"vl-prod","sink_type":"vl","write_url":"http://victorialogs:9428/insert/opentelemetry/v1/logs","query_url":"http://victorialogs:9428/select/logsql/query","vmui_url":"http://victorialogs:9428/select/vmui","scope_type":"k8s_cluster","cluster_id":"prod-1"}`
	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/endpoints", bytes.NewBufferString(endpointBody))
	createRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(createRecorder, createRequest)
	require.Equal(t, http.StatusCreated, createRecorder.Code)
	var created struct {
		Data logs.LogEndpoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &created))

	publishRecorder := httptest.NewRecorder()
	publishRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/endpoints/"+created.Data.ID+"/vmalert-runtime/publish", bytes.NewBufferString(`{"alert_ingest_url":"http://novaapm-api:8080"}`))
	publishRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(publishRecorder, publishRequest)

	require.Equal(t, http.StatusOK, publishRecorder.Code)
	require.Contains(t, publishRecorder.Body.String(), `"runtime_id":"vmalert-logs:`+created.Data.ID+`"`)
	require.Contains(t, publishRecorder.Body.String(), `"requires_confirmation":true`)
	require.Contains(t, publishRecorder.Body.String(), `"manifest_yaml"`)
	require.Contains(t, publishRecorder.Body.String(), "-datasource.url=http://victorialogs:9428")
	require.Contains(t, publishRecorder.Body.String(), "-notifier.url=http://novaapm-api:8080")
}

func TestRouterPublishesMetricsAlertRuntimeForVictoriaMetricsDestination(t *testing.T) {
	env := newTestRouter(t)
	require.NoError(t, env.store.LogEndpoints().Insert(context.Background(), logs.LogEndpoint{
		ID:          "vm-prod",
		Name:        "vm-prod",
		Kind:        "victoriametrics",
		SignalTypes: []string{logs.EndpointSignalMetrics},
		QueryURL:    "http://victoriametrics:8428/api/v1/query",
		ScopeType:   logs.EndpointScopeK8sCluster,
		ClusterID:   "prod-1",
		Status:      "active",
	}))

	publishRecorder := httptest.NewRecorder()
	publishRequest := httptest.NewRequest(http.MethodPost, "/api/v1/observability/endpoints/vm-prod/metrics-alert-runtime/publish", bytes.NewBufferString(`{"alert_ingest_url":"http://novaapm-api:8080"}`))
	publishRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(publishRecorder, publishRequest)

	require.Equal(t, http.StatusOK, publishRecorder.Code)
	require.Contains(t, publishRecorder.Body.String(), `"runtime_id":"vmalert-metrics:vm-prod"`)
	require.Contains(t, publishRecorder.Body.String(), `"requires_confirmation":true`)
	require.Contains(t, publishRecorder.Body.String(), "-datasource.url=http://victoriametrics:8428")
	require.NotContains(t, publishRecorder.Body.String(), "-rule.defaultRuleType=vlogs")
}

func TestRouterDoesNotExposeLegacyLogClusterConfig(t *testing.T) {
	env := newTestRouter(t)

	getRecorder := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/logs/cluster-config?cluster_id=test03&agent_namespace=novaapm-system", nil)
	env.router.ServeHTTP(getRecorder, getRequest)
	require.Equal(t, http.StatusNotFound, getRecorder.Code)

	putRecorder := httptest.NewRecorder()
	putRequest := httptest.NewRequest(http.MethodPut, "/api/v1/logs/cluster-config", bytes.NewBufferString(`{"cluster_id":"test03","agent_namespace":"novaapm-system","processor_patch":"processors: {}"}`))
	putRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(putRecorder, putRequest)
	require.Equal(t, http.StatusNotFound, putRecorder.Code)
}

func TestRouterSoftDeletesServiceWithoutBlockingDependencies(t *testing.T) {
	env := newTestRouter(t)

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/services/"+env.service.ID, nil)
	env.router.ServeHTTP(deleteRecorder, deleteRequest)
	require.Equal(t, http.StatusNoContent, deleteRecorder.Code)

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
	env.router.ServeHTTP(listRecorder, listRequest)
	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.NotContains(t, listRecorder.Body.String(), env.service.ID)

	deletedRecorder := httptest.NewRecorder()
	deletedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/services?status=deleted", nil)
	env.router.ServeHTTP(deletedRecorder, deletedRequest)
	require.Equal(t, http.StatusOK, deletedRecorder.Code)
	require.Contains(t, deletedRecorder.Body.String(), env.service.ID)
	require.Contains(t, deletedRecorder.Body.String(), `"status":"deleted"`)
}

func TestRouterRejectsServiceDeleteWhenLogRouteExists(t *testing.T) {
	env := newTestRouter(t)
	createK8sLogRoute(t, env)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/services/"+env.service.ID, nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "日志路由")
}

func TestRouterRejectsCollectorGroupDeleteWhenLogRouteExists(t *testing.T) {
	env := newTestRouter(t)
	createK8sLogRoute(t, env)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/collector-groups/"+env.group.ID, nil)
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "服务日志配置")
}

func TestRouterPreviewsLogsParseRules(t *testing.T) {
	env := newTestRouter(t)

	recorder := httptest.NewRecorder()
	body := `{"sample":"WARN payment timeout","parse_rules":[{"name":"text","rule_type":"regex","pattern":"^(?P<level>[A-Z]+)\\s+(?P<message>.*)$","enabled":true}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/logs/parse-preview", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"status":"ok"`)
	require.Contains(t, recorder.Body.String(), `"level":"WARN"`)
	require.Contains(t, recorder.Body.String(), `"message":"payment timeout"`)
}

func createVMLogRouteRequestBody(t *testing.T, env testEnv) string {
	t.Helper()
	vmService := createVMService(t, env)
	endpointRecorder := httptest.NewRecorder()
	endpointBody := `{"name":"vl-prod","write_url":"http://victorialogs:9428/insert/opentelemetry/v1/logs","query_url":"http://victorialogs:9428/select/logsql/query","vmui_url":"http://victorialogs:9428/select/vmui","secret_ref":"secret://vl/prod"}`
	endpointRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/endpoints", bytes.NewBufferString(endpointBody))
	endpointRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(endpointRecorder, endpointRequest)
	require.Equal(t, http.StatusCreated, endpointRecorder.Code)

	var endpointEnvelope struct {
		Data logs.LogEndpoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(endpointRecorder.Body.Bytes(), &endpointEnvelope))
	return `{"service_id":"` + vmService.ID + `","source_type":"vm_file","agent_group_id":"` + env.group.ID + `","endpoint_id":"` + endpointEnvelope.Data.ID + `","vm":{"host_group":"billing-vms","path_pattern":"/data/logs/*.log"}}`
}

func createVMLogRoute(t *testing.T, env testEnv) logs.LogRouteView {
	t.Helper()
	routeRecorder := httptest.NewRecorder()
	routeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes", bytes.NewBufferString(createVMLogRouteRequestBody(t, env)))
	routeRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(routeRecorder, routeRequest)
	require.Equal(t, http.StatusCreated, routeRecorder.Code)

	var routeEnvelope struct {
		Data logs.LogRouteView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(routeRecorder.Body.Bytes(), &routeEnvelope))
	return routeEnvelope.Data
}

func createK8sLogRoute(t *testing.T, env testEnv) logs.LogRouteView {
	t.Helper()
	now := time.Now().UTC()
	runtime := obsruntime.Runtime{
		ID:         "logs-collector:" + env.service.Cluster + ":novaapm-system",
		Kind:       obsruntime.KindLogsCollector,
		SignalType: obsruntime.SignalLogs,
		ClusterID:  env.service.Cluster,
		Namespace:  "novaapm-system",
		Status:     obsruntime.StatusReady,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	require.NoError(t, env.store.ObservabilityRuntimes().Upsert(context.Background(), runtime.ID, runtime))
	endpointRecorder := httptest.NewRecorder()
	endpointBody := `{"name":"vl-prod-k8s","write_url":"http://victorialogs:9428/insert/opentelemetry/v1/logs","query_url":"http://victorialogs:9428/select/logsql/query","vmui_url":"http://victorialogs:9428/select/vmui","secret_ref":"secret://vl/prod"}`
	endpointRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/endpoints", bytes.NewBufferString(endpointBody))
	endpointRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(endpointRecorder, endpointRequest)
	require.Equal(t, http.StatusCreated, endpointRecorder.Code)

	var endpointEnvelope struct {
		Data logs.LogEndpoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(endpointRecorder.Body.Bytes(), &endpointEnvelope))

	routeBody := `{"service_id":"` + env.service.ID + `","source_type":"k8s_stdout","agent_group_id":"` + env.group.ID + `","endpoint_id":"` + endpointEnvelope.Data.ID + `","k8s":{"cluster_id":"` + env.service.Cluster + `","namespace":"` + env.service.Namespace + `","workload_kind":"Deployment","workload_name":"` + env.service.Name + `"}}`
	routeRecorder := httptest.NewRecorder()
	routeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/routes", bytes.NewBufferString(routeBody))
	routeRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(routeRecorder, routeRequest)
	require.Equal(t, http.StatusCreated, routeRecorder.Code)

	var routeEnvelope struct {
		Data logs.LogRouteView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(routeRecorder.Body.Bytes(), &routeEnvelope))
	return routeEnvelope.Data
}

func createVMService(t *testing.T, env testEnv) servicecatalog.Service {
	t.Helper()
	repo := servicecatalog.NewRepository(env.store.Services(), env.store.Environments(), env.store.Products())
	service, err := repo.Create(context.Background(), servicecatalog.Service{
		ProductID:     env.product.ID,
		Name:          "billing-api",
		DisplayName:   "billing-api",
		EnvironmentID: "production",
		OwnerTeam:     "billing-team",
		IdentityType:  "host_process",
		ServiceType:   "VM/物理机业务",
		Status:        "active",
		Source:        "manual",
		SyncStatus:    "local",
	})
	require.NoError(t, err)
	return service
}

func TestRouterPreviewsLogsParseRulesHandlesInvalidAndDisabledRules(t *testing.T) {
	env := newTestRouter(t)

	invalidJSONRecorder := httptest.NewRecorder()
	invalidJSONRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/parse-preview", bytes.NewBufferString(`{"sample":`))
	invalidJSONRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(invalidJSONRecorder, invalidJSONRequest)
	require.Equal(t, http.StatusBadRequest, invalidJSONRecorder.Code)

	invalidRegexRecorder := httptest.NewRecorder()
	invalidRegexBody := `{"sample":"WARN payment timeout","parse_rules":[{"name":"broken","rule_type":"regex","pattern":"^([A-Z]+)\\s+(.*)$","enabled":true}]}`
	invalidRegexRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/parse-preview", bytes.NewBufferString(invalidRegexBody))
	invalidRegexRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(invalidRegexRecorder, invalidRegexRequest)
	require.Equal(t, http.StatusOK, invalidRegexRecorder.Code)
	require.Contains(t, invalidRegexRecorder.Body.String(), `"status":"error"`)

	disabledRecorder := httptest.NewRecorder()
	disabledBody := `{"sample":"WARN payment timeout","parse_rules":[{"name":"disabled","rule_type":"regex","pattern":"^(?P<level>[A-Z]+)\\s+(?P<message>.*)$","enabled":false}]}`
	disabledRequest := httptest.NewRequest(http.MethodPost, "/api/v1/logs/parse-preview", bytes.NewBufferString(disabledBody))
	disabledRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(disabledRecorder, disabledRequest)
	require.Equal(t, http.StatusOK, disabledRecorder.Code)
	require.Contains(t, disabledRecorder.Body.String(), `"body":"WARN payment timeout"`)
	require.NotContains(t, disabledRecorder.Body.String(), `"level"`)
}

func TestRouterRemovesOldServicePipelineRoutes(t *testing.T) {
	env := newTestRouter(t)

	for _, item := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/logs?service=orders-api"},
		{method: http.MethodPut, path: "/api/v1/services/" + env.service.ID + "/pipeline/base", body: `{"base_yaml":""}`},
		{method: http.MethodPost, path: "/api/v1/services/" + env.service.ID + "/pipeline/publish"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(item.method, item.path, bytes.NewBufferString(item.body))
		request.Header.Set("Content-Type", "application/json")
		env.router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusNotFound, recorder.Code, item.path)
	}
}

func TestRouterReturnsServiceBoundAgentDetail(t *testing.T) {
	env := newTestRouter(t)
	env.manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-service-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		AgentDescription: &protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol-contrib"}}},
			},
		},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\n")},
		}}},
	})

	assignRecorder := httptest.NewRecorder()
	assignRequest := httptest.NewRequest(http.MethodPost, "/api/v1/opamp/instances/collector-service-a/service", bytes.NewBufferString(`{"service_id":"`+env.service.ID+`"}`))
	assignRequest.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(assignRecorder, assignRequest)
	require.Equal(t, http.StatusOK, assignRecorder.Code)

	detailRecorder := httptest.NewRecorder()
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/opamp/agents/collector-service-a", nil)
	env.router.ServeHTTP(detailRecorder, detailRequest)
	require.Equal(t, http.StatusOK, detailRecorder.Code)
	require.Contains(t, detailRecorder.Body.String(), `"service_id":"`+env.service.ID+`"`)
	require.Contains(t, detailRecorder.Body.String(), `"name":"orders-api"`)

}
