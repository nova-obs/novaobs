package collectormanagement

import (
	"context"
	"strings"
	"time"

	"novaobs/internal/database"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Service struct {
	groupStore    database.CollectorGroupStore
	instanceStore database.CollectorInstanceStore
	configStore   database.CollectorConfigVersionStore
	onlineTTL     time.Duration
	staleTTL      time.Duration
}

type Option func(*Service)

func WithConfigVersionStore(store database.CollectorConfigVersionStore) Option {
	return func(s *Service) {
		s.configStore = store
	}
}

func NewService(groupStore database.CollectorGroupStore, instanceStore database.CollectorInstanceStore, options ...Option) Service {
	return NewServiceWithOptions(groupStore, instanceStore, options...)
}

func NewServiceWithOptions(groupStore database.CollectorGroupStore, instanceStore database.CollectorInstanceStore, options ...Option) Service {
	service := Service{
		groupStore:    groupStore,
		instanceStore: instanceStore,
		onlineTTL:     time.Minute,
		staleTTL:      5 * time.Minute,
	}
	for _, option := range options {
		option(&service)
	}
	return service
}

func (s Service) CreateGroup(ctx context.Context, group CollectorGroup) (CollectorGroup, error) {
	group = normalizeGroup(group)
	if err := validateGroup(group); err != nil {
		return CollectorGroup{}, err
	}
	if group.ID == "" {
		group.ID = primitive.NewObjectID().Hex()
	}
	now := time.Now().UTC()
	if group.CreatedAt.IsZero() {
		group.CreatedAt = now
	}
	group.UpdatedAt = now
	if err := s.groupStore.Insert(ctx, group); err != nil {
		return CollectorGroup{}, err
	}
	return group, nil
}

func (s Service) ListGroups(ctx context.Context, filters ...ListGroupFilter) ([]CollectorGroup, error) {
	var groups []CollectorGroup
	if err := s.groupStore.FindAll(ctx, &groups); err != nil {
		return nil, err
	}
	filter := ListGroupFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	groups = applyGroupFilter(groups, filter)
	return s.enrichGroupRuntimeSummaries(ctx, groups), nil
}

func (s Service) GetGroup(ctx context.Context, id string) (CollectorGroup, error) {
	var group CollectorGroup
	if err := s.groupStore.FindByID(ctx, id, &group); err != nil {
		return CollectorGroup{}, err
	}
	return s.enrichGroupRuntimeSummary(ctx, group), nil
}

func (s Service) UpdateGroup(ctx context.Context, id string, patch UpdateGroupRequest) (CollectorGroup, error) {
	group, err := s.GetGroup(ctx, id)
	if err != nil {
		return CollectorGroup{}, err
	}
	if patch.Status != nil && *patch.Status == "active" && group.Status != "active" {
		return CollectorGroup{}, apperr.InvalidRequest("请通过启用接口激活 Collector Group")
	}
	group = applyGroupPatch(group, patch)
	group = normalizeGroup(group)
	if err := validateGroup(group); err != nil {
		return CollectorGroup{}, err
	}
	group.UpdatedAt = time.Now().UTC()
	if err := s.groupStore.Update(ctx, id, group); err != nil {
		return CollectorGroup{}, err
	}
	return group, nil
}

func (s Service) ActivateGroup(ctx context.Context, id string) (CollectorGroup, error) {
	group, err := s.GetGroup(ctx, id)
	if err != nil {
		return CollectorGroup{}, err
	}
	if group.OnlineInstances == 0 {
		return CollectorGroup{}, apperr.InvalidRequest("Collector Group 至少需要一个在线 Agent")
	}
	if group.Status == "active" {
		return group, nil
	}
	group.Status = "active"
	group.UpdatedAt = time.Now().UTC()
	if err := s.groupStore.Update(ctx, id, group); err != nil {
		return CollectorGroup{}, err
	}
	return s.enrichGroupRuntimeSummary(ctx, group), nil
}

func (s Service) MarkGroupPublishPending(ctx context.Context, id string, configHash string, message string) (CollectorGroup, error) {
	group, err := s.GetGroup(ctx, id)
	if err != nil {
		return CollectorGroup{}, err
	}
	now := time.Now().UTC()
	group.ConfigVersion++
	group.DesiredConfigHash = strings.TrimSpace(configHash)
	group.LastPublishStatus = "pending"
	group.LastPublishMessage = message
	group.LastPublishedAt = &now
	group.UpdatedAt = now
	if err := s.groupStore.Update(ctx, id, group); err != nil {
		return CollectorGroup{}, err
	}
	return group, nil
}

func (s Service) ListInstances(ctx context.Context, groupID string) ([]CollectorInstance, error) {
	var instances []CollectorInstance
	var err error
	if groupID != "" {
		err = s.instanceStore.FindByGroup(ctx, groupID, &instances)
	} else {
		err = s.instanceStore.FindAll(ctx, &instances)
	}
	if err != nil {
		return nil, err
	}
	return s.applyRuntimeStatuses(instances), nil
}

func (s Service) UpsertInstance(ctx context.Context, instanceUID string, groupID string, status InstanceStatus) (CollectorInstance, error) {
	if strings.TrimSpace(instanceUID) == "" {
		return CollectorInstance{}, apperr.InvalidRequest("instance_uid 不能为空")
	}
	if strings.TrimSpace(groupID) != "" {
		if _, err := s.GetGroup(ctx, groupID); err != nil {
			return CollectorInstance{}, err
		}
	}
	if status.LastSeenAt.IsZero() {
		status.LastSeenAt = time.Now().UTC()
	}
	status.RuntimeIdentity = strings.TrimSpace(status.RuntimeIdentity)

	instance := CollectorInstance{}
	existingErr := s.instanceStore.FindByUID(ctx, instanceUID, &instance)
	if status.RuntimeIdentity != "" {
		runtimeErr := s.instanceStore.FindByRuntimeIdentity(ctx, status.RuntimeIdentity, &instance)
		if runtimeErr == nil {
			existingErr = nil
		}
	}
	incomingOpAMPUID := firstNonEmpty(strings.TrimSpace(status.OpAMPInstanceUID), instanceUID)
	if existingErr == nil && status.RuntimeIdentity != "" && !status.Online && instance.OpAMPInstanceUID != "" && instance.OpAMPInstanceUID != incomingOpAMPUID {
		return s.applyRuntimeStatus(instance), nil
	}
	if existingErr != nil {
		instance = CollectorInstance{
			ID:        primitive.NewObjectID().Hex(),
			CreatedAt: status.LastSeenAt,
		}
	}
	instance.InstanceUID = instanceUID
	instance.OpAMPInstanceUID = incomingOpAMPUID
	instance.RuntimeIdentity = status.RuntimeIdentity
	instance.CollectorGroupID = groupID
	if strings.TrimSpace(status.ServiceID) != "" {
		instance.ServiceID = strings.TrimSpace(status.ServiceID)
	}
	instance.ClusterID = strings.TrimSpace(status.ClusterID)
	instance.Namespace = strings.TrimSpace(status.Namespace)
	instance.AgentNamespace = strings.TrimSpace(status.AgentNamespace)
	instance.Hostname = strings.TrimSpace(status.Hostname)
	instance.PodUID = strings.TrimSpace(status.PodUID)
	instance.PodName = strings.TrimSpace(status.PodName)
	instance.NodeName = strings.TrimSpace(status.NodeName)
	instance.IP = strings.TrimSpace(status.IP)
	instance.PodIP = strings.TrimSpace(status.PodIP)
	instance.Version = strings.TrimSpace(status.Version)
	instance.Online = status.Online
	if status.HealthSet || existingErr != nil {
		instance.Healthy = status.Healthy
	}
	instance.Capabilities = status.Capabilities
	instance.RemoteConfigCapable = status.RemoteConfigCapable
	instance.EffectiveConfigHash = status.EffectiveConfigHash
	instance.RemoteConfigStatus = firstNonEmpty(status.RemoteConfigStatus, "unset")
	instance.LastConfigHash = status.LastConfigHash
	instance.LastError = status.LastError
	instance.LastSeenAt = status.LastSeenAt
	instance.UpdatedAt = status.LastSeenAt

	if err := s.instanceStore.Upsert(ctx, instanceUID, groupID, instance); err != nil {
		return CollectorInstance{}, err
	}
	return s.applyRuntimeStatus(instance), nil
}

func (s Service) AssignInstanceGroup(ctx context.Context, instanceUID string, groupID string) (CollectorInstance, error) {
	if strings.TrimSpace(instanceUID) == "" {
		return CollectorInstance{}, apperr.InvalidRequest("instance_uid 不能为空")
	}
	if strings.TrimSpace(groupID) == "" {
		return CollectorInstance{}, apperr.InvalidRequest("collector_group_id 不能为空")
	}
	if _, err := s.GetGroup(ctx, groupID); err != nil {
		return CollectorInstance{}, err
	}
	var instance CollectorInstance
	if err := s.instanceStore.FindByUID(ctx, instanceUID, &instance); err != nil {
		return CollectorInstance{}, err
	}
	instance.CollectorGroupID = groupID
	instance.UpdatedAt = time.Now().UTC()
	if err := s.instanceStore.Update(ctx, instanceUID, instance); err != nil {
		return CollectorInstance{}, err
	}
	return s.applyRuntimeStatus(instance), nil
}

func (s Service) UnassignInstance(ctx context.Context, instanceUID string) (CollectorInstance, error) {
	var instance CollectorInstance
	if err := s.instanceStore.FindByUID(ctx, instanceUID, &instance); err != nil {
		return CollectorInstance{}, err
	}
	instance.CollectorGroupID = ""
	instance.UpdatedAt = time.Now().UTC()
	if err := s.instanceStore.Update(ctx, instanceUID, instance); err != nil {
		return CollectorInstance{}, err
	}
	return s.applyRuntimeStatus(instance), nil
}

func (s Service) DeleteInstance(ctx context.Context, instanceUID string) error {
	var instance CollectorInstance
	if err := s.instanceStore.FindByUID(ctx, instanceUID, &instance); err != nil {
		return err
	}
	instance = s.applyRuntimeStatus(instance)
	if instance.RuntimeStatus == "online" {
		return apperr.Conflict("Collector Instance 仍在线，不能删除")
	}
	return s.instanceStore.Delete(ctx, instanceUID)
}

func (s Service) DeleteGroup(ctx context.Context, id string, deps DeleteGroupDependencies) (CollectorGroup, error) {
	group, err := s.GetGroup(ctx, id)
	if err != nil {
		return CollectorGroup{}, err
	}
	instances, err := s.ListInstances(ctx, id)
	if err != nil {
		return CollectorGroup{}, err
	}
	for _, instance := range instances {
		if instance.RuntimeStatus == "online" {
			deps.OnlineInstances++
		}
	}
	if deps.OnlineInstances > 0 {
		return CollectorGroup{}, apperr.Conflict("Collector Group 仍有关联在线实例，不能删除")
	}
	if deps.OnboardingRefs > 0 {
		return CollectorGroup{}, apperr.Conflict("Collector Group 仍被服务接入引用，不能删除")
	}
	if deps.ConfigRefs > 0 {
		return CollectorGroup{}, apperr.Conflict("Collector Group 仍被服务日志配置引用，不能删除")
	}
	if deps.PendingPublishes > 0 || group.LastPublishStatus == "pending" || group.LastPublishStatus == "applying" {
		return CollectorGroup{}, apperr.Conflict("Collector Group 仍有未完成发布，不能删除")
	}
	now := time.Now().UTC()
	group.Status = "deleted"
	group.UpdatedAt = now
	if err := s.groupStore.Update(ctx, id, group); err != nil {
		return CollectorGroup{}, err
	}
	return group, nil
}

func (s Service) CreateConfigVersion(ctx context.Context, version CollectorConfigVersion) (CollectorConfigVersion, error) {
	if s.configStore == nil {
		return CollectorConfigVersion{}, apperr.InvalidRequest("Collector Config Version 存储未配置")
	}
	if strings.TrimSpace(version.CollectorGroupID) == "" {
		return CollectorConfigVersion{}, apperr.InvalidRequest("collector_group_id 不能为空")
	}
	if version.ID == "" {
		version.ID = primitive.NewObjectID().Hex()
	}
	if version.Version == 0 {
		group, err := s.GetGroup(ctx, version.CollectorGroupID)
		if err != nil {
			return CollectorConfigVersion{}, err
		}
		version.Version = group.ConfigVersion + 1
	}
	if version.Status == "" {
		version.Status = "pending"
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}
	if err := s.configStore.Insert(ctx, version); err != nil {
		return CollectorConfigVersion{}, err
	}
	return version, nil
}

func (s Service) MarkGroupConfigStatus(ctx context.Context, groupID string, configHash string, status string, message string) (CollectorGroup, error) {
	group, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return CollectorGroup{}, err
	}
	status = strings.TrimSpace(status)
	configHash = strings.TrimSpace(configHash)
	if configHash == "" || configHash != group.DesiredConfigHash {
		return group, nil
	}
	now := time.Now().UTC()
	switch status {
	case "applied":
		group.LastPublishStatus = "applied"
		group.LastAppliedConfigHash = configHash
		if strings.TrimSpace(message) == "" {
			message = "配置已应用"
		}
	case "failed":
		group.LastPublishStatus = "failed"
		if strings.TrimSpace(message) == "" {
			message = "Collector 应用配置失败"
		}
	case "applying":
		group.LastPublishStatus = "applying"
		if strings.TrimSpace(message) == "" {
			message = "Collector 正在应用配置"
		}
	default:
		return group, nil
	}
	group.LastPublishMessage = message
	group.UpdatedAt = now
	if err := s.groupStore.Update(ctx, groupID, group); err != nil {
		return CollectorGroup{}, err
	}
	if s.configStore != nil {
		updates := map[string]interface{}{
			"status":  status,
			"message": message,
		}
		if status == "applied" {
			updates["applied_at"] = now
		}
		if err := s.configStore.UpdateStatusByGroupAndHash(ctx, groupID, configHash, updates); err != nil {
			return CollectorGroup{}, err
		}
	}
	return group, nil
}

func (s Service) ListConfigVersions(ctx context.Context, groupID string) ([]CollectorConfigVersion, error) {
	if s.configStore == nil {
		return []CollectorConfigVersion{}, nil
	}
	var versions []CollectorConfigVersion
	if err := s.configStore.FindByGroup(ctx, groupID, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

func (s Service) LatestConfigVersion(ctx context.Context, groupID string) (CollectorConfigVersion, error) {
	versions, err := s.ListConfigVersions(ctx, groupID)
	if err != nil {
		return CollectorConfigVersion{}, err
	}
	if len(versions) == 0 {
		return CollectorConfigVersion{}, apperr.NotFound("Collector Group 尚未发布配置")
	}
	var latest CollectorConfigVersion
	found := false
	for _, version := range versions {
		if version.Status == "draft" {
			continue
		}
		if !found || version.Version > latest.Version {
			latest = version
			found = true
		}
	}
	if !found {
		return CollectorConfigVersion{}, apperr.NotFound("Collector Group 尚未发布配置")
	}
	return latest, nil
}

func (s Service) applyRuntimeStatuses(instances []CollectorInstance) []CollectorInstance {
	result := make([]CollectorInstance, 0, len(instances))
	for _, instance := range instances {
		result = append(result, s.applyRuntimeStatus(instance))
	}
	return result
}

func (s Service) applyRuntimeStatus(instance CollectorInstance) CollectorInstance {
	return s.ApplyRuntimeStatus(instance)
}

func (s Service) ApplyRuntimeStatus(instance CollectorInstance) CollectorInstance {
	status := "offline"
	if instance.Online && !instance.LastSeenAt.IsZero() {
		age := time.Since(instance.LastSeenAt)
		switch {
		case age <= s.onlineTTL:
			status = "online"
		case age <= s.staleTTL:
			status = "stale"
		default:
			status = "offline"
		}
	}
	instance.RuntimeStatus = status
	instance.Online = status == "online"
	return instance
}

func (s Service) enrichGroupRuntimeSummaries(ctx context.Context, groups []CollectorGroup) []CollectorGroup {
	out := make([]CollectorGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, s.enrichGroupRuntimeSummary(ctx, group))
	}
	return out
}

func (s Service) enrichGroupRuntimeSummary(ctx context.Context, group CollectorGroup) CollectorGroup {
	group.InstanceCount = 0
	group.OnlineInstances = 0
	group.HealthyInstances = 0
	group.RemoteConfigCapableInstances = 0
	instances, err := s.ListInstances(ctx, group.ID)
	if err != nil {
		return group
	}
	group.InstanceCount = len(instances)
	for _, instance := range instances {
		if instance.Online {
			group.OnlineInstances++
		}
		if instance.Online && instance.Healthy {
			group.HealthyInstances++
		}
		if instance.Online && instance.RemoteConfigCapable {
			group.RemoteConfigCapableInstances++
		}
	}
	return group
}

func normalizeGroup(group CollectorGroup) CollectorGroup {
	group.Name = strings.TrimSpace(group.Name)
	group.Mode = strings.TrimSpace(group.Mode)
	group.Environment = strings.TrimSpace(group.Environment)
	group.IngestEndpoint = strings.TrimSpace(group.IngestEndpoint)
	if strings.TrimSpace(group.Status) == "" {
		group.Status = "draft"
	}
	if strings.TrimSpace(group.IsolationLevel) == "" {
		if group.Mode == "dedicated_collector" {
			group.IsolationLevel = "service_dedicated"
		} else {
			group.IsolationLevel = "shared"
		}
	}
	if strings.TrimSpace(group.ReceiverProfile) == "" {
		group.ReceiverProfile = "mixed"
	}
	if strings.TrimSpace(group.ExporterProfile) == "" {
		group.ExporterProfile = "logs/downstream"
	}
	if group.DesiredReplicas == 0 {
		group.DesiredReplicas = 1
	}
	if strings.TrimSpace(group.LastPublishStatus) == "" {
		group.LastPublishStatus = "none"
	}
	return group
}

func validateGroup(group CollectorGroup) error {
	if group.Name == "" {
		return apperr.InvalidRequest("Collector Group 名称不能为空")
	}
	if group.Mode != "shared_gateway" && group.Mode != "dedicated_collector" {
		return apperr.InvalidRequest("Collector Group 模式只能是 shared_gateway 或 dedicated_collector")
	}
	switch group.Status {
	case "draft", "active", "draining", "disabled", "deleted":
	default:
		return apperr.InvalidRequest("Collector Group 状态只能是 draft、active、draining、disabled 或 deleted")
	}
	return nil
}

func applyGroupFilter(groups []CollectorGroup, filter ListGroupFilter) []CollectorGroup {
	out := make([]CollectorGroup, 0, len(groups))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, group := range groups {
		if filter.Status == "" && group.Status == "deleted" {
			continue
		}
		if filter.Environment != "" && group.Environment != filter.Environment {
			continue
		}
		if filter.Cluster != "" && group.Cluster != filter.Cluster {
			continue
		}
		if filter.Namespace != "" && group.Namespace != filter.Namespace {
			continue
		}
		if filter.Mode != "" && group.Mode != filter.Mode {
			continue
		}
		if filter.Status != "" && group.Status != filter.Status {
			continue
		}
		if filter.ReceiverProfile != "" && group.ReceiverProfile != filter.ReceiverProfile {
			continue
		}
		if query != "" && !groupMatchesQuery(group, query) {
			continue
		}
		out = append(out, group)
	}
	return out
}

func groupMatchesQuery(group CollectorGroup, query string) bool {
	values := []string{
		group.Name,
		group.DisplayName,
		group.Description,
		group.Environment,
		group.Cluster,
		group.Namespace,
		group.TenantID,
		group.OwnerTeam,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func applyGroupPatch(group CollectorGroup, patch UpdateGroupRequest) CollectorGroup {
	if patch.Name != nil {
		group.Name = *patch.Name
	}
	if patch.DisplayName != nil {
		group.DisplayName = *patch.DisplayName
	}
	if patch.Description != nil {
		group.Description = *patch.Description
	}
	if patch.Mode != nil {
		group.Mode = *patch.Mode
	}
	if patch.Environment != nil {
		group.Environment = *patch.Environment
	}
	if patch.Cluster != nil {
		group.Cluster = *patch.Cluster
	}
	if patch.Namespace != nil {
		group.Namespace = *patch.Namespace
	}
	if patch.TenantID != nil {
		group.TenantID = *patch.TenantID
	}
	if patch.OwnerTeam != nil {
		group.OwnerTeam = *patch.OwnerTeam
	}
	if patch.IsolationLevel != nil {
		group.IsolationLevel = *patch.IsolationLevel
	}
	if patch.PlatformTemplateID != nil {
		group.PlatformTemplateID = *patch.PlatformTemplateID
	}
	if patch.Status != nil {
		group.Status = *patch.Status
	}
	if patch.ReceiverProfile != nil {
		group.ReceiverProfile = *patch.ReceiverProfile
	}
	if patch.ExporterProfile != nil {
		group.ExporterProfile = *patch.ExporterProfile
	}
	if patch.IngestEndpoint != nil {
		group.IngestEndpoint = *patch.IngestEndpoint
	}
	if patch.DesiredReplicas != nil {
		group.DesiredReplicas = *patch.DesiredReplicas
	}
	if patch.MaxServices != nil {
		group.MaxServices = *patch.MaxServices
	}
	if patch.ConfigVersion != nil {
		group.ConfigVersion = *patch.ConfigVersion
	}
	if patch.DesiredConfigHash != nil {
		group.DesiredConfigHash = *patch.DesiredConfigHash
	}
	if patch.LastAppliedConfigHash != nil {
		group.LastAppliedConfigHash = *patch.LastAppliedConfigHash
	}
	if patch.LastPublishStatus != nil {
		group.LastPublishStatus = *patch.LastPublishStatus
	}
	if patch.LastPublishMessage != nil {
		group.LastPublishMessage = *patch.LastPublishMessage
	}
	return group
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s Service) ListInstancesByService(ctx context.Context, serviceID string) ([]CollectorInstance, error) {
	instances, err := s.ListInstances(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]CollectorInstance, 0, len(instances))
	for _, instance := range instances {
		if instance.ServiceID == serviceID {
			out = append(out, instance)
		}
	}
	return out, nil
}

func (s Service) AssignInstanceService(ctx context.Context, instanceUID string, serviceID string) (CollectorInstance, error) {
	if strings.TrimSpace(instanceUID) == "" {
		return CollectorInstance{}, apperr.InvalidRequest("instance_uid 不能为空")
	}
	if strings.TrimSpace(serviceID) == "" {
		return CollectorInstance{}, apperr.InvalidRequest("service_id 不能为空")
	}
	var instance CollectorInstance
	if err := s.instanceStore.FindByUID(ctx, instanceUID, &instance); err != nil {
		return CollectorInstance{}, err
	}
	instance.ServiceID = strings.TrimSpace(serviceID)
	instance.CollectorGroupID = ""
	instance.UpdatedAt = time.Now().UTC()
	if err := s.instanceStore.Update(ctx, instanceUID, instance); err != nil {
		return CollectorInstance{}, err
	}
	return s.applyRuntimeStatus(instance), nil
}

func (s Service) UnassignInstanceService(ctx context.Context, instanceUID string) (CollectorInstance, error) {
	var instance CollectorInstance
	if err := s.instanceStore.FindByUID(ctx, instanceUID, &instance); err != nil {
		return CollectorInstance{}, err
	}
	instance.ServiceID = ""
	instance.UpdatedAt = time.Now().UTC()
	if err := s.instanceStore.Update(ctx, instanceUID, instance); err != nil {
		return CollectorInstance{}, err
	}
	return s.applyRuntimeStatus(instance), nil
}
