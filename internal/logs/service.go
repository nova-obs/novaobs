package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"novaobs/internal/collectormanagement"
	"novaobs/internal/database"
	k8sopscluster "novaobs/internal/modules/k8sops/cluster"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	k8sopsresource "novaobs/internal/modules/k8sops/resource"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"gopkg.in/yaml.v3"
)

type K8sClusterService interface {
	List(ctx context.Context, filter k8sopscluster.ListFilter) ([]k8sopscluster.Cluster, error)
	Get(ctx context.Context, id string) (k8sopscluster.Cluster, error)
}

type K8sResourceService interface {
	ListRuntimeGroups(ctx context.Context, query k8sopsresource.RuntimeGroupsQuery) (k8sopsresource.RuntimeGroupsResponse, error)
}

type K8sDeploymentService interface {
	Preview(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
	Apply(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
}

type ImageTemplateValueService interface {
	TemplateValues(ctx context.Context) (map[string]string, error)
}

type Service struct {
	endpoints        database.LogEndpointStore
	sources          database.LogSourceStore
	routes           database.LogRouteStore
	configVersions   database.LogCollectorConfigVersionStore
	manifestVersions database.LogDeploymentManifestVersionStore
	plans            database.LogAgentPlanStore
	clusterConfigs   database.LogCollectorClusterConfigStore
	services         servicecatalog.Repository
	targets          servicecatalog.TargetRepository
	collectorGroups  collectormanagement.Service
	k8sClusters      K8sClusterService
	k8sResources     K8sResourceService
	k8sDeployments   K8sDeploymentService
	imageTemplates   ImageTemplateValueService
	deployment       agentDeploymentOptions
}

type agentDeploymentOptions struct {
	OpAMPEndpoint string
}

type ServiceOption func(*Service)

func WithAgentOpAMPEndpoint(endpoint string) ServiceOption {
	return func(s *Service) {
		s.deployment.OpAMPEndpoint = strings.TrimSpace(endpoint)
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
	plans database.LogAgentPlanStore,
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
		endpoints:        endpoints,
		sources:          sources,
		routes:           routes,
		configVersions:   configVersions,
		manifestVersions: manifestVersions,
		plans:            plans,
		clusterConfigs:   clusterConfigs,
		services:         services,
		targets:          targets,
		collectorGroups:  collectorGroups,
		k8sClusters:      k8sClusters,
		k8sResources:     k8sResources,
		k8sDeployments:   k8sDeployments,
	}
	for _, option := range options {
		if option != nil {
			option(&service)
		}
	}
	return service
}

func (s Service) GetClusterConfig(ctx context.Context, clusterID string, agentNamespace string) (LogCollectorClusterConfig, error) {
	agentNamespace = firstNonEmpty(strings.TrimSpace(agentNamespace), "novaobs-system")
	var cfg LogCollectorClusterConfig
	err := s.clusterConfigs.FindByCluster(ctx, strings.TrimSpace(clusterID), agentNamespace, &cfg)
	if err != nil {
		return LogCollectorClusterConfig{ClusterID: clusterID, AgentNamespace: agentNamespace}, nil
	}
	return cfg, nil
}

func (s Service) UpsertClusterConfig(ctx context.Context, clusterID string, agentNamespace string, processorPatch string) (LogCollectorClusterConfig, error) {
	agentNamespace = firstNonEmpty(strings.TrimSpace(agentNamespace), "novaobs-system")
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

func (s Service) Workspace(ctx context.Context) (Workspace, error) {
	services, err := s.services.List(ctx)
	if err != nil {
		return Workspace{}, err
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
	clusters := []k8sopscluster.Cluster{}
	if s.k8sClusters != nil {
		clusters, err = s.k8sClusters.List(ctx, k8sopscluster.ListFilter{PageSize: 0})
		if err != nil {
			return Workspace{}, err
		}
	}
	return Workspace{
		Services:        serviceSummaries(services),
		CollectorGroups: agentGroupSummaries(groups),
		Clusters:        clusterSummaries(clusters),
		Endpoints:       endpoints,
		Routes:          routes,
	}, nil
}

func (s Service) CreateEndpoint(ctx context.Context, endpoint LogEndpoint) (LogEndpoint, error) {
	endpoint = normalizeEndpoint(endpoint)
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
	endpoint = normalizeEndpoint(endpoint)
	endpoint.ID = id
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
		endpoints[index] = normalizeEndpoint(endpoints[index])
	}
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
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Environment = strings.TrimSpace(req.Environment)
	req.OwnerTeam = strings.TrimSpace(req.OwnerTeam)
	req.WorkloadKind = strings.TrimSpace(req.WorkloadKind)
	if req.Environment == "" {
		req.Environment = "prod"
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
		if service.Name == workload.Name && service.Environment == req.Environment && service.Cluster == req.ClusterID && service.Namespace == req.Namespace {
			return service, false, nil
		}
	}
	service, err := s.services.Create(ctx, servicecatalog.Service{
		Name:         workload.Name,
		DisplayName:  workload.Name,
		Environment:  req.Environment,
		Cluster:      req.ClusterID,
		Namespace:    req.Namespace,
		OwnerTeam:    req.OwnerTeam,
		IdentityType: "k8s_workload",
		ServiceType:  "k8s业务",
		Status:       "active",
		Source:       "k8s",
		SyncStatus:   "synced",
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
		ServiceID:   service.ID,
		TargetType:  "cloud_native_workload",
		Environment: service.Environment,
		DisplayName: workload.Kind + "/" + workload.Name,
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
	rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
		ServiceName: firstNonEmpty(service.DisplayName, service.Name),
		Environment: service.Environment,
		Source:      effectiveSource,
		Endpoint:    endpoint,
		Route:       route,
		Deployment:  s.deployment,
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
	return LogRoutePreview{
		Source:                 effectiveSource,
		Endpoint:               endpoint,
		AgentYAML:              rendered.ManifestYAML,
		CollectorYAML:          rendered.CollectorYAML,
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
	return LogRouteCollectorConfig{
		RouteID:                route.ID,
		CollectorConfigHash:    config.CollectorConfigHash,
		DeploymentManifestHash: deploymentHash,
		SourceType:             source.SourceType,
		CollectorYAML:          config.CollectorYAML,
	}, nil
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

func (s Service) latestPlanByPreview(ctx context.Context, routeID string, previewID string) (LogAgentPlan, error) {
	var plans []LogAgentPlan
	if err := s.plans.FindByRoute(ctx, routeID, &plans); err != nil {
		return LogAgentPlan{}, err
	}
	for _, plan := range plans {
		if plan.PreviewID == previewID {
			return plan, nil
		}
	}
	return LogAgentPlan{}, mongo.ErrNoDocuments
}

func (s Service) renderedFromPlan(plan LogAgentPlan) renderedRouteConfig {
	return renderedRouteConfig{
		ManifestYAML:           plan.RenderedYAML,
		CollectorConfigHash:    plan.CollectorConfigHash,
		DeploymentManifestHash: plan.DeploymentManifestHash,
	}
}

func (s Service) CreateRoute(ctx context.Context, req UpsertRouteRequest) (LogRouteView, error) {
	route, source, endpoint, service, err := s.routeDraft(ctx, req, true)
	if err != nil {
		return LogRouteView{}, err
	}
	effectiveSource := routeEffectiveSource(route, source)
	rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
		ServiceName: firstNonEmpty(service.DisplayName, service.Name),
		Environment: service.Environment,
		Source:      effectiveSource,
		Endpoint:    endpoint,
		Route:       route,
		Deployment:  s.deployment,
	})
	if err != nil {
		return LogRouteView{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	route.Status = "ready"
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
	rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
		ServiceName: firstNonEmpty(service.DisplayName, service.Name),
		Environment: service.Environment,
		Source:      effectiveSource,
		Endpoint:    endpoint,
		Route:       route,
		Deployment:  s.deployment,
	})
	if err != nil {
		return LogRouteView{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	route.Status = "ready"
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
		route.LastPublishStatus = existing.LastPublishStatus
		route.LastPublishMessage = existing.LastPublishMessage
		route.LastPublishedAt = existing.LastPublishedAt
		route.LastAuditID = existing.LastAuditID
		route.LastPreviewID = existing.LastPreviewID
	} else {
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

func (s Service) PublishRoute(ctx context.Context, subject platformrbac.Subject, routeID string, req PublishRouteRequest) (PublishRouteResult, error) {
	route, source, endpoint, service, err := s.routeParts(ctx, routeID)
	if err != nil {
		return PublishRouteResult{}, err
	}
	if source.SourceType == SourceTypeVMFile {
		rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
			ServiceName: firstNonEmpty(service.DisplayName, service.Name),
			Environment: service.Environment,
			Source:      source,
			Endpoint:    endpoint,
			Route:       route,
			Deployment:  s.deployment,
		})
		if err != nil {
			return PublishRouteResult{}, err
		}
		return s.publishVM(ctx, route, source, rendered)
	}
	if blocked, _ := s.publishBlock(ctx, source); blocked {
		return PublishRouteResult{}, k8sopscluster.ErrClusterReadOnly
	}
	if strings.TrimSpace(req.PreviewID) == "" || strings.TrimSpace(req.ConfirmationToken) == "" {
		rendered, err := s.renderRouteConfigWithHashes(ctx, renderInput{
			ServiceName: firstNonEmpty(service.DisplayName, service.Name),
			Environment: service.Environment,
			Source:      source,
			Endpoint:    endpoint,
			Route:       route,
			Deployment:  s.deployment,
		})
		if err != nil {
			return PublishRouteResult{}, err
		}
		return s.previewK8sPublish(ctx, subject, route, source, rendered)
	}
	plan, err := s.latestPlanByPreview(ctx, route.ID, strings.TrimSpace(req.PreviewID))
	if err != nil {
		return PublishRouteResult{}, normalizeNotFound(err, "发布预览不存在")
	}
	if route.CollectorConfigHash != plan.CollectorConfigHash {
		return PublishRouteResult{}, apperr.InvalidRequest("预览对应的采集配置已失效，请重新生成发布预览")
	}
	rendered := s.renderedFromPlan(plan)
	return s.applyK8sPublish(ctx, subject, route, source, rendered, req)
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

func (s Service) publishVM(ctx context.Context, route LogRoute, source LogSource, rendered renderedRouteConfig) (PublishRouteResult, error) {
	now := time.Now().UTC()
	if err := s.persistRenderedArtifacts(ctx, source, rendered, now); err != nil {
		return PublishRouteResult{}, err
	}
	plan := LogAgentPlan{
		ID:                  primitive.NewObjectID().Hex(),
		RouteID:             route.ID,
		AgentGroupID:        route.AgentGroupID,
		SourceType:          source.SourceType,
		CollectorConfigHash: rendered.CollectorConfigHash,
		RenderedYAML:        rendered.ManifestYAML,
		Status:              "ready_for_agent_sync",
		Message:             "VM Agent 配置已生成，等待 Agent 运维模块下发",
		CreatedAt:           now,
	}
	if err := s.plans.Insert(ctx, plan); err != nil {
		return PublishRouteResult{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	route.LastPublishStatus = plan.Status
	route.LastPublishMessage = plan.Message
	route.LastPublishedAt = &now
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return PublishRouteResult{}, err
	}
	return PublishRouteResult{Route: route, Plan: plan, Status: plan.Status, Message: plan.Message, Warnings: []string{}}, nil
}

func (s Service) previewK8sPublish(ctx context.Context, subject platformrbac.Subject, route LogRoute, source LogSource, rendered renderedRouteConfig) (PublishRouteResult, error) {
	if s.k8sDeployments == nil {
		return PublishRouteResult{}, apperr.InvalidRequest("K8s 部署服务不可用")
	}
	now := time.Now().UTC()
	if err := s.persistRenderedArtifacts(ctx, source, rendered, now); err != nil {
		return PublishRouteResult{}, err
	}
	result, err := s.k8sDeployments.Preview(ctx, subject, k8sopsdeployment.OperationRequest{
		ClusterID:      source.ClusterID,
		YAMLContent:    rendered.ManifestYAML,
		ForceConflicts: true,
	})
	if err != nil {
		return PublishRouteResult{}, err
	}
	plan := LogAgentPlan{
		ID:                     primitive.NewObjectID().Hex(),
		RouteID:                route.ID,
		AgentGroupID:           route.AgentGroupID,
		SourceType:             source.SourceType,
		ClusterID:              source.ClusterID,
		Namespace:              source.AgentNamespace,
		CollectorConfigHash:    rendered.CollectorConfigHash,
		DeploymentManifestHash: rendered.DeploymentManifestHash,
		RenderedYAML:           rendered.ManifestYAML,
		Status:                 "previewed",
		PreviewID:              result.PreviewID,
		ConfirmationToken:      result.ConfirmationToken,
		AuditID:                result.AuditID,
		Message:                result.Message,
		CreatedAt:              now,
	}
	if err := s.plans.Insert(ctx, plan); err != nil {
		return PublishRouteResult{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	route.LastPublishStatus = "previewed"
	route.LastPublishMessage = "K8s Agent DaemonSet 发布预览已生成，请确认后执行"
	route.LastPreviewID = result.PreviewID
	route.LastAuditID = result.AuditID
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return PublishRouteResult{}, err
	}
	if err := s.syncK8sBundlePublishState(ctx, source, rendered.CollectorConfigHash, "previewed", route.LastPublishMessage, result.PreviewID, result.AuditID, nil, now); err != nil {
		return PublishRouteResult{}, err
	}
	return PublishRouteResult{
		Route:                route,
		Plan:                 plan,
		Status:               "previewed",
		Message:              "K8s Agent DaemonSet 发布预览已生成，请确认后执行",
		RequiresConfirmation: true,
		PreviewID:            result.PreviewID,
		ConfirmationToken:    result.ConfirmationToken,
		AuditID:              result.AuditID,
		Resources:            result.Resources,
		Diffs:                result.Diffs,
		Warnings:             result.Warnings,
	}, nil
}

func (s Service) applyK8sPublish(ctx context.Context, subject platformrbac.Subject, route LogRoute, source LogSource, rendered renderedRouteConfig, req PublishRouteRequest) (PublishRouteResult, error) {
	result, err := s.k8sDeployments.Apply(ctx, subject, k8sopsdeployment.OperationRequest{
		ClusterID:         source.ClusterID,
		YAMLContent:       rendered.ManifestYAML,
		PreviewID:         req.PreviewID,
		ConfirmationToken: req.ConfirmationToken,
		ForceConflicts:    true,
	})
	if err != nil {
		return PublishRouteResult{}, err
	}
	now := time.Now().UTC()
	plan := LogAgentPlan{
		ID:                     primitive.NewObjectID().Hex(),
		RouteID:                route.ID,
		AgentGroupID:           route.AgentGroupID,
		SourceType:             source.SourceType,
		ClusterID:              source.ClusterID,
		Namespace:              source.AgentNamespace,
		CollectorConfigHash:    rendered.CollectorConfigHash,
		DeploymentManifestHash: rendered.DeploymentManifestHash,
		RenderedYAML:           rendered.ManifestYAML,
		Status:                 result.Status,
		PreviewID:              req.PreviewID,
		AuditID:                result.AuditID,
		Message:                result.Message,
		CreatedAt:              now,
	}
	if err := s.plans.Insert(ctx, plan); err != nil {
		return PublishRouteResult{}, err
	}
	applyRenderedRouteHashes(&route, rendered)
	route.LastPublishStatus = result.Status
	route.LastPublishMessage = result.Message
	route.LastPublishedAt = &now
	route.LastAuditID = result.AuditID
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return PublishRouteResult{}, err
	}
	if err := s.syncK8sBundlePublishState(ctx, source, rendered.CollectorConfigHash, result.Status, result.Message, req.PreviewID, result.AuditID, &now, now); err != nil {
		return PublishRouteResult{}, err
	}
	return PublishRouteResult{
		Route:     route,
		Plan:      plan,
		Status:    result.Status,
		Message:   result.Message,
		AuditID:   result.AuditID,
		Resources: result.Resources,
		Diffs:     result.Diffs,
		Warnings:  result.Warnings,
	}, nil
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
	if req.SourceType == SourceTypeVMFile {
		if err := validateCollectorYAML(req.VM.CollectorYAML, endpoint); err != nil {
			return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
		}
	}
	source := sourceFromRequest(req)
	agentGroupID, err := s.resolveAgentGroup(ctx, req.AgentGroupID, source, service, ensureAgentGroup)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
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
	environment := firstNonEmpty(service.Environment, "prod")
	ownerTeam := service.OwnerTeam
	if source.SourceType == SourceTypeVMFile {
		scope := firstNonEmpty(source.HostGroup, selectorFingerprint(source.HostSelector), "hosts")
		return collectormanagement.CollectorGroup{
			Name:            "logs-vm-" + safeSegment(environment) + "-" + safeSegment(scope),
			DisplayName:     "VM Logs / " + scope,
			Mode:            "shared_gateway",
			Environment:     environment,
			OwnerTeam:       ownerTeam,
			Status:          "active",
			ReceiverProfile: "filelog",
			MaxServices:     0,
		}
	}
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaobs-system")
	return collectormanagement.CollectorGroup{
		Name:            "logs-k8s-" + safeSegment(source.ClusterID) + "-" + safeSegment(agentNamespace),
		DisplayName:     "K8s Logs / " + source.ClusterID + " / " + agentNamespace,
		Mode:            "dedicated_collector",
		Environment:     environment,
		Cluster:         source.ClusterID,
		Namespace:       agentNamespace,
		OwnerTeam:       ownerTeam,
		Status:          "active",
		ReceiverProfile: "filelog",
		DesiredReplicas: 1,
	}
}

func selectorFingerprint(selector map[string]string) string {
	if len(selector) == 0 {
		return ""
	}
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+selector[key])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return "selector-" + hex.EncodeToString(sum[:])[:10]
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
	return route, routeEffectiveSource(route, source), endpoint, service, nil
}

func (s Service) getEndpoint(ctx context.Context, id string) (LogEndpoint, error) {
	var endpoint LogEndpoint
	if err := s.endpoints.FindByID(ctx, strings.TrimSpace(id), &endpoint); err != nil {
		return LogEndpoint{}, normalizeNotFound(err, "日志下游端点不存在")
	}
	endpoint = normalizeEndpoint(endpoint)
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

func (s Service) renderRouteConfigWithHashes(ctx context.Context, input renderInput) (renderedRouteConfig, error) {
	if input.Source.SourceType == SourceTypeVMFile {
		yaml, hash := renderAgentConfig(input)
		return renderedRouteConfig{
			ManifestYAML:        yaml,
			CollectorYAML:       yaml,
			CollectorConfigHash: hash,
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
		if err := s.clusterConfigs.FindByCluster(ctx, input.Source.ClusterID, firstNonEmpty(input.Source.AgentNamespace, "novaobs-system"), &clusterCfg); err == nil {
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
			firstNonEmpty(view.Source.AgentNamespace, "novaobs-system") != firstNonEmpty(current.Source.AgentNamespace, "novaobs-system") {
			continue
		}
		service, err := s.services.Get(ctx, view.Route.ServiceID)
		if err != nil {
			return nil, normalizeNotFound(err, "服务不存在")
		}
		inputs = append(inputs, renderInput{
			ServiceName: firstNonEmpty(service.DisplayName, service.Name),
			Environment: service.Environment,
			Source:      *view.Source,
			Endpoint:    *view.Endpoint,
			Route:       view.Route,
			Deployment:  current.Deployment,
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
		if route.ID != currentRouteID && route.LastPublishStatus != "" && route.LastPublishStatus != "pending_publish" {
			route.LastPublishStatus = "pending_publish"
			route.LastPublishMessage = "采集域配置已更新，等待发布"
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
		firstNonEmpty(left.AgentNamespace, "novaobs-system") == firstNonEmpty(right.AgentNamespace, "novaobs-system")
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
	agentNamespace := firstNonEmpty(req.K8s.AgentNamespace, "novaobs-system")
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
		if req.VM.PathPattern == "" || (req.VM.HostGroup == "" && len(req.VM.HostSelector) == 0) {
			return apperr.InvalidRequest("VM 文件接入必须填写主机组或主机标签，并填写日志路径")
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

func normalizeEndpoint(endpoint LogEndpoint) LogEndpoint {
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.Description = strings.TrimSpace(endpoint.Description)
	endpoint.SinkType = strings.ToLower(strings.TrimSpace(endpoint.SinkType))
	endpoint.StreamName = strings.TrimSpace(endpoint.StreamName)
	endpoint.WriteURL = strings.TrimSpace(endpoint.WriteURL)
	endpoint.QueryURL = strings.TrimSpace(endpoint.QueryURL)
	endpoint.VMUIURL = strings.TrimSpace(endpoint.VMUIURL)
	endpoint.AlertmanagerURL = strings.TrimSpace(endpoint.AlertmanagerURL)
	endpoint.AccountID = strings.TrimSpace(endpoint.AccountID)
	endpoint.ProjectID = strings.TrimSpace(endpoint.ProjectID)
	endpoint.SecretRef = strings.TrimSpace(endpoint.SecretRef)
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
	if endpoint.SinkType == "" {
		endpoint.SinkType = EndpointSinkVL
	}
	if endpoint.SinkType != EndpointSinkVL {
		endpoint.AccountID = ""
		endpoint.ProjectID = ""
		endpoint.AlertmanagerURL = ""
	}
	return endpoint
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
		if endpoint.AlertmanagerURL != "" {
			if err := validateHTTPURL(endpoint.AlertmanagerURL, "Alertmanager 通知地址"); err != nil {
				return err
			}
		}
		if err := validateVictoriaLogsTenant(endpoint.AccountID, endpoint.ProjectID); err != nil {
			return err
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

func validateVictoriaLogsTenant(accountID string, projectID string) error {
	if (accountID == "") != (projectID == "") {
		return apperr.InvalidRequest("VictoriaLogs AccountID 和 ProjectID 必须同时填写")
	}
	for _, item := range []struct {
		name  string
		value string
	}{{"AccountID", accountID}, {"ProjectID", projectID}} {
		name, value := item.name, item.value
		if value == "" {
			continue
		}
		if _, err := strconv.ParseUint(value, 10, 32); err != nil {
			return apperr.InvalidRequest("VictoriaLogs " + name + " 必须是 uint32")
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

func agentGroupSummaries(items []collectormanagement.CollectorGroup) []AgentGroupSummary {
	out := make([]AgentGroupSummary, 0, len(items))
	for _, item := range items {
		out = append(out, AgentGroupSummary{
			ID:              item.ID,
			Name:            item.Name,
			DisplayName:     item.DisplayName,
			Mode:            item.Mode,
			Environment:     item.Environment,
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
	if endpoint.SecretRef == "" {
		warnings = append(warnings, "日志下游端点未配置 secret_ref，当前预览不会持久化明文凭据")
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
		return apperr.InvalidRequest("collector_yaml 不能直接包含 token、password、secret 或 authorization 等敏感字段，请使用 secret_ref")
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
		ID:           item.ID,
		Name:         item.Name,
		DisplayName:  item.DisplayName,
		Environment:  item.Environment,
		Cluster:      item.Cluster,
		Namespace:    item.Namespace,
		OwnerTeam:    item.OwnerTeam,
		IdentityType: item.IdentityType,
		ServiceType:  item.ServiceType,
		Source:       item.Source,
		SyncStatus:   item.SyncStatus,
	}
}
