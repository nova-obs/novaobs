package httpapi

import (
	"strings"

	"novaobs/internal/alerting"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/logs"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

type serviceObservabilityGraph struct {
	Service    servicecatalog.Service                  `json:"service"`
	Targets    []servicecatalog.ObservedTarget         `json:"targets"`
	Agents     []collectormanagement.CollectorInstance `json:"agents"`
	LogRoutes  serviceGraphLogRoutesSummary            `json:"log_routes"`
	AlertRules []alerting.Rule                         `json:"alert_rules"`
}

type serviceGraphLogRoutesSummary struct {
	Total  int                 `json:"total"`
	Routes []logs.LogRouteView `json:"routes"`
}

func getServiceObservabilityGraphHandler(deps Dependencies) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, deps.ServiceRepo)
		if !ok {
			return
		}
		targets, err := deps.ServiceTargetRepo.ListByService(bg, service.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		agents, err := deps.CollectorService.ListInstancesByService(bg, service.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		logRoutes, err := serviceGraphLogRoutes(deps.LogsService, service.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		rules, err := serviceGraphAlertRules(deps.AlertService, service)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, serviceObservabilityGraph{
			Service:    service,
			Targets:    targets,
			Agents:     agents,
			LogRoutes:  logRoutes,
			AlertRules: rules,
		}, gin.H{})
	}
}

func listServiceTargetsHandler(repo servicecatalog.Repository, targetRepo servicecatalog.TargetRepository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		targets, err := targetRepo.ListByService(bg, service.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, targets, gin.H{"total": len(targets)})
	}
}

func createServiceTargetHandler(repo servicecatalog.Repository, targetRepo servicecatalog.TargetRepository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		var body servicecatalog.ObservedTarget
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("服务目标请求无效"))
			return
		}
		body.ServiceID = service.ID
		if body.Environment == "" {
			body.Environment = service.Environment
		}
		target, err := targetRepo.Create(bg, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.Created(ctx, target)
	}
}

func serviceGraphLogRoutes(service logs.Service, serviceID string) (serviceGraphLogRoutesSummary, error) {
	routes, err := service.ServiceRouteSummary(bg, serviceID)
	if err != nil {
		return serviceGraphLogRoutesSummary{}, err
	}
	return serviceGraphLogRoutesSummary{Total: len(routes), Routes: routes}, nil
}

func serviceGraphAlertRules(alertService alerting.Service, service servicecatalog.Service) ([]alerting.Rule, error) {
	rules, err := alertService.List(bg)
	if err != nil {
		return nil, err
	}
	out := make([]alerting.Rule, 0, len(rules))
	for _, rule := range rules {
		if alertRuleMatchesService(rule, service) {
			out = append(out, rule)
		}
	}
	return out, nil
}

func alertRuleMatchesService(rule alerting.Rule, service servicecatalog.Service) bool {
	query := strings.ToLower(rule.Query)
	return strings.EqualFold(rule.OwnerTeam, service.OwnerTeam) ||
		strings.EqualFold(rule.AlertRoute, service.AlertRoute) ||
		strings.Contains(query, strings.ToLower(service.Name)) ||
		strings.Contains(query, strings.ToLower(service.DisplayName))
}
