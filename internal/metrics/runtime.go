package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	k8sopsdeployment "novaapm/internal/modules/k8sops/deployment"
	k8sopsresource "novaapm/internal/modules/k8sops/resource"
	obsendpoint "novaapm/internal/observability/endpoint"
	obsruntime "novaapm/internal/observability/runtime"
	platformimages "novaapm/internal/platform/images"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"
)

const (
	defaultMetricsCollectorNamespace = "novaapm-system"
	legacyMetricsCollectorNamespace  = "novaobs-system"
)

type K8sResourceService interface {
	List(ctx context.Context, filter k8sopsresource.ListFilter) ([]k8sopsresource.ResourceSummary, error)
	GetDetail(ctx context.Context, query k8sopsresource.DetailQuery) (k8sopsresource.ResourceDetail, error)
}

type K8sDeploymentService interface {
	Preview(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
	Apply(ctx context.Context, subject platformrbac.Subject, req k8sopsdeployment.OperationRequest) (k8sopsdeployment.OperationResult, error)
}

type ImageTemplateValueService interface {
	TemplateValues(ctx context.Context) (map[string]string, error)
}

type metricRuntimeGroup struct {
	RuntimeID        string
	RuntimeNamespace string
	Anchor           MetricRoute
	ProductID        string
	Endpoint         obsendpoint.Endpoint
	Routes           []metricRuntimeRoute
	AllRouteIDs      []string
}

func normalizeMetricsCollectorNamespace(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == legacyMetricsCollectorNamespace {
		return defaultMetricsCollectorNamespace
	}
	return value
}

func metricsCollectorRuntimeID(clusterID string, runtimeNamespace string, productID string, endpointID string) string {
	return strings.Join([]string{"metrics-collector", strings.TrimSpace(clusterID), normalizeMetricsCollectorNamespace(runtimeNamespace), strings.TrimSpace(productID), strings.TrimSpace(endpointID)}, ":")
}

func (s Service) PublishCollectorRuntime(ctx context.Context, subject platformrbac.Subject, req CollectorRuntimePublishRequest) (CollectorRuntimePublishResult, error) {
	if s.routes == nil || s.runtimes == nil || s.k8sDeployments == nil {
		return CollectorRuntimePublishResult{}, apperr.InvalidRequest("指标采集运行时依赖不可用")
	}
	req.RouteID = strings.TrimSpace(req.RouteID)
	req.Namespace = normalizeMetricsCollectorNamespace(req.Namespace)
	if req.RouteID == "" {
		return CollectorRuntimePublishResult{}, apperr.InvalidRequest("route_id 不能为空")
	}
	if err := validateMetricsCollectorNamespace(req.Namespace); err != nil {
		return CollectorRuntimePublishResult{}, err
	}
	group, err := s.resolveMetricRuntimeGroup(ctx, req.RouteID, req.Namespace)
	if err != nil {
		return CollectorRuntimePublishResult{}, err
	}
	if !s.allowedMetricRuntimeGroup(subject, group, "manage") {
		return CollectorRuntimePublishResult{}, ErrPermissionDenied
	}
	if strings.TrimSpace(group.Endpoint.SecretRef) != "" {
		return CollectorRuntimePublishResult{}, apperr.InvalidRequest("第一阶段暂不支持需要凭据的 VictoriaMetrics Remote Write 端点")
	}
	if err := validateMetricRuntimeEndpoint(group); err != nil {
		return CollectorRuntimePublishResult{}, err
	}
	if err := s.validateMetricRuntimeTargets(ctx, &group); err != nil {
		return CollectorRuntimePublishResult{}, err
	}
	remoteWriteURL := firstNonEmpty(group.Endpoint.URLs.RemoteWriteURL, group.Endpoint.URLs.WriteURL)
	rendered, err := renderMetricsCollectorRuntime(group.Anchor.ClusterID, group.RuntimeNamespace, remoteWriteURL, group.Routes)
	if err != nil {
		s.markMetricRoutePublishFailure(ctx, group.AllRouteIDs, "", err)
		return CollectorRuntimePublishResult{}, err
	}
	templateValues := platformimages.DefaultTemplateValues
	if s.imageTemplates != nil {
		templateValues, err = s.imageTemplates.TemplateValues(ctx)
		if err != nil {
			return CollectorRuntimePublishResult{}, err
		}
	}
	rendered.ManifestYAML = platformimages.ApplyTemplateValues(rendered.ManifestYAML, templateValues)
	rendered.ManifestHash = metricDigest(rendered.ManifestYAML)
	operation := k8sopsdeployment.OperationRequest{
		ClusterID: group.Anchor.ClusterID, YAMLContent: rendered.ManifestYAML, ForceConflicts: true,
	}
	requiresConfirmation := strings.TrimSpace(req.PreviewID) == "" || strings.TrimSpace(req.ConfirmationToken) == ""
	var deployed k8sopsdeployment.OperationResult
	if requiresConfirmation {
		deployed, err = s.k8sDeployments.Preview(ctx, subject, operation)
	} else {
		operation.PreviewID = strings.TrimSpace(req.PreviewID)
		operation.ConfirmationToken = strings.TrimSpace(req.ConfirmationToken)
		deployed, err = s.k8sDeployments.Apply(ctx, subject, operation)
	}
	if err != nil {
		s.markMetricRoutePublishFailure(ctx, group.AllRouteIDs, rendered.ConfigHash, err)
		return CollectorRuntimePublishResult{}, err
	}
	now := s.now().UTC()
	runtime := s.metricsCollectorRuntime(ctx, group, rendered, deployed, requiresConfirmation, now)
	if err := s.runtimes.Upsert(ctx, runtime.ID, runtime); err != nil {
		s.markMetricRoutePublishFailure(ctx, group.AllRouteIDs, rendered.ConfigHash, err)
		return CollectorRuntimePublishResult{}, err
	}
	if err := s.syncMetricRoutePublishState(ctx, group.AllRouteIDs, rendered.ConfigHash, deployed, requiresConfirmation, now); err != nil {
		runtime.Status = obsruntime.StatusFailed
		runtime.LastError = "K8s 已完成操作，但平台路由状态同步失败: " + err.Error()
		runtime.UpdatedAt = now
		_ = s.runtimes.Upsert(ctx, runtime.ID, runtime)
		s.markMetricRoutePublishFailure(ctx, group.AllRouteIDs, rendered.ConfigHash, err)
		return CollectorRuntimePublishResult{}, err
	}
	return CollectorRuntimePublishResult{
		Runtime: runtime, RouteIDs: group.AllRouteIDs, ManifestYAML: rendered.ManifestYAML, ConfigYAML: rendered.ConfigYAML,
		ConfigHash: rendered.ConfigHash, ManifestHash: rendered.ManifestHash,
		Status: deployed.Status, Message: deployed.Message, RequiresConfirmation: requiresConfirmation,
		PreviewID: deployed.PreviewID, ConfirmationToken: deployed.ConfirmationToken, AuditID: deployed.AuditID,
		Resources: deployed.Resources, Diffs: deployed.Diffs, Warnings: append([]string{}, deployed.Warnings...),
	}, nil
}

func (s Service) markMetricRoutePublishFailure(ctx context.Context, routeIDs []string, configHash string, cause error) {
	now := s.now().UTC()
	for _, routeID := range routeIDs {
		route, err := s.getMetricRoute(ctx, routeID)
		if err != nil {
			continue
		}
		route.DesiredConfigHash = configHash
		route.LastPublishStatus = RoutePublishStatusFailed
		route.LastPublishMessage = cause.Error()
		route.UpdatedAt = now
		_ = s.routes.Update(ctx, route.ID, route)
	}
}

func validateMetricRuntimeEndpoint(group metricRuntimeGroup) error {
	if group.Endpoint.Kind != obsendpoint.KindVictoriaMetrics {
		return apperr.InvalidRequest("vmagent 运行时只能使用 VictoriaMetrics 指标端点")
	}
	switch group.Endpoint.Scope.Type {
	case "", "global":
	case "k8s_cluster":
		if strings.TrimSpace(group.Endpoint.Scope.ClusterID) != group.Anchor.ClusterID {
			return apperr.InvalidRequest("指标端点已不再绑定当前采集集群")
		}
	default:
		return apperr.InvalidRequest("指标端点作用域不支持 K8s 指标采集")
	}
	for _, item := range group.Routes {
		if item.Route.Status == MetricRouteStatusActive && group.Endpoint.Status != "active" {
			return apperr.InvalidRequest("指标端点必须处于 active 状态")
		}
	}
	return validateMetricRemoteWriteURL(firstNonEmpty(group.Endpoint.URLs.RemoteWriteURL, group.Endpoint.URLs.WriteURL))
}

func (s Service) validateMetricRuntimeTargets(ctx context.Context, group *metricRuntimeGroup) error {
	if s.k8sResources == nil {
		return nil
	}
	for index := range group.Routes {
		item := &group.Routes[index]
		if item.Route.Status == MetricRouteStatusDisabled {
			continue
		}
		resources, err := s.k8sResources.List(ctx, k8sopsresource.ListFilter{
			ClusterID: item.Route.ClusterID, Namespace: item.Route.Namespace, Kind: "Service", Query: item.Route.K8sServiceName, PageSize: 200,
		})
		if err != nil {
			return err
		}
		var serviceResource *k8sopsresource.ResourceSummary
		for index := range resources {
			if resources[index].Identity.Name == item.Route.K8sServiceName {
				serviceResource = &resources[index]
				break
			}
		}
		if serviceResource == nil {
			return apperr.InvalidRequest(fmt.Sprintf("指标采集目标 Service %s/%s 不存在", item.Route.Namespace, item.Route.K8sServiceName))
		}
		detail, err := s.k8sResources.GetDetail(ctx, k8sopsresource.DetailQuery{Identity: serviceResource.Identity})
		if err != nil {
			return err
		}
		resolvedPort, ok := resolveMetricServiceDiscoveryPort(detail.Spec["ports"], item.Route.Port)
		if !ok {
			return apperr.InvalidRequest(fmt.Sprintf("指标采集目标 Service %s/%s 不存在端口 %s", item.Route.Namespace, item.Route.K8sServiceName, item.Route.Port))
		}
		item.Route.Port = resolvedPort
	}
	return nil
}

func metricServicePortExists(raw any, expected string) bool {
	_, ok := resolveMetricServiceDiscoveryPort(raw, expected)
	return ok
}

func resolveMetricServiceDiscoveryPort(raw any, expected string) (string, bool) {
	ports, ok := raw.([]any)
	if !ok {
		return "", false
	}
	for _, rawPort := range ports {
		port, ok := rawPort.(map[string]any)
		if !ok {
			continue
		}
		name := fmt.Sprint(port["name"])
		servicePort := fmt.Sprint(port["port"])
		targetPort := fmt.Sprint(port["targetPort"])
		if name == expected {
			return expected, true
		}
		if targetPort == expected {
			return expected, true
		}
		if servicePort == expected {
			if name != "" && name != "<nil>" {
				return name, true
			}
			if targetPort == "" || targetPort == "<nil>" || targetPort == servicePort {
				return expected, true
			}
			return "", false
		}
	}
	return "", false
}

func (s Service) resolveMetricRuntimeGroup(ctx context.Context, routeID string, runtimeNamespace string) (metricRuntimeGroup, error) {
	anchor, err := s.getMetricRoute(ctx, routeID)
	if err != nil {
		return metricRuntimeGroup{}, err
	}
	anchorService, err := s.services.Get(ctx, anchor.ServiceID)
	if err != nil {
		return metricRuntimeGroup{}, normalizeNotFound(err, "服务不存在")
	}
	endpoint, err := s.metricsEndpoint(ctx, anchor.EndpointID)
	if err != nil {
		return metricRuntimeGroup{}, err
	}
	endpoint, err = resolveMetricsEndpointForService(endpoint, anchorService)
	if err != nil {
		return metricRuntimeGroup{}, err
	}
	var stored []MetricRoute
	if err := s.routes.FindRuntimeGroup(ctx, anchor.ClusterID, anchorService.ProductID, anchor.EndpointID, &stored); err != nil {
		return metricRuntimeGroup{}, err
	}
	routes := make([]metricRuntimeRoute, 0)
	allRouteIDs := make([]string, 0)
	for _, route := range stored {
		route = normalizeMetricRoute(route)
		if route.ClusterID != anchor.ClusterID || route.EndpointID != anchor.EndpointID {
			continue
		}
		service, err := s.services.Get(ctx, route.ServiceID)
		if err != nil {
			return metricRuntimeGroup{}, normalizeNotFound(err, "指标采集路由关联服务不存在")
		}
		if service.ProductID != anchorService.ProductID {
			continue
		}
		routes = append(routes, metricRuntimeRoute{Route: route, Service: service})
		allRouteIDs = append(allRouteIDs, route.ID)
	}
	if len(routes) == 0 {
		return metricRuntimeGroup{}, apperr.InvalidRequest("指标采集运行时没有可发布路由")
	}
	sort.SliceStable(routes, func(left, right int) bool { return routes[left].Route.ID < routes[right].Route.ID })
	sort.Strings(allRouteIDs)
	runtimeNamespace = normalizeMetricsCollectorNamespace(runtimeNamespace)
	return metricRuntimeGroup{
		RuntimeID:        metricsCollectorRuntimeID(anchor.ClusterID, runtimeNamespace, anchorService.ProductID, anchor.EndpointID),
		RuntimeNamespace: runtimeNamespace, Anchor: anchor, ProductID: anchorService.ProductID,
		Endpoint: endpoint, Routes: routes, AllRouteIDs: allRouteIDs,
	}, nil
}

func (s Service) metricsCollectorRuntime(ctx context.Context, group metricRuntimeGroup, rendered renderedMetricsRuntime, deployed k8sopsdeployment.OperationResult, preview bool, now time.Time) obsruntime.Runtime {
	runtime := obsruntime.Runtime{ID: group.RuntimeID, CreatedAt: now}
	var existing obsruntime.Runtime
	if err := s.runtimes.FindByID(ctx, group.RuntimeID, &existing); err == nil {
		runtime = existing
	}
	runtime.Kind = obsruntime.KindMetricsCollector
	runtime.SignalType = obsruntime.SignalMetrics
	runtime.ClusterID = group.Anchor.ClusterID
	runtime.Namespace = group.RuntimeNamespace
	runtime.ProductID = group.ProductID
	runtime.EndpointID = group.Anchor.EndpointID
	runtime.DesiredConfigHash = rendered.ConfigHash
	runtime.LastPreviewID = deployed.PreviewID
	runtime.LastAuditID = deployed.AuditID
	runtime.LastError = ""
	runtime.UpdatedAt = now
	if preview {
		runtime.Status = obsruntime.StatusPreviewed
	} else {
		runtime.Status = obsruntime.StatusDeployed
		runtime.CollectorConfigHash = rendered.ConfigHash
		runtime.ManifestHash = rendered.ManifestHash
		runtime.LastPublishedAt = &now
	}
	runtime.Resources = expectedMetricRuntimeResourceRefs(rendered.Name, group.RuntimeNamespace, metricRuntimeNamespaces(group.Routes))
	return runtime
}

func (s Service) syncMetricRoutePublishState(ctx context.Context, routeIDs []string, configHash string, deployed k8sopsdeployment.OperationResult, preview bool, now time.Time) error {
	for _, routeID := range routeIDs {
		route, err := s.getMetricRoute(ctx, routeID)
		if err != nil {
			return err
		}
		route.DesiredConfigHash = configHash
		route.LastPublishMessage = deployed.Message
		route.LastPreviewID = deployed.PreviewID
		route.LastAuditID = deployed.AuditID
		route.UpdatedAt = now
		if preview {
			route.LastPublishStatus = RoutePublishStatusPreviewed
		} else {
			route.LastPublishStatus = RoutePublishStatusApplied
			route.AppliedConfigHash = configHash
			route.LastPublishedAt = &now
		}
		if err := s.routes.Update(ctx, route.ID, route); err != nil {
			return err
		}
	}
	return nil
}

func expectedMetricRuntimeResourceRefs(name string, namespace string, targetNamespaces []string) []obsruntime.ResourceRef {
	refs := []obsruntime.ResourceRef{
		{APIVersion: "v1", Kind: "Namespace", Name: namespace},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: name},
		{APIVersion: "v1", Kind: "ServiceAccount", Namespace: namespace, Name: name},
		{APIVersion: "v1", Kind: "ConfigMap", Namespace: namespace, Name: name},
		{APIVersion: "v1", Kind: "Service", Namespace: namespace, Name: name},
		{APIVersion: "apps/v1", Kind: "Deployment", Namespace: namespace, Name: name},
	}
	for _, targetNamespace := range targetNamespaces {
		refs = append(refs, obsruntime.ResourceRef{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding", Namespace: targetNamespace, Name: name})
	}
	return refs
}

func (s Service) CheckCollectorRuntimeStatus(ctx context.Context, subject platformrbac.Subject, routeID string, namespace string) (CollectorRuntimeStatus, error) {
	namespace = normalizeMetricsCollectorNamespace(namespace)
	if err := validateMetricsCollectorNamespace(namespace); err != nil {
		return CollectorRuntimeStatus{}, err
	}
	group, err := s.resolveMetricRuntimeGroup(ctx, strings.TrimSpace(routeID), namespace)
	if err != nil {
		return CollectorRuntimeStatus{}, err
	}
	if !s.allowedMetricRuntimeGroup(subject, group, "read") {
		return CollectorRuntimeStatus{}, ErrPermissionDenied
	}
	if err := validateMetricRuntimeEndpoint(group); err != nil {
		return CollectorRuntimeStatus{}, err
	}
	if err := s.validateMetricRuntimeTargets(ctx, &group); err != nil {
		return CollectorRuntimeStatus{}, err
	}
	remoteWriteURL := firstNonEmpty(group.Endpoint.URLs.RemoteWriteURL, group.Endpoint.URLs.WriteURL)
	rendered, err := renderMetricsCollectorRuntime(group.Anchor.ClusterID, group.RuntimeNamespace, remoteWriteURL, group.Routes)
	if err != nil {
		return CollectorRuntimeStatus{}, err
	}
	status := CollectorRuntimeStatus{
		RuntimeID: group.RuntimeID, ClusterID: group.Anchor.ClusterID, Namespace: group.RuntimeNamespace,
		RouteIDs: group.AllRouteIDs, Status: obsruntime.StatusPendingPublish,
		Message: "指标采集运行时等待发布", Resources: expectedMetricRuntimeResourceStatuses(group.Anchor.ClusterID, rendered.Name, group.RuntimeNamespace, metricRuntimeNamespaces(group.Routes)),
	}
	var runtime obsruntime.Runtime
	if s.runtimes != nil && s.runtimes.FindByID(ctx, group.RuntimeID, &runtime) == nil {
		status.Runtime = &runtime
	}
	if s.k8sResources == nil {
		return status, nil
	}
	for index := range status.Resources {
		resource := &status.Resources[index]
		items, err := s.k8sResources.List(ctx, k8sopsresource.ListFilter{
			ClusterID: resource.ClusterID, Namespace: resource.Namespace, APIVersion: resource.APIVersion, Kind: resource.Kind, Query: resource.Name, PageSize: 200,
		})
		if err != nil {
			return CollectorRuntimeStatus{}, err
		}
		for _, item := range items {
			if item.Identity.Name != resource.Name {
				continue
			}
			resource.Exists = true
			resource.Healthy = resource.Kind != "Deployment" || item.Status == "healthy"
			if resource.Kind == "ConfigMap" && item.Labels["novaapm.io/config-hash"] != rendered.ConfigHash[:16] {
				resource.Healthy = false
			}
			break
		}
		if !resource.Exists || !resource.Healthy {
			status.MissingResources = append(status.MissingResources, *resource)
		}
	}
	if len(status.MissingResources) > 0 {
		status.Status = "missing_resources"
		status.Message = "指标采集运行时资源缺失或尚未就绪"
		return status, nil
	}
	if status.Runtime == nil || status.Runtime.CollectorConfigHash != rendered.ConfigHash {
		status.Status = obsruntime.StatusPendingPublish
		status.Message = "指标采集路由存在待发布变更"
		return status, nil
	}
	if status.Runtime.Status == obsruntime.StatusFailed && strings.TrimSpace(status.Runtime.LastError) != "" {
		status.Status = obsruntime.StatusFailed
		status.Message = status.Runtime.LastError
		return status, nil
	}
	status.Ready = true
	status.Status = obsruntime.StatusDeployed
	status.Message = "指标采集运行时已部署且 Kubernetes 资源就绪；采集与写入健康需结合 vmagent targets 和自身指标判断"
	return status, nil
}

func (s Service) allowedMetricRuntime(subject platformrbac.Subject, clusterID string, action string) bool {
	if subject.ID == "" || subject.Type == "" || s.authorizer == nil {
		return false
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "metrics.runtime",
		Action:   action,
		Scope:    platformrbac.Scope{ClusterID: strings.TrimSpace(clusterID)},
	}).Allowed
}

func (s Service) allowedMetricRuntimeGroup(subject platformrbac.Subject, group metricRuntimeGroup, action string) bool {
	if !s.allowedMetricRuntime(subject, group.Anchor.ClusterID, action) || !s.allowed(subject, "", "metrics.endpoint", "read") {
		return false
	}
	for _, item := range group.Routes {
		if !s.allowed(subject, item.Route.ServiceID, "metrics.route", action) {
			return false
		}
	}
	return true
}

func validateMetricsCollectorNamespace(namespace string) error {
	if len(namespace) > 63 || !k8sMetricTargetNamePattern.MatchString(namespace) {
		return apperr.InvalidRequest("运行时 namespace 必须是合法的 Kubernetes 名称")
	}
	if namespace != defaultMetricsCollectorNamespace {
		return apperr.InvalidRequest("第一阶段指标采集运行时 namespace 固定为 novaapm-system")
	}
	return nil
}

func expectedMetricRuntimeResourceStatuses(clusterID string, name string, namespace string, targetNamespaces []string) []CollectorRuntimeResourceStatus {
	refs := expectedMetricRuntimeResourceRefs(name, namespace, targetNamespaces)
	items := make([]CollectorRuntimeResourceStatus, 0, len(refs))
	for _, ref := range refs {
		items = append(items, CollectorRuntimeResourceStatus{
			ClusterID: clusterID, Namespace: ref.Namespace, APIVersion: ref.APIVersion, Kind: ref.Kind, Name: ref.Name, Required: true,
		})
	}
	return items
}
