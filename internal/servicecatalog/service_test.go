package servicecatalog

import (
	"context"
	"strconv"
	"testing"

	"novaapm/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestRepositoryCreatesAndListsServices(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	svc, err := repo.Create(ctx, Service{
		Name:        "payment-gateway",
		Environment: "production",
		Cluster:     "prod-cluster-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, svc.ID)
	require.Equal(t, "pending", svc.Status)
	require.Equal(t, "manual", svc.Source)
	require.Equal(t, "local", svc.SyncStatus)
	require.False(t, svc.CreatedAt.IsZero())
	require.False(t, svc.UpdatedAt.IsZero())

	services, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, services, 1)
	require.Equal(t, "payment-gateway", services[0].Name)
}

func TestProductRepositoryGeneratesUniqueProjectIDs(t *testing.T) {
	store := memstore.NewStore()
	repo := NewProductRepository(store.Products())
	ctx := context.Background()

	first, err := repo.Create(ctx, Product{ID: "client-product-id", Name: "commerce", ProjectID: "1", Status: "disabled"})
	require.NoError(t, err)
	second, err := repo.Create(ctx, Product{Name: "payments"})
	require.NoError(t, err)

	requireValidTenantID(t, first.ProjectID)
	require.NotEqual(t, "client-product-id", first.ID)
	require.Equal(t, "active", first.Status)
	require.NotEqual(t, "1", first.ProjectID)
	require.NotEqual(t, first.ProjectID, second.ProjectID)
}

func TestServicesInSameProductShareProjectTenant(t *testing.T) {
	store := memstore.NewStore()
	products := NewProductRepository(store.Products())
	repo := NewRepository(store.Services(), store.Products())
	ctx := context.Background()

	product, err := products.Create(ctx, Product{Name: "commerce"})
	require.NoError(t, err)
	first, err := repo.Create(ctx, Service{Name: "orders-api", Environment: "prod", ProductID: product.ID})
	require.NoError(t, err)
	second, err := repo.Create(ctx, Service{Name: "payments-api", Environment: "prod", ProductID: product.ID})
	require.NoError(t, err)

	require.Equal(t, "0", first.AccountID)
	require.Equal(t, product.ProjectID, first.ProjectID)
	require.Equal(t, first.ProjectID, second.ProjectID)
}

func TestProductAwareRepositoryRejectsOrphanService(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services(), store.Products())

	_, err := repo.Create(context.Background(), Service{Name: "orders-api", Environment: "prod"})

	require.ErrorContains(t, err, "所属产品")
}

func requireValidTenantID(t *testing.T, value string) {
	t.Helper()
	parsed, err := strconv.ParseUint(value, 10, 32)
	require.NoError(t, err)
	require.NotZero(t, parsed)
}

func TestRepositoryRejectsDuplicateServiceIdentity(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	_, err := repo.Create(ctx, Service{
		Name:        "payment-gateway",
		Environment: "production",
		Cluster:     "prod-cluster-1",
		Namespace:   "payments",
	})
	require.NoError(t, err)

	_, err = repo.Create(ctx, Service{
		Name:        "payment-gateway",
		Environment: "production",
		Cluster:     "prod-cluster-1",
		Namespace:   "payments",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "服务名称已存在")
}

func TestRepositoryRejectsUnsupportedServiceIdentityType(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	_, err := repo.Create(ctx, Service{
		Name:         "syslog-device",
		Environment:  "production",
		IdentityType: "syslog_device",
	})

	require.ErrorContains(t, err, "服务身份类型只能是 k8s_workload 或 host_process")
}

func TestRepositoryFiltersServices(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	_, err := repo.Create(ctx, Service{Name: "payment-api", Environment: "production", OwnerTeam: "payments"})
	require.NoError(t, err)
	_, err = repo.Create(ctx, Service{Name: "inventory-api", Environment: "staging", OwnerTeam: "inventory"})
	require.NoError(t, err)

	services, err := repo.List(ctx, ListFilter{Query: "payment", Environment: "production", Source: "manual"})
	require.NoError(t, err)
	require.Len(t, services, 1)
	require.Equal(t, "payment-api", services[0].Name)
}

func TestRepositoryUpdatesManualService(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	svc, err := repo.Create(ctx, Service{Name: "payment-api", Environment: "production"})
	require.NoError(t, err)

	ownerTeam := "payments"
	status := "active"
	updated, err := repo.Update(ctx, svc.ID, UpdateRequest{
		OwnerTeam: &ownerTeam,
		Status:    &status,
	})
	require.NoError(t, err)
	require.Equal(t, "payments", updated.OwnerTeam)
	require.Equal(t, "active", updated.Status)
	require.Equal(t, svc.CreatedAt, updated.CreatedAt)
	require.True(t, updated.UpdatedAt.After(svc.UpdatedAt) || updated.UpdatedAt.Equal(svc.UpdatedAt))
}

func TestRepositoryRejectsServiceNameChange(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	service, err := repo.Create(context.Background(), Service{Name: "orders-api", Environment: "prod"})
	require.NoError(t, err)
	changedName := "payments-api"

	_, err = repo.Update(context.Background(), service.ID, UpdateRequest{Name: &changedName})

	require.ErrorContains(t, err, "service.name 创建后不可修改")
}

func TestRepositorySoftDeletesServiceWhenNoBlockingDependencies(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	svc, err := repo.Create(ctx, Service{Name: "payment-api", Environment: "production"})
	require.NoError(t, err)

	deleted, err := repo.Delete(ctx, svc.ID, DeleteDependencies{})
	require.NoError(t, err)
	require.Equal(t, "deleted", deleted.Status)

	services, err := repo.List(ctx)
	require.NoError(t, err)
	require.Empty(t, services)

	services, err = repo.List(ctx, ListFilter{Status: "deleted"})
	require.NoError(t, err)
	require.Len(t, services, 1)
	require.Equal(t, svc.ID, services[0].ID)

	_, err = repo.Create(ctx, Service{Name: "payment-api", Environment: "production"})
	require.ErrorContains(t, err, "服务名称已存在")
}

func TestProductServiceNameIsNotReusedAfterDeletion(t *testing.T) {
	store := memstore.NewStore()
	products := NewProductRepository(store.Products())
	product, err := products.Create(context.Background(), Product{Name: "commerce"})
	require.NoError(t, err)
	repo := NewRepository(store.Services(), store.Products())
	service, err := repo.Create(context.Background(), Service{ProductID: product.ID, Name: "orders-api", Environment: "prod"})
	require.NoError(t, err)
	_, err = repo.Delete(context.Background(), service.ID, DeleteDependencies{})
	require.NoError(t, err)

	_, err = repo.Create(context.Background(), Service{ProductID: product.ID, Name: "orders-api", Environment: "prod"})

	require.ErrorContains(t, err, "服务名称已存在")
}

func TestRepositoryRejectsDeletingServiceWithBlockingDependencies(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	svc, err := repo.Create(ctx, Service{Name: "payment-api", Environment: "production"})
	require.NoError(t, err)

	_, err = repo.Delete(ctx, svc.ID, DeleteDependencies{LogRouteRefs: 1})
	require.ErrorContains(t, err, "日志路由")

	_, err = repo.Delete(ctx, svc.ID, DeleteDependencies{AgentRefs: 1})
	require.ErrorContains(t, err, "采集 Agent")

	_, err = repo.Delete(ctx, svc.ID, DeleteDependencies{MetricRouteRefs: 1})
	require.ErrorContains(t, err, "指标采集路由")

	_, err = repo.Delete(ctx, svc.ID, DeleteDependencies{MetricBindingRefs: 1})
	require.ErrorContains(t, err, "指标查询绑定")
}

func TestRepositoryGetsService(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	svc, err := repo.Create(ctx, Service{
		Name:          "payment-gateway",
		Environment:   "production",
		CMDBServiceID: "svc-001",
		DisplayName:   "支付网关",
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, svc.ID)
	require.NoError(t, err)
	require.Equal(t, "支付网关", got.DisplayName)
	require.Equal(t, "svc-001", got.CMDBServiceID)
}

func TestRepositoryPersistsAllFields(t *testing.T) {
	store := memstore.NewStore()
	repo := NewRepository(store.Services())
	ctx := context.Background()

	svc, err := repo.Create(ctx, Service{
		Name:          "api-gateway",
		Environment:   "staging",
		Cluster:       "staging-1",
		Namespace:     "default",
		BusinessID:    "biz-001",
		ApplicationID: "app-001",
		OwnerTeam:     "platform-team",
		AlertRoute:    "pagerduty-team-a",
		IdentityType:  "host_process",
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, svc.ID)
	require.NoError(t, err)
	require.Equal(t, "api-gateway", got.Name)
	require.Equal(t, "staging", got.Environment)
	require.Equal(t, "staging-1", got.Cluster)
	require.Equal(t, "default", got.Namespace)
	require.Equal(t, "biz-001", got.BusinessID)
	require.Equal(t, "app-001", got.ApplicationID)
	require.Equal(t, "host_process", got.IdentityType)
}

func TestTargetRepositoryCreatesHostProcessWithoutNamespace(t *testing.T) {
	store := memstore.NewStore()
	serviceRepo := NewRepository(store.Services())
	targetRepo := NewTargetRepository(store.ServiceTargets())
	ctx := context.Background()

	svc, err := serviceRepo.Create(ctx, Service{Name: "legacy-billing", Environment: "prod"})
	require.NoError(t, err)

	target, err := targetRepo.Create(ctx, ObservedTarget{
		ServiceID:   svc.ID,
		TargetType:  "host_process",
		Environment: "prod",
		DisplayName: "legacy-billing on vm-01",
		IdentityAttributes: map[string]string{
			"host.name":               "vm-01",
			"process.executable.name": "legacy-billing",
			"net.host.port":           "8080",
		},
		MatchRules: map[string]string{
			"service.name": "legacy-billing",
			"host.name":    "vm-01",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, target.ID)
	require.Equal(t, "host_process", target.TargetType)
	require.Equal(t, "vm-01", target.IdentityAttributes["host.name"])
	require.Equal(t, "manual", target.Source)
	require.Equal(t, "local", target.SyncStatus)

	targets, err := targetRepo.ListByService(ctx, svc.ID)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, target.ID, targets[0].ID)
}
