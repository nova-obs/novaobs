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
	endpoints database.LogEndpointStore
}

func NewStoreScopeResolver(services database.ServiceStore, routes database.LogRouteStore, endpoints database.LogEndpointStore) StoreScopeResolver {
	return StoreScopeResolver{services: services, routes: routes, endpoints: endpoints}
}

func (r StoreScopeResolver) ResolveScope(ctx context.Context, requested RuleScope) (RuleScope, error) {
	requested.ServiceID = strings.TrimSpace(requested.ServiceID)
	requested.LogRouteID = strings.TrimSpace(requested.LogRouteID)
	requested.EndpointID = strings.TrimSpace(requested.EndpointID)
	if requested.ServiceID == "" || requested.LogRouteID == "" || requested.EndpointID == "" {
		return RuleScope{}, invalidSpec("scope", "服务、日志路由和日志端点不能为空")
	}
	var service servicecatalog.Service
	if err := r.services.FindByID(ctx, requested.ServiceID, &service); err != nil {
		return RuleScope{}, scopeLookupError(err, "服务不存在")
	}
	var route logs.LogRoute
	if err := r.routes.FindByID(ctx, requested.LogRouteID, &route); err != nil {
		return RuleScope{}, scopeLookupError(err, "日志路由不存在")
	}
	var endpoint logs.LogEndpoint
	if err := r.endpoints.FindByID(ctx, requested.EndpointID, &endpoint); err != nil {
		return RuleScope{}, scopeLookupError(err, "日志端点不存在")
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

func (r StoreScopeResolver) ResolveQueryTarget(ctx context.Context, scope RuleScope) (QueryTarget, error) {
	resolved, err := r.ResolveScope(ctx, scope)
	if err != nil {
		return QueryTarget{}, err
	}
	var endpoint logs.LogEndpoint
	if err := r.endpoints.FindByID(ctx, resolved.EndpointID, &endpoint); err != nil {
		return QueryTarget{}, scopeLookupError(err, "日志端点不存在")
	}
	return QueryTarget{QueryURL: endpoint.QueryURL, AccountID: endpoint.AccountID, ProjectID: endpoint.ProjectID}, nil
}

func scopeLookupError(err error, message string) error {
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	}
	return fmt.Errorf("解析告警范围失败: %w", err)
}
