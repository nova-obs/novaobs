package metrics

import (
	"context"
	"testing"

	"novaapm/internal/database/memstore"
	"novaapm/internal/logs"
	k8sopsdeployment "novaapm/internal/modules/k8sops/deployment"
	obsendpoint "novaapm/internal/observability/endpoint"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"

	"github.com/stretchr/testify/require"
)

func TestCreateMetricRouteDefinesManagedK8sServiceScrape(t *testing.T) {
	fixture := newMetricRouteFixture(t)

	created, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), CreateRouteRequest{
		ServiceID:      fixture.catalogService.ID,
		Name:           "orders metrics",
		EndpointID:     fixture.endpoint.ID,
		ClusterID:      "prod-1",
		Namespace:      "orders",
		K8sServiceName: "orders-api",
		Port:           "metrics",
		Scheme:         "http",
		MetricsPath:    "/metrics",
		ScrapeInterval: "30s",
		ScrapeTimeout:  "10s",
	})

	require.NoError(t, err)
	require.Equal(t, MetricRouteSourceK8sService, created.Route.SourceKind)
	require.Equal(t, fixture.catalogService.ProductID, created.Route.ProductID)
	require.Equal(t, "orders-api", created.Route.K8sServiceName)
	require.Equal(t, RoutePublishStatusPending, created.Route.LastPublishStatus)
	require.Equal(t, `{service_name="orders-api"}`, created.Route.BasePromQL)
	require.Equal(t, fixture.catalogService.ProjectID, created.Endpoint.Tenant.ProjectID)
	require.Contains(t, created.RuntimeID, "metrics-collector:")
}

func TestCreateMetricRouteRejectsUnsafeOrAmbiguousScrapeConfiguration(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	request := fixture.validRouteRequest()
	request.MetricsPath = "http://metadata.internal/latest"

	_, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), request)
	require.ErrorContains(t, err, "metrics_path 必须是绝对路径")

	request = fixture.validRouteRequest()
	request.ScrapeInterval = "15s"
	request.ScrapeTimeout = "15s"
	_, err = fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), request)
	require.ErrorContains(t, err, "scrape_timeout 必须小于 scrape_interval")

	request = fixture.validRouteRequest()
	request.ClusterID = "another-cluster"
	_, err = fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), request)
	require.ErrorContains(t, err, "采集集群必须与当前服务集群一致")
}

func TestCreateMetricRouteRejectsDuplicateTargetWithinProductRuntime(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	_, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)
	second, err := servicecatalog.NewRepository(fixture.store.Services(), fixture.store.Products()).Create(context.Background(), servicecatalog.Service{
		ProductID: fixture.catalogService.ProductID, Name: "orders-shadow", DisplayName: "订单影子服务", Environment: "production",
		Cluster: "prod-1", Namespace: "orders", Status: "active",
	})
	require.NoError(t, err)
	request := fixture.validRouteRequest()
	request.ServiceID = second.ID

	_, err = fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), request)

	require.ErrorContains(t, err, "相同指标采集目标已存在")
}

func TestCreateMetricRouteRejectsEndpointFromAnotherCluster(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	require.NoError(t, fixture.store.LogEndpoints().Insert(context.Background(), logs.LogEndpoint{
		ID: "vm-other", Name: "vm-other", Kind: obsendpoint.KindVictoriaMetrics,
		SignalTypes: []string{obsendpoint.SignalTypeMetrics}, ScopeType: logs.EndpointScopeK8sCluster, ClusterID: "prod-2",
		WriteURL: "http://vminsert:8480/insert/0/prometheus/api/v1/write", QueryURL: "http://vmselect:8481/select/0/prometheus", Status: "active",
	}))
	request := fixture.validRouteRequest()
	request.EndpointID = "vm-other"

	_, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), request)

	require.ErrorContains(t, err, "当前集群绑定")
}

func TestUpdateMetricRouteMarksAppliedConfigurationPendingPublish(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	created, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)
	stored := created.Route
	stored.LastPublishStatus = RoutePublishStatusApplied
	stored.AppliedConfigHash = "old-hash"
	require.NoError(t, fixture.store.MetricsRoutes().Update(context.Background(), stored.ID, stored))
	interval := "45s"

	updated, err := fixture.service.UpdateRoute(context.Background(), platformrbac.DevAdminSubject(), stored.ID, UpdateRouteRequest{
		ScrapeInterval: &interval,
	})

	require.NoError(t, err)
	require.Equal(t, "45s", updated.Route.ScrapeInterval)
	require.Equal(t, RoutePublishStatusPending, updated.Route.LastPublishStatus)
	require.Equal(t, "old-hash", updated.Route.AppliedConfigHash)
}

func TestUpdateMetricRouteRejectsAppliedRuntimeGroupMigration(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	created, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)
	stored := created.Route
	stored.AppliedConfigHash = "applied-hash"
	require.NoError(t, fixture.store.MetricsRoutes().Update(context.Background(), stored.ID, stored))
	endpointID := "another-endpoint"

	_, err = fixture.service.UpdateRoute(context.Background(), platformrbac.DevAdminSubject(), stored.ID, UpdateRouteRequest{EndpointID: &endpointID})

	require.ErrorContains(t, err, "已部署路由不能变更")
}

func TestPublishMetricCollectorRuntimeUsesDeploymentAndTenantScopedRemoteWrite(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	created, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)

	preview, err := fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.DevAdminSubject(), CollectorRuntimePublishRequest{
		RouteID:   created.Route.ID,
		Namespace: "novaapm-system",
	})

	require.NoError(t, err)
	require.True(t, preview.RequiresConfirmation)
	require.Contains(t, preview.ManifestYAML, "kind: Deployment")
	require.NotContains(t, preview.ManifestYAML, "kind: DaemonSet")
	require.Contains(t, preview.ManifestYAML, "role: endpointslice")
	require.Contains(t, preview.ManifestYAML, "__meta_kubernetes_service_name")
	require.Contains(t, preview.ManifestYAML, "service_name")
	require.Contains(t, preview.ManifestYAML, "/insert/0:"+fixture.catalogService.ProjectID+"/prometheus/api/v1/write")
	require.Contains(t, preview.ManifestYAML, "kind: RoleBinding")
	require.NotContains(t, preview.ManifestYAML, "kind: ClusterRoleBinding")
	require.Contains(t, preview.ManifestYAML, "namespace: orders")

	applied, err := fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.DevAdminSubject(), CollectorRuntimePublishRequest{
		RouteID:           created.Route.ID,
		Namespace:         "novaapm-system",
		PreviewID:         preview.PreviewID,
		ConfirmationToken: preview.ConfirmationToken,
	})

	require.NoError(t, err)
	require.False(t, applied.RequiresConfirmation)
	require.Equal(t, "applied", applied.Status)
	routes, err := fixture.service.ListRoutes(context.Background(), platformrbac.DevAdminSubject(), created.Route.ServiceID, "")
	require.NoError(t, err)
	require.Equal(t, RoutePublishStatusApplied, routes[0].Route.LastPublishStatus)
	require.NotEmpty(t, routes[0].Route.AppliedConfigHash)
}

func TestPublishMetricCollectorRuntimeDoesNotMixProductsSharingEndpoint(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	first, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)
	productRepo := servicecatalog.NewProductRepository(fixture.store.Products())
	secondProduct, err := productRepo.Create(context.Background(), servicecatalog.Product{Name: "payments"})
	require.NoError(t, err)
	serviceRepo := servicecatalog.NewRepository(fixture.store.Services(), fixture.store.Products())
	secondService, err := serviceRepo.Create(context.Background(), servicecatalog.Service{
		ProductID: secondProduct.ID, Name: "payments-api", Environment: "production", Cluster: "prod-1", Namespace: "payments", Status: "active",
	})
	require.NoError(t, err)
	second, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), CreateRouteRequest{
		ServiceID: secondService.ID, Name: "payments metrics", EndpointID: fixture.endpoint.ID,
		ClusterID: "prod-1", Namespace: "payments", K8sServiceName: "payments-api", Port: "metrics",
		Scheme: "http", MetricsPath: "/metrics", ScrapeInterval: "30s", ScrapeTimeout: "10s",
	})
	require.NoError(t, err)

	preview, err := fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.DevAdminSubject(), CollectorRuntimePublishRequest{RouteID: first.Route.ID})
	require.NoError(t, err)

	require.Contains(t, preview.ManifestYAML, first.Route.ID)
	require.NotContains(t, preview.ManifestYAML, second.Route.ID)
	require.Contains(t, preview.ManifestYAML, "/insert/0:"+fixture.catalogService.ProjectID+"/prometheus/api/v1/write")
	require.NotContains(t, preview.ManifestYAML, secondService.ProjectID)
}

func TestPublishMetricCollectorRuntimeRequiresClusterRuntimePermission(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	created, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)
	fixture.service.authorizer = scopedPermissionAuthorizer{allowed: map[string]map[string]bool{
		"metrics.route:manage":  {fixture.catalogService.ID: true},
		"metrics.endpoint:read": {"*": true},
	}}

	_, err = fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.Subject{ID: "route-manager", Type: "user"}, CollectorRuntimePublishRequest{
		RouteID: created.Route.ID,
	})

	require.ErrorIs(t, err, ErrPermissionDenied)
}

func TestPublishMetricCollectorRuntimeRequiresRoutePermissionForWholeGroup(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	anchor, err := fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), fixture.validRouteRequest())
	require.NoError(t, err)
	serviceRepo := servicecatalog.NewRepository(fixture.store.Services(), fixture.store.Products())
	secondService, err := serviceRepo.Create(context.Background(), servicecatalog.Service{
		ProductID: fixture.catalogService.ProductID, Name: "payments-api", Environment: "production", Cluster: "prod-1", Namespace: "payments", Status: "active",
	})
	require.NoError(t, err)
	_, err = fixture.service.CreateRoute(context.Background(), platformrbac.DevAdminSubject(), CreateRouteRequest{
		ServiceID: secondService.ID, EndpointID: fixture.endpoint.ID, ClusterID: "prod-1", Namespace: "payments",
		K8sServiceName: "payments-api", Port: "metrics", Scheme: "http", MetricsPath: "/metrics", ScrapeInterval: "30s", ScrapeTimeout: "10s",
	})
	require.NoError(t, err)
	fixture.service.authorizer = scopedPermissionAuthorizer{allowed: map[string]map[string]bool{
		"metrics.route:manage":   {fixture.catalogService.ID: true},
		"metrics.runtime:manage": {"*": true},
		"metrics.endpoint:read":  {"*": true},
	}}

	_, err = fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.Subject{ID: "limited", Type: "user"}, CollectorRuntimePublishRequest{RouteID: anchor.Route.ID})

	require.ErrorIs(t, err, ErrPermissionDenied)
}

func TestRenderMetricRuntimeKeepsResourceIdentityWhenRemoteWriteAddressChanges(t *testing.T) {
	item := metricRuntimeRoute{
		Route:   MetricRoute{ID: "route-1", EndpointID: "vm-prod", Status: MetricRouteStatusActive, ClusterID: "prod-1", Namespace: "orders", K8sServiceName: "orders-api", Port: "metrics", Scheme: "http", MetricsPath: "/metrics", ScrapeInterval: "30s", ScrapeTimeout: "10s"},
		Service: servicecatalog.Service{ID: "service-1", ProductID: "product-1", Name: "orders-api"},
	}
	first, err := renderMetricsCollectorRuntime("prod-1", "novaapm-system", "http://vminsert-a/write", []metricRuntimeRoute{item})
	require.NoError(t, err)
	second, err := renderMetricsCollectorRuntime("prod-1", "novaapm-system", "http://vminsert-b/write", []metricRuntimeRoute{item})
	require.NoError(t, err)

	require.Equal(t, first.Name, second.Name)
	require.NotEqual(t, first.ConfigHash, second.ConfigHash)
	require.Contains(t, second.ManifestYAML, "http://vminsert-b/write")
}

func TestPublishMetricCollectorRuntimeRejectsInvalidNamespace(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	_, err := fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.DevAdminSubject(), CollectorRuntimePublishRequest{
		RouteID: "route-1", Namespace: "invalid namespace",
	})
	require.ErrorContains(t, err, "运行时 namespace")
}

func TestPublishMetricCollectorRuntimeRejectsAlternateNamespace(t *testing.T) {
	fixture := newMetricRouteFixture(t)
	_, err := fixture.service.PublishCollectorRuntime(context.Background(), platformrbac.DevAdminSubject(), CollectorRuntimePublishRequest{
		RouteID: "route-1", Namespace: "metrics-system",
	})
	require.ErrorContains(t, err, "固定为 novaapm-system")
}

func TestMetricRemoteWriteURLRejectsInlineCredentials(t *testing.T) {
	err := validateMetricRemoteWriteURL("http://user:password@vminsert:8480/insert/0:project/prometheus/api/v1/write")

	require.ErrorContains(t, err, "不能包含内联凭据")
}

func TestMetricRemoteWriteURLRejectsNonInsertPath(t *testing.T) {
	err := validateMetricRemoteWriteURL("http://vmselect:8481/select/0:project/prometheus/api/v1/query")

	require.ErrorContains(t, err, "insert Prometheus 写入路径")
}

func TestMetricServicePortExistsMatchesNameOrNumber(t *testing.T) {
	ports := []any{
		map[string]any{"name": "http", "port": int64(80), "targetPort": int64(8080)},
		map[string]any{"name": "metrics", "port": int64(9090), "targetPort": int64(9090)},
	}

	require.True(t, metricServicePortExists(ports, "metrics"))
	require.True(t, metricServicePortExists(ports, "9090"))
	resolved, ok := resolveMetricServiceDiscoveryPort(ports, "80")
	require.True(t, ok)
	require.Equal(t, "http", resolved)
	require.False(t, metricServicePortExists(ports, "admin"))
}

type metricRouteFixture struct {
	store          *memstore.Store
	catalogService servicecatalog.Service
	endpoint       obsendpoint.Endpoint
	deployment     *recordingMetricsDeploymentService
	service        Service
}

func newMetricRouteFixture(t *testing.T) metricRouteFixture {
	t.Helper()
	ctx := context.Background()
	store := memstore.NewStore()
	product, err := servicecatalog.NewProductRepository(store.Products()).Create(ctx, servicecatalog.Product{Name: "commerce"})
	require.NoError(t, err)
	serviceRepo := servicecatalog.NewRepository(store.Services(), store.Products())
	catalogService, err := serviceRepo.Create(ctx, servicecatalog.Service{
		ProductID: product.ID, Name: "orders-api", DisplayName: "订单服务", Environment: "production", Cluster: "prod-1", Namespace: "orders", Status: "active",
	})
	require.NoError(t, err)
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID: "vm-prod", Name: "vm-prod", Kind: obsendpoint.KindVictoriaMetrics,
		SignalTypes: []string{obsendpoint.SignalTypeMetrics},
		WriteURL:    "http://vminsert:8480/insert/0/prometheus/api/v1/write",
		QueryURL:    "http://vmselect:8481/select/0/prometheus",
		VMUIURL:     "http://vmselect:8481/select/0/vmui/", Status: "active",
	}))
	endpointReader := obsendpoint.NewLogEndpointFacade(store.LogEndpoints())
	endpoint, err := endpointReader.Get(ctx, "vm-prod")
	require.NoError(t, err)
	deployment := &recordingMetricsDeploymentService{}
	rbacService := platformrbac.NewService(platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings()), platformrbac.WithSuperSubjects(platformrbac.DevAdminSubject()))
	return metricRouteFixture{
		store: store, catalogService: catalogService, endpoint: endpoint, deployment: deployment,
		service: NewService(Dependencies{
			Bindings: store.MetricsServiceBindings(), Routes: store.MetricsRoutes(), Runtimes: store.ObservabilityRuntimes(),
			Endpoints: endpointReader, Services: serviceRepo, K8sDeployments: deployment, Authorizer: rbacService,
		}),
	}
}

func (f metricRouteFixture) validRouteRequest() CreateRouteRequest {
	return CreateRouteRequest{
		ServiceID: f.catalogService.ID, Name: "orders metrics", EndpointID: f.endpoint.ID,
		ClusterID: "prod-1", Namespace: "orders", K8sServiceName: "orders-api", Port: "metrics",
		Scheme: "http", MetricsPath: "/metrics", ScrapeInterval: "30s", ScrapeTimeout: "10s",
	}
}

type recordingMetricsDeploymentService struct {
	lastPreview k8sopsdeployment.OperationRequest
	lastApply   k8sopsdeployment.OperationRequest
}

func (s *recordingMetricsDeploymentService) Preview(_ context.Context, _ platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	s.lastPreview = req
	return k8sopsdeployment.OperationResult{Status: "previewed", Message: "ok", PreviewID: "preview-1", ConfirmationToken: "confirm-1"}, nil
}

func (s *recordingMetricsDeploymentService) Apply(_ context.Context, _ platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error) {
	s.lastApply = req
	return k8sopsdeployment.OperationResult{Status: "applied", Message: "ok", AuditID: "audit-1"}, nil
}
