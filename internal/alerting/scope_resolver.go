package alerting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"novaapm/internal/database"
	"novaapm/internal/logs"
	"novaapm/internal/metrics"
	"novaapm/internal/platform/environment"
	"novaapm/internal/servicecatalog"

	"go.mongodb.org/mongo-driver/mongo"
)

type ScopeResolver interface {
	ResolveScope(ctx context.Context, spec RuleSpec) (RuleScope, error)
}

type StoreScopeResolver struct {
	services           database.ServiceStore
	routes             database.LogRouteStore
	targets            database.LogTargetStore
	endpoints          database.LogEndpointStore
	metricIntegrations database.MetricsIntegrationStore
	environments       database.EnvironmentStore
	products           database.ProductStore
}

func NewStoreScopeResolver(services database.ServiceStore, routes database.LogRouteStore, targets database.LogTargetStore, endpoints database.LogEndpointStore, products ...database.ProductStore) StoreScopeResolver {
	resolver := StoreScopeResolver{services: services, routes: routes, targets: targets, endpoints: endpoints}
	if len(products) > 0 {
		resolver.products = products[0]
	}
	return resolver
}

func NewSignalAwareStoreScopeResolver(services database.ServiceStore, routes database.LogRouteStore, targets database.LogTargetStore, endpoints database.LogEndpointStore, metricIntegrations database.MetricsIntegrationStore, environments database.EnvironmentStore, products ...database.ProductStore) StoreScopeResolver {
	resolver := StoreScopeResolver{services: services, routes: routes, targets: targets, endpoints: endpoints, metricIntegrations: metricIntegrations, environments: environments}
	if len(products) > 0 {
		resolver.products = products[0]
	}
	return resolver
}

func (r StoreScopeResolver) ResolveScope(ctx context.Context, spec RuleSpec) (RuleScope, error) {
	spec = spec.Normalize()
	switch spec.SignalType {
	case SignalTypeLogs:
		return r.resolveLogScope(ctx, spec.Scope)
	case SignalTypeMetrics:
		return r.resolveMetricsScope(ctx, spec.Scope)
	default:
		return RuleScope{}, invalidSpec("signal_type", "告警信号类型无效")
	}
}

func (r StoreScopeResolver) resolveLogScope(ctx context.Context, requested RuleScope) (RuleScope, error) {
	requested.ServiceID = strings.TrimSpace(requested.ServiceID)
	requested.LogRouteID = strings.TrimSpace(requested.LogRouteID)
	requested.LogTargetID = strings.TrimSpace(requested.LogTargetID)
	requested.EndpointID = strings.TrimSpace(requested.EndpointID)
	if requested.ServiceID == "" || requested.EndpointID == "" || (requested.LogRouteID == "" && requested.LogTargetID == "") {
		return RuleScope{}, invalidSpec("scope", "服务、日志目标或日志路由、日志端点不能为空")
	}
	if requested.LogRouteID != "" && requested.LogTargetID != "" {
		return RuleScope{}, invalidSpec("scope", "日志目标和日志路由不能同时绑定")
	}
	var service servicecatalog.Service
	if err := r.services.FindByID(ctx, requested.ServiceID, &service); err != nil {
		return RuleScope{}, scopeLookupError(err, "服务不存在")
	}
	service, err := r.resolveServiceTenant(ctx, service)
	if err != nil {
		return RuleScope{}, err
	}
	var endpoint logs.LogEndpoint
	if err := r.endpoints.FindByID(ctx, requested.EndpointID, &endpoint); err != nil {
		return RuleScope{}, scopeLookupError(err, "日志端点不存在")
	}
	if requested.LogTargetID != "" {
		return r.resolveTargetScope(ctx, requested, service, endpoint)
	}
	var route logs.LogRoute
	if err := r.routes.FindByID(ctx, requested.LogRouteID, &route); err != nil {
		return RuleScope{}, scopeLookupError(err, "日志路由不存在")
	}
	if route.ServiceID != service.ID || route.EndpointID != endpoint.ID {
		return RuleScope{}, invalidSpec("scope", "服务、日志路由和日志端点关系不一致")
	}
	if endpoint.SinkType != logs.EndpointSinkVL || strings.TrimSpace(endpoint.QueryURL) == "" {
		return RuleScope{}, invalidSpec("scope", "日志端点不支持 VictoriaLogs 查询")
	}
	return RuleScope{
		ServiceID: service.ID, ServiceName: service.Name, EnvironmentID: service.EnvironmentID,
		LogRouteID: route.ID, EndpointID: endpoint.ID,
		AccountID: service.AccountID, ProjectID: service.ProjectID,
	}, nil
}

func (r StoreScopeResolver) resolveTargetScope(ctx context.Context, requested RuleScope, service servicecatalog.Service, endpoint logs.LogEndpoint) (RuleScope, error) {
	if r.targets == nil {
		return RuleScope{}, invalidSpec("scope", "日志目标存储不可用")
	}
	var target logs.LogTarget
	if err := r.targets.FindByID(ctx, requested.LogTargetID, &target); err != nil {
		return RuleScope{}, scopeLookupError(err, "日志目标不存在")
	}
	target.BaseFilter = strings.TrimSpace(target.BaseFilter)
	if target.ServiceID != service.ID || target.EndpointID != endpoint.ID {
		return RuleScope{}, invalidSpec("scope", "服务、日志目标和日志端点关系不一致")
	}
	if target.Status == logs.LogTargetStatusDisabled {
		return RuleScope{}, invalidSpec("scope", "日志目标已停用")
	}
	if endpoint.SinkType != logs.EndpointSinkVL || strings.TrimSpace(endpoint.QueryURL) == "" {
		return RuleScope{}, invalidSpec("scope", "日志端点不支持 VictoriaLogs 查询")
	}
	if err := logs.ValidateLogTargetBaseFilter(target.BaseFilter); err != nil {
		return RuleScope{}, invalidSpec("scope", err.Error())
	}
	accountID, projectID := service.AccountID, service.ProjectID
	if strings.TrimSpace(target.AccountID) != "" && strings.TrimSpace(target.ProjectID) != "" {
		accountID, projectID = strings.TrimSpace(target.AccountID), strings.TrimSpace(target.ProjectID)
	}
	return RuleScope{
		ServiceID: service.ID, ServiceName: service.Name, EnvironmentID: service.EnvironmentID,
		LogTargetID: target.ID, EndpointID: endpoint.ID,
		AccountID: accountID, ProjectID: projectID,
		BaseFilter: target.BaseFilter,
	}, nil
}

func (r StoreScopeResolver) resolveMetricsScope(ctx context.Context, requested RuleScope) (RuleScope, error) {
	if r.metricIntegrations == nil || r.environments == nil {
		return RuleScope{}, ErrUnavailable
	}
	requested.EnvironmentID = strings.TrimSpace(requested.EnvironmentID)
	requested.EndpointID = strings.TrimSpace(requested.EndpointID)
	if requested.EnvironmentID == "" {
		return RuleScope{}, invalidSpec("scope.environment_id", "指标告警必须选择环境")
	}
	var env environment.Environment
	if err := r.environments.FindByID(ctx, requested.EnvironmentID, &env); err != nil {
		return RuleScope{}, scopeLookupError(err, "环境不存在")
	}
	if env.Status != environment.StatusActive {
		return RuleScope{}, invalidSpec("scope.environment_id", "指标告警只能使用 active 环境")
	}
	var integration metrics.Integration
	if err := r.metricIntegrations.FindByEnvironment(ctx, env.ID, &integration); err != nil {
		return RuleScope{}, scopeLookupError(err, "环境尚未接入指标")
	}
	if integration.DesiredState != metrics.DesiredStateConnected {
		return RuleScope{}, invalidSpec("scope.environment_id", "环境指标接入未连接")
	}
	if requested.EndpointID != "" && requested.EndpointID != integration.DestinationRef {
		return RuleScope{}, invalidSpec("scope.endpoint_id", "指标端点必须由环境接入关系决定")
	}
	labels := make(map[string]string, len(requested.ScopeLabels))
	for key, value := range requested.ScopeLabels {
		labels[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return RuleScope{
		EnvironmentID: env.ID, EnvironmentName: env.Name,
		EndpointID: integration.DestinationRef, ScopeLabels: labels,
	}, nil
}

func (r StoreScopeResolver) resolveServiceTenant(ctx context.Context, service servicecatalog.Service) (servicecatalog.Service, error) {
	if service.ProductID == "" || r.products == nil {
		return service, nil
	}
	var product servicecatalog.Product
	if err := r.products.FindByID(ctx, service.ProductID, &product); err != nil {
		return servicecatalog.Service{}, scopeLookupError(err, "所属产品不存在")
	}
	service.AccountID = "0"
	service.ProjectID = product.ProjectID
	return service, nil
}

func (r StoreScopeResolver) ResolveQueryTarget(ctx context.Context, scope RuleScope) (QueryTarget, error) {
	resolved, err := r.ResolveScope(ctx, RuleSpec{SignalType: SignalTypeLogs, Scope: scope})
	if err != nil {
		return QueryTarget{}, err
	}
	var endpoint logs.LogEndpoint
	if err := r.endpoints.FindByID(ctx, resolved.EndpointID, &endpoint); err != nil {
		return QueryTarget{}, scopeLookupError(err, "日志端点不存在")
	}
	return QueryTarget{QueryURL: endpoint.QueryURL, AccountID: resolved.AccountID, ProjectID: resolved.ProjectID, BaseFilter: resolved.BaseFilter}, nil
}

func scopeLookupError(err error, message string) error {
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	}
	return fmt.Errorf("解析告警范围失败: %w", err)
}
