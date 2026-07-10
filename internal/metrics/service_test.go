package metrics

import (
	"context"
	"testing"

	"novaapm/internal/database/memstore"
	"novaapm/internal/logs"
	obsendpoint "novaapm/internal/observability/endpoint"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"

	"github.com/stretchr/testify/require"
)

func TestServiceCreatesActiveBinding(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")

	created, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})

	require.NoError(t, err)
	require.Equal(t, fixture.serviceCatalog.ID, created.Binding.ServiceID)
	require.Equal(t, endpoint.ID, created.Binding.EndpointID)
	require.Equal(t, BindingStatusActive, created.Binding.Status)
	require.Equal(t, ProbeStatusPending, created.Binding.LastProbeStatus)
	require.Equal(t, `{service_name="orders-api"}`, created.Binding.BasePromQL)
	require.Equal(t, "dev-admin", created.Binding.CreatedBy.ID)
	require.NotNil(t, created.Service)
	require.NotNil(t, created.Endpoint)
}

func TestServiceRejectsActiveBindingWithUnscopedBasePromQL(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")

	_, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
		BasePromQL: `up`,
	})

	require.ErrorContains(t, err, "active metrics binding 必须具备可收敛服务作用域")
}

func TestServiceRejectsBindingForAnotherServiceName(t *testing.T) {
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")

	_, err := fixture.service.CreateBinding(context.Background(), platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "payments-api"},
	})

	require.ErrorContains(t, err, "必须与当前服务名称一致")
}

func TestServiceRejectsDuplicateActiveBinding(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")
	secondEndpoint := fixture.createMetricsEndpoint(t, "vm-dr")
	_, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})
	require.NoError(t, err)

	_, err = fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: secondEndpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})

	require.ErrorContains(t, err, "服务已存在 active metrics binding")
}

func TestServiceCreatesBindingWithBindingManagePermission(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixtureWithAuthorizer(t, scopedPermissionAuthorizer{
		allowed: map[string]map[string]bool{
			"metrics.binding:manage": {fixtureServiceScope: true},
		},
	})
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")

	created, err := fixture.service.CreateBinding(ctx, platformrbac.Subject{ID: "alice", Type: "user"}, CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})

	require.NoError(t, err)
	require.Equal(t, fixture.serviceCatalog.ID, created.Binding.ServiceID)
}

func TestServiceRejectsMissingEndpoint(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)

	_, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: "missing-endpoint",
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})

	require.ErrorContains(t, err, "指标端点不存在")
}

func TestServiceProbeBindingValidatesConfigurationOnly(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")
	created, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})
	require.NoError(t, err)

	probed, err := fixture.service.ProbeBinding(ctx, platformrbac.DevAdminSubject(), created.Binding.ID)

	require.NoError(t, err)
	require.Equal(t, ProbeStatusVerified, probed.Binding.LastProbeStatus)
	require.Contains(t, probed.Binding.LastProbeMessage, "配置完整")
	require.NotNil(t, probed.Binding.LastProbeAt)
}

func TestServiceProbeBindingReturnsFailedForIncompleteEndpoint(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	require.NoError(t, fixture.store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:          "vm-incomplete",
		Name:        "vm-incomplete",
		Kind:        obsendpoint.KindVictoriaMetrics,
		SignalTypes: []string{obsendpoint.SignalTypeMetrics},
		Status:      "active",
	}))
	created, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: "vm-incomplete",
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})
	require.NoError(t, err)

	probed, err := fixture.service.ProbeBinding(ctx, platformrbac.DevAdminSubject(), created.Binding.ID)

	require.NoError(t, err)
	require.Equal(t, ProbeStatusFailed, probed.Binding.LastProbeStatus)
	require.Contains(t, probed.Binding.LastProbeMessage, "缺少查询、VMUI 或写入地址")
}

func TestServiceWorkspaceReturnsServiceEndpointsAndBinding(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")
	_, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})
	require.NoError(t, err)

	workspace, err := fixture.service.Workspace(ctx, platformrbac.DevAdminSubject(), fixture.serviceCatalog.ProductID, fixture.serviceCatalog.ID)

	require.NoError(t, err)
	require.Equal(t, fixture.serviceCatalog.ID, workspace.ActiveServiceID)
	require.Len(t, workspace.Services, 1)
	require.Equal(t, fixture.serviceCatalog.AccountID, workspace.Services[0].AccountID)
	require.Equal(t, fixture.serviceCatalog.ProjectID, workspace.Services[0].ProjectID)
	require.Len(t, workspace.Endpoints, 1)
	require.Equal(t, fixture.serviceCatalog.AccountID, workspace.Endpoints[0].Tenant.AccountID)
	require.Equal(t, fixture.serviceCatalog.ProjectID, workspace.Endpoints[0].Tenant.ProjectID)
	require.NotNil(t, workspace.Binding)
	require.Equal(t, endpoint.ID, workspace.Binding.Binding.EndpointID)
	require.Equal(t, fixture.serviceCatalog.AccountID, workspace.Binding.Binding.Tenant.AccountID)
	require.Equal(t, fixture.serviceCatalog.ProjectID, workspace.Binding.Binding.Tenant.ProjectID)
}

func TestServiceWorkspaceRequiresExplicitService(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	serviceRepo := servicecatalog.NewRepository(store.Services())
	_, err := serviceRepo.Create(ctx, servicecatalog.Service{
		Name:        "blocked-api",
		DisplayName: "无权限服务",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "blocked",
		Status:      "active",
	})
	require.NoError(t, err)
	readable, err := serviceRepo.Create(ctx, servicecatalog.Service{
		Name:        "orders-api",
		DisplayName: "订单服务",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "orders",
		Status:      "active",
	})
	require.NoError(t, err)
	service := NewService(Dependencies{
		Bindings:  store.MetricsServiceBindings(),
		Endpoints: obsendpoint.NewLogEndpointFacade(store.LogEndpoints()),
		Services:  serviceRepo,
		Authorizer: scopedPermissionAuthorizer{
			allowed: map[string]map[string]bool{
				"metrics.query:read": {readable.ID: true},
			},
		},
	})

	_, err = service.Workspace(ctx, platformrbac.Subject{ID: "alice", Type: "user"}, "", "")

	require.ErrorContains(t, err, "service_id")
	_ = readable
}

func TestServiceWorkspaceDoesNotLeakEndpointOrBindingWithoutReadPermissions(t *testing.T) {
	ctx := context.Background()
	fixture := newMetricsFixture(t)
	endpoint := fixture.createMetricsEndpoint(t, "vm-prod")
	_, err := fixture.service.CreateBinding(ctx, platformrbac.DevAdminSubject(), CreateServiceBindingRequest{
		ServiceID:  fixture.serviceCatalog.ID,
		EndpointID: endpoint.ID,
		LabelMatch: map[string]string{"service.name": "orders-api"},
	})
	require.NoError(t, err)
	serviceRepo := servicecatalog.NewRepository(fixture.store.Services())
	_, err = serviceRepo.Create(ctx, servicecatalog.Service{
		Name:        "blocked-api",
		DisplayName: "无权限服务",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "blocked",
		Status:      "active",
	})
	require.NoError(t, err)
	service := NewService(Dependencies{
		Bindings:  fixture.store.MetricsServiceBindings(),
		Endpoints: obsendpoint.NewLogEndpointFacade(fixture.store.LogEndpoints()),
		Services:  serviceRepo,
		Authorizer: scopedPermissionAuthorizer{
			allowed: map[string]map[string]bool{
				"metrics.query:read": {fixture.serviceCatalog.ID: true},
			},
		},
	})

	workspace, err := service.Workspace(ctx, platformrbac.Subject{ID: "alice", Type: "user"}, fixture.serviceCatalog.ProductID, fixture.serviceCatalog.ID)

	require.NoError(t, err)
	require.Equal(t, fixture.serviceCatalog.ID, workspace.ActiveServiceID)
	require.Len(t, workspace.Services, 1)
	require.Equal(t, fixture.serviceCatalog.ID, workspace.Services[0].ID)
	require.Empty(t, workspace.Endpoints)
	require.Nil(t, workspace.Binding)
}

type metricsFixture struct {
	store          *memstore.Store
	serviceCatalog servicecatalog.Service
	service        Service
}

func newMetricsFixture(t *testing.T) metricsFixture {
	return newMetricsFixtureWithAuthorizer(t, nil)
}

func newMetricsFixtureWithAuthorizer(t *testing.T, authorizer Authorizer) metricsFixture {
	t.Helper()
	ctx := context.Background()
	store := memstore.NewStore()
	productRepo := servicecatalog.NewProductRepository(store.Products())
	product, err := productRepo.Create(ctx, servicecatalog.Product{Name: "commerce"})
	require.NoError(t, err)
	serviceRepo := servicecatalog.NewRepository(store.Services(), store.Products())
	serviceCatalog, err := serviceRepo.Create(ctx, servicecatalog.Service{
		ProductID:   product.ID,
		Name:        "orders-api",
		DisplayName: "订单服务",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "orders",
		OwnerTeam:   "orders-team",
		Status:      "active",
	})
	require.NoError(t, err)
	if scoped, ok := authorizer.(scopedPermissionAuthorizer); ok {
		scoped.allowed = scoped.withFixtureServiceScope(serviceCatalog.ID)
		authorizer = scoped
	}
	if authorizer == nil {
		rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
		authorizer = platformrbac.NewService(rbacRepo, platformrbac.WithSuperSubjects(platformrbac.DevAdminSubject()))
	}
	return metricsFixture{
		store:          store,
		serviceCatalog: serviceCatalog,
		service: NewService(Dependencies{
			Bindings:   store.MetricsServiceBindings(),
			Endpoints:  obsendpoint.NewLogEndpointFacade(store.LogEndpoints()),
			Services:   serviceRepo,
			Authorizer: authorizer,
		}),
	}
}

func (f metricsFixture) createMetricsEndpoint(t *testing.T, id string) obsendpoint.Endpoint {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:          id,
		Name:        id,
		Kind:        obsendpoint.KindVictoriaMetrics,
		SignalTypes: []string{obsendpoint.SignalTypeMetrics},
		WriteURL:    "http://" + id + ":8480/insert/0/prometheus/api/v1/write",
		QueryURL:    "http://" + id + ":8481/select/0/prometheus",
		VMUIURL:     "http://" + id + ":8481/select/0/vmui/",
		Status:      "active",
	}))
	endpoint, err := obsendpoint.NewLogEndpointFacade(f.store.LogEndpoints()).Get(ctx, id)
	require.NoError(t, err)
	return endpoint
}

const fixtureServiceScope = "__fixture_service__"

type scopedPermissionAuthorizer struct {
	allowed map[string]map[string]bool
}

func (a scopedPermissionAuthorizer) Authorize(_ platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	serviceIDs := a.allowed[req.Resource+":"+req.Action]
	if serviceIDs == nil {
		return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
	}
	if serviceIDs["*"] || serviceIDs[req.Scope.ServiceID] {
		return platformrbac.Decision{Allowed: true}
	}
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

func (a scopedPermissionAuthorizer) withFixtureServiceScope(serviceID string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for permission, serviceIDs := range a.allowed {
		out[permission] = map[string]bool{}
		for id, allowed := range serviceIDs {
			if id == fixtureServiceScope {
				out[permission][serviceID] = allowed
				continue
			}
			out[permission][id] = allowed
		}
	}
	return out
}
