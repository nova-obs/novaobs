package metrics

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"novaapm/internal/database"
	obsendpoint "novaapm/internal/observability/endpoint"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var k8sMetricTargetNamePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var k8sMetricPortNamePattern = regexp.MustCompile(`^[a-z](?:[-a-z0-9]*[a-z0-9])?$`)

func (s Service) ListRoutes(ctx context.Context, subject platformrbac.Subject, serviceID string, clusterID string) ([]MetricRouteView, error) {
	if s.routes == nil {
		return []MetricRouteView{}, nil
	}
	serviceID = strings.TrimSpace(serviceID)
	clusterID = strings.TrimSpace(clusterID)
	if !s.allowed(subject, "", "metrics.endpoint", "read") {
		return nil, ErrPermissionDenied
	}
	var routes []MetricRoute
	var err error
	if serviceID != "" {
		if !s.allowed(subject, serviceID, "metrics.route", "read") {
			return nil, ErrPermissionDenied
		}
		err = s.routes.FindByService(ctx, serviceID, &routes)
	} else {
		err = s.routes.FindAll(ctx, &routes)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(routes, func(left, right int) bool { return routes[left].UpdatedAt.After(routes[right].UpdatedAt) })
	views := make([]MetricRouteView, 0, len(routes))
	for _, route := range routes {
		route = normalizeMetricRoute(route)
		if clusterID != "" && route.ClusterID != clusterID {
			continue
		}
		if serviceID == "" && !s.allowed(subject, route.ServiceID, "metrics.route", "read") {
			continue
		}
		view, err := s.metricRouteView(ctx, route)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s Service) GetRoute(ctx context.Context, subject platformrbac.Subject, id string) (MetricRouteView, error) {
	route, err := s.getMetricRoute(ctx, id)
	if err != nil {
		return MetricRouteView{}, err
	}
	if !s.allowed(subject, route.ServiceID, "metrics.route", "read") || !s.allowed(subject, "", "metrics.endpoint", "read") {
		return MetricRouteView{}, ErrPermissionDenied
	}
	return s.metricRouteView(ctx, route)
}

func (s Service) CreateRoute(ctx context.Context, subject platformrbac.Subject, req CreateRouteRequest) (MetricRouteView, error) {
	if s.routes == nil {
		return MetricRouteView{}, apperr.InvalidRequest("指标采集路由存储不可用")
	}
	req = normalizeCreateRouteRequest(req)
	if req.ServiceID == "" || req.EndpointID == "" || req.ClusterID == "" || req.Namespace == "" || req.K8sServiceName == "" || req.Port == "" {
		return MetricRouteView{}, apperr.InvalidRequest("service_id、endpoint_id、cluster_id、namespace、k8s_service_name 和 port 不能为空")
	}
	if err := validateMetricRouteFields(req); err != nil {
		return MetricRouteView{}, err
	}
	if !s.allowed(subject, req.ServiceID, "metrics.route", "manage") || !s.allowed(subject, "", "metrics.endpoint", "read") {
		return MetricRouteView{}, ErrPermissionDenied
	}
	service, endpoint, err := s.validateMetricRouteRequest(ctx, req)
	if err != nil {
		return MetricRouteView{}, err
	}
	if err := s.ensureUniqueMetricRoute(ctx, "", service.ProductID, req); err != nil {
		return MetricRouteView{}, err
	}
	now := s.now().UTC()
	route := MetricRoute{
		ID: primitive.NewObjectID().Hex(), Name: req.Name, ProductID: service.ProductID, ServiceID: service.ID, EndpointID: endpoint.ID,
		SourceKind: MetricRouteSourceK8sService, ClusterID: req.ClusterID, Namespace: req.Namespace,
		K8sServiceName: req.K8sServiceName, Port: req.Port, Scheme: req.Scheme, MetricsPath: req.MetricsPath,
		ScrapeInterval: req.ScrapeInterval, ScrapeTimeout: req.ScrapeTimeout,
		LabelMatch: map[string]string{"service.name": service.Name},
		BasePromQL: MetricScopePromQL("", map[string]string{"service.name": service.Name}),
		Status:     req.Status, LastPublishStatus: RoutePublishStatusPending,
		LastPublishMessage: "采集路由已保存，等待运行时发布",
		CreatedBy:          actorRefFromSubject(subject), UpdatedBy: actorRefFromSubject(subject), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.routes.Insert(ctx, route); err != nil {
		if errors.Is(err, database.ErrConflict) {
			return MetricRouteView{}, apperr.Conflict("相同指标采集目标已存在")
		}
		return MetricRouteView{}, err
	}
	return s.metricRouteView(ctx, route)
}

func (s Service) UpdateRoute(ctx context.Context, subject platformrbac.Subject, id string, req UpdateRouteRequest) (MetricRouteView, error) {
	current, err := s.getMetricRoute(ctx, id)
	if err != nil {
		return MetricRouteView{}, err
	}
	if !s.allowed(subject, current.ServiceID, "metrics.route", "manage") || !s.allowed(subject, "", "metrics.endpoint", "read") {
		return MetricRouteView{}, ErrPermissionDenied
	}
	updated := applyMetricRouteUpdate(current, req)
	if current.AppliedConfigHash != "" && (updated.EndpointID != current.EndpointID || updated.ClusterID != current.ClusterID || updated.Namespace != current.Namespace) {
		return MetricRouteView{}, apperr.InvalidRequest("已部署路由不能变更 endpoint_id、cluster_id 或 namespace；第一阶段请新建路由并先停用旧路由")
	}
	createReq := createRequestFromMetricRoute(updated)
	service, endpoint, err := s.validateMetricRouteRequest(ctx, createReq)
	if err != nil {
		return MetricRouteView{}, err
	}
	if err := s.ensureUniqueMetricRoute(ctx, current.ID, service.ProductID, createReq); err != nil {
		return MetricRouteView{}, err
	}
	configurationChanged := metricRouteConfigurationChanged(current, updated)
	updated.Name = createReq.Name
	updated.ProductID = service.ProductID
	updated.EndpointID = endpoint.ID
	updated.LabelMatch = map[string]string{"service.name": service.Name}
	updated.BasePromQL = MetricScopePromQL("", updated.LabelMatch)
	if configurationChanged {
		updated.DesiredConfigHash = ""
		updated.LastPublishStatus = RoutePublishStatusPending
		updated.LastPublishMessage = "采集路由已变更，等待运行时重新发布"
		updated.LastPreviewID = ""
	}
	updated.UpdatedBy = actorRefFromSubject(subject)
	updated.UpdatedAt = s.now().UTC()
	if err := s.routes.Update(ctx, current.ID, updated); err != nil {
		if errors.Is(err, database.ErrConflict) {
			return MetricRouteView{}, apperr.Conflict("相同指标采集目标已存在")
		}
		return MetricRouteView{}, normalizeNotFound(err, "指标采集路由不存在")
	}
	return s.metricRouteView(ctx, updated)
}

func (s Service) getMetricRoute(ctx context.Context, id string) (MetricRoute, error) {
	if s.routes == nil {
		return MetricRoute{}, apperr.NotFound("指标采集路由不存在")
	}
	var route MetricRoute
	if err := s.routes.FindByID(ctx, strings.TrimSpace(id), &route); err != nil {
		return MetricRoute{}, normalizeNotFound(err, "指标采集路由不存在")
	}
	return normalizeMetricRoute(route), nil
}

func (s Service) metricRouteView(ctx context.Context, route MetricRoute) (MetricRouteView, error) {
	view := MetricRouteView{Route: normalizeMetricRoute(route)}
	service, err := s.services.Get(ctx, route.ServiceID)
	if err != nil {
		return MetricRouteView{}, normalizeNotFound(err, "服务不存在")
	}
	view.Service = &service
	endpoint, err := s.metricsEndpoint(ctx, route.EndpointID)
	if err != nil {
		return MetricRouteView{}, err
	}
	endpoint, err = resolveMetricsEndpointForService(endpoint, service)
	if err != nil {
		return MetricRouteView{}, err
	}
	view.Endpoint = &endpoint
	view.RuntimeID = metricsCollectorRuntimeID(route.ClusterID, defaultMetricsCollectorNamespace, service.ProductID, route.EndpointID)
	return view, nil
}

func (s Service) validateMetricRouteRequest(ctx context.Context, req CreateRouteRequest) (servicecatalog.Service, obsendpoint.Endpoint, error) {
	if req.ServiceID == "" || req.EndpointID == "" || req.ClusterID == "" || req.Namespace == "" || req.K8sServiceName == "" || req.Port == "" {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("service_id、endpoint_id、cluster_id、namespace、k8s_service_name 和 port 不能为空")
	}
	if err := validateMetricRouteFields(req); err != nil {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, err
	}
	service, err := s.services.Get(ctx, req.ServiceID)
	if err != nil {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, normalizeNotFound(err, "服务不存在")
	}
	if strings.TrimSpace(service.Cluster) != "" && req.ClusterID != strings.TrimSpace(service.Cluster) {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("采集集群必须与当前服务集群一致")
	}
	if strings.TrimSpace(service.Namespace) != "" && req.Namespace != strings.TrimSpace(service.Namespace) {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("采集 Namespace 必须与当前服务 Namespace 一致")
	}
	endpoint, err := s.metricsEndpoint(ctx, req.EndpointID)
	if err != nil {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, err
	}
	if endpoint.Kind != obsendpoint.KindVictoriaMetrics {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("vmagent 采集路由只能使用 VictoriaMetrics 指标端点")
	}
	switch endpoint.Scope.Type {
	case "", "global":
	case "k8s_cluster":
		if strings.TrimSpace(endpoint.Scope.ClusterID) != req.ClusterID {
			return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("K8s 指标采集路由只能选择当前集群绑定的指标端点")
		}
	case "vm":
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("K8s 指标采集路由不能选择 VM 专用指标端点")
	default:
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("指标端点作用域无效")
	}
	if req.Status == MetricRouteStatusActive && endpoint.Status != "active" {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("指标端点必须处于 active 状态")
	}
	resolved, err := resolveMetricsEndpointForService(endpoint, service)
	if err != nil {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, err
	}
	remoteWriteURL := strings.TrimSpace(firstNonEmpty(resolved.URLs.RemoteWriteURL, resolved.URLs.WriteURL))
	if remoteWriteURL == "" {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, apperr.InvalidRequest("指标端点缺少 Remote Write 地址")
	}
	if err := validateMetricRemoteWriteURL(remoteWriteURL); err != nil {
		return servicecatalog.Service{}, obsendpoint.Endpoint{}, err
	}
	return service, endpoint, nil
}

func validateMetricRemoteWriteURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return apperr.InvalidRequest("指标端点 Remote Write 地址必须是有效的 HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return apperr.InvalidRequest("指标端点 Remote Write 地址不能包含内联凭据、查询参数或片段")
	}
	if !strings.Contains(parsed.Path, "/insert/") || !strings.HasSuffix(strings.TrimSuffix(parsed.Path, "/"), "/prometheus/api/v1/write") {
		return apperr.InvalidRequest("指标端点 Remote Write 地址必须使用 VictoriaMetrics Cluster insert Prometheus 写入路径")
	}
	return nil
}

func validateMetricRouteFields(req CreateRouteRequest) error {
	if len(req.Name) > 120 {
		return apperr.InvalidRequest("采集路由名称不能超过 120 个字符")
	}
	if len(req.Namespace) > 63 || !k8sMetricTargetNamePattern.MatchString(req.Namespace) {
		return apperr.InvalidRequest("namespace 必须是合法的 Kubernetes 名称")
	}
	if len(req.K8sServiceName) > 63 || !k8sMetricTargetNamePattern.MatchString(req.K8sServiceName) {
		return apperr.InvalidRequest("k8s_service_name 必须是合法的 Kubernetes Service 名称")
	}
	if err := validateMetricPort(req.Port); err != nil {
		return err
	}
	if req.Scheme != "http" && req.Scheme != "https" {
		return apperr.InvalidRequest("scheme 只支持 http 或 https")
	}
	if !strings.HasPrefix(req.MetricsPath, "/") || strings.Contains(req.MetricsPath, "://") || strings.ContainsAny(req.MetricsPath, "?#\r\n") || len(req.MetricsPath) > 256 {
		return apperr.InvalidRequest("metrics_path 必须是绝对路径且不能包含 URL、查询参数或换行")
	}
	interval, err := time.ParseDuration(req.ScrapeInterval)
	if err != nil || interval < 10*time.Second || interval > 5*time.Minute {
		return apperr.InvalidRequest("scrape_interval 必须在 10s 到 5m 之间")
	}
	timeout, err := time.ParseDuration(req.ScrapeTimeout)
	if err != nil || timeout <= 0 {
		return apperr.InvalidRequest("scrape_timeout 必须是正数时长")
	}
	if timeout >= interval {
		return apperr.InvalidRequest("scrape_timeout 必须小于 scrape_interval")
	}
	if req.Status != MetricRouteStatusActive && req.Status != MetricRouteStatusDisabled {
		return apperr.InvalidRequest("指标采集路由状态只支持 active 或 disabled")
	}
	return nil
}

func validateMetricPort(value string) error {
	if number, err := strconv.Atoi(value); err == nil {
		if number < 1 || number > 65535 {
			return apperr.InvalidRequest("port 数字必须在 1 到 65535 之间")
		}
		return nil
	}
	if len(value) > 15 || !k8sMetricPortNamePattern.MatchString(value) {
		return apperr.InvalidRequest("port 必须是合法的 Kubernetes 端口名称或端口号")
	}
	return nil
}

func (s Service) ensureUniqueMetricRoute(ctx context.Context, currentID string, productID string, req CreateRouteRequest) error {
	var routes []MetricRoute
	if err := s.routes.FindAll(ctx, &routes); err != nil {
		return err
	}
	for _, route := range routes {
		route = normalizeMetricRoute(route)
		if route.ID == currentID || route.Status == MetricRouteStatusDisabled || route.ProductID != productID || route.EndpointID != req.EndpointID {
			continue
		}
		if route.ClusterID == req.ClusterID && route.Namespace == req.Namespace && route.K8sServiceName == req.K8sServiceName && route.Port == req.Port && route.MetricsPath == req.MetricsPath {
			return apperr.Conflict("相同指标采集目标已存在")
		}
	}
	return nil
}

func normalizeCreateRouteRequest(req CreateRouteRequest) CreateRouteRequest {
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.Name = strings.TrimSpace(req.Name)
	req.EndpointID = strings.TrimSpace(req.EndpointID)
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.K8sServiceName = strings.TrimSpace(req.K8sServiceName)
	req.Port = strings.TrimSpace(req.Port)
	req.Scheme = strings.ToLower(strings.TrimSpace(req.Scheme))
	req.MetricsPath = strings.TrimSpace(req.MetricsPath)
	req.ScrapeInterval = strings.TrimSpace(req.ScrapeInterval)
	req.ScrapeTimeout = strings.TrimSpace(req.ScrapeTimeout)
	req.Status = strings.TrimSpace(req.Status)
	if req.Name == "" {
		req.Name = req.K8sServiceName + " metrics"
	}
	if req.Scheme == "" {
		req.Scheme = "http"
	}
	if req.MetricsPath == "" {
		req.MetricsPath = "/metrics"
	}
	if req.ScrapeInterval == "" {
		req.ScrapeInterval = "30s"
	}
	if req.ScrapeTimeout == "" {
		req.ScrapeTimeout = "10s"
	}
	if req.Status == "" {
		req.Status = MetricRouteStatusActive
	}
	return req
}

func normalizeMetricRoute(route MetricRoute) MetricRoute {
	req := normalizeCreateRouteRequest(createRequestFromMetricRoute(route))
	route.Name = req.Name
	route.ProductID = strings.TrimSpace(route.ProductID)
	route.ServiceID = req.ServiceID
	route.EndpointID = req.EndpointID
	route.SourceKind = firstNonEmpty(strings.TrimSpace(route.SourceKind), MetricRouteSourceK8sService)
	route.ClusterID = req.ClusterID
	route.Namespace = req.Namespace
	route.K8sServiceName = req.K8sServiceName
	route.Port = req.Port
	route.Scheme = req.Scheme
	route.MetricsPath = req.MetricsPath
	route.ScrapeInterval = req.ScrapeInterval
	route.ScrapeTimeout = req.ScrapeTimeout
	route.Status = req.Status
	route.LabelMatch = normalizeLabels(route.LabelMatch)
	route.BasePromQL = strings.TrimSpace(route.BasePromQL)
	if route.LastPublishStatus == "" {
		route.LastPublishStatus = RoutePublishStatusPending
	}
	return route
}

func createRequestFromMetricRoute(route MetricRoute) CreateRouteRequest {
	return normalizeCreateRouteRequest(CreateRouteRequest{
		ServiceID: route.ServiceID, Name: route.Name, EndpointID: route.EndpointID, ClusterID: route.ClusterID,
		Namespace: route.Namespace, K8sServiceName: route.K8sServiceName, Port: route.Port, Scheme: route.Scheme,
		MetricsPath: route.MetricsPath, ScrapeInterval: route.ScrapeInterval, ScrapeTimeout: route.ScrapeTimeout, Status: route.Status,
	})
}

func applyMetricRouteUpdate(current MetricRoute, req UpdateRouteRequest) MetricRoute {
	updated := current
	assign := func(target *string, value *string) {
		if value != nil {
			*target = strings.TrimSpace(*value)
		}
	}
	assign(&updated.Name, req.Name)
	assign(&updated.EndpointID, req.EndpointID)
	assign(&updated.ClusterID, req.ClusterID)
	assign(&updated.Namespace, req.Namespace)
	assign(&updated.K8sServiceName, req.K8sServiceName)
	assign(&updated.Port, req.Port)
	assign(&updated.Scheme, req.Scheme)
	assign(&updated.MetricsPath, req.MetricsPath)
	assign(&updated.ScrapeInterval, req.ScrapeInterval)
	assign(&updated.ScrapeTimeout, req.ScrapeTimeout)
	assign(&updated.Status, req.Status)
	return normalizeMetricRoute(updated)
}

func metricRouteConfigurationChanged(left MetricRoute, right MetricRoute) bool {
	return left.EndpointID != right.EndpointID || left.ClusterID != right.ClusterID || left.Namespace != right.Namespace ||
		left.K8sServiceName != right.K8sServiceName || left.Port != right.Port || left.Scheme != right.Scheme ||
		left.MetricsPath != right.MetricsPath || left.ScrapeInterval != right.ScrapeInterval ||
		left.ScrapeTimeout != right.ScrapeTimeout || left.Status != right.Status
}
