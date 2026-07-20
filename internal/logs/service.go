package logs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"novaapm/internal/collectormanagement"
	"novaapm/internal/database"
	k8sopscluster "novaapm/internal/modules/k8sops/cluster"
	k8sopsdeployment "novaapm/internal/modules/k8sops/deployment"
	k8sopsresource "novaapm/internal/modules/k8sops/resource"
	obsruntime "novaapm/internal/observability/runtime"
	platformaudit "novaapm/internal/platform/audit"
	platformimages "novaapm/internal/platform/images"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"gopkg.in/yaml.v3"
)

var ErrPermissionDenied = errors.New("permission_denied")

const (
	defaultK8sCollectorNamespace = "novaapm-system"
	legacyK8sCollectorNamespace  = "novaobs-system"
)

func normalizeK8sCollectorNamespace(namespace string) string {
	value := strings.TrimSpace(namespace)
	if value == "" || value == legacyK8sCollectorNamespace {
		return defaultK8sCollectorNamespace
	}
	return value
}

type K8sClusterService interface {
	List(ctx context.Context, filter k8sopscluster.ListFilter) ([]k8sopscluster.Cluster, error)
	Get(ctx context.Context, id string) (k8sopscluster.Cluster, error)
}

type K8sResourceService interface {
	List(ctx context.Context, filter k8sopsresource.ListFilter) ([]k8sopsresource.ResourceSummary, error)
	ListRuntimeGroups(ctx context.Context, query k8sopsresource.RuntimeGroupsQuery) (k8sopsresource.RuntimeGroupsResponse, error)
}

type K8sDeploymentService interface {
	Preview(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
	Apply(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
}

type ImageTemplateValueService interface {
	TemplateValues(ctx context.Context) (map[string]string, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type EndpointAuditor interface {
	Record(ctx context.Context, event platformaudit.Event) (platformaudit.Event, error)
}

type Service struct {
	endpoints         database.LogEndpointStore
	sources           database.LogSourceStore
	routes            database.LogRouteStore
	vmEndpoints       database.VMLogAgentEndpointStore
	logTargets        database.LogTargetStore
	configVersions    database.LogCollectorConfigVersionStore
	manifestVersions  database.LogDeploymentManifestVersionStore
	clusterConfigs    database.LogCollectorClusterConfigStore
	runtimes          database.ObservabilityRuntimeStore
	services          servicecatalog.Repository
	targets           servicecatalog.TargetRepository
	collectorGroups   collectormanagement.Service
	k8sClusters       K8sClusterService
	k8sResources      K8sResourceService
	k8sDeployments    K8sDeploymentService
	imageTemplates    ImageTemplateValueService
	authorizer        Authorizer
	endpointAuditor   EndpointAuditor
	deployment        agentDeploymentOptions
	vmEndpointChecker VMEndpointChecker
}

type VMEndpointChecker interface {
	Check(ctx context.Context, address string) (time.Duration, error)
}

type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type tcpVMEndpointChecker struct {
	resolver ipResolver
	dial     func(ctx context.Context, network, address string) (net.Conn, error)
}

func (c tcpVMEndpointChecker) Check(ctx context.Context, address string) (time.Duration, error) {
	started := time.Now()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return 0, apperr.InvalidRequest("节点地址必须为 host:port")
	}
	if port != "13133" {
		return 0, apperr.InvalidRequest("节点端口固定为 health_check 端口 13133")
	}
	resolver := c.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	resolved, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return time.Since(started), fmt.Errorf("解析节点地址失败: %w", err)
	}
	if len(resolved) == 0 {
		return time.Since(started), errors.New("解析节点地址失败: 未返回 IP")
	}
	for _, candidate := range resolved {
		if err := validateVMEndpointIP(candidate.IP); err != nil {
			return time.Since(started), err
		}
	}
	dial := c.dial
	if dial == nil {
		dialer := net.Dialer{Timeout: 2 * time.Second}
		dial = dialer.DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(resolved[0].IP.String(), port))
	if err != nil {
		return time.Since(started), err
	}
	_ = conn.Close()
	return time.Since(started), nil
}

type agentDeploymentOptions struct {
	OpAMPEndpoint string
}

type ServiceOption func(*Service)

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

func WithAgentOpAMPEndpoint(endpoint string) ServiceOption {
	return func(s *Service) {
		s.deployment.OpAMPEndpoint = strings.TrimSpace(endpoint)
	}
}

func WithLogTargets(targets database.LogTargetStore) ServiceOption {
	return func(s *Service) {
		s.logTargets = targets
	}
}

func WithVMLogAgentEndpoints(endpoints database.VMLogAgentEndpointStore) ServiceOption {
	return func(s *Service) { s.vmEndpoints = endpoints }
}

func WithVMEndpointChecker(checker VMEndpointChecker) ServiceOption {
	return func(s *Service) { s.vmEndpointChecker = checker }
}

func WithObservabilityRuntimes(runtimes database.ObservabilityRuntimeStore) ServiceOption {
	return func(s *Service) {
		s.runtimes = runtimes
	}
}

func WithAuthorizer(authorizer Authorizer) ServiceOption {
	return func(s *Service) {
		if authorizer != nil {
			s.authorizer = authorizer
		}
	}
}

func WithEndpointAuditor(auditor EndpointAuditor) ServiceOption {
	return func(s *Service) {
		s.endpointAuditor = auditor
	}
}

func WithImageTemplateValues(service ImageTemplateValueService) ServiceOption {
	return func(s *Service) {
		s.imageTemplates = service
	}
}

func NewService(
	endpoints database.LogEndpointStore,
	sources database.LogSourceStore,
	routes database.LogRouteStore,
	configVersions database.LogCollectorConfigVersionStore,
	manifestVersions database.LogDeploymentManifestVersionStore,
	clusterConfigs database.LogCollectorClusterConfigStore,
	services servicecatalog.Repository,
	targets servicecatalog.TargetRepository,
	collectorGroups collectormanagement.Service,
	k8sClusters K8sClusterService,
	k8sResources K8sResourceService,
	k8sDeployments K8sDeploymentService,
	options ...ServiceOption,
) Service {
	service := Service{
		endpoints:         endpoints,
		sources:           sources,
		routes:            routes,
		configVersions:    configVersions,
		manifestVersions:  manifestVersions,
		clusterConfigs:    clusterConfigs,
		services:          services,
		targets:           targets,
		collectorGroups:   collectorGroups,
		k8sClusters:       k8sClusters,
		k8sResources:      k8sResources,
		k8sDeployments:    k8sDeployments,
		authorizer:        denyAuthorizer{},
		vmEndpointChecker: tcpVMEndpointChecker{},
	}
	for _, option := range options {
		if option != nil {
			option(&service)
		}
	}
	return service
}

func (s Service) GetClusterConfig(ctx context.Context, clusterID string, agentNamespace string) (LogCollectorClusterConfig, error) {
	agentNamespace = normalizeK8sCollectorNamespace(agentNamespace)
	var cfg LogCollectorClusterConfig
	err := s.clusterConfigs.FindByCluster(ctx, strings.TrimSpace(clusterID), agentNamespace, &cfg)
	if err != nil {
		return LogCollectorClusterConfig{ClusterID: clusterID, AgentNamespace: agentNamespace}, nil
	}
	return cfg, nil
}

func (s Service) UpsertClusterConfig(ctx context.Context, clusterID string, agentNamespace string, processorPatch string) (LogCollectorClusterConfig, error) {
	agentNamespace = normalizeK8sCollectorNamespace(agentNamespace)
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return LogCollectorClusterConfig{}, apperr.InvalidRequest("cluster_id 不能为空")
	}
	cfg := LogCollectorClusterConfig{
		ClusterID:      clusterID,
		AgentNamespace: agentNamespace,
		ProcessorPatch: strings.TrimSpace(processorPatch),
		UpdatedAt:      time.Now(),
	}
	if err := s.clusterConfigs.Upsert(ctx, clusterID, agentNamespace, cfg); err != nil {
		return LogCollectorClusterConfig{}, err
	}
	return cfg, nil
}

func (s Service) Workspace(ctx context.Context, subject platformrbac.Subject, productID string, serviceID string) (Workspace, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return Workspace{}, apperr.InvalidRequest("service_id 不能为空")
	}
	service, err := s.services.Get(ctx, serviceID)
	if err != nil {
		return Workspace{}, normalizeNotFound(err, "服务不存在")
	}
	if service.ProductID == "" || service.ProductID != strings.TrimSpace(productID) {
		return Workspace{}, apperr.InvalidRequest("服务不属于路径中的产品")
	}
	if !s.allowed(subject, serviceID, "logs.query", "read") {
		return Workspace{}, ErrPermissionDenied
	}
	groups, err := s.collectorGroups.ListGroups(ctx)
	if err != nil {
		return Workspace{}, err
	}
	endpoints, err := s.ListEndpoints(ctx)
	if err != nil {
		return Workspace{}, err
	}
	routes, err := s.ListRoutes(ctx)
	if err != nil {
		return Workspace{}, err
	}
	serviceRoutes := make([]LogRouteView, 0, len(routes))
	for _, route := range routes {
		if route.Route.ServiceID != serviceID {
			continue
		}
		if route.Endpoint != nil {
			projected := endpointForService(*route.Endpoint, service)
			route.Endpoint = &projected
		}
		serviceRoutes = append(serviceRoutes, route)
	}
	for index := range endpoints {
		endpoints[index] = endpointForService(endpoints[index], service)
	}
	targets, err := s.listTargetViews(ctx, serviceID)
	if err != nil {
		return Workspace{}, err
	}
	clusters := []k8sopscluster.Cluster{}
	if s.k8sClusters != nil {
		clusters, err = s.k8sClusters.List(ctx, k8sopscluster.ListFilter{PageSize: 0})
		if err != nil {
			return Workspace{}, err
		}
	}
	return Workspace{
		Services:        serviceSummaries([]servicecatalog.Service{service}),
		CollectorGroups: agentGroupSummaries(groups),
		Clusters:        clusterSummaries(clusters),
		Endpoints:       endpoints,
		Routes:          serviceRoutes,
		Targets:         targets,
	}, nil
}

func endpointForService(endpoint LogEndpoint, service servicecatalog.Service) LogEndpoint {
	if endpoint.SinkType == EndpointSinkVL {
		endpoint.AccountID = service.AccountID
		endpoint.ProjectID = service.ProjectID
	}
	return endpoint
}

func (s Service) ListEndpointsForSubject(ctx context.Context, subject platformrbac.Subject) ([]LogEndpoint, error) {
	if !s.allowedGlobal(subject, "observability.endpoint", "read") {
		return nil, ErrPermissionDenied
	}
	return s.ListEndpoints(ctx)
}

func (s Service) CreateEndpointForSubject(ctx context.Context, subject platformrbac.Subject, endpoint LogEndpoint) (LogEndpoint, error) {
	if !s.allowedGlobal(subject, "observability.endpoint", "manage") {
		return LogEndpoint{}, ErrPermissionDenied
	}
	created, err := s.CreateEndpoint(ctx, endpoint)
	if err != nil {
		return LogEndpoint{}, err
	}
	if err := s.recordEndpointAudit(ctx, subject, "create", created); err != nil {
		return LogEndpoint{}, err
	}
	return created, nil
}

func (s Service) UpdateEndpointForSubject(ctx context.Context, subject platformrbac.Subject, id string, endpoint LogEndpoint) (LogEndpoint, error) {
	if !s.allowedGlobal(subject, "observability.endpoint", "manage") {
		return LogEndpoint{}, ErrPermissionDenied
	}
	updated, err := s.UpdateEndpoint(ctx, id, endpoint)
	if err != nil {
		return LogEndpoint{}, err
	}
	if err := s.recordEndpointAudit(ctx, subject, "update", updated); err != nil {
		return LogEndpoint{}, err
	}
	return updated, nil
}

func (s Service) recordEndpointAudit(ctx context.Context, subject platformrbac.Subject, action string, endpoint LogEndpoint) error {
	if s.endpointAuditor == nil {
		return nil
	}
	_, err := s.endpointAuditor.Record(ctx, platformaudit.Event{
		Actor:    platformaudit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource: platformaudit.Resource{Type: "observability_endpoint", Name: endpoint.ID},
		Action:   action, Scope: endpoint.ScopeType,
		RequestSummary: map[string]any{"kind": endpoint.Kind, "signal_types": append([]string{}, endpoint.SignalTypes...), "cluster_id": endpoint.ClusterID, "status": endpoint.Status},
		Result:         "success",
	})
	return err
}

func (s Service) CreateEndpoint(ctx context.Context, endpoint LogEndpoint) (LogEndpoint, error) {
	endpoint = normalizeStoredEndpoint(endpoint)
	if err := validateEndpoint(endpoint); err != nil {
		return LogEndpoint{}, err
	}
	if endpoint.ScopeType != EndpointScopeGlobal && endpoint.ScopeType != EndpointScopeK8sCluster && endpoint.ScopeType != EndpointScopeVM {
		return LogEndpoint{}, apperr.InvalidRequest("日志下游端点 scope_type 只支持 global、k8s_cluster 或 vm")
	}
	if endpoint.ScopeType == EndpointScopeK8sCluster && endpoint.ClusterID == "" {
		return LogEndpoint{}, apperr.InvalidRequest("K8s 集群级日志下游端点必须填写 cluster_id")
	}
	if err := s.ensureEndpointClusterRegistered(ctx, endpoint); err != nil {
		return LogEndpoint{}, err
	}
	existing, err := s.ListEndpoints(ctx)
	if err != nil {
		return LogEndpoint{}, err
	}
	for _, item := range existing {
		if strings.EqualFold(item.Name, endpoint.Name) {
			return LogEndpoint{}, apperr.Conflict("日志下游端点名称已存在")
		}
	}
	if endpoint.ID == "" {
		endpoint.ID = primitive.NewObjectID().Hex()
	}
	now := time.Now().UTC()
	endpoint.CreatedAt = now
	endpoint.UpdatedAt = now
	if endpoint.Status == "" {
		endpoint.Status = "active"
	}
	if err := s.endpoints.Insert(ctx, endpoint); err != nil {
		return LogEndpoint{}, err
	}
	return endpoint, nil
}

func (s Service) UpdateEndpoint(ctx context.Context, id string, endpoint LogEndpoint) (LogEndpoint, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return LogEndpoint{}, apperr.InvalidRequest("日志下游端点 ID 不能为空")
	}
	var existing LogEndpoint
	if err := s.endpoints.FindByID(ctx, id, &existing); err != nil {
		return LogEndpoint{}, normalizeNotFound(err, "日志下游端点不存在")
	}
	endpoint = normalizeStoredEndpoint(endpoint)
	endpoint.ID = id
	existing = normalizeStoredEndpoint(existing)
	if endpoint.Kind != existing.Kind || !sameStringSet(endpoint.SignalTypes, existing.SignalTypes) {
		return LogEndpoint{}, apperr.InvalidRequest("观测端点创建后不能变更 kind 或 signal_types")
	}
	if err := validateEndpoint(endpoint); err != nil {
		return LogEndpoint{}, err
	}
	if endpoint.ScopeType != EndpointScopeGlobal && endpoint.ScopeType != EndpointScopeK8sCluster && endpoint.ScopeType != EndpointScopeVM {
		return LogEndpoint{}, apperr.InvalidRequest("日志下游端点 scope_type 只支持 global、k8s_cluster 或 vm")
	}
	if endpoint.ScopeType == EndpointScopeK8sCluster && endpoint.ClusterID == "" {
		return LogEndpoint{}, apperr.InvalidRequest("K8s 集群级日志下游端点必须填写 cluster_id")
	}
	if err := s.ensureEndpointClusterRegistered(ctx, endpoint); err != nil {
		return LogEndpoint{}, err
	}
	all, err := s.ListEndpoints(ctx)
	if err != nil {
		return LogEndpoint{}, err
	}
	for _, item := range all {
		if item.ID == id {
			continue
		}
		if strings.EqualFold(item.Name, endpoint.Name) {
			return LogEndpoint{}, apperr.Conflict("日志下游端点名称已存在")
		}
	}
	endpoint.CreatedAt = existing.CreatedAt
	endpoint.UpdatedAt = time.Now().UTC()
	if endpoint.Status == "" {
		endpoint.Status = firstNonEmpty(existing.Status, "active")
	}
	if err := s.endpoints.Update(ctx, id, endpoint); err != nil {
		return LogEndpoint{}, err
	}
	return endpoint, nil
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, item := range left {
		seen[strings.TrimSpace(item)]++
	}
	for _, item := range right {
		key := strings.TrimSpace(item)
		if seen[key] == 0 {
			return false
		}
		seen[key]--
	}
	return true
}

func (s Service) ensureEndpointClusterRegistered(ctx context.Context, endpoint LogEndpoint) error {
	if endpoint.ScopeType != EndpointScopeK8sCluster {
		return nil
	}
	if s.k8sClusters == nil {
		return nil
	}
	if _, err := s.k8sClusters.Get(ctx, endpoint.ClusterID); err != nil {
		if errors.Is(err, k8sopscluster.ErrClusterNotFound) || errors.Is(err, k8sopscluster.ErrInvalidClusterRequest) {
			return apperr.InvalidRequest("日志下游端点只能绑定已登记的 K8s 集群")
		}
		return err
	}
	return nil
}

func (s Service) ListEndpoints(ctx context.Context) ([]LogEndpoint, error) {
	var endpoints []LogEndpoint
	if err := s.endpoints.FindAll(ctx, &endpoints); err != nil {
		return nil, err
	}
	for index := range endpoints {
		endpoints[index] = normalizeStoredEndpoint(endpoints[index])
	}
	filtered := endpoints[:0]
	for _, endpoint := range endpoints {
		if endpointSupportsLogsSignal(endpoint.SignalTypes) {
			filtered = append(filtered, endpoint)
		}
	}
	endpoints = filtered
	sort.SliceStable(endpoints, func(i, j int) bool {
		return endpoints[i].Name < endpoints[j].Name
	})
	return endpoints, nil
}

func (s Service) ListRoutes(ctx context.Context) ([]LogRouteView, error) {
	var routes []LogRoute
	if err := s.routes.FindAll(ctx, &routes); err != nil {
		return nil, err
	}
	sort.SliceStable(routes, func(i, j int) bool {
		return routes[i].UpdatedAt.After(routes[j].UpdatedAt)
	})
	views := make([]LogRouteView, 0, len(routes))
	for _, route := range routes {
		view := LogRouteView{Route: route}
		if source, err := s.getSource(ctx, route.SourceID); err == nil {
			effectiveSource := routeEffectiveSource(route, source)
			view.Source = &effectiveSource
		}
		if endpoint, err := s.getEndpoint(ctx, route.EndpointID); err == nil {
			view.Endpoint = &endpoint
		}
		views = append(views, view)
	}
	return views, nil
}

func (s Service) ListTargets(ctx context.Context, subject platformrbac.Subject, serviceID string) ([]LogTargetView, error) {
	serviceID = strings.TrimSpace(serviceID)
	views, err := s.listTargetViews(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	out := make([]LogTargetView, 0, len(views))
	for _, view := range views {
		if s.allowedTarget(subject, view.Target.ServiceID, "read") {
			out = append(out, view)
		}
	}
	return out, nil
}

func (s Service) CreateTarget(ctx context.Context, subject platformrbac.Subject, req CreateLogTargetRequest) (LogTargetView, error) {
	if s.logTargets == nil {
		return LogTargetView{}, apperr.InvalidRequest("日志目标存储不可用")
	}
	req = normalizeCreateTargetRequest(req)
	if req.ServiceID == "" || req.EndpointID == "" || req.BaseFilter == "" {
		return LogTargetView{}, apperr.InvalidRequest("service_id、endpoint_id 和 base_filter 不能为空")
	}
	if req.SourceKind != LogTargetSourceExternalVLogs {
		return LogTargetView{}, apperr.InvalidRequest("日志目标来源只支持 external_vlogs")
	}
	if err := ValidateLogTargetBaseFilter(req.BaseFilter); err != nil {
		return LogTargetView{}, err
	}
	if err := validateOptionalVictoriaLogsTenant(req.AccountID, req.ProjectID); err != nil {
		return LogTargetView{}, err
	}
	service, err := s.services.Get(ctx, req.ServiceID)
	if err != nil {
		return LogTargetView{}, normalizeNotFound(err, "服务不存在")
	}
	if !s.allowedTarget(subject, service.ID, "manage") {
		return LogTargetView{}, ErrPermissionDenied
	}
	if req.AccountID != "" && !s.allowedExternalTenantOverride(subject) {
		return LogTargetView{}, ErrPermissionDenied
	}
	endpoint, err := s.getEndpoint(ctx, req.EndpointID)
	if err != nil {
		return LogTargetView{}, err
	}
	if err := validateTargetEndpoint(endpoint); err != nil {
		return LogTargetView{}, err
	}
	if err := s.ensureUniqueExternalTarget(ctx, "", service.ID, endpoint.ID, req.BaseFilter); err != nil {
		return LogTargetView{}, err
	}
	now := time.Now().UTC()
	actor := actorRefFromSubject(subject)
	target := LogTarget{
		ID:         primitive.NewObjectID().Hex(),
		Name:       firstNonEmpty(req.Name, service.DisplayName, service.Name),
		ServiceID:  service.ID,
		EndpointID: endpoint.ID,
		SourceKind: req.SourceKind,
		BaseFilter: req.BaseFilter,
		AccountID:  req.AccountID,
		ProjectID:  req.ProjectID,
		Status:     LogTargetStatusPendingVerification,
		CreatedBy:  actor,
		UpdatedBy:  actor,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.logTargets.Insert(ctx, target); err != nil {
		if errors.Is(err, database.ErrConflict) {
			return LogTargetView{}, apperr.Conflict("日志目标已存在")
		}
		return LogTargetView{}, err
	}
	return s.targetView(ctx, target)
}

func (s Service) UpdateTarget(ctx context.Context, subject platformrbac.Subject, targetID string, req UpdateLogTargetRequest) (LogTargetView, error) {
	if s.logTargets == nil {
		return LogTargetView{}, apperr.InvalidRequest("日志目标存储不可用")
	}
	current, err := s.getTarget(ctx, targetID)
	if err != nil {
		return LogTargetView{}, err
	}
	if !s.allowedTarget(subject, current.ServiceID, "manage") {
		return LogTargetView{}, ErrPermissionDenied
	}
	req = normalizeUpdateTargetRequest(req)
	updated := current
	configurationChanged := false
	if req.Name != "" {
		updated.Name = req.Name
	}
	if req.EndpointID != "" {
		endpoint, err := s.getEndpoint(ctx, req.EndpointID)
		if err != nil {
			return LogTargetView{}, err
		}
		if err := validateTargetEndpoint(endpoint); err != nil {
			return LogTargetView{}, err
		}
		configurationChanged = configurationChanged || updated.EndpointID != endpoint.ID
		updated.EndpointID = endpoint.ID
	}
	if req.BaseFilter != "" {
		if err := ValidateLogTargetBaseFilter(req.BaseFilter); err != nil {
			return LogTargetView{}, err
		}
		configurationChanged = configurationChanged || updated.BaseFilter != req.BaseFilter
		updated.BaseFilter = req.BaseFilter
	}
	if req.AccountID != nil || req.ProjectID != nil {
		if req.AccountID == nil || req.ProjectID == nil {
			return LogTargetView{}, apperr.InvalidRequest("VictoriaLogs AccountID 和 ProjectID 必须同时填写")
		}
		if err := validateOptionalVictoriaLogsTenant(*req.AccountID, *req.ProjectID); err != nil {
			return LogTargetView{}, err
		}
		if *req.AccountID != "" && (*req.AccountID != current.AccountID || *req.ProjectID != current.ProjectID) && !s.allowedExternalTenantOverride(subject) {
			return LogTargetView{}, ErrPermissionDenied
		}
		configurationChanged = configurationChanged || updated.AccountID != *req.AccountID || updated.ProjectID != *req.ProjectID
		updated.AccountID = *req.AccountID
		updated.ProjectID = *req.ProjectID
	}
	if req.Status != "" {
		if req.Status == LogTargetStatusVerified {
			return LogTargetView{}, apperr.InvalidRequest("verified 状态只能通过探测产生")
		}
		if req.Status != LogTargetStatusPendingVerification && req.Status != LogTargetStatusDisabled {
			return LogTargetView{}, apperr.InvalidRequest("日志目标状态只支持 pending_verification 或 disabled")
		}
		updated.Status = req.Status
	}
	if configurationChanged {
		if updated.Status != LogTargetStatusDisabled {
			updated.Status = LogTargetStatusPendingVerification
		}
		updated.LastProbeStatus = ""
		updated.LastProbeMessage = ""
		updated.LastProbeAt = nil
	}
	if err := s.ensureUniqueExternalTarget(ctx, current.ID, updated.ServiceID, updated.EndpointID, updated.BaseFilter); err != nil {
		return LogTargetView{}, err
	}
	updated.UpdatedBy = actorRefFromSubject(subject)
	updated.UpdatedAt = time.Now().UTC()
	if err := s.logTargets.Update(ctx, current.ID, updated); err != nil {
		return LogTargetView{}, normalizeNotFound(err, "日志目标不存在")
	}
	return s.targetView(ctx, updated)
}

func (s Service) ProbeTarget(ctx context.Context, subject platformrbac.Subject, targetID string) (LogTargetView, error) {
	target, err := s.getTarget(ctx, targetID)
	if err != nil {
		return LogTargetView{}, err
	}
	if !s.allowedTarget(subject, target.ServiceID, "manage") {
		return LogTargetView{}, ErrPermissionDenied
	}
	if _, err := s.services.Get(ctx, target.ServiceID); err != nil {
		return LogTargetView{}, normalizeNotFound(err, "服务不存在")
	}
	endpoint, err := s.getEndpoint(ctx, target.EndpointID)
	if err != nil {
		return LogTargetView{}, err
	}
	if err := validateTargetEndpoint(endpoint); err != nil {
		return LogTargetView{}, err
	}
	if err := ValidateLogTargetBaseFilter(target.BaseFilter); err != nil {
		return LogTargetView{}, err
	}
	if err := validateOptionalVictoriaLogsTenant(target.AccountID, target.ProjectID); err != nil {
		return LogTargetView{}, err
	}
	now := time.Now().UTC()
	target.Status = LogTargetStatusVerified
	target.LastProbeStatus = "ready"
	target.LastProbeMessage = "日志目标配置完整，VictoriaLogs 查询地址有效"
	target.LastProbeAt = &now
	target.UpdatedBy = actorRefFromSubject(subject)
	target.UpdatedAt = now
	if err := s.logTargets.Update(ctx, target.ID, target); err != nil {
		return LogTargetView{}, normalizeNotFound(err, "日志目标不存在")
	}
	return s.targetView(ctx, target)
}

func (s Service) ListK8sWorkloads(ctx context.Context, clusterID string, namespace string) ([]Workload, error) {
	clusterID = strings.TrimSpace(clusterID)
	namespace = strings.TrimSpace(namespace)
	if clusterID == "" || namespace == "" {
		return nil, apperr.InvalidRequest("cluster_id 和 namespace 不能为空")
	}
	if s.k8sResources == nil {
		return nil, apperr.InvalidRequest("K8s 资源服务不可用")
	}
	result, err := s.k8sResources.ListRuntimeGroups(ctx, k8sopsresource.RuntimeGroupsQuery{
		ClusterID: clusterID,
		Namespace: namespace,
	})
	if err != nil {
		return nil, err
	}
	workloads := make([]Workload, 0)
	for _, group := range result.Groups {
		for _, item := range group.Workloads {
			workloads = append(workloads, Workload{
				ClusterID:       clusterID,
				Namespace:       namespace,
				GroupKey:        group.Key,
				GroupName:       group.DisplayName,
				Key:             item.Key,
				Name:            item.Name,
				Kind:            item.Kind,
				Selector:        copyStringMap(item.Selector),
				TemplateLabels:  copyStringMap(item.TemplateLabels),
				ServiceAccounts: append([]string{}, item.ServiceAccounts...),
				PodsTotal:       item.PodsSummary.Total,
				PodsRunning:     item.PodsSummary.Running,
				RestartCount:    item.PodsSummary.RestartCount,
			})
		}
	}
	sort.SliceStable(workloads, func(i, j int) bool {
		return workloads[i].Kind+"/"+workloads[i].Name < workloads[j].Kind+"/"+workloads[j].Name
	})
	return workloads, nil
}

func (s Service) SyncK8sNamespaceServices(ctx context.Context, req SyncK8sNamespaceRequest) (SyncK8sNamespaceResult, error) {
	req.ProductID = strings.TrimSpace(req.ProductID)
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.EnvironmentID = strings.TrimSpace(req.EnvironmentID)
	req.OwnerTeam = strings.TrimSpace(req.OwnerTeam)
	req.WorkloadKind = strings.TrimSpace(req.WorkloadKind)
	if req.ProductID == "" {
		return SyncK8sNamespaceResult{}, apperr.InvalidRequest("请选择服务所属产品")
	}
	if req.EnvironmentID == "" {
		return SyncK8sNamespaceResult{}, apperr.InvalidRequest("请选择服务所属环境")
	}
	workloads, err := s.ListK8sWorkloads(ctx, req.ClusterID, req.Namespace)
	if err != nil {
		return SyncK8sNamespaceResult{}, err
	}
	services, err := s.services.List(ctx)
	if err != nil {
		return SyncK8sNamespaceResult{}, err
	}
	out := make([]SyncedK8sService, 0, len(workloads))
	for _, workload := range workloads {
		if req.WorkloadKind != "" && !strings.EqualFold(req.WorkloadKind, workload.Kind) {
			continue
		}
		service, created, err := s.findOrCreateK8sService(ctx, services, req, workload)
		if err != nil {
			return SyncK8sNamespaceResult{}, err
		}
		if created {
			services = append(services, service)
		}
		targetID, err := s.ensureK8sServiceTarget(ctx, service, req, workload)
		if err != nil {
			return SyncK8sNamespaceResult{}, err
		}
		out = append(out, SyncedK8sService{
			Service:  serviceSummary(service),
			Workload: workload,
			TargetID: targetID,
			Created:  created,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Service.Name < out[j].Service.Name
	})
	return SyncK8sNamespaceResult{Services: out, Total: len(out)}, nil
}

func (s Service) findOrCreateK8sService(ctx context.Context, services []servicecatalog.Service, req SyncK8sNamespaceRequest, workload Workload) (servicecatalog.Service, bool, error) {
	for _, service := range services {
		if service.ProductID == req.ProductID && service.Name == workload.Name {
			return service, false, nil
		}
	}
	service, err := s.services.Create(ctx, servicecatalog.Service{
		ProductID:     req.ProductID,
		Name:          workload.Name,
		DisplayName:   workload.Name,
		EnvironmentID: req.EnvironmentID,
		Cluster:       req.ClusterID,
		Namespace:     req.Namespace,
		OwnerTeam:     req.OwnerTeam,
		IdentityType:  "k8s_workload",
		ServiceType:   "k8s业务",
		Status:        "active",
		Source:        "k8s",
		SyncStatus:    "synced",
	})
	return service, true, err
}

func (s Service) ensureK8sServiceTarget(ctx context.Context, service servicecatalog.Service, req SyncK8sNamespaceRequest, workload Workload) (string, error) {
	if s.targets == (servicecatalog.TargetRepository{}) {
		return "", nil
	}
	targets, err := s.targets.ListByService(ctx, service.ID)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		if target.TargetType == "cloud_native_workload" &&
			target.IdentityAttributes["k8s.cluster.id"] == req.ClusterID &&
			target.IdentityAttributes["k8s.namespace.name"] == req.Namespace &&
			target.IdentityAttributes["k8s.workload.kind"] == workload.Kind &&
			target.IdentityAttributes["k8s.workload.name"] == workload.Name {
			return target.ID, nil
		}
	}
	target, err := s.targets.Create(ctx, servicecatalog.ObservedTarget{
		ServiceID:     service.ID,
		TargetType:    "cloud_native_workload",
		EnvironmentID: service.EnvironmentID,
		DisplayName:   workload.Kind + "/" + workload.Name,
		IdentityAttributes: map[string]string{
			"k8s.cluster.id":       req.ClusterID,
			"k8s.namespace.name":   req.Namespace,
			"k8s.workload.kind":    workload.Kind,
			"k8s.workload.name":    workload.Name,
			"k8s.service.account":  strings.Join(workload.ServiceAccounts, ","),
			"k8s.workload.service": service.Name,
		},
		MatchRules: map[string]string{
			"k8s.namespace.name": req.Namespace,
			"k8s.workload.name":  workload.Name,
		},
		Source:     "discovered",
		SyncStatus: "synced",
	})
	if err != nil {
		return "", err
	}
	return target.ID, nil
}

func (s Service) PreviewRoute(ctx context.Context, req UpsertRouteRequest) (LogRoutePreview, error) {
	route, source, endpoint, service, err := s.routeDraft(ctx, req, false)
	if err != nil {
		return LogRoutePreview{}, err
	}
	effectiveSource := routeEffectiveSource(route, source)
	if err := s.ensureK8sObservabilityRuntimeReady(ctx, effectiveSource); err != nil {
		return LogRoutePreview{}, err
	}
	rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
		ServiceName:   service.Name,
		EnvironmentID: service.EnvironmentID,
		Source:        effectiveSource,
		Endpoint:      endpoint,
		Route:         route,
		Deployment:    s.deployment,
	})
	if err != nil {
		return LogRoutePreview{}, err
	}
	now := time.Now().UTC()
	if err := s.persistRenderedArtifacts(ctx, effectiveSource, rendered, now); err != nil {
		return LogRoutePreview{}, err
	}
	warnings := previewWarnings(effectiveSource, endpoint)
	blocked, reason := s.publishBlock(ctx, effectiveSource)
	serviceConfigRef := configFileRefForRoute(rendered.ConfigFileRefs, route.ID)
	return LogRoutePreview{
		Source:                 effectiveSource,
		Endpoint:               endpoint,
		AgentYAML:              rendered.ManifestYAML,
		CollectorYAML:          rendered.CollectorYAML,
		CollectorConfigFiles:   copyConfigFiles(rendered.CollectorConfigFiles),
		ServiceConfigPath:      serviceConfigRef.Path,
		ServiceConfigMapName:   serviceConfigRef.ConfigMapName,
		ServiceConfigYAML:      rendered.CollectorConfigFiles[serviceConfigRef.Path],
		CollectorConfigHash:    rendered.CollectorConfigHash,
		DeploymentManifestHash: rendered.DeploymentManifestHash,
		Mode:                   previewMode(source),
		PublishBlocked:         blocked,
		PublishBlockedReason:   reason,
		Warnings:               warnings,
	}, nil
}

func (s Service) RouteCollectorConfig(ctx context.Context, routeID string) (LogRouteCollectorConfig, error) {
	route, source, _, _, err := s.routeParts(ctx, routeID)
	if err != nil {
		return LogRouteCollectorConfig{}, err
	}
	var config LogCollectorConfigVersion
	if err := s.configVersions.FindByHash(ctx, route.CollectorConfigHash, &config); err != nil {
		return LogRouteCollectorConfig{}, normalizeNotFound(err, "采集配置版本不存在")
	}
	var manifest LogDeploymentManifestVersion
	if source.SourceType == SourceTypeK8sStdout && source.DeploymentManifestHash != "" {
		if err := s.manifestVersions.FindByHash(ctx, source.DeploymentManifestHash, &manifest); err != nil {
			return LogRouteCollectorConfig{}, normalizeNotFound(err, "部署清单版本不存在")
		}
	}
	if config.CollectorConfigHash != route.CollectorConfigHash {
		return LogRouteCollectorConfig{}, apperr.InvalidRequest("采集配置版本 hash 不匹配")
	}
	deploymentHash := source.DeploymentManifestHash
	if manifest.DeploymentManifestHash != "" {
		deploymentHash = manifest.DeploymentManifestHash
	}
	configFiles := copyConfigFiles(config.ConfigFiles)
	serviceRef := configFileRefForRoute(config.ConfigFileRefs, route.ID)
	serviceYAML := configFiles[serviceRef.Path]
	return LogRouteCollectorConfig{
		RouteID:                route.ID,
		CollectorConfigHash:    config.CollectorConfigHash,
		DeploymentManifestHash: deploymentHash,
		SourceType:             source.SourceType,
		CollectorYAML:          config.CollectorYAML,
		CollectorConfigFiles:   configFiles,
		ServiceConfigPath:      serviceRef.Path,
		ServiceConfigMapName:   serviceRef.ConfigMapName,
		ServiceConfigYAML:      serviceYAML,
	}, nil
}

func (s Service) VMInstallation(ctx context.Context, subject platformrbac.Subject, routeID string) (VMInstallationArtifact, error) {
	route, source, _, _, err := s.routeParts(ctx, routeID)
	if err != nil {
		return VMInstallationArtifact{}, err
	}
	if source.SourceType != SourceTypeVMFile {
		return VMInstallationArtifact{}, apperr.InvalidRequest("仅 VM 日志路由提供手工安装材料")
	}
	config, err := s.RouteCollectorConfig(ctx, routeID)
	if err != nil {
		return VMInstallationArtifact{}, err
	}
	encodedConfig := base64.StdEncoding.EncodeToString([]byte(config.CollectorYAML))
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
CONFIG_DIR=/etc/otelcol-contrib
CONFIG_FILE=${CONFIG_DIR}/novaapm-logs.yaml
if ! command -v otelcol-contrib >/dev/null 2>&1; then
  echo "请先安装平台批准版本的 otelcol-contrib" >&2
  exit 1
fi
install -d -m 0750 "$CONFIG_DIR"
echo '%s' | base64 --decode >"$CONFIG_FILE"
chmod 0640 "$CONFIG_FILE"
cat >/etc/systemd/system/novaapm-log-collector.service <<'UNIT'
[Unit]
Description=NovaAPM VM Log Collector
After=network-online.target
[Service]
ExecStart=/usr/bin/env otelcol-contrib --config=/etc/otelcol-contrib/novaapm-logs.yaml
Restart=always
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable novaapm-log-collector.service
if ! systemctl restart novaapm-log-collector.service; then
  echo "启动失败，请执行 systemctl status novaapm-log-collector.service 查看详情" >&2
  systemctl status novaapm-log-collector.service --no-pager || true
  exit 1
fi
`, encodedConfig)
	artifact := VMInstallationArtifact{
		RouteID: route.ID, ServiceID: route.ServiceID, CollectorYAML: config.CollectorYAML,
		CollectorConfigHash: config.CollectorConfigHash, InstallScript: script,
		HealthAddressExample: "<vm-address>:13133",
		Prerequisites:        []string{"已预装平台批准版本的 otelcol-contrib", "root 或 sudo 权限", "可访问日志文件与日志下游", "systemd、base64"},
	}
	if err := s.recordVMAudit(ctx, subject, "export_installation", "log_route", route.ID, route.ServiceID, map[string]any{"collector_config_hash": artifact.CollectorConfigHash}); err != nil {
		return VMInstallationArtifact{}, err
	}
	return artifact, nil
}

func (s Service) ListVMEndpoints(ctx context.Context, routeID string) ([]VMLogAgentEndpoint, error) {
	if _, err := s.requireVMRoute(ctx, routeID); err != nil {
		return nil, err
	}
	if s.vmEndpoints == nil {
		return []VMLogAgentEndpoint{}, nil
	}
	var items []VMLogAgentEndpoint
	if err := s.vmEndpoints.FindByRoute(ctx, strings.TrimSpace(routeID), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s Service) CreateVMEndpoint(ctx context.Context, subject platformrbac.Subject, routeID string, req UpsertVMEndpointRequest) (VMLogAgentEndpoint, error) {
	route, err := s.requireVMRoute(ctx, routeID)
	if err != nil {
		return VMLogAgentEndpoint{}, err
	}
	if s.vmEndpoints == nil {
		return VMLogAgentEndpoint{}, apperr.InvalidRequest("VM 节点存储不可用")
	}
	req.Name, req.Address = strings.TrimSpace(req.Name), strings.TrimSpace(req.Address)
	if req.Name == "" {
		return VMLogAgentEndpoint{}, apperr.InvalidRequest("节点名称不能为空")
	}
	if err := validateVMEndpointAddress(req.Address); err != nil {
		return VMLogAgentEndpoint{}, err
	}
	now := time.Now().UTC()
	actor := actorRefFromSubject(subject)
	item := VMLogAgentEndpoint{ID: primitive.NewObjectID().Hex(), RouteID: route.ID, ServiceID: route.ServiceID, Name: req.Name, Address: req.Address, Status: VMEndpointStatusPendingProbe, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	auditSummary := map[string]any{"route_id": item.RouteID, "name": item.Name, "address": item.Address}
	if err := s.recordVMAuditPhase(ctx, subject, "create", "vm_log_agent_endpoint", item.ID, item.ServiceID, auditSummary, "processing", "attempt"); err != nil {
		return VMLogAgentEndpoint{}, err
	}
	if err := s.vmEndpoints.Insert(ctx, item); err != nil {
		if errors.Is(err, database.ErrConflict) || mongo.IsDuplicateKeyError(err) {
			return VMLogAgentEndpoint{}, apperr.Conflict("该路由下相同节点地址已存在")
		}
		return VMLogAgentEndpoint{}, err
	}
	if err := s.recordVMAuditPhase(ctx, subject, "create", "vm_log_agent_endpoint", item.ID, item.ServiceID, auditSummary, "success", "success"); err != nil {
		if compensationErr := s.vmEndpoints.Delete(ctx, item.ID); compensationErr != nil {
			return VMLogAgentEndpoint{}, compensationError(err, compensationErr)
		}
		return VMLogAgentEndpoint{}, err
	}
	return item, nil
}

func (s Service) DeleteVMEndpoint(ctx context.Context, subject platformrbac.Subject, routeID, endpointID string) error {
	item, err := s.vmEndpointForRoute(ctx, routeID, endpointID)
	if err != nil {
		return err
	}
	auditSummary := map[string]any{"route_id": item.RouteID, "name": item.Name, "address": item.Address}
	if err := s.recordVMAuditPhase(ctx, subject, "delete", "vm_log_agent_endpoint", item.ID, item.ServiceID, auditSummary, "processing", "attempt"); err != nil {
		return err
	}
	if err := s.vmEndpoints.Delete(ctx, strings.TrimSpace(endpointID)); err != nil {
		return err
	}
	if err := s.recordVMAuditPhase(ctx, subject, "delete", "vm_log_agent_endpoint", item.ID, item.ServiceID, auditSummary, "success", "success"); err != nil {
		if compensationErr := s.vmEndpoints.Insert(ctx, item); compensationErr != nil {
			return compensationError(err, compensationErr)
		}
		return err
	}
	return nil
}

func (s Service) ProbeVMEndpoint(ctx context.Context, subject platformrbac.Subject, routeID, endpointID string) (VMLogAgentEndpoint, error) {
	item, err := s.vmEndpointForRoute(ctx, routeID, endpointID)
	if err != nil {
		return VMLogAgentEndpoint{}, err
	}
	original := item
	if err := s.recordVMAuditPhase(ctx, subject, "probe", "vm_log_agent_endpoint", item.ID, item.ServiceID, map[string]any{"route_id": item.RouteID, "address": item.Address}, "processing", "attempt"); err != nil {
		return VMLogAgentEndpoint{}, err
	}
	checker := s.vmEndpointChecker
	if checker == nil {
		checker = tcpVMEndpointChecker{}
	}
	latency, probeErr := checker.Check(ctx, item.Address)
	now := time.Now().UTC()
	item.LastProbeAt, item.UpdatedAt, item.UpdatedBy = &now, now, actorRefFromSubject(subject)
	item.LastProbeLatencyMS = latency.Milliseconds()
	if probeErr != nil {
		item.Status, item.LastProbeStatus, item.LastProbeMessage = VMEndpointStatusUnreachable, VMEndpointStatusUnreachable, normalizedVMProbeMessage(probeErr)
	} else {
		item.Status, item.LastProbeStatus, item.LastProbeMessage = VMEndpointStatusReachable, VMEndpointStatusReachable, "TCP 地址可达"
	}
	if err := s.vmEndpoints.Update(ctx, item.ID, item); err != nil {
		return VMLogAgentEndpoint{}, err
	}
	if err := s.recordVMAuditPhase(ctx, subject, "probe", "vm_log_agent_endpoint", item.ID, item.ServiceID, map[string]any{"route_id": item.RouteID, "status": item.Status, "last_probe_latency_ms": item.LastProbeLatencyMS}, "success", "success"); err != nil {
		if compensationErr := s.vmEndpoints.Update(ctx, original.ID, original); compensationErr != nil {
			return VMLogAgentEndpoint{}, compensationError(err, compensationErr)
		}
		return VMLogAgentEndpoint{}, err
	}
	return item, nil
}

func compensationError(operationErr, compensationErr error) error {
	return fmt.Errorf("%w；补偿操作失败: %v", operationErr, compensationErr)
}

func (s Service) recordVMAudit(ctx context.Context, subject platformrbac.Subject, action, resourceType, resourceID, serviceID string, summary map[string]any) error {
	return s.recordVMAuditPhase(ctx, subject, action, resourceType, resourceID, serviceID, summary, "success", "")
}

func (s Service) recordVMAuditPhase(ctx context.Context, subject platformrbac.Subject, action, resourceType, resourceID, serviceID string, summary map[string]any, result, phase string) error {
	if s.endpointAuditor == nil {
		return nil
	}
	auditSummary := make(map[string]any, len(summary)+1)
	for key, value := range summary {
		auditSummary[key] = value
	}
	if phase != "" {
		auditSummary["phase"] = phase
	}
	_, err := s.endpointAuditor.Record(ctx, platformaudit.Event{
		Actor: platformaudit.Actor{ID: subject.ID, Name: subject.DisplayName}, Resource: platformaudit.Resource{Type: resourceType, Name: resourceID},
		Action: action, Scope: serviceID, RequestSummary: auditSummary, Result: result,
	})
	return err
}

func normalizedVMProbeMessage(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
		return "节点 TCP 探测超时"
	case strings.Contains(message, "refused"):
		return "节点拒绝 TCP 连接"
	case strings.Contains(message, "解析节点地址失败"), strings.Contains(message, "no such host"):
		return "节点地址解析失败"
	case strings.Contains(message, "特殊地址"), strings.Contains(message, "blocked"):
		return "节点地址被安全策略阻断"
	default:
		return "节点 TCP 地址不可达"
	}
}

func (s Service) requireVMRoute(ctx context.Context, routeID string) (LogRoute, error) {
	route, source, _, _, err := s.routeParts(ctx, strings.TrimSpace(routeID))
	if err != nil {
		return LogRoute{}, err
	}
	if source.SourceType != SourceTypeVMFile {
		return LogRoute{}, apperr.InvalidRequest("该日志路由不是 VM 类型")
	}
	return route, nil
}

func (s Service) vmEndpointForRoute(ctx context.Context, routeID, endpointID string) (VMLogAgentEndpoint, error) {
	if _, err := s.requireVMRoute(ctx, routeID); err != nil {
		return VMLogAgentEndpoint{}, err
	}
	if s.vmEndpoints == nil {
		return VMLogAgentEndpoint{}, apperr.NotFound("VM 节点不存在")
	}
	var item VMLogAgentEndpoint
	if err := s.vmEndpoints.FindByID(ctx, strings.TrimSpace(endpointID), &item); err != nil || item.RouteID != strings.TrimSpace(routeID) {
		return VMLogAgentEndpoint{}, apperr.NotFound("VM 节点不存在")
	}
	return item, nil
}

func validateVMEndpointAddress(address string) error {
	address = strings.TrimSpace(address)
	if address == "" || strings.Contains(address, "://") {
		return apperr.InvalidRequest("节点地址必须为 host:port")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" {
		return apperr.InvalidRequest("节点地址必须为 host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber != 13133 {
		return apperr.InvalidRequest("节点端口固定为 health_check 端口 13133")
	}
	if strings.EqualFold(host, "localhost") {
		return apperr.InvalidRequest("节点地址不允许使用本机或特殊地址")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	return validateVMEndpointIP(ip)
}

func validateVMEndpointIP(ip net.IP) error {
	metadataIPs := []net.IP{net.ParseIP("169.254.169.254"), net.ParseIP("100.100.100.200"), net.ParseIP("fd00:ec2::254")}
	blockedMetadata := false
	for _, metadataIP := range metadataIPs {
		blockedMetadata = blockedMetadata || ip.Equal(metadataIP)
	}
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || blockedMetadata {
		return apperr.InvalidRequest("节点地址不允许使用本机或特殊地址")
	}
	return nil
}

func (s Service) persistRenderedArtifacts(ctx context.Context, source LogSource, rendered renderedRouteConfig, now time.Time) error {
	if strings.TrimSpace(rendered.CollectorConfigHash) == "" || strings.TrimSpace(rendered.CollectorYAML) == "" {
		return nil
	}
	configVersion := LogCollectorConfigVersion{
		ID:                  rendered.CollectorConfigHash,
		CollectorConfigHash: rendered.CollectorConfigHash,
		SourceType:          source.SourceType,
		ClusterID:           source.ClusterID,
		AgentNamespace:      source.AgentNamespace,
		CollectorYAML:       rendered.CollectorYAML,
		ConfigFiles:         copyConfigFiles(rendered.CollectorConfigFiles),
		ConfigFileRefs:      copyConfigFileRefs(rendered.ConfigFileRefs),
		RouteIDs:            copyStringSlice(rendered.RouteIDs),
		CreatedAt:           now,
	}
	if err := s.configVersions.Upsert(ctx, rendered.CollectorConfigHash, configVersion); err != nil {
		return err
	}
	if source.SourceType != SourceTypeK8sStdout || strings.TrimSpace(rendered.DeploymentManifestHash) == "" {
		return nil
	}
	manifestVersion := LogDeploymentManifestVersion{
		ID:                     rendered.DeploymentManifestHash,
		DeploymentManifestHash: rendered.DeploymentManifestHash,
		SourceType:             source.SourceType,
		ClusterID:              source.ClusterID,
		AgentNamespace:         source.AgentNamespace,
		ManifestYAML:           rendered.DeploymentManifestYAML,
		CreatedAt:              now,
	}
	return s.manifestVersions.Upsert(ctx, rendered.DeploymentManifestHash, manifestVersion)
}

func (s Service) CreateRoute(ctx context.Context, req UpsertRouteRequest) (LogRouteView, error) {
	route, source, endpoint, service, err := s.routeDraft(ctx, req, true)
	if err != nil {
		return LogRouteView{}, err
	}
	effectiveSource := routeEffectiveSource(route, source)
	if err := s.ensureK8sObservabilityRuntimeReady(ctx, effectiveSource); err != nil {
		return LogRouteView{}, err
	}
	rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
		ServiceName:   service.Name,
		EnvironmentID: service.EnvironmentID,
		Source:        effectiveSource,
		Endpoint:      endpoint,
		Route:         route,
		Deployment:    s.deployment,
	})
	if err != nil {
		return LogRouteView{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	if source.SourceType == SourceTypeVMFile {
		route.Status = "awaiting_nodes"
	} else {
		route.Status = "ready"
	}
	now := time.Now().UTC()
	route.CreatedAt = now
	route.UpdatedAt = now
	source.CreatedAt = now
	if existingSource, err := s.getSource(ctx, source.ID); err == nil && !existingSource.CreatedAt.IsZero() {
		source.CreatedAt = existingSource.CreatedAt
	}
	applyRenderedSourceHashes(&source, rendered)
	source.UpdatedAt = now
	if err := s.persistRenderedArtifacts(ctx, source, rendered, now); err != nil {
		return LogRouteView{}, err
	}
	if err := s.sources.Upsert(ctx, source.ID, source); err != nil {
		return LogRouteView{}, err
	}
	if err := s.routes.Upsert(ctx, route.ID, route); err != nil {
		return LogRouteView{}, err
	}
	if err := s.markK8sBundlePending(ctx, source, rendered.CollectorConfigHash, route.ID, now); err != nil {
		return LogRouteView{}, err
	}
	effectiveSource = routeEffectiveSource(route, source)
	return LogRouteView{Route: route, Source: &effectiveSource, Endpoint: &endpoint}, nil
}

func (s Service) UpdateRoute(ctx context.Context, routeID string, req UpsertRouteRequest) (LogRouteView, error) {
	existing, err := s.getRoute(ctx, routeID)
	if err != nil {
		return LogRouteView{}, err
	}
	existingSource, err := s.getSource(ctx, existing.SourceID)
	if err != nil {
		return LogRouteView{}, err
	}
	req.RouteID = existing.ID
	route, source, endpoint, service, err := s.routeDraft(ctx, req, true)
	if err != nil {
		return LogRouteView{}, err
	}
	route.ID = existing.ID
	route.CreatedAt = existing.CreatedAt
	if source.SourceType == SourceTypeK8sStdout {
		route.SourceID = source.ID
	} else {
		route.SourceID = existing.SourceID
		source.ID = existing.SourceID
	}
	now := time.Now().UTC()
	if source.SourceType == SourceTypeK8sStdout {
		source.CreatedAt = now
		if persistedSource, err := s.getSource(ctx, source.ID); err == nil && !persistedSource.CreatedAt.IsZero() {
			source.CreatedAt = persistedSource.CreatedAt
		}
	} else {
		source.CreatedAt = existingSource.CreatedAt
	}
	effectiveSource := routeEffectiveSource(route, source)
	if err := s.ensureK8sObservabilityRuntimeReady(ctx, effectiveSource); err != nil {
		return LogRouteView{}, err
	}
	rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
		ServiceName:   service.Name,
		EnvironmentID: service.EnvironmentID,
		Source:        effectiveSource,
		Endpoint:      endpoint,
		Route:         route,
		Deployment:    s.deployment,
	})
	if err != nil {
		return LogRouteView{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	if source.SourceType == SourceTypeVMFile {
		route.Status = "awaiting_nodes"
	} else {
		route.Status = "ready"
	}
	route.UpdatedAt = now
	applyRenderedSourceHashes(&source, rendered)
	source.UpdatedAt = now
	if err := s.persistRenderedArtifacts(ctx, source, rendered, now); err != nil {
		return LogRouteView{}, err
	}
	if existing.CollectorConfigHash == rendered.CollectorConfigHash {
		route.LastProbeStatus = existing.LastProbeStatus
		route.LastProbeMessage = existing.LastProbeMessage
		route.LastProbeAt = existing.LastProbeAt
		if source.SourceType == SourceTypeK8sStdout {
			route.LastPublishStatus = existing.LastPublishStatus
			route.LastPublishMessage = existing.LastPublishMessage
			route.LastPublishedAt = existing.LastPublishedAt
			route.LastAuditID = existing.LastAuditID
			route.LastPreviewID = existing.LastPreviewID
		}
	} else if source.SourceType == SourceTypeK8sStdout {
		route.LastPublishStatus = "pending_publish"
		route.LastPublishMessage = "配置已更新，等待发布"
	}
	if err := s.sources.Upsert(ctx, source.ID, source); err != nil {
		return LogRouteView{}, err
	}
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return LogRouteView{}, err
	}
	if err := s.markK8sBundlePending(ctx, source, rendered.CollectorConfigHash, route.ID, now); err != nil {
		return LogRouteView{}, err
	}
	effectiveSource = routeEffectiveSource(route, source)
	return LogRouteView{Route: route, Source: &effectiveSource, Endpoint: &endpoint}, nil
}

func (s Service) ProbeRoute(ctx context.Context, routeID string) (ProbeResult, error) {
	route, source, endpoint, _, err := s.routeParts(ctx, routeID)
	if err != nil {
		return ProbeResult{}, err
	}
	if source.SourceType == SourceTypeVMFile {
		return ProbeResult{}, apperr.InvalidRequest("VM 路由请逐节点校验 VM 地址")
	}
	warnings := previewWarnings(source, endpoint)
	status := "ready"
	message := "路由配置完整，端点 URL 格式有效"
	if len(warnings) > 0 {
		status = "warning"
		message = strings.Join(warnings, "; ")
	}
	now := time.Now().UTC()
	route.LastProbeStatus = status
	route.LastProbeMessage = message
	route.LastProbeAt = &now
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{
		RouteID:   route.ID,
		Status:    status,
		Message:   message,
		CheckedAt: now.Format(time.RFC3339),
		Warnings:  warnings,
	}, nil
}

func (s Service) DeleteRoute(ctx context.Context, routeID string) error {
	routeID = strings.TrimSpace(routeID)
	if routeID == "" {
		return apperr.InvalidRequest("路由 ID 不能为空")
	}
	var route LogRoute
	if err := s.routes.FindByID(ctx, routeID, &route); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return apperr.NotFound("日志路由不存在")
		}
		return err
	}
	if s.vmEndpoints != nil {
		if err := s.vmEndpoints.DeleteByRoute(ctx, routeID); err != nil {
			return err
		}
	}
	return s.routes.Delete(ctx, routeID)
}

func (s Service) PublishRoute(ctx context.Context, subject platformrbac.Subject, routeID string, req PublishRouteRequest) (PublishRouteResult, error) {
	_, source, _, _, err := s.routeParts(ctx, routeID)
	if err != nil {
		return PublishRouteResult{}, err
	}
	if source.SourceType == SourceTypeVMFile {
		return PublishRouteResult{}, apperr.InvalidRequest("VM 日志路由没有平台发布语义，请获取安装材料后由运维在节点手工部署")
	}
	return PublishRouteResult{}, apperr.InvalidRequest("K8s 日志路由不再负责发布采集器，请在集群可观测性中发布 logs_collector 运行时")
}

func (s Service) ServiceRouteSummary(ctx context.Context, serviceID string) ([]LogRouteView, error) {
	views, err := s.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LogRouteView, 0, len(views))
	for _, view := range views {
		if view.Route.ServiceID == serviceID {
			out = append(out, view)
		}
	}
	return out, nil
}

func (s Service) PublishK8sCollectorRuntime(ctx context.Context, subject platformrbac.Subject, req K8sCollectorRuntimePublishRequest) (K8sCollectorRuntimePublishResult, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	agentNamespace := normalizeK8sCollectorNamespace(req.Namespace)
	taskType := strings.TrimSpace(req.TaskType)
	if clusterID == "" {
		return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest("cluster_id 不能为空")
	}
	if taskType != "base" && taskType != "incremental" {
		return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest("task_type 必须为 base 或 incremental")
	}
	if s.k8sDeployments == nil {
		return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest("K8s 部署服务不可用")
	}
	if s.runtimes == nil {
		return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest("观测运行时存储不可用")
	}
	if err := s.ensureRuntimeClusterWritable(ctx, clusterID); err != nil {
		return K8sCollectorRuntimePublishResult{}, err
	}
	rendered, err := s.renderK8sCollectorRuntimeBundle(ctx, clusterID, agentNamespace)
	if err != nil {
		return K8sCollectorRuntimePublishResult{}, err
	}
	if err := validateGeneratedK8sCollectorConfig(rendered.CollectorYAML); err != nil {
		return K8sCollectorRuntimePublishResult{}, err
	}
	runtimeStatus, err := s.CheckK8sCollectorRuntimeStatus(ctx, K8sCollectorRuntimeStatusRequest{
		ClusterID: clusterID,
		Namespace: agentNamespace,
	})
	if err != nil {
		return K8sCollectorRuntimePublishResult{}, err
	}
	manifestYAML := rendered.ManifestYAML
	changedConfigMaps := []string{}
	switch taskType {
	case "base":
		if runtimeStatus.Ready {
			return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest("logs_collector 基础组件已就绪，请使用增量发布")
		}
		if runtimeStatus.Status != "missing_resources" {
			return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest(runtimeStatus.Message)
		}
	case "incremental":
		if !runtimeStatus.Ready {
			return K8sCollectorRuntimePublishResult{}, apperr.InvalidRequest(runtimeStatus.Message)
		}
		changed := s.changedK8sRuntimeConfigMapNames(ctx, rendered, runtimeStatus.Runtime)
		if len(req.RouteIDs) > 0 {
			changed = filterConfigMapsByRouteIDs(changed, rendered.ConfigFileRefs, req.RouteIDs)
		}
		changedConfigMaps = sortedStringSet(changed)
		manifestYAML = renderK8sRuntimeServiceConfigManifest(rendered.ManifestYAML, changed)
	}
	now := time.Now().UTC()
	source := LogSource{SourceType: SourceTypeK8sStdout, ClusterID: clusterID, AgentNamespace: agentNamespace}
	if err := s.persistRenderedArtifacts(ctx, source, rendered, now); err != nil {
		return K8sCollectorRuntimePublishResult{}, err
	}
	runtime := s.logsCollectorRuntime(ctx, clusterID, agentNamespace, rendered, now)
	runtimeBaseline := runtimeStatus.Runtime
	operation := k8sopsdeployment.OperationRequest{
		ClusterID:      clusterID,
		YAMLContent:    manifestYAML,
		ForceConflicts: true,
	}
	var deployed k8sopsdeployment.OperationResult
	requiresConfirmation := true
	if strings.TrimSpace(req.PreviewID) == "" || strings.TrimSpace(req.ConfirmationToken) == "" {
		deployed, err = s.k8sDeployments.Preview(ctx, subject, operation)
		if err != nil {
			return K8sCollectorRuntimePublishResult{}, err
		}
		runtime.Status = obsruntime.StatusPreviewed
		runtime.LastPreviewID = deployed.PreviewID
		runtime.LastAuditID = deployed.AuditID
		if runtimeBaseline != nil && runtimeBaseline.LastPublishedAt != nil && strings.TrimSpace(runtimeBaseline.CollectorConfigHash) != "" {
			runtime.CollectorConfigHash = runtimeBaseline.CollectorConfigHash
			runtime.ManifestHash = runtimeBaseline.ManifestHash
			runtime.LastPublishedAt = runtimeBaseline.LastPublishedAt
		}
		runtime.Resources = runtimeResourceRefs(deployed.Resources)
		if err := s.runtimes.Upsert(ctx, runtime.ID, runtime); err != nil {
			return K8sCollectorRuntimePublishResult{}, err
		}
		if err := s.syncK8sBundlePublishState(ctx, source, rendered.CollectorConfigHash, "previewed", deployed.Message, deployed.PreviewID, deployed.AuditID, nil, now); err != nil {
			return K8sCollectorRuntimePublishResult{}, err
		}
	} else {
		operation.PreviewID = strings.TrimSpace(req.PreviewID)
		operation.ConfirmationToken = strings.TrimSpace(req.ConfirmationToken)
		deployed, err = s.k8sDeployments.Apply(ctx, subject, operation)
		if err != nil {
			return K8sCollectorRuntimePublishResult{}, err
		}
		requiresConfirmation = false
		runtime.Status = obsruntime.StatusReady
		runtime.LastPreviewID = operation.PreviewID
		runtime.LastAuditID = deployed.AuditID
		runtime.LastPublishedAt = &now
		runtime.Resources = runtimeResourceRefs(deployed.Resources)
		if err := s.runtimes.Upsert(ctx, runtime.ID, runtime); err != nil {
			return K8sCollectorRuntimePublishResult{}, err
		}
		if err := s.syncK8sBundlePublishState(ctx, source, rendered.CollectorConfigHash, deployed.Status, deployed.Message, operation.PreviewID, deployed.AuditID, &now, now); err != nil {
			return K8sCollectorRuntimePublishResult{}, err
		}
	}
	return K8sCollectorRuntimePublishResult{
		Runtime:              runtime,
		TaskType:             taskType,
		ManifestYAML:         manifestYAML,
		CollectorYAML:        rendered.CollectorYAML,
		CollectorConfigFiles: copyConfigFiles(rendered.CollectorConfigFiles),
		CollectorConfigHash:  rendered.CollectorConfigHash,
		ManifestHash:         rendered.DeploymentManifestHash,
		ChangedConfigMaps:    changedConfigMaps,
		Status:               deployed.Status,
		Message:              deployed.Message,
		RequiresConfirmation: requiresConfirmation,
		PreviewID:            deployed.PreviewID,
		ConfirmationToken:    deployed.ConfirmationToken,
		AuditID:              deployed.AuditID,
		Resources:            deployed.Resources,
		Diffs:                deployed.Diffs,
		Warnings:             deployed.Warnings,
	}, nil
}

func (s Service) ensureRuntimeClusterWritable(ctx context.Context, clusterID string) error {
	if s.k8sClusters == nil {
		return nil
	}
	cluster, err := s.k8sClusters.Get(ctx, clusterID)
	if err != nil {
		return err
	}
	if cluster.ReadOnly {
		return k8sopscluster.ErrClusterReadOnly
	}
	return nil
}

func (s Service) logsCollectorRuntime(ctx context.Context, clusterID string, agentNamespace string, rendered renderedRouteConfig, now time.Time) obsruntime.Runtime {
	id := logsCollectorRuntimeID(clusterID, agentNamespace)
	runtime := obsruntime.Runtime{ID: id, CreatedAt: now}
	var existing obsruntime.Runtime
	if err := s.runtimes.FindByID(ctx, id, &existing); err == nil {
		runtime = existing
	}
	runtime.ID = id
	runtime.Kind = obsruntime.KindLogsCollector
	runtime.SignalType = obsruntime.SignalLogs
	runtime.ClusterID = clusterID
	runtime.Namespace = agentNamespace
	runtime.CollectorConfigHash = rendered.CollectorConfigHash
	runtime.ManifestHash = rendered.DeploymentManifestHash
	runtime.LastError = ""
	runtime.UpdatedAt = now
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	return runtime
}

func (s Service) CheckK8sCollectorRuntimeStatus(ctx context.Context, req K8sCollectorRuntimeStatusRequest) (K8sCollectorRuntimeStatus, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	agentNamespace := normalizeK8sCollectorNamespace(req.Namespace)
	if clusterID == "" {
		return K8sCollectorRuntimeStatus{}, apperr.InvalidRequest("cluster_id 不能为空")
	}
	status := K8sCollectorRuntimeStatus{
		ClusterID: clusterID,
		Namespace: agentNamespace,
		Ready:     false,
		Status:    "checking",
		Resources: logsCollectorRuntimeRequiredResources(clusterID, agentNamespace),
		Message:   "正在确认 logs_collector 集群资源状态",
		Runtime:   s.findLogsCollectorRuntime(ctx, clusterID, agentNamespace),
	}
	if s.k8sResources == nil {
		status.Status = "unavailable"
		status.Message = "无法读取 logs_collector 集群资源状态，请检查 K8s 资源读取服务"
		return status, nil
	}
	resources, err := s.checkLogsCollectorRuntimeResources(ctx, clusterID, agentNamespace)
	status.Resources = resources
	if err != nil {
		status.Status = "unavailable"
		status.Message = "无法读取 logs_collector 集群资源状态，请检查集群凭据和 K8s 资源读取权限"
		return status, nil
	}
	status.MissingResources = missingRuntimeResources(resources)
	if len(status.MissingResources) > 0 {
		status.Status = "missing_resources"
		status.Message = fmt.Sprintf("logs_collector 基础组件缺失：%s。请到 K8s / 观测接入重新部署。", formatRuntimeResources(status.MissingResources))
		return status, nil
	}
	status.Ready = true
	status.Status = "ready"
	status.Message = "集群 logs_collector 基础组件已就绪，可发布服务采集配置"
	return status, nil
}

func (s Service) findLogsCollectorRuntime(ctx context.Context, clusterID string, agentNamespace string) *obsruntime.Runtime {
	if s.runtimes == nil {
		return nil
	}
	var runtime obsruntime.Runtime
	if err := s.runtimes.FindByID(ctx, logsCollectorRuntimeID(clusterID, agentNamespace), &runtime); err != nil {
		return nil
	}
	if runtime.Kind != obsruntime.KindLogsCollector {
		return nil
	}
	return &runtime
}

func logsCollectorRuntimeRequiredResources(clusterID string, agentNamespace string) []K8sCollectorRuntimeResourceStatus {
	namespace := normalizeK8sCollectorNamespace(agentNamespace)
	return []K8sCollectorRuntimeResourceStatus{
		{ClusterID: clusterID, APIVersion: "v1", Kind: "Namespace", Name: namespace, Required: true},
		{ClusterID: clusterID, APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: k8sLogsAgentName, Required: true},
		{ClusterID: clusterID, APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: k8sLogsAgentName, Required: true},
		{ClusterID: clusterID, APIVersion: "v1", Kind: "ServiceAccount", Namespace: namespace, Name: k8sLogsAgentName, Required: true},
		{ClusterID: clusterID, APIVersion: "v1", Kind: "ConfigMap", Namespace: namespace, Name: k8sBaseConfigMapName(k8sLogsAgentName), Required: true},
		{ClusterID: clusterID, APIVersion: "v1", Kind: "Service", Namespace: namespace, Name: k8sLogsAgentName, Required: true},
		{ClusterID: clusterID, APIVersion: "apps/v1", Kind: "DaemonSet", Namespace: namespace, Name: k8sLogsAgentName, Required: true},
	}
}

func (s Service) checkLogsCollectorRuntimeResources(ctx context.Context, clusterID string, agentNamespace string) ([]K8sCollectorRuntimeResourceStatus, error) {
	resources := logsCollectorRuntimeRequiredResources(clusterID, agentNamespace)
	for i := range resources {
		exists, err := s.logsCollectorRuntimeResourceExists(ctx, resources[i], agentNamespace)
		if err != nil {
			return resources, err
		}
		resources[i].Exists = exists
	}
	return resources, nil
}

func (s Service) logsCollectorRuntimeResourceExists(ctx context.Context, expected K8sCollectorRuntimeResourceStatus, readNamespace string) (bool, error) {
	items, err := s.k8sResources.List(ctx, k8sopsresource.ListFilter{
		ClusterID:  expected.ClusterID,
		Namespace:  normalizeK8sCollectorNamespace(firstNonEmpty(readNamespace, expected.Namespace)),
		APIVersion: expected.APIVersion,
		Kind:       expected.Kind,
		Query:      expected.Name,
		PageSize:   200,
	})
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if runtimeResourceMatches(item.Identity, expected) {
			return true, nil
		}
	}
	return false, nil
}

func runtimeResourceMatches(identity k8sopsresource.Identity, expected K8sCollectorRuntimeResourceStatus) bool {
	if identity.ClusterID != expected.ClusterID || identity.APIVersion != expected.APIVersion || identity.Kind != expected.Kind || identity.Name != expected.Name {
		return false
	}
	if expected.Namespace != "" && identity.Namespace != expected.Namespace {
		return false
	}
	return true
}

func missingRuntimeResources(resources []K8sCollectorRuntimeResourceStatus) []K8sCollectorRuntimeResourceStatus {
	missing := make([]K8sCollectorRuntimeResourceStatus, 0)
	for _, resource := range resources {
		if resource.Required && !resource.Exists {
			missing = append(missing, resource)
		}
	}
	return missing
}

func formatRuntimeResources(resources []K8sCollectorRuntimeResourceStatus) string {
	labels := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource.Namespace == "" {
			labels = append(labels, resource.Kind+"/"+resource.Name)
			continue
		}
		labels = append(labels, resource.Kind+"/"+resource.Namespace+"/"+resource.Name)
	}
	return strings.Join(labels, "、")
}

func (s Service) changedK8sRuntimeConfigMapNames(ctx context.Context, rendered renderedRouteConfig, runtime *obsruntime.Runtime) map[string]struct{} {
	allConfigMaps := configMapNamesForRefs(rendered.ConfigFileRefs)
	if runtime == nil || runtime.LastPublishedAt == nil || strings.TrimSpace(runtime.CollectorConfigHash) == "" || s.configVersions == nil {
		return allConfigMaps
	}
	var previous LogCollectorConfigVersion
	if err := s.configVersions.FindByHash(ctx, runtime.CollectorConfigHash, &previous); err != nil {
		return allConfigMaps
	}
	if len(previous.ConfigFiles) == 0 {
		return allConfigMaps
	}
	changed := map[string]struct{}{}
	for path, content := range rendered.CollectorConfigFiles {
		if previous.ConfigFiles[path] == content {
			continue
		}
		name := configMapNameForPath(rendered.ConfigFileRefs, path)
		if name != "" {
			changed[name] = struct{}{}
		}
	}
	return changed
}

func filterConfigMapsByRouteIDs(changed map[string]struct{}, refs []LogCollectorConfigFile, routeIDs []string) map[string]struct{} {
	allowed := map[string]struct{}{}
	routeSet := map[string]struct{}{}
	for _, id := range routeIDs {
		routeSet[strings.TrimSpace(id)] = struct{}{}
	}
	for _, ref := range refs {
		if ref.RouteID == "" {
			continue
		}
		if _, ok := routeSet[ref.RouteID]; !ok {
			continue
		}
		name := strings.TrimSpace(ref.ConfigMapName)
		if name == "" {
			continue
		}
		if _, ok := changed[name]; ok {
			allowed[name] = struct{}{}
		}
	}
	return allowed
}

func configMapNamesForRefs(refs []LogCollectorConfigFile) map[string]struct{} {
	names := map[string]struct{}{}
	for _, ref := range refs {
		name := strings.TrimSpace(ref.ConfigMapName)
		if name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func configMapNameForPath(refs []LogCollectorConfigFile, path string) string {
	path = strings.TrimSpace(path)
	for _, ref := range refs {
		if strings.TrimSpace(ref.Path) == path {
			return strings.TrimSpace(ref.ConfigMapName)
		}
	}
	return ""
}

func sortedStringSet(items map[string]struct{}) []string {
	out := make([]string, 0, len(items))
	for item := range items {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func (s Service) ensureK8sObservabilityRuntimeReady(ctx context.Context, source LogSource) error {
	if source.SourceType != SourceTypeK8sStdout {
		return nil
	}
	status, err := s.CheckK8sCollectorRuntimeStatus(ctx, K8sCollectorRuntimeStatusRequest{
		ClusterID: source.ClusterID,
		Namespace: source.AgentNamespace,
	})
	if err != nil {
		return err
	}
	if !status.Ready {
		return apperr.InvalidRequest(status.Message)
	}
	return nil
}

func logsCollectorRuntimeID(clusterID string, agentNamespace string) string {
	return "logs-collector:" + strings.TrimSpace(clusterID) + ":" + normalizeK8sCollectorNamespace(agentNamespace)
}

func (s Service) renderK8sCollectorRuntimeBundle(ctx context.Context, clusterID string, agentNamespace string) (renderedRouteConfig, error) {
	inputs, err := s.k8sCollectorRuntimeInputs(ctx, clusterID, agentNamespace)
	if err != nil {
		return renderedRouteConfig{}, err
	}
	processorPatch := ""
	if s.clusterConfigs != nil {
		var clusterCfg LogCollectorClusterConfig
		if err := s.clusterConfigs.FindByCluster(ctx, clusterID, agentNamespace, &clusterCfg); err == nil {
			processorPatch = clusterCfg.ProcessorPatch
		}
	}
	templateValues := platformimages.DefaultTemplateValues
	if s.imageTemplates == nil {
		templateValues = platformimages.DefaultTemplateValues
	} else {
		values, err := s.imageTemplates.TemplateValues(ctx)
		if err != nil {
			return renderedRouteConfig{}, err
		}
		templateValues = values
	}
	rendered, err := renderK8sCollectorRuntimeBundleWithTemplateValues(clusterID, agentNamespace, logsCollectorRuntimeID(clusterID, agentNamespace), inputs, processorPatch, s.deployment, templateValues)
	if err != nil {
		return renderedRouteConfig{}, apperr.InvalidRequest(err.Error())
	}
	return rendered, nil
}

func (s Service) k8sCollectorRuntimeInputs(ctx context.Context, clusterID string, agentNamespace string) ([]renderInput, error) {
	views, err := s.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	inputs := []renderInput{}
	for _, view := range views {
		if view.Source == nil || view.Endpoint == nil || view.Source.SourceType == SourceTypeVMFile {
			continue
		}
		if view.Source.ClusterID != clusterID || normalizeK8sCollectorNamespace(view.Source.AgentNamespace) != normalizeK8sCollectorNamespace(agentNamespace) {
			continue
		}
		service, err := s.services.Get(ctx, view.Route.ServiceID)
		if err != nil {
			return nil, normalizeNotFound(err, "服务不存在")
		}
		inputs = append(inputs, renderInput{
			ServiceName:   service.Name,
			EnvironmentID: service.EnvironmentID,
			Source:        *view.Source,
			Endpoint:      *view.Endpoint,
			Route:         view.Route,
			Deployment:    s.deployment,
		})
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		return inputs[i].Route.ID < inputs[j].Route.ID
	})
	return inputs, nil
}

func runtimeResourceRefs(resources []k8sopsdeployment.ResourceIdentity) []obsruntime.ResourceRef {
	out := make([]obsruntime.ResourceRef, 0, len(resources))
	for _, resource := range resources {
		out = append(out, obsruntime.ResourceRef{
			APIVersion: resource.APIVersion,
			Kind:       resource.Kind,
			Namespace:  resource.Namespace,
			Name:       resource.Name,
		})
	}
	return out
}

func (s Service) routeDraft(ctx context.Context, req UpsertRouteRequest, ensureAgentGroup bool) (LogRoute, LogSource, LogEndpoint, servicecatalog.Service, error) {
	req = normalizeRouteRequest(req)
	if err := validateRouteRequest(req); err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	service, err := s.services.Get(ctx, req.ServiceID)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, normalizeNotFound(err, "服务不存在")
	}
	if err := validateServiceSourceMatch(service, req.SourceType); err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	endpoint, err := s.endpointForRoute(ctx, req)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	endpoint = endpointForService(endpoint, service)
	if req.SourceType == SourceTypeVMFile {
		if err := validateCollectorYAML(req.VM.CollectorYAML, endpoint); err != nil {
			return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
		}
		if err := validateVMCollectorHealthYAML(req.VM.CollectorYAML); err != nil {
			return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
		}
	}
	source := sourceFromRequest(req)
	agentGroupID := ""
	if source.SourceType == SourceTypeK8sStdout {
		agentGroupID, err = s.resolveAgentGroup(ctx, req.AgentGroupID, source, service, ensureAgentGroup)
		if err != nil {
			return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
		}
	}
	route := LogRoute{
		ID:           firstNonEmpty(req.RouteID, primitive.NewObjectID().Hex()),
		Name:         firstNonEmpty(req.Name, service.DisplayName, service.Name),
		ServiceID:    service.ID,
		SourceID:     source.ID,
		SourceType:   source.SourceType,
		AgentGroupID: agentGroupID,
		EndpointID:   endpoint.ID,
		Status:       "draft",
	}
	if source.SourceType == SourceTypeK8sStdout {
		route.K8s = k8sRouteConfigFromRequest(req.K8s)
	}
	return route, source, endpoint, service, nil
}

func validateServiceSourceMatch(service servicecatalog.Service, sourceType string) error {
	switch sourceType {
	case SourceTypeVMFile:
		if service.IdentityType != "host_process" {
			return apperr.InvalidRequest("VM 日志接入只能选择 VM/物理机服务")
		}
	case SourceTypeK8sStdout:
		if service.IdentityType != "" && service.IdentityType != "k8s_workload" {
			return apperr.InvalidRequest("K8s 日志接入只能选择 K8s 服务")
		}
	}
	return nil
}

func (s Service) resolveAgentGroup(ctx context.Context, explicitID string, source LogSource, service servicecatalog.Service, ensure bool) (string, error) {
	if strings.TrimSpace(explicitID) != "" {
		if _, err := s.collectorGroups.GetGroup(ctx, explicitID); err != nil {
			return "", normalizeNotFound(err, "AgentGroup 不存在")
		}
		return explicitID, nil
	}
	group := derivedAgentGroup(source, service)
	groups, err := s.collectorGroups.ListGroups(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range groups {
		if item.Name == group.Name {
			return item.ID, nil
		}
	}
	if !ensure {
		return group.Name, nil
	}
	created, err := s.collectorGroups.CreateGroup(ctx, group)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func derivedAgentGroup(source LogSource, service servicecatalog.Service) collectormanagement.CollectorGroup {
	environment := service.EnvironmentID
	ownerTeam := service.OwnerTeam
	agentNamespace := normalizeK8sCollectorNamespace(source.AgentNamespace)
	return collectormanagement.CollectorGroup{
		Name:            "logs-k8s-" + safeSegment(source.ClusterID) + "-" + safeSegment(agentNamespace),
		DisplayName:     "K8s Logs / " + source.ClusterID + " / " + agentNamespace,
		Mode:            "dedicated_collector",
		EnvironmentID:   environment,
		Cluster:         source.ClusterID,
		Namespace:       agentNamespace,
		OwnerTeam:       ownerTeam,
		Status:          "active",
		ReceiverProfile: "filelog",
		DesiredReplicas: 1,
	}
}

func safeSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func (s Service) routeParts(ctx context.Context, routeID string) (LogRoute, LogSource, LogEndpoint, servicecatalog.Service, error) {
	route, err := s.getRoute(ctx, routeID)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	source, err := s.getSource(ctx, route.SourceID)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	endpoint, err := s.getEndpoint(ctx, route.EndpointID)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	service, err := s.services.Get(ctx, route.ServiceID)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, normalizeNotFound(err, "服务不存在")
	}
	return route, routeEffectiveSource(route, source), endpointForService(endpoint, service), service, nil
}

func (s Service) getEndpoint(ctx context.Context, id string) (LogEndpoint, error) {
	var endpoint LogEndpoint
	if err := s.endpoints.FindByID(ctx, strings.TrimSpace(id), &endpoint); err != nil {
		return LogEndpoint{}, normalizeNotFound(err, "日志下游端点不存在")
	}
	endpoint = normalizeStoredEndpoint(endpoint)
	if !endpointSupportsLogsSignal(endpoint.SignalTypes) {
		return LogEndpoint{}, apperr.InvalidRequest("日志路由只能选择支持 logs 的观测端点")
	}
	if endpoint.SinkType == EndpointSinkVL {
		if err := validateVLWriteURL(endpoint.WriteURL); err != nil {
			return LogEndpoint{}, err
		}
	}
	return endpoint, nil
}

func (s Service) endpointForRoute(ctx context.Context, req UpsertRouteRequest) (LogEndpoint, error) {
	if strings.TrimSpace(req.EndpointID) != "" {
		endpoint, err := s.getEndpoint(ctx, req.EndpointID)
		if err != nil {
			return LogEndpoint{}, err
		}
		if req.SourceType == SourceTypeVMFile {
			if endpoint.ScopeType == EndpointScopeK8sCluster {
				return LogEndpoint{}, apperr.InvalidRequest("VM 文件日志路由不能选择 K8s 集群级日志下游端点")
			}
			return endpoint, nil
		}
		switch endpoint.ScopeType {
		case EndpointScopeVM:
			return LogEndpoint{}, apperr.InvalidRequest("K8s 日志路由不能选择 VM 专用日志下游端点")
		case EndpointScopeK8sCluster:
			if endpoint.ClusterID != req.K8s.ClusterID {
				return LogEndpoint{}, apperr.InvalidRequest("K8s 日志路由只能选择当前集群绑定的日志下游端点")
			}
		}
		return endpoint, nil
	}
	if req.SourceType == SourceTypeVMFile {
		return LogEndpoint{}, apperr.InvalidRequest("VM 文件接入必须选择日志下游端点")
	}
	endpoints, err := s.ListEndpoints(ctx)
	if err != nil {
		return LogEndpoint{}, err
	}
	clusterEndpoints := []LogEndpoint{}
	for _, endpoint := range endpoints {
		if endpoint.ScopeType == EndpointScopeK8sCluster && endpoint.ClusterID == req.K8s.ClusterID {
			clusterEndpoints = append(clusterEndpoints, endpoint)
		}
	}
	if len(clusterEndpoints) == 1 {
		return clusterEndpoints[0], nil
	}
	if len(clusterEndpoints) > 1 {
		return LogEndpoint{}, apperr.InvalidRequest("当前 K8s 集群存在多个日志下游端点，请显式选择端点")
	}
	for _, endpoint := range endpoints {
		if endpoint.ScopeType == EndpointScopeGlobal {
			return endpoint, nil
		}
	}
	return LogEndpoint{}, apperr.InvalidRequest("当前 K8s 集群未绑定日志下游端点")
}

func (s Service) getSource(ctx context.Context, id string) (LogSource, error) {
	var source LogSource
	if err := s.sources.FindByID(ctx, strings.TrimSpace(id), &source); err != nil {
		return LogSource{}, normalizeNotFound(err, "日志来源不存在")
	}
	return source, nil
}

func (s Service) getRoute(ctx context.Context, id string) (LogRoute, error) {
	var route LogRoute
	if err := s.routes.FindByID(ctx, strings.TrimSpace(id), &route); err != nil {
		return LogRoute{}, normalizeNotFound(err, "日志路由不存在")
	}
	return route, nil
}

func (s Service) getTarget(ctx context.Context, id string) (LogTarget, error) {
	if s.logTargets == nil {
		return LogTarget{}, apperr.InvalidRequest("日志目标存储不可用")
	}
	var target LogTarget
	if err := s.logTargets.FindByID(ctx, strings.TrimSpace(id), &target); err != nil {
		return LogTarget{}, normalizeNotFound(err, "日志目标不存在")
	}
	return normalizeTarget(target), nil
}

func (s Service) listTargetViews(ctx context.Context, serviceID string) ([]LogTargetView, error) {
	if s.logTargets == nil {
		return []LogTargetView{}, nil
	}
	var targets []LogTarget
	var err error
	if strings.TrimSpace(serviceID) != "" {
		err = s.logTargets.FindByService(ctx, strings.TrimSpace(serviceID), &targets)
	} else {
		err = s.logTargets.FindAll(ctx, &targets)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i].UpdatedAt.After(targets[j].UpdatedAt)
	})
	views := make([]LogTargetView, 0, len(targets))
	for _, target := range targets {
		view, err := s.targetView(ctx, normalizeTarget(target))
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s Service) targetView(ctx context.Context, target LogTarget) (LogTargetView, error) {
	view := LogTargetView{Target: normalizeTarget(target)}
	var resolvedService servicecatalog.Service
	if service, err := s.services.Get(ctx, target.ServiceID); err == nil {
		resolvedService = service
		summary := serviceSummary(service)
		view.Service = &summary
	}
	if endpoint, err := s.getEndpoint(ctx, target.EndpointID); err == nil {
		endpoint = endpointForService(endpoint, resolvedService)
		if target.AccountID != "" && target.ProjectID != "" {
			endpoint.AccountID = target.AccountID
			endpoint.ProjectID = target.ProjectID
		}
		view.Endpoint = &endpoint
	}
	return view, nil
}

func (s Service) ensureUniqueExternalTarget(ctx context.Context, currentID string, serviceID string, endpointID string, baseFilter string) error {
	if s.logTargets == nil {
		return nil
	}
	var targets []LogTarget
	if err := s.logTargets.FindByService(ctx, serviceID, &targets); err != nil {
		return err
	}
	for _, target := range targets {
		target = normalizeTarget(target)
		if target.ID == currentID || target.Status == LogTargetStatusDisabled {
			continue
		}
		if target.EndpointID == endpointID && target.SourceKind == LogTargetSourceExternalVLogs && target.BaseFilter == baseFilter {
			return apperr.Conflict("相同服务、端点和过滤条件的日志目标已存在")
		}
	}
	return nil
}

func (s Service) allowedTarget(subject platformrbac.Subject, serviceID string, action string) bool {
	return s.allowed(subject, serviceID, "logs.target", action)
}

func (s Service) allowedExternalTenantOverride(subject platformrbac.Subject) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "logs.external-tenant",
		Action:   "manage",
		Scope:    platformrbac.Scope{Global: true},
	}).Allowed
}

func (s Service) AuthorizeServiceRoute(subject platformrbac.Subject, serviceID string, action string) error {
	if !s.allowed(subject, strings.TrimSpace(serviceID), "logs.route", action) {
		return ErrPermissionDenied
	}
	return nil
}

func (s Service) AuthorizeRoute(ctx context.Context, subject platformrbac.Subject, routeID string, action string) error {
	route, err := s.getRoute(ctx, routeID)
	if err != nil {
		return err
	}
	return s.AuthorizeServiceRoute(subject, route.ServiceID, action)
}

func (s Service) allowed(subject platformrbac.Subject, serviceID string, resource string, action string) bool {
	if subject.ID == "" || subject.Type == "" {
		return false
	}
	if s.authorizer == nil {
		return false
	}
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: resource,
		Action:   action,
		Scope:    platformrbac.Scope{ServiceID: serviceID},
	})
	return decision.Allowed
}

func (s Service) allowedGlobal(subject platformrbac.Subject, resource string, action string) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: resource,
		Action:   action,
		Scope:    platformrbac.Scope{Global: true},
	}).Allowed
}

func (s Service) renderRouteConfigWithHashes(ctx context.Context, input renderInput) (renderedRouteConfig, error) {
	if input.Source.SourceType == SourceTypeVMFile {
		yaml, _ := renderAgentConfig(input)
		filePath := vmConfigFilePath(input.Route.ID)
		configFiles := map[string]string{filePath: yaml}
		return renderedRouteConfig{
			ManifestYAML:         yaml,
			CollectorYAML:        yaml,
			CollectorConfigFiles: configFiles,
			ConfigFileRefs: []LogCollectorConfigFile{{
				Path:          filePath,
				ConfigMapName: "",
				Role:          "vm",
				RouteID:       input.Route.ID,
				ServiceID:     input.Route.ServiceID,
			}},
			CollectorConfigHash: hashConfigFiles(configFiles),
			RouteIDs:            []string{input.Route.ID},
		}, nil
	}
	inputs, err := s.k8sBundleInputs(ctx, input)
	if err != nil {
		return renderedRouteConfig{}, err
	}
	processorPatch := ""
	if s.clusterConfigs != nil {
		var clusterCfg LogCollectorClusterConfig
		if err := s.clusterConfigs.FindByCluster(ctx, input.Source.ClusterID, normalizeK8sCollectorNamespace(input.Source.AgentNamespace), &clusterCfg); err == nil {
			processorPatch = clusterCfg.ProcessorPatch
		}
	}
	var rendered renderedRouteConfig
	if s.imageTemplates == nil {
		rendered, err = renderK8sDaemonSetBundleWithHashes(inputs, processorPatch)
	} else {
		templateValues, valuesErr := s.imageTemplates.TemplateValues(ctx)
		if valuesErr != nil {
			return renderedRouteConfig{}, valuesErr
		}
		rendered, err = renderK8sDaemonSetBundleWithTemplateValues(inputs, processorPatch, templateValues)
	}
	if err != nil {
		return renderedRouteConfig{}, apperr.InvalidRequest(err.Error())
	}
	if err := validateGeneratedK8sCollectorConfig(rendered.CollectorYAML); err != nil {
		return renderedRouteConfig{}, err
	}
	return rendered, nil
}

func applyRenderedRouteHashes(route *LogRoute, rendered renderedRouteConfig) {
	route.CollectorConfigHash = rendered.CollectorConfigHash
}

func applyRenderedSourceHashes(source *LogSource, rendered renderedRouteConfig) {
	source.CollectorConfigHash = rendered.CollectorConfigHash
	source.DeploymentManifestHash = rendered.DeploymentManifestHash
}

func (s Service) k8sBundleInputs(ctx context.Context, current renderInput) ([]renderInput, error) {
	views, err := s.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	inputs := []renderInput{current}
	for _, view := range views {
		if view.Source == nil || view.Endpoint == nil {
			continue
		}
		if view.Route.ID == current.Route.ID || view.Source.SourceType == SourceTypeVMFile {
			continue
		}
		if view.Source.ClusterID != current.Source.ClusterID ||
			normalizeK8sCollectorNamespace(view.Source.AgentNamespace) != normalizeK8sCollectorNamespace(current.Source.AgentNamespace) {
			continue
		}
		service, err := s.services.Get(ctx, view.Route.ServiceID)
		if err != nil {
			return nil, normalizeNotFound(err, "服务不存在")
		}
		inputs = append(inputs, renderInput{
			ServiceName:   service.Name,
			EnvironmentID: service.EnvironmentID,
			Source:        *view.Source,
			Endpoint:      *view.Endpoint,
			Route:         view.Route,
			Deployment:    current.Deployment,
		})
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		return inputs[i].Route.ID < inputs[j].Route.ID
	})
	return inputs, nil
}

func (s Service) markK8sBundlePending(ctx context.Context, source LogSource, collectorConfigHash string, currentRouteID string, now time.Time) error {
	if source.SourceType != SourceTypeK8sStdout {
		return nil
	}
	return s.updateK8sCollectorDomainRoutes(ctx, source, func(route LogRoute) LogRoute {
		route.CollectorConfigHash = collectorConfigHash
		if route.ID == currentRouteID || (route.LastPublishStatus != "" && route.LastPublishStatus != "pending_publish") {
			route.LastPublishStatus = "pending_publish"
			route.LastPublishMessage = "采集域配置已更新，等待观测接入发布"
			route.LastPreviewID = ""
		}
		route.UpdatedAt = now
		return route
	})
}

func (s Service) syncK8sBundlePublishState(ctx context.Context, source LogSource, collectorConfigHash string, status string, message string, previewID string, auditID string, publishedAt *time.Time, now time.Time) error {
	if source.SourceType != SourceTypeK8sStdout {
		return nil
	}
	return s.updateK8sCollectorDomainRoutes(ctx, source, func(route LogRoute) LogRoute {
		route.CollectorConfigHash = collectorConfigHash
		route.LastPublishStatus = status
		route.LastPublishMessage = message
		if previewID != "" {
			route.LastPreviewID = previewID
		}
		if auditID != "" {
			route.LastAuditID = auditID
		}
		if publishedAt != nil {
			route.LastPublishedAt = publishedAt
		}
		route.UpdatedAt = now
		return route
	})
}

func (s Service) updateK8sCollectorDomainRoutes(ctx context.Context, source LogSource, mutate func(LogRoute) LogRoute) error {
	views, err := s.ListRoutes(ctx)
	if err != nil {
		return err
	}
	for _, view := range views {
		if view.Source == nil || !sameK8sCollectorDomain(*view.Source, source) {
			continue
		}
		if err := s.routes.Update(ctx, view.Route.ID, mutate(view.Route)); err != nil {
			return err
		}
	}
	return nil
}

func sameK8sCollectorDomain(left LogSource, right LogSource) bool {
	return left.SourceType == SourceTypeK8sStdout &&
		right.SourceType == SourceTypeK8sStdout &&
		left.ClusterID == right.ClusterID &&
		normalizeK8sCollectorNamespace(left.AgentNamespace) == normalizeK8sCollectorNamespace(right.AgentNamespace)
}

func (s Service) publishBlock(ctx context.Context, source LogSource) (bool, string) {
	if source.SourceType == SourceTypeVMFile || s.k8sClusters == nil {
		return false, ""
	}
	cluster, err := s.k8sClusters.Get(ctx, source.ClusterID)
	if err != nil {
		return false, ""
	}
	if cluster.ReadOnly {
		return true, "当前集群为只读接入，只能生成 Agent 配置预览，不能发布 DaemonSet"
	}
	return false, ""
}

func sourceFromRequest(req UpsertRouteRequest) LogSource {
	source := LogSource{ID: primitive.NewObjectID().Hex(), SourceType: req.SourceType}
	if req.SourceType == SourceTypeVMFile {
		source.HostGroup = req.VM.HostGroup
		source.HostSelector = copyStringMap(req.VM.HostSelector)
		source.PathPattern = req.VM.PathPattern
		source.ParseRules = normalizeParseRules(req.VM.ParseRules)
		source.CustomCollectorYAML = req.VM.CollectorYAML
		return source
	}
	agentNamespace := normalizeK8sCollectorNamespace(req.K8s.AgentNamespace)
	source.ID = k8sCollectorDomainSourceID(req.K8s.ClusterID, agentNamespace)
	source.ClusterID = req.K8s.ClusterID
	source.AgentNamespace = agentNamespace
	return source
}

func k8sRouteConfigFromRequest(input K8sSourceInput) *K8sRouteConfig {
	return &K8sRouteConfig{
		Namespace:             input.Namespace,
		WorkloadKind:          input.WorkloadKind,
		WorkloadName:          input.WorkloadName,
		PathPattern:           input.PathPattern,
		ParseRules:            normalizeParseRules(input.ParseRules),
		OperatorsYAML:         input.OperatorsYAML,
		CollectorFragmentYAML: input.CollectorFragmentYAML,
	}
}

func routeEffectiveSource(route LogRoute, source LogSource) LogSource {
	if source.SourceType != SourceTypeK8sStdout || route.K8s == nil {
		return source
	}
	out := source
	out.Namespace = route.K8s.Namespace
	out.WorkloadKind = route.K8s.WorkloadKind
	out.WorkloadName = route.K8s.WorkloadName
	out.PathPattern = route.K8s.PathPattern
	out.ParseRules = normalizeParseRules(route.K8s.ParseRules)
	out.OperatorsYAML = route.K8s.OperatorsYAML
	out.CollectorFragmentYAML = route.K8s.CollectorFragmentYAML
	out.CustomCollectorYAML = ""
	return out
}

func k8sCollectorDomainSourceID(clusterID string, agentNamespace string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(clusterID) + "\x00" + strings.TrimSpace(agentNamespace)))
	return "k8s-collector-domain-" + hex.EncodeToString(sum[:])[:16]
}

func vmConfigFilePath(routeID string) string {
	return "vm/" + safeSegment(firstNonEmpty(routeID, "route")) + ".yaml"
}

func normalizeRouteRequest(req UpsertRouteRequest) UpsertRouteRequest {
	req.RouteID = strings.TrimSpace(req.RouteID)
	req.Name = strings.TrimSpace(req.Name)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.SourceType = strings.TrimSpace(req.SourceType)
	req.AgentGroupID = strings.TrimSpace(req.AgentGroupID)
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	req.K8s.ClusterID = strings.TrimSpace(req.K8s.ClusterID)
	req.K8s.Namespace = strings.TrimSpace(req.K8s.Namespace)
	req.K8s.AgentNamespace = strings.TrimSpace(req.K8s.AgentNamespace)
	req.K8s.WorkloadKind = strings.TrimSpace(req.K8s.WorkloadKind)
	req.K8s.WorkloadName = strings.TrimSpace(req.K8s.WorkloadName)
	req.K8s.PathPattern = strings.TrimSpace(req.K8s.PathPattern)
	req.K8s.CollectorFragmentYAML = strings.TrimSpace(req.K8s.CollectorFragmentYAML)
	req.K8s.ParseRules = normalizeParseRules(req.K8s.ParseRules)
	req.VM.HostGroup = strings.TrimSpace(req.VM.HostGroup)
	req.VM.PathPattern = strings.TrimSpace(req.VM.PathPattern)
	req.VM.CollectorYAML = strings.TrimSpace(req.VM.CollectorYAML)
	req.VM.ParseRules = normalizeParseRules(req.VM.ParseRules)
	return req
}

func validateRouteRequest(req UpsertRouteRequest) error {
	if req.ServiceID == "" || req.SourceType == "" {
		return apperr.InvalidRequest("service_id 和 source_type 不能为空")
	}
	if !validSourceType(req.SourceType) {
		return apperr.InvalidRequest("日志来源类型只支持 k8s_stdout、vm_file")
	}
	switch req.SourceType {
	case SourceTypeK8sStdout:
		if req.K8s.ClusterID == "" || req.K8s.Namespace == "" || req.K8s.WorkloadKind == "" || req.K8s.WorkloadName == "" {
			return apperr.InvalidRequest("K8s 标准输出接入必须选择集群、namespace 和 workload")
		}
	case SourceTypeVMFile:
		if req.EndpointID == "" {
			return apperr.InvalidRequest("VM 文件接入必须选择日志下游端点")
		}
		if req.VM.PathPattern == "" {
			return apperr.InvalidRequest("VM 文件接入必须填写日志路径")
		}
	}
	if err := validateParseRules(req.K8s.ParseRules); err != nil {
		return err
	}
	if err := validateParseRules(req.VM.ParseRules); err != nil {
		return err
	}
	return nil
}

func validSourceType(value string) bool {
	return value == SourceTypeK8sStdout || value == SourceTypeVMFile
}

func normalizeCreateTargetRequest(req CreateLogTargetRequest) CreateLogTargetRequest {
	req.Name = strings.TrimSpace(req.Name)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	req.SourceKind = strings.TrimSpace(req.SourceKind)
	if req.SourceKind == "" {
		req.SourceKind = LogTargetSourceExternalVLogs
	}
	req.BaseFilter = strings.TrimSpace(req.BaseFilter)
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	return req
}

func normalizeUpdateTargetRequest(req UpdateLogTargetRequest) UpdateLogTargetRequest {
	req.Name = strings.TrimSpace(req.Name)
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	req.BaseFilter = strings.TrimSpace(req.BaseFilter)
	req.AccountID = trimOptionalString(req.AccountID)
	req.ProjectID = trimOptionalString(req.ProjectID)
	req.Status = strings.TrimSpace(req.Status)
	return req
}

func normalizeTarget(target LogTarget) LogTarget {
	target.Name = strings.TrimSpace(target.Name)
	target.ServiceID = strings.TrimSpace(target.ServiceID)
	target.EndpointID = strings.TrimSpace(target.EndpointID)
	target.SourceKind = strings.TrimSpace(target.SourceKind)
	if target.SourceKind == "" {
		target.SourceKind = LogTargetSourceExternalVLogs
	}
	target.LogRouteID = strings.TrimSpace(target.LogRouteID)
	target.BaseFilter = strings.TrimSpace(target.BaseFilter)
	target.AccountID = strings.TrimSpace(target.AccountID)
	target.ProjectID = strings.TrimSpace(target.ProjectID)
	target.Status = strings.TrimSpace(target.Status)
	if target.Status == "" {
		target.Status = LogTargetStatusPendingVerification
	}
	target.LastProbeStatus = strings.TrimSpace(target.LastProbeStatus)
	target.LastProbeMessage = strings.TrimSpace(target.LastProbeMessage)
	return target
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func validateOptionalVictoriaLogsTenant(accountID string, projectID string) error {
	if (accountID == "") != (projectID == "") {
		return apperr.InvalidRequest("VictoriaLogs AccountID 和 ProjectID 必须同时填写")
	}
	for _, item := range []struct {
		name  string
		value string
	}{{"AccountID", accountID}, {"ProjectID", projectID}} {
		if item.value == "" {
			continue
		}
		if _, err := strconv.ParseUint(item.value, 10, 32); err != nil {
			return apperr.InvalidRequest("VictoriaLogs " + item.name + " 必须是 uint32")
		}
	}
	return nil
}

func normalizeEndpoint(endpoint LogEndpoint) LogEndpoint {
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.Description = strings.TrimSpace(endpoint.Description)
	endpoint.Kind = strings.ToLower(strings.TrimSpace(endpoint.Kind))
	endpoint.SignalTypes = normalizeEndpointSignalTypes(endpoint.SignalTypes)
	endpoint.SinkType = strings.ToLower(strings.TrimSpace(endpoint.SinkType))
	endpoint.StreamName = strings.TrimSpace(endpoint.StreamName)
	endpoint.WriteURL = strings.TrimSpace(endpoint.WriteURL)
	endpoint.QueryURL = strings.TrimSpace(endpoint.QueryURL)
	endpoint.VMUIURL = strings.TrimSpace(endpoint.VMUIURL)
	endpoint.AccountID = strings.TrimSpace(endpoint.AccountID)
	endpoint.ProjectID = strings.TrimSpace(endpoint.ProjectID)
	endpoint.ScopeType = strings.TrimSpace(endpoint.ScopeType)
	endpoint.ClusterID = strings.TrimSpace(endpoint.ClusterID)
	endpoint.Status = strings.TrimSpace(endpoint.Status)
	if endpoint.ScopeType == "" {
		if endpoint.ClusterID != "" {
			endpoint.ScopeType = EndpointScopeK8sCluster
		} else {
			endpoint.ScopeType = EndpointScopeGlobal
		}
	}
	if endpoint.SinkType == "" && endpointSupportsLogsSignal(endpoint.SignalTypes) {
		endpoint.SinkType = EndpointSinkVL
	}
	return endpoint
}

func normalizeStoredEndpoint(endpoint LogEndpoint) LogEndpoint {
	endpoint = normalizeEndpoint(endpoint)
	// 租户由服务所属产品派生，日志端点只保存物理连接信息。
	endpoint.AccountID = ""
	endpoint.ProjectID = ""
	return endpoint
}

func normalizeEndpointSignalTypes(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func endpointSupportsLogsSignal(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == EndpointSignalLogs {
			return true
		}
	}
	return false
}

func validateTargetEndpoint(endpoint LogEndpoint) error {
	if !endpointSupportsLogsSignal(endpoint.SignalTypes) {
		return apperr.InvalidRequest("日志目标只能选择支持 logs 的观测端点")
	}
	if endpoint.Status == LogTargetStatusDisabled || endpoint.Status == "disabled" {
		return apperr.InvalidRequest("日志目标不能绑定已停用的日志下游端点")
	}
	if endpoint.SinkType != EndpointSinkVL || strings.TrimSpace(endpoint.QueryURL) == "" {
		return apperr.InvalidRequest("日志目标只能绑定可查询的 VictoriaLogs 端点")
	}
	return nil
}

func ValidateLogTargetBaseFilter(filter string) error {
	filter = strings.TrimSpace(filter)
	if filter == "" || len(filter) > 8*1024 {
		return apperr.InvalidRequest("日志目标过滤条件不能为空且不能超过 8 KiB")
	}
	lower := strings.ToLower(filter)
	if strings.Contains(filter, "|") || strings.Contains(lower, "_time") {
		return apperr.InvalidRequest("日志目标过滤条件仅接受过滤表达式，时间窗口和统计由 Explore 或告警统一生成")
	}
	if !balancedLogsQLFilter(filter) {
		return apperr.InvalidRequest("日志目标过滤条件括号或引号不完整")
	}
	return nil
}

func balancedLogsQLFilter(expression string) bool {
	depth := 0
	quoted := false
	escaped := false
	for _, char := range expression {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quoted {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return !quoted && !escaped && depth == 0
}

func validateEndpoint(endpoint LogEndpoint) error {
	if endpoint.Name == "" || endpoint.WriteURL == "" {
		return apperr.InvalidRequest("日志下游端点名称和写入地址不能为空")
	}
	if !validEndpointSinkType(endpoint.SinkType) {
		return apperr.InvalidRequest("日志下游端点类型只支持 vl、es、kafka 或 otel")
	}
	switch endpoint.SinkType {
	case EndpointSinkVL:
		if endpoint.QueryURL == "" || endpoint.VMUIURL == "" {
			return apperr.InvalidRequest("VL 下游端点必须填写写入地址、查询地址和 VMUI 地址")
		}
		if err := validateVLWriteURL(endpoint.WriteURL); err != nil {
			return err
		}
		for _, rawURL := range []string{endpoint.WriteURL, endpoint.QueryURL, endpoint.VMUIURL} {
			if err := validateHTTPURL(rawURL, "VL 下游端点地址"); err != nil {
				return err
			}
		}
	case EndpointSinkES:
		if err := validateHTTPURL(endpoint.WriteURL, "ES 下游端点写入地址"); err != nil {
			return err
		}
		for _, rawURL := range []string{endpoint.QueryURL, endpoint.VMUIURL} {
			if rawURL == "" {
				continue
			}
			if err := validateHTTPURL(rawURL, "ES 下游端点查询地址"); err != nil {
				return err
			}
		}
	case EndpointSinkKafka:
		if endpoint.StreamName == "" {
			return apperr.InvalidRequest("Kafka 下游端点必须填写 topic")
		}
		if err := validateKafkaBrokers(endpoint.WriteURL); err != nil {
			return err
		}
	case EndpointSinkOTel:
		if err := validateHTTPURL(endpoint.WriteURL, "OTel 下游端点写入地址"); err != nil {
			return err
		}
	}
	return nil
}

func validEndpointSinkType(value string) bool {
	return value == EndpointSinkVL || value == EndpointSinkES || value == EndpointSinkKafka || value == EndpointSinkOTel
}

func validateHTTPURL(raw string, label string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return apperr.InvalidRequest(label + "必须是完整的 http/https 地址")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return apperr.InvalidRequest(label + "只支持 http/https")
	}
	return nil
}

func validateVLWriteURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return apperr.InvalidRequest("VL 下游端点写入地址必须是完整的 http/https 地址")
	}
	if strings.TrimRight(parsed.Path, "/") != "/insert/opentelemetry/v1/logs" {
		return apperr.InvalidRequest("VL 下游端点写入地址必须指向 /insert/opentelemetry/v1/logs")
	}
	return nil
}

func validateKafkaBrokers(raw string) error {
	brokers := splitEndpointList(raw)
	if len(brokers) == 0 {
		return apperr.InvalidRequest("Kafka 下游端点 brokers 不能为空")
	}
	for _, broker := range brokers {
		if strings.ContainsAny(broker, " \t\r\n") {
			return apperr.InvalidRequest("Kafka 下游端点 brokers 不能包含空白字符")
		}
	}
	return nil
}

func normalizeNotFound(err error, message string) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return apperr.NotFound(message)
	}
	return err
}

func serviceSummaries(items []servicecatalog.Service) []ServiceSummary {
	out := make([]ServiceSummary, 0, len(items))
	for _, item := range items {
		out = append(out, serviceSummary(item))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func actorRefFromSubject(subject platformrbac.Subject) ActorRef {
	return ActorRef{ID: subject.ID, Type: subject.Type, Name: subject.DisplayName}
}

func agentGroupSummaries(items []collectormanagement.CollectorGroup) []AgentGroupSummary {
	out := make([]AgentGroupSummary, 0, len(items))
	for _, item := range items {
		out = append(out, AgentGroupSummary{
			ID:              item.ID,
			Name:            item.Name,
			DisplayName:     item.DisplayName,
			Mode:            item.Mode,
			EnvironmentID:   item.EnvironmentID,
			Cluster:         item.Cluster,
			Namespace:       item.Namespace,
			Status:          item.Status,
			OnlineInstances: item.OnlineInstances,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func clusterSummaries(items []k8sopscluster.Cluster) []ClusterSummary {
	out := make([]ClusterSummary, 0, len(items))
	for _, item := range items {
		out = append(out, ClusterSummary{
			ID:         item.ID,
			Name:       item.Name,
			Version:    item.Version,
			Region:     item.Region,
			Status:     item.Status,
			AccessMode: item.AccessMode,
			ReadOnly:   item.ReadOnly,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func copyStringMap(input map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func copyConfigFiles(input map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range input {
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func copyConfigFileRefs(input []LogCollectorConfigFile) []LogCollectorConfigFile {
	out := make([]LogCollectorConfigFile, 0, len(input))
	for _, item := range input {
		item.Path = strings.TrimSpace(item.Path)
		item.ConfigMapName = strings.TrimSpace(item.ConfigMapName)
		item.Role = strings.TrimSpace(item.Role)
		item.RouteID = strings.TrimSpace(item.RouteID)
		item.ServiceID = strings.TrimSpace(item.ServiceID)
		if item.Path != "" {
			out = append(out, item)
		}
	}
	return out
}

func configFileRefForRoute(refs []LogCollectorConfigFile, routeID string) LogCollectorConfigFile {
	routeID = strings.TrimSpace(routeID)
	for _, ref := range refs {
		if ref.Role == "service" && ref.RouteID == routeID {
			return ref
		}
	}
	for _, ref := range refs {
		if ref.RouteID == routeID {
			return ref
		}
	}
	return LogCollectorConfigFile{}
}

func copyStringSlice(input []string) []string {
	out := make([]string, 0, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func previewMode(source LogSource) string {
	if source.SourceType == SourceTypeVMFile {
		return "vm-agent-config"
	}
	return "k8s-daemonset"
}

func previewWarnings(source LogSource, endpoint LogEndpoint) []string {
	warnings := []string{}
	for _, rule := range source.ParseRules {
		if rule.RuleType == ParseRuleRegex {
			warnings = append(warnings, "正则解析规则会在 OTel Collector transform processor 中按 workload 条件执行，请先通过预览确认字段映射")
			break
		}
	}
	return warnings
}

func normalizeParseRules(rules []LogParseRule) []LogParseRule {
	out := make([]LogParseRule, 0, len(rules))
	for _, rule := range rules {
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.RuleType = strings.TrimSpace(rule.RuleType)
		rule.Pattern = strings.TrimSpace(rule.Pattern)
		if rule.RuleType == "" {
			rule.RuleType = ParseRuleRegex
		}
		if rule.Name == "" {
			rule.Name = rule.RuleType
		}
		out = append(out, rule)
	}
	return out
}

func validateParseRules(rules []LogParseRule) error {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		switch rule.RuleType {
		case ParseRuleRegex:
			if rule.Pattern == "" {
				return apperr.InvalidRequest("regex 解析规则必须填写 pattern")
			}
			if !strings.Contains(rule.Pattern, "?P<") {
				return apperr.InvalidRequest("regex 解析规则必须使用命名捕获组")
			}
		case ParseRuleJSON:
		default:
			return apperr.InvalidRequest("日志解析规则只支持 regex 或 json")
		}
	}
	return nil
}

func validateGeneratedK8sCollectorConfig(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	root, err := parseCollectorYAML(raw)
	if err != nil {
		return apperr.InvalidRequest("collector_yaml 必须是合法 YAML")
	}
	receivers := collectorReceivers(root)
	if len(receivers) == 0 {
		return apperr.InvalidRequest("K8s collector_yaml 必须声明 file_log receiver")
	}
	hasFilelogReceiver := false
	for name, receiver := range receivers {
		if strings.HasPrefix(name, "filelog") {
			return apperr.InvalidRequest("K8s collector_yaml receiver 类型必须使用 file_log，不支持 filelog alias")
		}
		includes := yamlStringValues(yamlMappingValue(receiver, "include"))
		for _, include := range includes {
			if err := validateK8sCollectorInclude(include); err != nil {
				return err
			}
		}
		if !collectorReceiverReadsPods(includes) {
			continue
		}
		if !strings.HasPrefix(name, "file_log") {
			return apperr.InvalidRequest("K8s collector_yaml 读取 Pod 标准日志时必须使用 file_log receiver")
		}
		hasFilelogReceiver = true
		if err := validateK8sFilelogSafety(receiver); err != nil {
			return err
		}
	}
	if !hasFilelogReceiver {
		return apperr.InvalidRequest("K8s collector_yaml 必须包含读取 /var/log/pods 的 file_log receiver")
	}
	if !collectorHasProcessor(root, "memory_limiter") {
		return apperr.InvalidRequest("K8s collector_yaml 必须配置 memory_limiter 处理器，避免采集端内存失控")
	}
	if collectorHasProcessor(root, "k8sattributes") {
		return apperr.InvalidRequest("K8s collector_yaml processor 必须使用 k8s_attributes，不支持 k8sattributes alias")
	}
	if !collectorHasProcessor(root, "k8s_attributes") {
		return apperr.InvalidRequest("K8s collector_yaml 必须配置 k8s_attributes 处理器，并按节点过滤 Pod 元数据")
	}
	return nil
}

func validateK8sFilelogSafety(receiver *yaml.Node) error {
	pollRaw := yamlScalarValue(yamlMappingValue(receiver, "poll_interval"))
	if pollRaw == "" {
		return apperr.InvalidRequest("K8s collector_yaml file_log receiver 必须设置 poll_interval，且不小于 5s")
	}
	pollInterval, err := time.ParseDuration(pollRaw)
	if err != nil || pollInterval < 5*time.Second {
		return apperr.InvalidRequest("K8s collector_yaml file_log receiver 的 poll_interval 不能小于 5s")
	}
	if yamlMappingValue(receiver, "storage") == nil {
		return apperr.InvalidRequest("K8s collector_yaml file_log receiver 必须设置 storage 保存 offset")
	}
	if len(yamlStringValues(yamlMappingValue(receiver, "exclude"))) == 0 {
		return apperr.InvalidRequest("K8s collector_yaml file_log receiver 必须设置 exclude，排除采集器自身和轮转文件")
	}
	maxConcurrent, ok := yamlPositiveInt(receiver, "max_concurrent_files")
	if !ok || maxConcurrent > 256 {
		return apperr.InvalidRequest("K8s collector_yaml file_log receiver 必须设置 max_concurrent_files，且不能大于 256")
	}
	maxBatches, ok := yamlPositiveInt(receiver, "max_batches")
	if !ok || maxBatches > 4 {
		return apperr.InvalidRequest("K8s collector_yaml file_log receiver 必须设置 max_batches，且不能大于 4")
	}
	return nil
}

func collectorReceiverReadsPods(includes []string) bool {
	for _, include := range includes {
		if strings.HasPrefix(strings.TrimSpace(include), "/var/log/pods/") {
			return true
		}
	}
	return false
}

func collectorHasProcessor(root *yaml.Node, processorType string) bool {
	processors := yamlMappingValue(root, "processors")
	if processors == nil || processors.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(processors.Content); i += 2 {
		name := strings.TrimSpace(processors.Content[i].Value)
		if name == processorType || strings.HasPrefix(name, processorType+"/") {
			return true
		}
	}
	return false
}

func validateK8sCollectorInclude(include string) error {
	include = strings.TrimSpace(include)
	if include == "" {
		return nil
	}
	if strings.HasPrefix(include, "/var/log/pods/") {
		podDirPattern, _, _ := strings.Cut(strings.TrimPrefix(include, "/var/log/pods/"), "/")
		namespacePattern, _, _ := strings.Cut(podDirPattern, "_")
		if podDirPattern == "" || strings.HasPrefix(podDirPattern, "*") || strings.ContainsAny(namespacePattern, "*?[") {
			return apperr.InvalidRequest("K8s collector_yaml 不能使用全局 Pod 日志路径，请按 Namespace/Workload 生成采集路径后再发布")
		}
		return nil
	}
	if include == "/var/log" || strings.HasPrefix(include, "/var/log/") ||
		include == "/var/lib/kubelet" || strings.HasPrefix(include, "/var/lib/kubelet/") ||
		include == "/var/lib/docker" || strings.HasPrefix(include, "/var/lib/docker/") {
		return apperr.InvalidRequest("K8s collector_yaml 只能读取 /var/log/pods 下按 Namespace/Workload 收窄后的容器标准日志")
	}
	return nil
}

func validateCollectorYAML(raw string, endpoint LogEndpoint) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	root, err := parseCollectorYAML(raw)
	if err != nil {
		return apperr.InvalidRequest("collector_yaml 必须是合法 YAML")
	}
	if !collectorHasLogsPipeline(root) {
		return apperr.InvalidRequest("collector_yaml 必须包含 logs pipeline（service.pipelines.logs 或 service.pipelines.logs/<service>）")
	}
	exporters := yamlMappingValue(root, "exporters")
	if exporters == nil || exporters.Kind != yaml.MappingNode {
		return apperr.InvalidRequest("collector_yaml 必须声明 exporters")
	}
	endpoints := collectorExporterEndpoints(exporters)
	if len(endpoints) == 0 {
		return apperr.InvalidRequest("collector_yaml exporter 必须显式配置日志下游写入地址")
	}
	if !collectorExporterTargetsMatch(endpoints, endpoint) {
		return apperr.InvalidRequest("collector_yaml exporter 写入地址必须与当前日志下游端点一致")
	}
	if err := validateVictoriaLogsCollectorTenant(exporters, endpoint); err != nil {
		return err
	}
	if containsSecretLikeKey(root) {
		return apperr.InvalidRequest("collector_yaml 不能直接包含 token、password、secret 或 authorization 等凭据字段")
	}
	return nil
}

func validateVMCollectorHealthYAML(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	root, err := parseCollectorYAML(raw)
	if err != nil {
		return apperr.InvalidRequest("collector_yaml 必须是合法 YAML")
	}
	return validateVMCollectorHealthCheck(root)
}

func validateVMCollectorHealthCheck(root *yaml.Node) error {
	extensions := yamlMappingValue(root, "extensions")
	healthCheck := yamlMappingValue(extensions, "health_check")
	if healthCheck == nil || yamlScalarValue(yamlMappingValue(healthCheck, "endpoint")) != "0.0.0.0:13133" {
		return apperr.InvalidRequest("collector_yaml 必须配置 health_check endpoint 0.0.0.0:13133")
	}
	enabled := false
	for _, extension := range yamlStringValues(mappingValue(root, "service", "extensions")) {
		enabled = enabled || extension == "health_check"
	}
	if !enabled {
		return apperr.InvalidRequest("collector_yaml 的 service.extensions 必须启用 health_check")
	}
	return nil
}

func validateVictoriaLogsCollectorTenant(exporters *yaml.Node, endpoint LogEndpoint) error {
	if endpoint.SinkType != EndpointSinkVL || endpoint.AccountID == "" {
		return nil
	}
	if !collectorExporterTenantMatches(exporters, endpoint) {
		return apperr.InvalidRequest("VictoriaLogs collector_yaml 必须携带匹配的 AccountID 和 ProjectID")
	}
	return nil
}

func collectorExporterTenantMatches(exporters *yaml.Node, endpoint LogEndpoint) bool {
	for i := 0; i+1 < len(exporters.Content); i += 2 {
		exporter := exporters.Content[i+1]
		if exporter.Kind != yaml.MappingNode || !yamlNodeContainsString(yamlMappingValue(exporter, "logs_endpoint"), endpoint.WriteURL) {
			continue
		}
		headers := yamlMappingValue(exporter, "headers")
		if yamlScalarValue(yamlMappingValue(headers, "AccountID")) == endpoint.AccountID &&
			yamlScalarValue(yamlMappingValue(headers, "ProjectID")) == endpoint.ProjectID {
			return true
		}
	}
	return false
}

func yamlNodeContainsString(node *yaml.Node, expected string) bool {
	for _, value := range yamlStringValues(node) {
		if value == expected {
			return true
		}
	}
	return false
}

func collectorHasLogsPipeline(root *yaml.Node) bool {
	pipelines := mappingValue(root, "service", "pipelines")
	if pipelines == nil || pipelines.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(pipelines.Content); i += 2 {
		name := strings.TrimSpace(pipelines.Content[i].Value)
		if name == "logs" || strings.HasPrefix(name, "logs/") {
			return true
		}
	}
	return false
}

func parseCollectorYAML(raw string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("collector yaml root must be mapping")
	}
	return doc.Content[0], nil
}

func collectorExporterEndpoints(exporters *yaml.Node) []string {
	out := []string{}
	for i := 0; i+1 < len(exporters.Content); i += 2 {
		exporter := exporters.Content[i+1]
		if exporter.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(exporter.Content); j += 2 {
			key := exporter.Content[j].Value
			if key != "endpoint" && key != "logs_endpoint" && key != "endpoints" && key != "brokers" {
				continue
			}
			out = append(out, yamlStringValues(exporter.Content[j+1])...)
		}
	}
	sort.Strings(out)
	return out
}

func yamlStringValues(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return splitEndpointList(node.Value)
	case yaml.SequenceNode:
		out := []string{}
		for _, item := range node.Content {
			out = append(out, yamlStringValues(item)...)
		}
		return out
	default:
		return nil
	}
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func yamlPositiveInt(mapping *yaml.Node, key string) (int, bool) {
	raw := yamlScalarValue(yamlMappingValue(mapping, key))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func collectorReceivers(root *yaml.Node) map[string]*yaml.Node {
	receivers := yamlMappingValue(root, "receivers")
	if receivers == nil || receivers.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]*yaml.Node{}
	for i := 0; i+1 < len(receivers.Content); i += 2 {
		receiver := receivers.Content[i+1]
		if receiver.Kind != yaml.MappingNode {
			continue
		}
		out[strings.TrimSpace(receivers.Content[i].Value)] = receiver
	}
	return out
}

func collectorExporterTargetsMatch(actual []string, endpoint LogEndpoint) bool {
	expected := splitEndpointList(endpoint.WriteURL)
	if endpoint.SinkType != EndpointSinkKafka {
		expected = []string{endpoint.WriteURL}
	}
	if len(expected) == 0 {
		return false
	}
	actualSet := map[string]bool{}
	for _, item := range actual {
		actualSet[item] = true
	}
	for _, item := range expected {
		if !actualSet[item] {
			return false
		}
	}
	return len(actualSet) == len(expected)
}

func splitEndpointList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func containsSecretLikeKey(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := strings.ToLower(strings.TrimSpace(node.Content[i].Value))
			if isSecretLikeKey(key) {
				return true
			}
			if containsSecretLikeKey(node.Content[i+1]) {
				return true
			}
		}
		return false
	}
	for _, child := range node.Content {
		if containsSecretLikeKey(child) {
			return true
		}
	}
	return false
}

func isSecretLikeKey(key string) bool {
	for _, token := range []string{"authorization", "password", "passwd", "token", "secret", "api_key", "apikey"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mappingValue(node *yaml.Node, path ...string) *yaml.Node {
	current := node
	for _, key := range path {
		current = yamlMappingValue(current, key)
		if current == nil {
			return nil
		}
	}
	return current
}

func serviceSummary(item servicecatalog.Service) ServiceSummary {
	return ServiceSummary{
		ID:            item.ID,
		ProductID:     item.ProductID,
		AccountID:     item.AccountID,
		ProjectID:     item.ProjectID,
		Name:          item.Name,
		DisplayName:   item.DisplayName,
		EnvironmentID: item.EnvironmentID,
		Cluster:       item.Cluster,
		Namespace:     item.Namespace,
		OwnerTeam:     item.OwnerTeam,
		IdentityType:  item.IdentityType,
		ServiceType:   item.ServiceType,
		Source:        item.Source,
		SyncStatus:    item.SyncStatus,
	}
}
