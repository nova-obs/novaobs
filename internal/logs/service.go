package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
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

type Service struct {
	endpoints       database.LogEndpointStore
	sources         database.LogSourceStore
	routes          database.LogRouteStore
	plans           database.LogAgentPlanStore
	services        servicecatalog.Repository
	targets         servicecatalog.TargetRepository
	collectorGroups collectormanagement.Service
	k8sClusters     K8sClusterService
	k8sResources    K8sResourceService
	k8sDeployments  K8sDeploymentService
}

func NewService(
	endpoints database.LogEndpointStore,
	sources database.LogSourceStore,
	routes database.LogRouteStore,
	plans database.LogAgentPlanStore,
	services servicecatalog.Repository,
	targets servicecatalog.TargetRepository,
	collectorGroups collectormanagement.Service,
	k8sClusters K8sClusterService,
	k8sResources K8sResourceService,
	k8sDeployments K8sDeploymentService,
) Service {
	return Service{
		endpoints:       endpoints,
		sources:         sources,
		routes:          routes,
		plans:           plans,
		services:        services,
		targets:         targets,
		collectorGroups: collectorGroups,
		k8sClusters:     k8sClusters,
		k8sResources:    k8sResources,
		k8sDeployments:  k8sDeployments,
	}
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
	if endpoint.Name == "" || endpoint.WriteURL == "" || endpoint.QueryURL == "" || endpoint.VMUIURL == "" {
		return LogEndpoint{}, apperr.InvalidRequest("VictoriaLogs 端点名称、写入地址、查询地址和 VMUI 地址不能为空")
	}
	if endpoint.ScopeType != EndpointScopeGlobal && endpoint.ScopeType != EndpointScopeK8sCluster && endpoint.ScopeType != EndpointScopeVM {
		return LogEndpoint{}, apperr.InvalidRequest("VictoriaLogs 端点 scope_type 只支持 global、k8s_cluster 或 vm")
	}
	if endpoint.ScopeType == EndpointScopeK8sCluster && endpoint.ClusterID == "" {
		return LogEndpoint{}, apperr.InvalidRequest("K8s 集群级 VictoriaLogs 端点必须填写 cluster_id")
	}
	for _, rawURL := range []string{endpoint.WriteURL, endpoint.QueryURL, endpoint.VMUIURL} {
		if err := validateURL(rawURL); err != nil {
			return LogEndpoint{}, err
		}
	}
	existing, err := s.ListEndpoints(ctx)
	if err != nil {
		return LogEndpoint{}, err
	}
	for _, item := range existing {
		if strings.EqualFold(item.Name, endpoint.Name) {
			return LogEndpoint{}, apperr.Conflict("VictoriaLogs 端点名称已存在")
		}
		if endpoint.ScopeType == EndpointScopeK8sCluster && item.ScopeType == EndpointScopeK8sCluster && item.ClusterID == endpoint.ClusterID {
			return LogEndpoint{}, apperr.Conflict("该 K8s 集群已绑定 VictoriaLogs 端点")
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

func (s Service) ListEndpoints(ctx context.Context) ([]LogEndpoint, error) {
	var endpoints []LogEndpoint
	if err := s.endpoints.FindAll(ctx, &endpoints); err != nil {
		return nil, err
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
			view.Source = &source
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
	renderedYAML, configHash, err := s.renderRouteConfig(ctx, renderInput{
		ServiceName: firstNonEmpty(service.DisplayName, service.Name),
		Environment: service.Environment,
		Source:      source,
		Endpoint:    endpoint,
		Route:       route,
	})
	if err != nil {
		return LogRoutePreview{}, err
	}
	warnings := previewWarnings(source, endpoint)
	blocked, reason := s.publishBlock(ctx, source)
	return LogRoutePreview{
		Source:               source,
		Endpoint:             endpoint,
		AgentYAML:            renderedYAML,
		ConfigHash:           configHash,
		Mode:                 previewMode(source),
		PublishBlocked:       blocked,
		PublishBlockedReason: reason,
		Warnings:             warnings,
	}, nil
}

func (s Service) CreateRoute(ctx context.Context, req UpsertRouteRequest) (LogRouteView, error) {
	route, source, endpoint, service, err := s.routeDraft(ctx, req, true)
	if err != nil {
		return LogRouteView{}, err
	}
	_, configHash, err := s.renderRouteConfig(ctx, renderInput{
		ServiceName: firstNonEmpty(service.DisplayName, service.Name),
		Environment: service.Environment,
		Source:      source,
		Endpoint:    endpoint,
		Route:       route,
	})
	if err != nil {
		return LogRouteView{}, err
	}
	route.ConfigHash = configHash
	route.Status = "ready"
	now := time.Now().UTC()
	route.CreatedAt = now
	route.UpdatedAt = now
	source.CreatedAt = now
	source.UpdatedAt = now
	if err := s.sources.Upsert(ctx, source.ID, source); err != nil {
		return LogRouteView{}, err
	}
	if err := s.routes.Upsert(ctx, route.ID, route); err != nil {
		return LogRouteView{}, err
	}
	return LogRouteView{Route: route, Source: &source, Endpoint: &endpoint}, nil
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
	yaml, configHash, err := s.renderRouteConfig(ctx, renderInput{
		ServiceName: firstNonEmpty(service.DisplayName, service.Name),
		Environment: service.Environment,
		Source:      source,
		Endpoint:    endpoint,
		Route:       route,
	})
	if err != nil {
		return PublishRouteResult{}, err
	}
	if source.SourceType == SourceTypeVMFile {
		return s.publishVM(ctx, route, source, yaml, configHash)
	}
	if blocked, _ := s.publishBlock(ctx, source); blocked {
		return PublishRouteResult{}, k8sopscluster.ErrClusterReadOnly
	}
	if strings.TrimSpace(req.PreviewID) == "" || strings.TrimSpace(req.ConfirmationToken) == "" {
		return s.previewK8sPublish(ctx, subject, route, source, yaml, configHash)
	}
	return s.applyK8sPublish(ctx, subject, route, source, yaml, configHash, req)
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

func (s Service) publishVM(ctx context.Context, route LogRoute, source LogSource, yaml string, configHash string) (PublishRouteResult, error) {
	now := time.Now().UTC()
	plan := LogAgentPlan{
		ID:           primitive.NewObjectID().Hex(),
		RouteID:      route.ID,
		AgentGroupID: route.AgentGroupID,
		SourceType:   source.SourceType,
		ConfigHash:   configHash,
		RenderedYAML: yaml,
		Status:       "ready_for_agent_sync",
		Message:      "VM Agent 配置已生成，等待 Agent 运维模块下发",
		CreatedAt:    now,
	}
	if err := s.plans.Insert(ctx, plan); err != nil {
		return PublishRouteResult{}, err
	}
	route.ConfigHash = configHash
	route.LastPublishStatus = plan.Status
	route.LastPublishMessage = plan.Message
	route.LastPublishedAt = &now
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return PublishRouteResult{}, err
	}
	return PublishRouteResult{Route: route, Plan: plan, Status: plan.Status, Message: plan.Message, Warnings: []string{}}, nil
}

func (s Service) previewK8sPublish(ctx context.Context, subject platformrbac.Subject, route LogRoute, source LogSource, yaml string, configHash string) (PublishRouteResult, error) {
	if s.k8sDeployments == nil {
		return PublishRouteResult{}, apperr.InvalidRequest("K8s 部署服务不可用")
	}
	result, err := s.k8sDeployments.Preview(ctx, subject, k8sopsdeployment.OperationRequest{
		ClusterID:   source.ClusterID,
		YAMLContent: yaml,
	})
	if err != nil {
		return PublishRouteResult{}, err
	}
	now := time.Now().UTC()
	plan := LogAgentPlan{
		ID:                primitive.NewObjectID().Hex(),
		RouteID:           route.ID,
		AgentGroupID:      route.AgentGroupID,
		SourceType:        source.SourceType,
		ClusterID:         source.ClusterID,
		Namespace:         source.AgentNamespace,
		ConfigHash:        configHash,
		RenderedYAML:      yaml,
		Status:            "previewed",
		PreviewID:         result.PreviewID,
		ConfirmationToken: result.ConfirmationToken,
		AuditID:           result.AuditID,
		Message:           result.Message,
		CreatedAt:         now,
	}
	if err := s.plans.Insert(ctx, plan); err != nil {
		return PublishRouteResult{}, err
	}
	route.ConfigHash = configHash
	route.LastPublishStatus = "previewed"
	route.LastPublishMessage = "K8s Agent DaemonSet 发布预览已生成，请确认后执行"
	route.LastPreviewID = result.PreviewID
	route.LastAuditID = result.AuditID
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
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
		Warnings:             result.Warnings,
	}, nil
}

func (s Service) applyK8sPublish(ctx context.Context, subject platformrbac.Subject, route LogRoute, source LogSource, yaml string, configHash string, req PublishRouteRequest) (PublishRouteResult, error) {
	result, err := s.k8sDeployments.Apply(ctx, subject, k8sopsdeployment.OperationRequest{
		ClusterID:         source.ClusterID,
		YAMLContent:       yaml,
		PreviewID:         req.PreviewID,
		ConfirmationToken: req.ConfirmationToken,
	})
	if err != nil {
		return PublishRouteResult{}, err
	}
	now := time.Now().UTC()
	plan := LogAgentPlan{
		ID:           primitive.NewObjectID().Hex(),
		RouteID:      route.ID,
		AgentGroupID: route.AgentGroupID,
		SourceType:   source.SourceType,
		ClusterID:    source.ClusterID,
		Namespace:    source.AgentNamespace,
		ConfigHash:   configHash,
		RenderedYAML: yaml,
		Status:       result.Status,
		PreviewID:    req.PreviewID,
		AuditID:      result.AuditID,
		Message:      result.Message,
		CreatedAt:    now,
	}
	if err := s.plans.Insert(ctx, plan); err != nil {
		return PublishRouteResult{}, err
	}
	route.ConfigHash = configHash
	route.LastPublishStatus = result.Status
	route.LastPublishMessage = result.Message
	route.LastPublishedAt = &now
	route.LastAuditID = result.AuditID
	route.UpdatedAt = now
	if err := s.routes.Update(ctx, route.ID, route); err != nil {
		return PublishRouteResult{}, err
	}
	return PublishRouteResult{
		Route:     route,
		Plan:      plan,
		Status:    result.Status,
		Message:   result.Message,
		AuditID:   result.AuditID,
		Resources: result.Resources,
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
	endpoint, err := s.endpointForRoute(ctx, req)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	if err := validateCollectorYAMLForRoute(req, endpoint); err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	source := sourceFromRequest(req)
	agentGroupID, err := s.resolveAgentGroup(ctx, req.AgentGroupID, source, service, ensureAgentGroup)
	if err != nil {
		return LogRoute{}, LogSource{}, LogEndpoint{}, servicecatalog.Service{}, err
	}
	route := LogRoute{
		ID:           primitive.NewObjectID().Hex(),
		Name:         firstNonEmpty(req.Name, service.DisplayName, service.Name),
		ServiceID:    service.ID,
		SourceID:     source.ID,
		SourceType:   source.SourceType,
		AgentGroupID: agentGroupID,
		EndpointID:   endpoint.ID,
		Status:       "draft",
	}
	return route, source, endpoint, service, nil
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
	return route, source, endpoint, service, nil
}

func (s Service) getEndpoint(ctx context.Context, id string) (LogEndpoint, error) {
	var endpoint LogEndpoint
	if err := s.endpoints.FindByID(ctx, strings.TrimSpace(id), &endpoint); err != nil {
		return LogEndpoint{}, normalizeNotFound(err, "VictoriaLogs 端点不存在")
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
				return LogEndpoint{}, apperr.InvalidRequest("VM 文件日志路由不能选择 K8s 集群级 VictoriaLogs 端点")
			}
			return endpoint, nil
		}
		switch endpoint.ScopeType {
		case EndpointScopeVM:
			return LogEndpoint{}, apperr.InvalidRequest("K8s 日志路由不能选择 VM 专用 VictoriaLogs 端点")
		case EndpointScopeK8sCluster:
			if endpoint.ClusterID != req.K8s.ClusterID {
				return LogEndpoint{}, apperr.InvalidRequest("K8s 日志路由只能选择当前集群绑定的 VictoriaLogs 端点")
			}
		case EndpointScopeGlobal:
			clusterEndpoint, ok, err := s.clusterEndpoint(ctx, req.K8s.ClusterID)
			if err != nil {
				return LogEndpoint{}, err
			}
			if ok && clusterEndpoint.ID != endpoint.ID {
				return LogEndpoint{}, apperr.InvalidRequest("当前集群已有绑定的 VictoriaLogs 端点，请使用集群绑定端点")
			}
		}
		return endpoint, nil
	}
	if req.SourceType == SourceTypeVMFile {
		return LogEndpoint{}, apperr.InvalidRequest("VM 文件接入必须选择 VictoriaLogs 端点")
	}
	endpoints, err := s.ListEndpoints(ctx)
	if err != nil {
		return LogEndpoint{}, err
	}
	for _, endpoint := range endpoints {
		if endpoint.ScopeType == EndpointScopeK8sCluster && endpoint.ClusterID == req.K8s.ClusterID {
			return endpoint, nil
		}
	}
	for _, endpoint := range endpoints {
		if endpoint.ScopeType == EndpointScopeGlobal {
			return endpoint, nil
		}
	}
	return LogEndpoint{}, apperr.InvalidRequest("当前 K8s 集群未绑定 VictoriaLogs 端点")
}

func (s Service) clusterEndpoint(ctx context.Context, clusterID string) (LogEndpoint, bool, error) {
	endpoints, err := s.ListEndpoints(ctx)
	if err != nil {
		return LogEndpoint{}, false, err
	}
	for _, endpoint := range endpoints {
		if endpoint.ScopeType == EndpointScopeK8sCluster && endpoint.ClusterID == clusterID {
			return endpoint, true, nil
		}
	}
	return LogEndpoint{}, false, nil
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

func (s Service) renderRouteConfig(ctx context.Context, input renderInput) (string, string, error) {
	if input.Source.SourceType == SourceTypeVMFile {
		yaml, hash := renderAgentConfig(input)
		return yaml, hash, nil
	}
	inputs, err := s.k8sBundleInputs(ctx, input)
	if err != nil {
		return "", "", err
	}
	if err := validateK8sBundleCollectorYAML(inputs); err != nil {
		return "", "", err
	}
	yaml, hash := renderK8sDaemonSetBundle(inputs)
	return yaml, hash, nil
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
			firstNonEmpty(view.Source.AgentNamespace, "novaobs-system") != firstNonEmpty(current.Source.AgentNamespace, "novaobs-system") ||
			view.Endpoint.ID != current.Endpoint.ID {
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
		})
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		return inputs[i].Route.ID < inputs[j].Route.ID
	})
	return inputs, nil
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
		source.CollectorYAML = req.VM.CollectorYAML
		return source
	}
	source.ClusterID = req.K8s.ClusterID
	source.Namespace = req.K8s.Namespace
	source.AgentNamespace = firstNonEmpty(req.K8s.AgentNamespace, "novaobs-system")
	source.WorkloadKind = req.K8s.WorkloadKind
	source.WorkloadName = req.K8s.WorkloadName
	source.Container = req.K8s.Container
	source.WorkloadSelector = copyStringMap(req.K8s.WorkloadSelector)
	source.PathPattern = req.K8s.PathPattern
	source.ParseRules = normalizeParseRules(req.K8s.ParseRules)
	source.CollectorYAML = req.K8s.CollectorYAML
	return source
}

func normalizeRouteRequest(req UpsertRouteRequest) UpsertRouteRequest {
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
	req.K8s.Container = strings.TrimSpace(req.K8s.Container)
	req.K8s.PathPattern = strings.TrimSpace(req.K8s.PathPattern)
	req.K8s.CollectorYAML = strings.TrimSpace(req.K8s.CollectorYAML)
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
		return apperr.InvalidRequest("日志来源类型只支持 k8s_stdout、k8s_hostpath、vm_file")
	}
	switch req.SourceType {
	case SourceTypeK8sStdout:
		if req.K8s.ClusterID == "" || req.K8s.Namespace == "" || req.K8s.WorkloadKind == "" || req.K8s.WorkloadName == "" {
			return apperr.InvalidRequest("K8s 标准输出接入必须选择集群、namespace 和 workload")
		}
	case SourceTypeK8sHostPath:
		if req.K8s.ClusterID == "" || req.K8s.Namespace == "" || req.K8s.WorkloadKind == "" || req.K8s.WorkloadName == "" || req.K8s.PathPattern == "" {
			return apperr.InvalidRequest("K8s hostPath 接入必须选择集群、namespace、workload 并填写日志路径")
		}
	case SourceTypeVMFile:
		if req.EndpointID == "" {
			return apperr.InvalidRequest("VM 文件接入必须选择 VictoriaLogs 端点")
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
	return value == SourceTypeK8sStdout || value == SourceTypeK8sHostPath || value == SourceTypeVMFile
}

func normalizeEndpoint(endpoint LogEndpoint) LogEndpoint {
	endpoint.Name = strings.TrimSpace(endpoint.Name)
	endpoint.Description = strings.TrimSpace(endpoint.Description)
	endpoint.WriteURL = strings.TrimSpace(endpoint.WriteURL)
	endpoint.QueryURL = strings.TrimSpace(endpoint.QueryURL)
	endpoint.VMUIURL = strings.TrimSpace(endpoint.VMUIURL)
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
	return endpoint
}

func validateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return apperr.InvalidRequest("VictoriaLogs URL 必须是完整的 http/https 地址")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return apperr.InvalidRequest("VictoriaLogs URL 只支持 http/https")
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

func previewMode(source LogSource) string {
	if source.SourceType == SourceTypeVMFile {
		return "vm-agent-config"
	}
	return "k8s-daemonset"
}

func previewWarnings(source LogSource, endpoint LogEndpoint) []string {
	warnings := []string{}
	if source.SourceType == SourceTypeK8sHostPath {
		warnings = append(warnings, "K8s hostPath 接入依赖业务写宿主机目录，请确认日志轮转和磁盘配额")
	}
	for _, rule := range source.ParseRules {
		if rule.RuleType == ParseRuleRegex {
			warnings = append(warnings, "正则解析规则会在 OTel Collector transform processor 中按 workload 条件执行，请先通过预览确认字段映射")
			break
		}
	}
	if endpoint.SecretRef == "" {
		warnings = append(warnings, "VictoriaLogs 端点未配置 secret_ref，当前预览不会持久化明文凭据")
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
		rule.Fields = copyStringMap(rule.Fields)
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

func validateCollectorYAMLForRoute(req UpsertRouteRequest, endpoint LogEndpoint) error {
	switch req.SourceType {
	case SourceTypeVMFile:
		return validateCollectorYAML(req.VM.CollectorYAML, endpoint)
	case SourceTypeK8sStdout, SourceTypeK8sHostPath:
		return validateCollectorYAML(req.K8s.CollectorYAML, endpoint)
	default:
		return nil
	}
}

func validateK8sBundleCollectorYAML(inputs []renderInput) error {
	if len(inputs) <= 1 {
		return nil
	}
	for _, input := range inputs {
		if strings.TrimSpace(input.Source.CollectorYAML) != "" {
			return apperr.InvalidRequest("同一 K8s 采集域包含多条日志路由时，暂不支持完整 collector_yaml 覆盖，请使用解析规则或拆分采集域")
		}
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
	if mappingValue(root, "service", "pipelines", "logs") == nil {
		return apperr.InvalidRequest("collector_yaml 必须包含 service.pipelines.logs")
	}
	exporters := yamlMappingValue(root, "exporters")
	if exporters == nil || exporters.Kind != yaml.MappingNode {
		return apperr.InvalidRequest("collector_yaml 必须声明 exporters")
	}
	endpoints := collectorExporterEndpoints(exporters)
	if len(endpoints) == 0 {
		return apperr.InvalidRequest("collector_yaml exporter 必须显式配置 VictoriaLogs 写入地址")
	}
	for _, item := range endpoints {
		if item != endpoint.WriteURL {
			return apperr.InvalidRequest("collector_yaml exporter 写入地址必须与当前 VictoriaLogs 端点一致")
		}
	}
	if containsSecretLikeKey(root) {
		return apperr.InvalidRequest("collector_yaml 不能直接包含 token、password、secret 或 authorization 等敏感字段，请使用 secret_ref")
	}
	return nil
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
			if key != "endpoint" && key != "logs_endpoint" {
				continue
			}
			value := strings.TrimSpace(exporter.Content[j+1].Value)
			if value != "" {
				out = append(out, value)
			}
		}
	}
	sort.Strings(out)
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
