package alerting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"novaobs/internal/database"
	"novaobs/internal/logs"
	"novaobs/internal/metrics"
	"novaobs/internal/servicecatalog"

	"go.mongodb.org/mongo-driver/mongo"
)

type ScopeResolver interface {
	ResolveScope(ctx context.Context, spec RuleSpec) (RuleScope, error)
}

type StoreScopeResolver struct {
	services       database.ServiceStore
	routes         database.LogRouteStore
	targets        database.LogTargetStore
	endpoints      database.LogEndpointStore
	metricBindings database.MetricsServiceBindingStore
}

func NewStoreScopeResolver(services database.ServiceStore, routes database.LogRouteStore, targets database.LogTargetStore, endpoints database.LogEndpointStore) StoreScopeResolver {
	return StoreScopeResolver{services: services, routes: routes, targets: targets, endpoints: endpoints}
}

func NewSignalAwareStoreScopeResolver(services database.ServiceStore, routes database.LogRouteStore, targets database.LogTargetStore, endpoints database.LogEndpointStore, metricBindings database.MetricsServiceBindingStore) StoreScopeResolver {
	return StoreScopeResolver{services: services, routes: routes, targets: targets, endpoints: endpoints, metricBindings: metricBindings}
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
		ServiceID: service.ID, ServiceName: service.Name,
		LogRouteID: route.ID, EndpointID: endpoint.ID,
		AccountID: endpoint.AccountID, ProjectID: endpoint.ProjectID,
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
	return RuleScope{
		ServiceID: service.ID, ServiceName: service.Name,
		LogTargetID: target.ID, EndpointID: endpoint.ID,
		AccountID: endpoint.AccountID, ProjectID: endpoint.ProjectID,
		BaseFilter: target.BaseFilter,
	}, nil
}

func (r StoreScopeResolver) resolveMetricsScope(ctx context.Context, requested RuleScope) (RuleScope, error) {
	if r.metricBindings == nil {
		return RuleScope{}, ErrUnavailable
	}
	requested.ServiceID = strings.TrimSpace(requested.ServiceID)
	requested.MetricsBindingID = strings.TrimSpace(requested.MetricsBindingID)
	requested.EndpointID = strings.TrimSpace(requested.EndpointID)
	binding, err := r.selectMetricsBinding(ctx, requested)
	if err != nil {
		return RuleScope{}, err
	}
	if requested.ServiceID != "" && requested.ServiceID != binding.ServiceID {
		return RuleScope{}, invalidSpec("scope", "服务和指标绑定关系不一致")
	}
	if requested.EndpointID != "" && requested.EndpointID != binding.EndpointID {
		return RuleScope{}, invalidSpec("scope", "指标绑定和指标端点关系不一致")
	}
	var service servicecatalog.Service
	if err := r.services.FindByID(ctx, binding.ServiceID, &service); err != nil {
		return RuleScope{}, scopeLookupError(err, "服务不存在")
	}
	basePromQL := metrics.MetricScopePromQL(binding.BasePromQL, binding.LabelMatch)
	if basePromQL == "" || !metrics.BasePromQLMatchesLabelMatch(basePromQL, binding.LabelMatch) {
		return RuleScope{}, invalidSpec("scope", "指标绑定缺少可收敛服务作用域的 base_promql")
	}
	return RuleScope{
		ServiceID:        service.ID,
		ServiceName:      service.Name,
		MetricsBindingID: binding.ID,
		EndpointID:       binding.EndpointID,
		AccountID:        binding.Tenant.AccountID,
		ProjectID:        binding.Tenant.ProjectID,
		BasePromQL:       basePromQL,
	}, nil
}

func (r StoreScopeResolver) selectMetricsBinding(ctx context.Context, requested RuleScope) (metrics.ServiceBinding, error) {
	if requested.MetricsBindingID != "" {
		var binding metrics.ServiceBinding
		if err := r.metricBindings.FindByID(ctx, requested.MetricsBindingID, &binding); err != nil {
			return metrics.ServiceBinding{}, scopeLookupError(err, "指标服务绑定不存在")
		}
		binding = normalizeMetricsBindingRecord(binding)
		if binding.Status != metrics.BindingStatusActive {
			return metrics.ServiceBinding{}, invalidSpec("scope", "指标告警只能使用 active metrics binding")
		}
		return binding, nil
	}
	if requested.ServiceID == "" {
		return metrics.ServiceBinding{}, invalidSpec("scope", "服务或指标绑定不能为空")
	}
	var bindings []metrics.ServiceBinding
	if err := r.metricBindings.FindByService(ctx, requested.ServiceID, &bindings); err != nil {
		return metrics.ServiceBinding{}, err
	}
	for _, binding := range bindings {
		binding = normalizeMetricsBindingRecord(binding)
		if binding.Status == metrics.BindingStatusActive {
			return binding, nil
		}
	}
	return metrics.ServiceBinding{}, invalidSpec("scope", "服务没有 active metrics binding")
}

func normalizeMetricsBindingRecord(binding metrics.ServiceBinding) metrics.ServiceBinding {
	binding.ID = strings.TrimSpace(binding.ID)
	binding.ServiceID = strings.TrimSpace(binding.ServiceID)
	binding.EndpointID = strings.TrimSpace(binding.EndpointID)
	binding.Tenant.AccountID = strings.TrimSpace(binding.Tenant.AccountID)
	binding.Tenant.ProjectID = strings.TrimSpace(binding.Tenant.ProjectID)
	binding.BasePromQL = strings.TrimSpace(binding.BasePromQL)
	binding.Status = strings.TrimSpace(binding.Status)
	if binding.Status == "" {
		binding.Status = metrics.BindingStatusActive
	}
	return binding
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
	return QueryTarget{QueryURL: endpoint.QueryURL, AccountID: endpoint.AccountID, ProjectID: endpoint.ProjectID, BaseFilter: resolved.BaseFilter}, nil
}

func scopeLookupError(err error, message string) error {
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	}
	return fmt.Errorf("解析告警范围失败: %w", err)
}
