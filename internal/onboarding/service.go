package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"novaobs/internal/collectormanagement"
	"novaobs/internal/database"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type Guide struct {
	ServiceName        string            `json:"service_name"`
	Environment        string            `json:"environment"`
	Endpoint           string            `json:"endpoint"`
	ResourceAttributes string            `json:"resource_attributes"`
	KubernetesLabels   map[string]string `json:"kubernetes_labels"`
	Checklist          []ChecklistItem   `json:"checklist"`
	CodeSamples        map[string]string `json:"code_samples"`
}

type ChecklistItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Blocking    bool   `json:"blocking"`
	Message     string `json:"message"`
	Passed      bool   `json:"passed"`
}

type CheckResult struct {
	ServiceName string          `json:"service_name"`
	Ready       bool            `json:"ready"`
	Status      string          `json:"status"`
	Checks      []ChecklistItem `json:"checks"`
	CheckedAt   *time.Time      `json:"checked_at"`
}

type Service struct {
	onboardingStore database.OnboardingStore
	identityStore   database.IngestionIdentityStore
	serviceRepo     servicecatalog.Repository
	collectorSvc    collectormanagement.Service
}

func NewService(onboardingStore database.OnboardingStore, identityStore database.IngestionIdentityStore, serviceRepo servicecatalog.Repository, collectorSvc collectormanagement.Service) Service {
	return Service{onboardingStore: onboardingStore, identityStore: identityStore, serviceRepo: serviceRepo, collectorSvc: collectorSvc}
}

func (s Service) Upsert(ctx context.Context, service servicecatalog.Service, req UpsertRequest) (Workspace, error) {
	req = normalizeUpsertRequest(service, req)
	if err := s.validateUpsertRequest(ctx, service, req); err != nil {
		return Workspace{}, err
	}
	identity, err := s.upsertIdentity(ctx, service, req)
	if err != nil {
		return Workspace{}, err
	}

	target := s.collectorTarget(ctx, req.CollectorGroupID, service)
	config := BuildGeneratedConfigWithEndpoint(service, target.Endpoint)
	onboarding := ServiceOnboarding{}
	err = s.onboardingStore.FindByService(ctx, service.ID, &onboarding)
	if err != nil && err != mongo.ErrNoDocuments {
		return Workspace{}, err
	}
	now := time.Now().UTC()
	if err == mongo.ErrNoDocuments {
		onboarding.ID = primitive.NewObjectID().Hex()
		onboarding.ServiceID = service.ID
		onboarding.CreatedAt = now
	}
	onboarding.Mode = req.Mode
	onboarding.CollectorGroupID = req.CollectorGroupID
	onboarding.IdentityID = identity.ID
	onboarding.Status = "pending_verification"
	onboarding.Endpoint = config.Endpoint
	onboarding.ResourceAttributes = config.ResourceAttributesText
	onboarding.KubernetesLabels = labelsToText(config.KubernetesLabels)
	onboarding.LastCheckStatus = "pending"
	onboarding.LastCheckMessage = "等待首条日志验证"
	onboarding.UpdatedAt = now

	if err := s.onboardingStore.Upsert(ctx, service.ID, onboarding); err != nil {
		return Workspace{}, err
	}
	return s.buildWorkspace(ctx, service, onboarding, identity, CheckResult{})
}

func (s Service) Get(ctx context.Context, service servicecatalog.Service) (Workspace, error) {
	onboarding := s.defaultOnboarding(service)
	var identity IngestionIdentity
	identityErr := s.identityStore.FindByService(ctx, service.ID, &identity)
	if identityErr != nil && identityErr != mongo.ErrNoDocuments {
		return Workspace{}, identityErr
	}
	err := s.onboardingStore.FindByService(ctx, service.ID, &onboarding)
	if err != nil && err != mongo.ErrNoDocuments {
		return Workspace{}, err
	}
	if err == mongo.ErrNoDocuments {
		onboarding.CollectorGroupID = s.recommendedCollectorGroupID(ctx, service)
	}
	if identityErr == mongo.ErrNoDocuments {
		identity = IngestionIdentity{}
	}
	return s.buildWorkspace(ctx, service, onboarding, identity, CheckResult{})
}

func (s Service) Check(ctx context.Context, service servicecatalog.Service) (Workspace, error) {
	var onboarding ServiceOnboarding
	err := s.onboardingStore.FindByService(ctx, service.ID, &onboarding)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return Workspace{}, apperr.InvalidRequest("请先保存服务接入配置")
		}
		return Workspace{}, err
	}
	var identity IngestionIdentity
	identityErr := s.identityStore.FindByService(ctx, service.ID, &identity)
	if identityErr != nil && identityErr != mongo.ErrNoDocuments {
		return Workspace{}, identityErr
	}
	if identityErr == mongo.ErrNoDocuments {
		identity = IngestionIdentity{}
	}
	checks := s.evaluateChecklist(ctx, service, onboarding, identity)
	now := time.Now().UTC()
	result := CheckResult{
		ServiceName: service.Name,
		Ready:       allBlockingPassed(checks) && checklistStatus(checks, "log_signal_seen") == "passed",
		Status:      statusFromChecklist(checks),
		Checks:      checks,
		CheckedAt:   &now,
	}
	onboarding.Status = result.Status
	onboarding.LastCheckStatus = result.Status
	onboarding.LastCheckMessage = resultMessage(checks)
	onboarding.LastCheckAt = &now
	onboarding.VerificationAttempts++
	if result.Ready {
		onboarding.LastVerifiedAt = &now
	}
	if details, err := json.Marshal(checks); err == nil {
		onboarding.LastCheckDetails = string(details)
	}
	onboarding.UpdatedAt = now
	if err := s.onboardingStore.Upsert(ctx, service.ID, onboarding); err != nil {
		return Workspace{}, err
	}
	return s.buildWorkspace(ctx, service, onboarding, identity, result)
}

func BuildGuide(service servicecatalog.Service) Guide {
	config := BuildGeneratedConfig(service)

	return Guide{
		ServiceName:        service.Name,
		Environment:        service.Environment,
		Endpoint:           config.Endpoint,
		ResourceAttributes: config.ResourceAttributesText,
		KubernetesLabels:   config.KubernetesLabels,
		Checklist: []ChecklistItem{
			{Key: "network", Name: "内部防火墙", Description: "允许访问 4317/4318 端口", Status: "passed", Passed: true},
			{Key: "identity_bound", Name: "接入身份", Description: "生产环境使用平台生成的可信身份", Status: "pending", Blocking: true, Passed: false},
			{Key: "resource_limit", Name: "资源限制", Description: "SDK 或 Agent 配置缓冲和内存限制", Status: "pending", Passed: false},
		},
		CodeSamples: config.CodeSamples,
	}
}

func Check(service servicecatalog.Service) CheckResult {
	guide := BuildGuide(service)
	return CheckResult{
		ServiceName: service.Name,
		Ready:       false,
		Checks:      guide.Checklist,
	}
}

func (s Service) upsertIdentity(ctx context.Context, service servicecatalog.Service, req UpsertRequest) (IngestionIdentity, error) {
	var identity IngestionIdentity
	err := s.identityStore.FindByService(ctx, service.ID, &identity)
	if err != nil && err != mongo.ErrNoDocuments {
		return IngestionIdentity{}, err
	}
	if err == mongo.ErrNoDocuments {
		identity.ID = primitive.NewObjectID().Hex()
		identity.ServiceID = service.ID
		identity.Environment = service.Environment
		identity.IdentityType = req.IdentityType
		identity.Enabled = true
		identity.CreatedAt = time.Now().UTC()
	}
	identity.TenantID = service.BusinessID
	identity.IdentityType = req.IdentityType
	identity.K8sNamespace = req.K8sNamespace
	identity.K8sWorkload = req.K8sWorkload
	identity.UpdatedAt = time.Now().UTC()
	if err := s.identityStore.Upsert(ctx, service.ID, identity); err != nil {
		return IngestionIdentity{}, err
	}
	return identity, nil
}

func normalizeUpsertRequest(service servicecatalog.Service, req UpsertRequest) UpsertRequest {
	if strings.TrimSpace(req.Mode) == "" {
		req.Mode = "shared_gateway"
	}
	if strings.TrimSpace(req.IdentityType) == "" {
		req.IdentityType = service.IdentityType
	}
	if strings.TrimSpace(req.IdentityType) == "" {
		req.IdentityType = "k8s_workload"
	}
	if strings.TrimSpace(req.K8sNamespace) == "" {
		req.K8sNamespace = service.Namespace
	}
	if strings.TrimSpace(req.K8sWorkload) == "" {
		req.K8sWorkload = service.Name
	}
	return req
}

func cmdbServiceID(service servicecatalog.Service) string {
	if strings.TrimSpace(service.CMDBServiceID) != "" {
		return service.CMDBServiceID
	}
	return fmt.Sprintf("svc-%s", service.ID)
}

func labelsToText(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func BuildGeneratedConfig(service servicecatalog.Service) GeneratedConfig {
	return BuildGeneratedConfigWithEndpoint(service, "")
}

func BuildGeneratedConfigWithEndpoint(service servicecatalog.Service, endpoint string) GeneratedConfig {
	endpoint = strings.TrimSpace(endpoint)
	resourceAttributes := map[string]string{
		"service.name":           service.Name,
		"deployment.environment": service.Environment,
		"cmdb.service_id":        cmdbServiceID(service),
		"business_id":            service.BusinessID,
		"application_id":         service.ApplicationID,
		"k8s.cluster.name":       service.Cluster,
		"k8s.namespace.name":     service.Namespace,
		"owner_team":             service.OwnerTeam,
		"alert_route":            service.AlertRoute,
	}
	kubernetesLabels := map[string]string{
		"cmdb.business_id":                     service.BusinessID,
		"cmdb.app_id":                          service.ApplicationID,
		"cmdb.service_id":                      cmdbServiceID(service),
		"env":                                  service.Environment,
		"observability.ucloud.cn/service":      service.Name,
		"observability.ucloud.cn/environment":  service.Environment,
		"observability.ucloud.cn/collector-id": cmdbServiceID(service),
	}
	env := map[string]string{
		"OTEL_RESOURCE_ATTRIBUTES": attributesText(resourceAttributes),
	}
	if endpoint != "" {
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = endpoint
	}
	hint := "receiver/exporter 由 Collector 部署模板定义，服务侧只需携带标准资源属性。"
	if endpoint == "" {
		hint = "Collector Group 尚未配置接入入口，请先在 Collector Group 设置 ingest_endpoint。"
	}
	return GeneratedConfig{
		Endpoint:               endpoint,
		ResourceAttributes:     resourceAttributes,
		ResourceAttributesText: attributesText(resourceAttributes),
		KubernetesLabels:       kubernetesLabels,
		EnvironmentVariables:   env,
		EnvBlock:               envBlock(env),
		OTelCollectorHint:      hint,
		CodeSamples: map[string]string{
			"env": envBlock(env),
			"go":  "otel.SetTracerProvider(tracerProvider)",
			"k8s": fmt.Sprintf("observability.ucloud.cn/service: %s", service.Name),
		},
	}
}

func (s Service) validateUpsertRequest(ctx context.Context, service servicecatalog.Service, req UpsertRequest) error {
	if req.Mode != "shared_gateway" && req.Mode != "dedicated_collector" {
		return apperr.InvalidRequest("mode 只支持 shared_gateway 或 dedicated_collector")
	}
	if req.IdentityType != "k8s_workload" && req.IdentityType != "host_process" {
		return apperr.InvalidRequest("identity_type 只支持 k8s_workload 或 host_process")
	}
	if req.IdentityType == "k8s_workload" && (strings.TrimSpace(req.K8sNamespace) == "" || strings.TrimSpace(req.K8sWorkload) == "") {
		return apperr.InvalidRequest("k8s_workload 身份必须包含 namespace 和 workload")
	}
	if strings.TrimSpace(req.CollectorGroupID) == "" {
		return apperr.InvalidRequest("Collector Group 不能为空")
	}
	group, err := s.collectorSvc.GetGroup(ctx, req.CollectorGroupID)
	if err != nil {
		return apperr.InvalidRequest("Collector Group 不存在")
	}
	if group.Status != "" && group.Status != "active" {
		return apperr.InvalidRequest("Collector Group 当前不可用")
	}
	if group.Mode != "" && group.Mode != req.Mode {
		return apperr.InvalidRequest("Collector Group 模式与接入模式不匹配")
	}
	if group.Environment != "" && service.Environment != "" && group.Environment != service.Environment {
		return apperr.InvalidRequest("Collector Group 环境与服务不匹配")
	}
	if group.Cluster != "" && service.Cluster != "" && group.Cluster != service.Cluster {
		return apperr.InvalidRequest("Collector Group 集群与服务不匹配")
	}
	return nil
}

func (s Service) defaultOnboarding(service servicecatalog.Service) ServiceOnboarding {
	return ServiceOnboarding{
		ServiceID:        service.ID,
		Mode:             "shared_gateway",
		Status:           "not_started",
		LastCheckStatus:  "not_started",
		LastCheckMessage: "尚未保存接入配置",
	}
}

func (s Service) buildWorkspace(ctx context.Context, service servicecatalog.Service, onboarding ServiceOnboarding, identity IngestionIdentity, lastCheck CheckResult) (Workspace, error) {
	target := s.collectorTarget(ctx, onboarding.CollectorGroupID, service)
	config := BuildGeneratedConfigWithEndpoint(service, target.Endpoint)
	if onboarding.Endpoint == "" {
		onboarding.Endpoint = config.Endpoint
	}
	checks := lastCheck.Checks
	if len(checks) == 0 {
		checks = s.evaluateChecklist(ctx, service, onboarding, identity)
	}
	if lastCheck.ServiceName == "" {
		lastCheck = CheckResult{
			ServiceName: service.Name,
			Ready:       onboarding.Status == "verified",
			Status:      onboarding.LastCheckStatus,
			Checks:      checks,
			CheckedAt:   onboarding.LastCheckAt,
		}
	}
	return Workspace{
		Service:          serviceSummary(service),
		Onboarding:       onboarding,
		Identity:         identitySummary(identity),
		CollectorTarget:  target,
		GeneratedConfig:  config,
		Checklist:        checks,
		LastCheck:        lastCheck,
		AvailableActions: availableActions(onboarding),
	}, nil
}

func (s Service) recommendedCollectorGroupID(ctx context.Context, service servicecatalog.Service) string {
	groups, err := s.collectorSvc.ListGroups(ctx)
	if err != nil {
		return ""
	}
	for _, group := range groups {
		if group.Mode == "shared_gateway" && group.Status == "active" && group.Environment == service.Environment && group.Cluster == service.Cluster {
			return group.ID
		}
	}
	return ""
}

func (s Service) collectorTarget(ctx context.Context, groupID string, service servicecatalog.Service) CollectorTarget {
	if groupID == "" {
		groupID = s.recommendedCollectorGroupID(ctx, service)
	}
	if groupID == "" {
		return CollectorTarget{}
	}
	group, err := s.collectorSvc.GetGroup(ctx, groupID)
	if err != nil {
		return CollectorTarget{GroupID: groupID}
	}
	instances, _ := s.collectorSvc.ListInstances(ctx, group.ID)
	target := CollectorTarget{
		GroupID:         group.ID,
		Name:            group.Name,
		Mode:            group.Mode,
		Environment:     group.Environment,
		Cluster:         group.Cluster,
		Namespace:       group.Namespace,
		Status:          group.Status,
		ReceiverProfile: group.ReceiverProfile,
		ExporterProfile: group.ExporterProfile,
		Endpoint:        group.IngestEndpoint,
	}
	for _, instance := range instances {
		if instance.Online {
			target.OnlineInstances++
		}
		if instance.Healthy {
			target.HealthyInstances++
		}
		if instance.RemoteConfigCapable {
			target.RemoteConfigCapableInstances++
		}
	}
	return target
}

func (s Service) evaluateChecklist(ctx context.Context, service servicecatalog.Service, onboarding ServiceOnboarding, identity IngestionIdentity) []ChecklistItem {
	config := BuildGeneratedConfig(service)
	target := s.collectorTarget(ctx, onboarding.CollectorGroupID, service)
	return []ChecklistItem{
		check("service_metadata", "服务元数据", "服务目录包含 owner_team、alert_route、cluster 和 namespace", service.OwnerTeam != "" && service.AlertRoute != "" && service.Cluster != "" && service.Namespace != "", true),
		check("identity_bound", "接入身份", "服务已绑定平台可信身份", identity.ID != "" && identity.Enabled, true),
		check("collector_group_selected", "Collector 目标", "服务已绑定 Collector Group", onboarding.CollectorGroupID != "", true),
		check("collector_group_available", "Collector Group 可用", "目标 Collector Group 处于 active 状态", target.GroupID != "" && target.Status == "active", true),
		check("collector_instance_online", "Collector 实例在线", "目标 Collector Group 至少有一个在线实例", target.OnlineInstances > 0, true),
		check("resource_attributes_complete", "资源属性完整", "接入配置包含 service、environment、cmdb、cluster 和 namespace", resourceAttributesComplete(config.ResourceAttributes), true),
		{Key: "log_signal_seen", Name: "日志信号", Description: "服务日志已带标准资源属性进入查询链路", Status: "warning", Blocking: false, Message: "日志查询验证尚未接入日志下游", Passed: false},
	}
}

func check(key string, name string, description string, passed bool, blocking bool) ChecklistItem {
	status := "failed"
	message := "未通过"
	if passed {
		status = "passed"
		message = "已通过"
	}
	return ChecklistItem{Key: key, Name: name, Description: description, Status: status, Blocking: blocking, Message: message, Passed: passed}
}

func serviceSummary(service servicecatalog.Service) ServiceSummary {
	return ServiceSummary{
		ID:            service.ID,
		CMDBServiceID: service.CMDBServiceID,
		BusinessID:    service.BusinessID,
		ApplicationID: service.ApplicationID,
		Name:          service.Name,
		DisplayName:   service.DisplayName,
		IdentityType:  service.IdentityType,
		Environment:   service.Environment,
		Cluster:       service.Cluster,
		Namespace:     service.Namespace,
		OwnerTeam:     service.OwnerTeam,
		Owner:         service.Owner,
		AlertRoute:    service.AlertRoute,
		Status:        service.Status,
	}
}

func identitySummary(identity IngestionIdentity) IdentitySummary {
	return IdentitySummary{
		ID:           identity.ID,
		IdentityType: identity.IdentityType,
		Enabled:      identity.Enabled,
		TenantID:     identity.TenantID,
		Environment:  identity.Environment,
		K8sNamespace: identity.K8sNamespace,
		K8sWorkload:  identity.K8sWorkload,
		ExpiresAt:    identity.ExpiresAt,
		CreatedAt:    identity.CreatedAt,
		UpdatedAt:    identity.UpdatedAt,
		TokenPresent: identity.TokenHash != "",
	}
}

func attributesText(attributes map[string]string) string {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if attributes[key] != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", key, attributes[key]))
		}
	}
	return strings.Join(parts, ",")
}

func envBlock(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", key, env[key]))
	}
	return strings.Join(lines, "\n")
}

func resourceAttributesComplete(attributes map[string]string) bool {
	for _, key := range []string{"service.name", "deployment.environment", "cmdb.service_id", "k8s.cluster.name", "k8s.namespace.name"} {
		if strings.TrimSpace(attributes[key]) == "" {
			return false
		}
	}
	return true
}

func allBlockingPassed(checks []ChecklistItem) bool {
	for _, item := range checks {
		if item.Blocking && item.Status != "passed" {
			return false
		}
	}
	return true
}

func checklistStatus(checks []ChecklistItem, key string) string {
	for _, item := range checks {
		if item.Key == key {
			return item.Status
		}
	}
	return ""
}

func statusFromChecklist(checks []ChecklistItem) string {
	if !allBlockingPassed(checks) {
		return "failed"
	}
	if checklistStatus(checks, "log_signal_seen") == "passed" {
		return "verified"
	}
	return "pending_verification"
}

func resultMessage(checks []ChecklistItem) string {
	if !allBlockingPassed(checks) {
		return "控制面检查未通过"
	}
	if checklistStatus(checks, "log_signal_seen") != "passed" {
		return "控制面检查已通过，等待日志信号验证"
	}
	return "服务接入已验证"
}

func availableActions(onboarding ServiceOnboarding) []string {
	if onboarding.Status == "not_started" || onboarding.ID == "" {
		return []string{"save"}
	}
	return []string{"save", "check"}
}
