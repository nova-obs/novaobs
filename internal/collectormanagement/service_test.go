package collectormanagement

import (
	"context"
	"testing"
	"time"

	"novaobs/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestServiceCreatesAndListsGroups(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{
		Name: "prod-group",
		Mode: "shared_gateway",
	})
	require.NoError(t, err)
	require.NotEmpty(t, group.ID)
	require.Equal(t, "draft", group.Status)
	require.Equal(t, "shared", group.IsolationLevel)
	require.Equal(t, "mixed", group.ReceiverProfile)
	require.Equal(t, "logs/downstream", group.ExporterProfile)
	require.Equal(t, "none", group.LastPublishStatus)
	require.Equal(t, 1, group.DesiredReplicas)

	groups, err := svc.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "prod-group", groups[0].Name)
}

func TestServiceGetsGroup(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	created, err := svc.CreateGroup(ctx, CollectorGroup{
		Name:            "dedicated-group",
		Mode:            "dedicated_collector",
		ReceiverProfile: "kafka/syslog",
		ExporterProfile: "otlphttp/victorialogs",
	})
	require.NoError(t, err)

	got, err := svc.GetGroup(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "dedicated-group", got.Name)
	require.Equal(t, "dedicated_collector", got.Mode)
	require.Equal(t, "service_dedicated", got.IsolationLevel)
}

func TestServiceUpsertsAndListsInstances(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{
		Name: "prod-group",
		Mode: "shared_gateway",
	})
	require.NoError(t, err)
	otherGroup, err := svc.CreateGroup(ctx, CollectorGroup{
		Name: "staging-group",
		Mode: "shared_gateway",
	})
	require.NoError(t, err)

	inst, err := svc.UpsertInstance(ctx, "collector-a", group.ID, InstanceStatus{
		Online:              true,
		Healthy:             true,
		RemoteConfigCapable: true,
	})
	require.NoError(t, err)
	require.Equal(t, "collector-a", inst.InstanceUID)
	require.True(t, inst.RemoteConfigCapable)
	_, err = svc.UpsertInstance(ctx, "collector-b", otherGroup.ID, InstanceStatus{Online: true})
	require.NoError(t, err)

	instances, err := svc.ListInstances(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, instances, 1)
	require.Equal(t, "collector-a", instances[0].InstanceUID)
}

func TestServiceSummarizesRuntimeInstanceCountsOnGroups(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "syslog-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-online", group.ID, InstanceStatus{
		Online:              true,
		Healthy:             true,
		RemoteConfigCapable: true,
		LastSeenAt:          time.Now().UTC().Add(-30 * time.Second),
	})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-stale", group.ID, InstanceStatus{
		Online:              true,
		Healthy:             true,
		RemoteConfigCapable: true,
		LastSeenAt:          time.Now().UTC().Add(-2 * time.Minute),
	})
	require.NoError(t, err)

	groups, err := svc.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, 2, groups[0].InstanceCount)
	require.Equal(t, 1, groups[0].OnlineInstances)
	require.Equal(t, 1, groups[0].HealthyInstances)
	require.Equal(t, 1, groups[0].RemoteConfigCapableInstances)

	got, err := svc.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.InstanceCount)
	require.Equal(t, 1, got.OnlineInstances)
	require.Equal(t, 1, got.HealthyInstances)
}

func TestServiceAssignsInstanceGroupWithoutOverwritingRuntimeState(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	firstGroup, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	secondGroup, err := svc.CreateGroup(ctx, CollectorGroup{Name: "staging-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	lastSeen := time.Now().UTC().Add(-10 * time.Minute)
	_, err = svc.UpsertInstance(ctx, "collector-a", firstGroup.ID, InstanceStatus{
		Online:              true,
		Healthy:             true,
		Capabilities:        7,
		RemoteConfigCapable: true,
		RemoteConfigStatus:  "applied",
		LastConfigHash:      "hash-001",
		LastSeenAt:          lastSeen,
	})
	require.NoError(t, err)

	assigned, err := svc.AssignInstanceGroup(ctx, "collector-a", secondGroup.ID)
	require.NoError(t, err)
	require.Equal(t, secondGroup.ID, assigned.CollectorGroupID)
	require.Equal(t, "offline", assigned.RuntimeStatus)
	require.False(t, assigned.Online)
	require.True(t, assigned.Healthy)
	require.Equal(t, uint64(7), assigned.Capabilities)
	require.True(t, assigned.RemoteConfigCapable)
	require.Equal(t, "applied", assigned.RemoteConfigStatus)
	require.Equal(t, "hash-001", assigned.LastConfigHash)
	require.Equal(t, lastSeen, assigned.LastSeenAt)
}

func TestServicePreservesHealthWhenStatusUpdateOmitsHealth(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-a", group.ID, InstanceStatus{
		Online:  true,
		Healthy: true,
	})
	require.NoError(t, err)

	updated, err := svc.UpsertInstance(ctx, "collector-a", group.ID, InstanceStatus{
		Online:              true,
		RemoteConfigCapable: true,
		RemoteConfigStatus:  "applied",
		LastConfigHash:      "hash-001",
	})

	require.NoError(t, err)
	require.True(t, updated.Healthy)
	require.Equal(t, "applied", updated.RemoteConfigStatus)
	require.Equal(t, "hash-001", updated.LastConfigHash)
}

func TestServiceUpdatesGroup(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{
		Name:        "prod-group",
		Mode:        "shared_gateway",
		Environment: "production",
	})
	require.NoError(t, err)

	displayName := "生产共享网关"
	status := "draining"
	desiredReplicas := 3
	updated, err := svc.UpdateGroup(ctx, group.ID, UpdateGroupRequest{
		DisplayName:     &displayName,
		Status:          &status,
		DesiredReplicas: &desiredReplicas,
	})
	require.NoError(t, err)
	require.Equal(t, "生产共享网关", updated.DisplayName)
	require.Equal(t, "draining", updated.Status)
	require.Equal(t, 3, updated.DesiredReplicas)
	require.Equal(t, group.CreatedAt, updated.CreatedAt)
}

func TestServiceActivatesGroupOnlyWhenReady(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)

	_, err = svc.ActivateGroup(ctx, group.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "在线 Agent")

	_, err = svc.UpsertInstance(ctx, "collector-a", group.ID, InstanceStatus{Online: true, Healthy: true, LastSeenAt: time.Now().UTC()})
	require.NoError(t, err)
	activated, err := svc.ActivateGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, "active", activated.Status)
	require.Equal(t, 1, activated.OnlineInstances)
}

func TestServiceRejectsDirectActivationByPatch(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	status := "active"
	_, err = svc.UpdateGroup(ctx, group.ID, UpdateGroupRequest{Status: &status})

	require.Error(t, err)
	require.Contains(t, err.Error(), "启用接口")
}

func TestServiceRejectsInstanceAssignmentToMissingGroup(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	_, err := svc.UpsertInstance(ctx, "collector-a", "missing-group", InstanceStatus{Online: true})

	require.Error(t, err)
}

func TestServiceMarksStaleAndOfflineInstancesByLastSeenAt(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-online", group.ID, InstanceStatus{Online: true, Healthy: true, LastSeenAt: time.Now().UTC().Add(-30 * time.Second)})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-stale", group.ID, InstanceStatus{Online: true, Healthy: true, LastSeenAt: time.Now().UTC().Add(-2 * time.Minute)})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-offline", group.ID, InstanceStatus{Online: true, Healthy: true, LastSeenAt: time.Now().UTC().Add(-10 * time.Minute)})
	require.NoError(t, err)

	instances, err := svc.ListInstances(ctx, group.ID)
	require.NoError(t, err)
	statuses := map[string]string{}
	online := map[string]bool{}
	for _, instance := range instances {
		statuses[instance.InstanceUID] = instance.RuntimeStatus
		online[instance.InstanceUID] = instance.Online
	}
	require.Equal(t, "online", statuses["collector-online"])
	require.Equal(t, "stale", statuses["collector-stale"])
	require.Equal(t, "offline", statuses["collector-offline"])
	require.True(t, online["collector-online"])
	require.False(t, online["collector-stale"])
	require.False(t, online["collector-offline"])
}

func TestServiceUnassignsAndDeletesOfflineInstance(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-a", group.ID, InstanceStatus{Online: true, LastSeenAt: time.Now().UTC().Add(-10 * time.Minute)})
	require.NoError(t, err)

	instance, err := svc.UnassignInstance(ctx, "collector-a")
	require.NoError(t, err)
	require.Empty(t, instance.CollectorGroupID)

	require.NoError(t, svc.DeleteInstance(ctx, "collector-a"))
	instances, err := svc.ListInstances(ctx, "")
	require.NoError(t, err)
	require.Empty(t, instances)
}

func TestServiceRejectsDeletingOnlineInstance(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.UpsertInstance(ctx, "collector-a", group.ID, InstanceStatus{Online: true, LastSeenAt: time.Now().UTC()})
	require.NoError(t, err)

	err = svc.DeleteInstance(ctx, "collector-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "仍在线")
}

func TestServiceSoftDeletesGroupWhenNoDependencies(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "draft-group", Mode: "shared_gateway"})
	require.NoError(t, err)

	deleted, err := svc.DeleteGroup(ctx, group.ID, DeleteGroupDependencies{})
	require.NoError(t, err)
	require.Equal(t, "deleted", deleted.Status)

	groups, err := svc.ListGroups(ctx)
	require.NoError(t, err)
	require.Empty(t, groups)
	groups, err = svc.ListGroups(ctx, ListGroupFilter{Status: "deleted"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
}

func TestServiceRejectsGroupDeleteWithDependencies(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)

	_, err = svc.DeleteGroup(ctx, group.ID, DeleteGroupDependencies{OnboardingRefs: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "服务接入")
}

func TestServiceMarksGroupPublishPending(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances())
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)

	updated, err := svc.MarkGroupPublishPending(ctx, group.ID, "hash-001", "等待 Collector 实例拉取配置")
	require.NoError(t, err)
	require.Equal(t, 1, updated.ConfigVersion)
	require.Equal(t, "hash-001", updated.DesiredConfigHash)
	require.Equal(t, "pending", updated.LastPublishStatus)
	require.Equal(t, "等待 Collector 实例拉取配置", updated.LastPublishMessage)
	require.NotNil(t, updated.LastPublishedAt)
}

func TestServiceCreatesAndListsConfigVersions(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances(), WithConfigVersionStore(store.CollectorConfigVersions()))
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	version, err := svc.CreateConfigVersion(ctx, CollectorConfigVersion{
		CollectorGroupID: group.ID,
		ConfigHash:       "hash-001",
		CollectorYAML:    "receivers: {}",
		ServiceIDs:       []string{"service-1"},
		Status:           "pending",
	})
	require.NoError(t, err)
	require.Equal(t, 1, version.Version)

	versions, err := svc.ListConfigVersions(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, "hash-001", versions[0].ConfigHash)
}

func TestServiceReturnsLatestConfigVersion(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances(), WithConfigVersionStore(store.CollectorConfigVersions()))
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.CreateConfigVersion(ctx, CollectorConfigVersion{
		CollectorGroupID: group.ID,
		Version:          1,
		ConfigHash:       "hash-001",
		CollectorYAML:    "receivers:\n  otlp:\n",
		Status:           "applied",
	})
	require.NoError(t, err)
	_, err = svc.CreateConfigVersion(ctx, CollectorConfigVersion{
		CollectorGroupID: group.ID,
		Version:          2,
		ConfigHash:       "hash-002",
		CollectorYAML:    "receivers:\n  kafka/syslog:\n",
		Status:           "pending",
	})
	require.NoError(t, err)

	latest, err := svc.LatestConfigVersion(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, 2, latest.Version)
	require.Equal(t, "hash-002", latest.ConfigHash)
	require.Equal(t, "receivers:\n  kafka/syslog:\n", latest.CollectorYAML)
}

func TestServiceLatestConfigVersionIgnoresDraft(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances(), WithConfigVersionStore(store.CollectorConfigVersions()))
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	_, err = svc.CreateConfigVersion(ctx, CollectorConfigVersion{
		CollectorGroupID: group.ID,
		Version:          1,
		ConfigHash:       "hash-published",
		CollectorYAML:    "receivers:\n  otlp:\n",
		Status:           "pending",
	})
	require.NoError(t, err)
	_, err = svc.CreateConfigVersion(ctx, CollectorConfigVersion{
		CollectorGroupID: group.ID,
		Version:          2,
		ConfigHash:       "hash-draft",
		CollectorYAML:    "receivers:\n  draft:\n",
		Status:           "draft",
	})
	require.NoError(t, err)

	latest, err := svc.LatestConfigVersion(ctx, group.ID)

	require.NoError(t, err)
	require.Equal(t, 1, latest.Version)
	require.Equal(t, "hash-published", latest.ConfigHash)
}

func TestServiceMarksGroupConfigStatusFromCollectorReport(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(store.CollectorGroups(), store.CollectorInstances(), WithConfigVersionStore(store.CollectorConfigVersions()))
	ctx := context.Background()

	group, err := svc.CreateGroup(ctx, CollectorGroup{Name: "prod-group", Mode: "shared_gateway"})
	require.NoError(t, err)
	group, err = svc.MarkGroupPublishPending(ctx, group.ID, "hash-001", "等待 Collector 实例拉取配置")
	require.NoError(t, err)
	_, err = svc.CreateConfigVersion(ctx, CollectorConfigVersion{
		CollectorGroupID: group.ID,
		Version:          group.ConfigVersion,
		ConfigHash:       "hash-001",
		CollectorYAML:    "receivers: {}",
		Status:           "pending",
	})
	require.NoError(t, err)

	updated, err := svc.MarkGroupConfigStatus(ctx, group.ID, "hash-001", "applied", "")
	require.NoError(t, err)
	require.Equal(t, "applied", updated.LastPublishStatus)
	require.Equal(t, "hash-001", updated.LastAppliedConfigHash)
	require.Equal(t, "配置已应用", updated.LastPublishMessage)

	versions, err := svc.ListConfigVersions(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, "applied", versions[0].Status)
	require.NotNil(t, versions[0].AppliedAt)
}
