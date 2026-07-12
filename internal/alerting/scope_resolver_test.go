package alerting

import (
	"context"
	"errors"
	"testing"

	"novaapm/internal/database"
	"novaapm/internal/database/memstore"
	"novaapm/internal/logs"
	"novaapm/internal/metrics"
	"novaapm/internal/platform/environment"
	"novaapm/internal/servicecatalog"

	"github.com/stretchr/testify/require"
)

func TestStoreScopeResolverResolvesExternalLogTarget(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service, endpoint := seedExternalTargetScope(t, ctx, store)
	resolver := NewStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.Products())

	scope, err := resolver.ResolveScope(ctx, RuleSpec{
		Scope: RuleScope{
			ServiceID:   service.ID,
			LogTargetID: "target-orders",
			EndpointID:  endpoint.ID,
		},
	})

	require.NoError(t, err)
	require.Equal(t, service.ID, scope.ServiceID)
	require.Equal(t, service.Name, scope.ServiceName)
	require.Equal(t, service.EnvironmentID, scope.EnvironmentID)
	require.Equal(t, "target-orders", scope.LogTargetID)
	require.Empty(t, scope.LogRouteID)
	require.Equal(t, endpoint.ID, scope.EndpointID)
	require.Equal(t, service.AccountID, scope.AccountID)
	require.Equal(t, service.ProjectID, scope.ProjectID)
	require.Equal(t, `"stream":"orders"`, scope.BaseFilter)

	target, err := resolver.ResolveQueryTarget(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, endpoint.QueryURL, target.QueryURL)
	require.Equal(t, `"stream":"orders"`, target.BaseFilter)
}

func TestStoreScopeResolverPrefersExternalLogTargetTenant(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service, endpoint := seedExternalTargetScope(t, ctx, store)
	var target logs.LogTarget
	require.NoError(t, store.LogTargets().FindByID(ctx, "target-orders", &target))
	target.AccountID = "1001"
	target.ProjectID = "2001"
	require.NoError(t, store.LogTargets().Update(ctx, target.ID, target))
	resolver := NewStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.Products())

	scope, err := resolver.ResolveScope(ctx, RuleSpec{Scope: RuleScope{
		ServiceID: service.ID, LogTargetID: target.ID, EndpointID: endpoint.ID,
	}})

	require.NoError(t, err)
	require.Equal(t, "1001", scope.AccountID)
	require.Equal(t, "2001", scope.ProjectID)
}

func TestStoreScopeResolverResolvesMetricsEnvironment(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	seedMetricsEnvironmentScope(t, ctx, store, metrics.DesiredStateConnected)
	resolver := NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsIntegrations(), store.Environments(), store.Products())

	scope, err := resolver.ResolveScope(ctx, RuleSpec{
		SignalType: SignalTypeMetrics,
		Scope:      RuleScope{EnvironmentID: "env-prod", ScopeLabels: map[string]string{"cluster": "prod-a"}},
	})

	require.NoError(t, err)
	require.Equal(t, "env-prod", scope.EnvironmentID)
	require.Equal(t, "生产环境", scope.EnvironmentName)
	require.Equal(t, "vm-prod", scope.EndpointID)
	require.Equal(t, map[string]string{"cluster": "prod-a"}, scope.ScopeLabels)
}

func TestStoreScopeResolverRejectsEndpointOutsideEnvironmentIntegration(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	seedMetricsEnvironmentScope(t, ctx, store, metrics.DesiredStateConnected)
	resolver := NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsIntegrations(), store.Environments(), store.Products())

	scope, err := resolver.ResolveScope(ctx, RuleSpec{
		SignalType: SignalTypeMetrics,
		Scope:      RuleScope{EnvironmentID: "env-prod", EndpointID: "vm-other"},
	})

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Empty(t, scope.EndpointID)
}

func TestStoreScopeResolverRejectsDisconnectedMetricsEnvironment(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	seedMetricsEnvironmentScope(t, ctx, store, metrics.DesiredStateDisconnected)
	resolver := NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsIntegrations(), store.Environments(), store.Products())

	_, err := resolver.ResolveScope(ctx, RuleSpec{
		SignalType: SignalTypeMetrics,
		Scope:      RuleScope{EnvironmentID: "env-prod"},
	})

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.ErrorContains(t, err, "未连接")
}

func TestMetricsCompileOnlyTesterDoesNotCallVictoriaMetrics(t *testing.T) {
	tester := MetricsCompileOnlyTester{}
	result, err := tester.Test(context.Background(), TestRequest{Spec: validMetricsRuleSpec()})

	require.NoError(t, err)
	require.Contains(t, result.CompiledQuery, "http_requests_total")
	require.NotEmpty(t, result.Warnings)
	require.Zero(t, result.QueryDurationMillis)
}

func TestStoreScopeResolverRejectsExternalTargetEndpointMismatch(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service, _ := seedExternalTargetScope(t, ctx, store)
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:        "vl-other",
		Name:      "other",
		SinkType:  logs.EndpointSinkVL,
		WriteURL:  "http://vl.other:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.other:9428/select/logsql/query",
		VMUIURL:   "http://vl.other:9428/select/vmui",
		AccountID: "1001",
		ProjectID: "2001",
		Status:    "active",
	}))
	resolver := NewStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.Products())

	_, err := resolver.ResolveScope(ctx, RuleSpec{
		Scope: RuleScope{
			ServiceID:   service.ID,
			LogTargetID: "target-orders",
			EndpointID:  "vl-other",
		},
	})

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.ErrorContains(t, err, "服务、日志目标和日志端点关系不一致")
}

func seedExternalTargetScope(t *testing.T, ctx context.Context, store *memstore.Store) (servicecatalog.Service, logs.LogEndpoint) {
	t.Helper()
	if err := store.Environments().Insert(ctx, environment.Environment{ID: "prod", Name: "生产环境", Stage: environment.StageProduction, Status: environment.StatusActive}); err != nil && !errors.Is(err, database.ErrConflict) {
		require.NoError(t, err)
	}
	productRepo := servicecatalog.NewProductRepository(store.Products())
	product, err := productRepo.Create(ctx, servicecatalog.Product{Name: "commerce"})
	require.NoError(t, err)
	repo := servicecatalog.NewRepository(store.Services(), store.Environments(), store.Products())
	service, err := repo.Create(ctx, servicecatalog.Service{
		ProductID:     product.ID,
		Name:          "orders-api",
		DisplayName:   "订单服务",
		EnvironmentID: "prod",
		OwnerTeam:     "orders-team",
		IdentityType:  "host_process",
		Status:        "active",
		Source:        "manual",
		SyncStatus:    "local",
	})
	require.NoError(t, err)
	endpoint := logs.LogEndpoint{
		ID:        "vl-external",
		Name:      "vl-external",
		SinkType:  logs.EndpointSinkVL,
		WriteURL:  "http://vl.external:9428/insert/opentelemetry/v1/logs",
		QueryURL:  "http://vl.external:9428/select/logsql/query",
		VMUIURL:   "http://vl.external:9428/select/vmui",
		AccountID: "1001",
		ProjectID: "2001",
		Status:    "active",
	}
	require.NoError(t, store.LogEndpoints().Insert(ctx, endpoint))
	require.NoError(t, store.LogTargets().Insert(ctx, logs.LogTarget{
		ID:         "target-orders",
		Name:       "orders 自建 VL",
		ServiceID:  service.ID,
		EndpointID: endpoint.ID,
		SourceKind: logs.LogTargetSourceExternalVLogs,
		BaseFilter: `"stream":"orders"`,
		Status:     logs.LogTargetStatusVerified,
	}))
	return service, endpoint
}

func seedMetricsEnvironmentScope(t *testing.T, ctx context.Context, store *memstore.Store, state string) {
	t.Helper()
	require.NoError(t, store.Environments().Insert(ctx, environment.Environment{
		ID: "env-prod", Name: "生产环境", Stage: environment.StageProduction, Status: environment.StatusActive,
	}))
	require.NoError(t, store.MetricsIntegrations().Insert(ctx, metrics.Integration{
		ID: "integration-prod", EnvironmentID: "env-prod", DestinationRef: "vm-prod", DesiredState: state,
	}))
}
