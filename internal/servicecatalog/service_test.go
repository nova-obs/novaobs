package servicecatalog

import (
	"context"
	"testing"

	"novaobs/internal/database/memstore"

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
	require.Contains(t, err.Error(), "服务已存在")
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

	recreated, err := repo.Create(ctx, Service{Name: "payment-api", Environment: "production"})
	require.NoError(t, err)
	require.NotEqual(t, svc.ID, recreated.ID)
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
