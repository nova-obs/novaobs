package httpapi

import (
	"context"

	"novaapm/internal/alerting"
	"novaapm/internal/collectormanagement"
	"novaapm/internal/logs"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

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
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		rules, err := serviceGraphAlertRules(ctx.Request.Context(), subject, deps.AlertService, service)
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

func serviceGraphAlertRules(ctx context.Context, subject platformrbac.Subject, alertService alerting.Service, service servicecatalog.Service) ([]alerting.Rule, error) {
	rules, err := alertService.List(ctx, subject, alerting.RuleFilter{ServiceID: service.ID})
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
	return rule.Spec.Scope.ServiceID == service.ID
}
