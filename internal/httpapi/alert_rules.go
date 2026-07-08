package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"novaobs/internal/alerting"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func testAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.TestRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "规则测试请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Test(ctx.Request.Context(), subject, body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func testMetricsAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.TestRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "规则测试请求格式不正确")
			return
		}
		body.Spec = forceAlertRuleSignal(body.Spec, alerting.SignalTypeMetrics)
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Test(ctx.Request.Context(), subject, body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func listMetricsAlertRulesHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		rules, err := service.List(ctx.Request.Context(), subject, alerting.RuleFilter{
			ServiceID:  strings.TrimSpace(ctx.Query("service_id")),
			State:      strings.TrimSpace(ctx.Query("state")),
			SignalType: alerting.SignalTypeMetrics,
		})
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, rules, gin.H{"total": len(rules)})
	}
}

func createMetricsAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.EnableRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "告警规则请求格式不正确")
			return
		}
		body.Spec = forceAlertRuleSignal(body.Spec, alerting.SignalTypeMetrics)
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Enable(ctx.Request.Context(), subject, body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.Created(ctx, result)
	}
}

func forceAlertRuleSignal(spec alerting.RuleSpec, signalType string) alerting.RuleSpec {
	spec.SignalType = signalType
	return spec
}

func getAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		item, err := service.Get(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, item, gin.H{})
	}
}

func updateAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.UpdateRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "告警规则更新请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Update(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func disableAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body struct {
			ChangeSummary string `json:"change_summary"`
		}
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "停用请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Disable(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body.ChangeSummary)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func listAlertRuleUpdatesHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		limit := alertListLimit(ctx.Query("limit"))
		items, err := service.ListUpdates(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), limit)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func rollbackAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.RollbackRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "回退请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Rollback(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func alertListLimit(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100 {
		return 20
	}
	return value
}

func alertRuleSignalFilter(ctx *gin.Context) (string, bool) {
	signalType := strings.ToLower(strings.TrimSpace(ctx.Query("signal_type")))
	if signalType == "" {
		return alerting.SignalTypeLogs, true
	}
	if signalType != alerting.SignalTypeLogs && signalType != alerting.SignalTypeMetrics {
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "signal_type 只支持 logs 或 metrics")
		return "", false
	}
	return signalType, true
}
