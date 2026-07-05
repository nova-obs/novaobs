package alerting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"novaobs/internal/database"
	"novaobs/internal/logs"
	"novaobs/internal/servicecatalog"

	"go.mongodb.org/mongo-driver/mongo"
)

type ScopeResolver interface {
	ResolveScope(ctx context.Context, scope RuleScope) (RuleScope, error)
}

type StoreScopeResolver struct {
	services  database.ServiceStore
	routes    database.LogRouteStore
	targets   database.LogTargetStore
	endpoints database.LogEndpointStore
}

func NewStoreScopeResolver(services database.ServiceStore, routes database.LogRouteStore, targets database.LogTargetStore, endpoints database.LogEndpointStore) StoreScopeResolver {
	return StoreScopeResolver{services: services, routes: routes, targets: targets, endpoints: endpoints}
}

func (r StoreScopeResolver) ResolveScope(ctx context.Context, requested RuleScope) (RuleScope, error) {
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

func (r StoreScopeResolver) ResolveQueryTarget(ctx context.Context, scope RuleScope) (QueryTarget, error) {
	resolved, err := r.ResolveScope(ctx, scope)
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
