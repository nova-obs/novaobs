package alerting

import (
	"context"
	"testing"

	"novaobs/internal/database/memstore"
	"novaobs/internal/logs"
	"novaobs/internal/metrics"
	obsendpoint "novaobs/internal/observability/endpoint"
	"novaobs/internal/servicecatalog"

	"github.com/stretchr/testify/require"
)

func TestStoreScopeResolverResolvesExternalLogTarget(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service, endpoint := seedExternalTargetScope(t, ctx, store)
	resolver := NewStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints())

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
	require.Equal(t, "target-orders", scope.LogTargetID)
	require.Empty(t, scope.LogRouteID)
	require.Equal(t, endpoint.ID, scope.EndpointID)
	require.Equal(t, "1001", scope.AccountID)
	require.Equal(t, "2001", scope.ProjectID)
	require.Equal(t, `"stream":"orders"`, scope.BaseFilter)

	target, err := resolver.ResolveQueryTarget(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, endpoint.QueryURL, target.QueryURL)
	require.Equal(t, `"stream":"orders"`, target.BaseFilter)
}

func TestStoreScopeResolverResolvesExplicitMetricsBinding(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service := seedMetricsBindingScope(t, ctx, store, metrics.BindingStatusActive)
	resolver := NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsServiceBindings())

	scope, err := resolver.ResolveScope(ctx, RuleSpec{
		SignalType: SignalTypeMetrics,
		Scope:      RuleScope{MetricsBindingID: "binding-orders"},
	})

	require.NoError(t, err)
	require.Equal(t, service.ID, scope.ServiceID)
	require.Equal(t, service.Name, scope.ServiceName)
	require.Equal(t, "binding-orders", scope.MetricsBindingID)
	require.Equal(t, "vm-prod", scope.EndpointID)
	require.Equal(t, "1001", scope.AccountID)
	require.Equal(t, "2001", scope.ProjectID)
	require.Equal(t, `service:requests:rate5m{service="orders-api"}`, scope.BasePromQL)
}

func TestStoreScopeResolverSelectsActiveMetricsBindingForService(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service := seedMetricsBindingScope(t, ctx, store, metrics.BindingStatusActive)
	require.NoError(t, store.MetricsServiceBindings().Insert(ctx, metrics.ServiceBinding{
		ID:         "binding-disabled",
		ServiceID:  service.ID,
		EndpointID: "vm-disabled",
		Status:     metrics.BindingStatusDisabled,
	}))
	resolver := NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsServiceBindings())

	scope, err := resolver.ResolveScope(ctx, RuleSpec{
		SignalType: SignalTypeMetrics,
		Scope:      RuleScope{ServiceID: service.ID},
	})

	require.NoError(t, err)
	require.Equal(t, "binding-orders", scope.MetricsBindingID)
	require.Equal(t, "vm-prod", scope.EndpointID)
}

func TestStoreScopeResolverRejectsMetricsBindingWithoutQueryScope(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service := seedMetricsBindingScope(t, ctx, store, metrics.BindingStatusDisabled)
	require.NoError(t, store.MetricsServiceBindings().Insert(ctx, metrics.ServiceBinding{
		ID:         "binding-unscoped",
		ServiceID:  service.ID,
		EndpointID: "vm-prod",
		LabelMatch: map[string]string{"service.name": "orders-api"},
		BasePromQL: `up`,
		Status:     metrics.BindingStatusActive,
	}))
	resolver := NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsServiceBindings())

	_, err := resolver.ResolveScope(ctx, RuleSpec{
		SignalType: SignalTypeMetrics,
		Scope:      RuleScope{MetricsBindingID: "binding-unscoped"},
	})

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.ErrorContains(t, err, "base_promql")
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
	resolver := NewStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints())

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
	repo := servicecatalog.NewRepository(store.Services())
	service, err := repo.Create(ctx, servicecatalog.Service{
		Name:         "orders-api",
		DisplayName:  "订单服务",
		Environment:  "prod",
		OwnerTeam:    "orders-team",
		IdentityType: "host_process",
		Status:       "active",
		Source:       "manual",
		SyncStatus:   "local",
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

func seedMetricsBindingScope(t *testing.T, ctx context.Context, store *memstore.Store, status string) servicecatalog.Service {
	t.Helper()
	repo := servicecatalog.NewRepository(store.Services())
	service, err := repo.Create(ctx, servicecatalog.Service{
		Name:         "orders-api",
		DisplayName:  "订单服务",
		Environment:  "prod",
		OwnerTeam:    "orders-team",
		IdentityType: "k8s_workload",
		Status:       "active",
		Source:       "manual",
		SyncStatus:   "local",
	})
	require.NoError(t, err)
	require.NoError(t, store.MetricsServiceBindings().Insert(ctx, metrics.ServiceBinding{
		ID:         "binding-orders",
		ServiceID:  service.ID,
		EndpointID: "vm-prod",
		Tenant:     obsendpoint.EndpointTenant{AccountID: "1001", ProjectID: "2001"},
		LabelMatch: map[string]string{"service.name": "orders-api"},
		BasePromQL: `service:requests:rate5m{service="orders-api"}`,
		Status:     status,
	}))
	return service
}
