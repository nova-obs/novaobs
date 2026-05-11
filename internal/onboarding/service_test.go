package onboarding

import (
	"context"
	"testing"

	"novaobs/internal/collectormanagement"
	"novaobs/internal/database/memstore"
	"novaobs/internal/servicecatalog"

	"github.com/stretchr/testify/require"
)

func newOnboardingTestServices(t *testing.T) (context.Context, *memstore.Store, servicecatalog.Repository, collectormanagement.Service, Service) {
	t.Helper()
	store := memstore.NewStore()
	ctx := context.Background()
	svcRepo := servicecatalog.NewRepository(store.Services())
	collectorSvc := collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances())
	onbSvc := NewService(store.Onboardings(), store.IngestionIdentities(), svcRepo, collectorSvc)
	return ctx, store, svcRepo, collectorSvc, onbSvc
}

func TestServiceUpsertsOnboarding(t *testing.T) {
	ctx, _, svcRepo, collectorSvc, onbSvc := newOnboardingTestServices(t)
	s, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:          "payment-gateway",
		Environment:   "production",
		Cluster:       "prod-1",
		Namespace:     "payments",
		CMDBServiceID: "cmdb-svc-payment-gateway",
		OwnerTeam:     "payment-team",
		AlertRoute:    "payment-prod",
	})
	require.NoError(t, err)
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:           "shared-prod-1",
		Mode:           "shared_gateway",
		Environment:    "production",
		Cluster:        "prod-1",
		Status:         "active",
		IngestEndpoint: "http://collector-gateway:4317",
	})
	require.NoError(t, err)

	result, err := onbSvc.Upsert(ctx, s, UpsertRequest{
		Mode:             "shared_gateway",
		CollectorGroupID: group.ID,
		IdentityType:     "k8s_workload",
		K8sNamespace:     "payments",
		K8sWorkload:      "payment-gateway",
	})
	require.NoError(t, err)
	require.Equal(t, s.ID, result.Service.ID)
	require.Equal(t, "pending_verification", result.Onboarding.Status)
	require.Equal(t, group.ID, result.CollectorTarget.GroupID)
	require.Equal(t, "k8s_workload", result.Identity.IdentityType)
	require.Equal(t, "payment-gateway", result.GeneratedConfig.ResourceAttributes["service.name"])
	require.Equal(t, "http://collector-gateway:4317", result.GeneratedConfig.Endpoint)
	require.Equal(t, "http://collector-gateway:4317", result.GeneratedConfig.EnvironmentVariables["OTEL_EXPORTER_OTLP_ENDPOINT"])
	require.Contains(t, result.GeneratedConfig.EnvBlock, "OTEL_RESOURCE_ATTRIBUTES=")
}

func TestServiceDoesNotGenerateFakeEndpointWhenCollectorGroupHasNoEndpoint(t *testing.T) {
	ctx, _, svcRepo, collectorSvc, onbSvc := newOnboardingTestServices(t)
	s, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:        "api-gateway",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "gateway",
	})
	require.NoError(t, err)
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:        "shared-prod-1",
		Mode:        "shared_gateway",
		Environment: "production",
		Cluster:     "prod-1",
		Status:      "active",
	})
	require.NoError(t, err)

	result, err := onbSvc.Upsert(ctx, s, UpsertRequest{
		Mode:             "shared_gateway",
		CollectorGroupID: group.ID,
		IdentityType:     "k8s_workload",
		K8sNamespace:     "gateway",
		K8sWorkload:      "api-gateway",
	})
	require.NoError(t, err)
	require.Empty(t, result.GeneratedConfig.Endpoint)
	require.NotContains(t, result.GeneratedConfig.EnvironmentVariables, "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func TestServiceUsesServiceLevelRuntimeIdentityType(t *testing.T) {
	ctx, _, svcRepo, collectorSvc, onbSvc := newOnboardingTestServices(t)
	s, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:         "device-svc",
		Environment:  "production",
		Cluster:      "prod-1",
		Namespace:    "security",
		IdentityType: "syslog_device",
	})
	require.NoError(t, err)
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:        "shared-prod-1",
		Mode:        "shared_gateway",
		Environment: "production",
		Cluster:     "prod-1",
		Status:      "active",
	})
	require.NoError(t, err)

	result, err := onbSvc.Upsert(ctx, s, UpsertRequest{
		Mode:             "shared_gateway",
		CollectorGroupID: group.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "syslog_device", result.Service.IdentityType)
	require.Equal(t, "syslog_device", result.Identity.IdentityType)
}

func TestServiceReturnsWorkspaceForNewService(t *testing.T) {
	ctx, _, svcRepo, collectorSvc, onbSvc := newOnboardingTestServices(t)
	s, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:          "api-gateway",
		Environment:   "production",
		CMDBServiceID: "cmdb-svc-api",
		Cluster:       "prod-1",
		Namespace:     "gateway",
		OwnerTeam:     "platform-team",
		AlertRoute:    "platform-prod",
	})
	require.NoError(t, err)
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:        "shared-prod-1",
		Mode:        "shared_gateway",
		Environment: "production",
		Cluster:     "prod-1",
		Status:      "active",
	})
	require.NoError(t, err)

	workspace, err := onbSvc.Get(ctx, s)
	require.NoError(t, err)
	require.Equal(t, "api-gateway", workspace.Service.Name)
	require.Equal(t, "not_started", workspace.Onboarding.Status)
	require.Equal(t, group.ID, workspace.CollectorTarget.GroupID)
	require.Equal(t, "api-gateway", workspace.GeneratedConfig.ResourceAttributes["service.name"])
	require.Equal(t, "cmdb-svc-api", workspace.GeneratedConfig.ResourceAttributes["cmdb.service_id"])
	require.Contains(t, workspace.GeneratedConfig.ResourceAttributesText, "service.name=api-gateway")
	require.NotEmpty(t, workspace.Checklist)
	require.Contains(t, workspace.AvailableActions, "save")
}

func TestServiceChecksOnboardingWorkspace(t *testing.T) {
	ctx, _, svcRepo, collectorSvc, onbSvc := newOnboardingTestServices(t)
	s, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:          "check-svc",
		Environment:   "staging",
		Cluster:       "staging-1",
		Namespace:     "payments",
		CMDBServiceID: "cmdb-svc-check",
		OwnerTeam:     "payment-team",
		AlertRoute:    "payment-staging",
	})
	require.NoError(t, err)
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:        "shared-staging-1",
		Mode:        "shared_gateway",
		Environment: "staging",
		Cluster:     "staging-1",
		Status:      "active",
	})
	require.NoError(t, err)
	_, err = collectorSvc.UpsertInstance(ctx, "collector-a", group.ID, collectormanagement.InstanceStatus{
		Online:       true,
		Healthy:      true,
		Capabilities: 1,
	})
	require.NoError(t, err)

	_, err = onbSvc.Upsert(ctx, s, UpsertRequest{
		Mode:             "shared_gateway",
		CollectorGroupID: group.ID,
		IdentityType:     "k8s_workload",
		K8sNamespace:     "payments",
		K8sWorkload:      "check-svc",
	})
	require.NoError(t, err)

	result, err := onbSvc.Check(ctx, s)
	require.NoError(t, err)
	require.Equal(t, "check-svc", result.Service.Name)
	require.Equal(t, "pending_verification", result.Onboarding.Status)
	require.Equal(t, "warning", testChecklistStatus(result.Checklist, "log_signal_seen"))
	require.Equal(t, "passed", testChecklistStatus(result.Checklist, "collector_instance_online"))
	require.Contains(t, result.AvailableActions, "check")
}

func TestServiceRejectsMismatchedCollectorGroup(t *testing.T) {
	ctx, _, svcRepo, collectorSvc, onbSvc := newOnboardingTestServices(t)
	s, err := svcRepo.Create(ctx, servicecatalog.Service{
		Name:        "payment-gateway",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "payments",
	})
	require.NoError(t, err)
	group, err := collectorSvc.CreateGroup(ctx, collectormanagement.CollectorGroup{
		Name:        "staging-group",
		Mode:        "shared_gateway",
		Environment: "staging",
		Cluster:     "staging-1",
		Status:      "active",
	})
	require.NoError(t, err)

	_, err = onbSvc.Upsert(ctx, s, UpsertRequest{
		Mode:             "shared_gateway",
		CollectorGroupID: group.ID,
		IdentityType:     "k8s_workload",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Collector Group")
}

func testChecklistStatus(items []ChecklistItem, key string) string {
	for _, item := range items {
		if item.Key == key {
			return item.Status
		}
	}
	return ""
}
