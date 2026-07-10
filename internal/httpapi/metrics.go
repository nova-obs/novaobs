package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"novaapm/internal/alerting"
	"novaapm/internal/metrics"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func listMetricsEndpointsHandler(service metrics.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		endpoints, err := service.ListEndpoints(ctx.Request.Context(), subject)
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.OK(ctx, endpoints, gin.H{"total": len(endpoints)})
	}
}

func publishMetricsEndpointVmalertRuntimeHandler(service alerting.MetricsRuntimeService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.LogRuntimePublishRequest
		if err := ctx.ShouldBindJSON(&body); err != nil && ctx.Request.ContentLength > 0 {
			writeError(ctx, apperr.InvalidRequest("metrics vmalert Runtime 发布请求无效"))
			return
		}
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Publish(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func getMetricsWorkspaceHandler(service metrics.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		workspace, err := service.Workspace(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("productId")), strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.OK(ctx, workspace, gin.H{})
	}
}

func listMetricsServiceBindingsHandler(service metrics.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		if err := service.ValidateServiceScope(ctx.Request.Context(), ctx.Param("productId"), ctx.Param("id")); err != nil {
			writeMetricsError(ctx, err)
			return
		}
		bindings, err := service.ListBindings(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.OK(ctx, bindings, gin.H{"total": len(bindings)})
	}
}

func createMetricsServiceBindingHandler(service metrics.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		var body metrics.CreateServiceBindingRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("指标服务绑定请求无效"))
			return
		}
		if err := service.ValidateServiceScope(ctx.Request.Context(), ctx.Param("productId"), ctx.Param("id")); err != nil {
			writeMetricsError(ctx, err)
			return
		}
		pathServiceID := strings.TrimSpace(ctx.Param("id"))
		if body.ServiceID != "" && strings.TrimSpace(body.ServiceID) != pathServiceID {
			writeError(ctx, apperr.InvalidRequest("service_id 与路径服务不一致"))
			return
		}
		body.ServiceID = pathServiceID
		binding, err := service.CreateBinding(ctx.Request.Context(), subject, body)
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.Created(ctx, binding)
	}
}

func updateMetricsServiceBindingHandler(service metrics.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		var body metrics.UpdateServiceBindingRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("指标服务绑定更新请求无效"))
			return
		}
		if err := service.ValidateServiceScope(ctx.Request.Context(), ctx.Param("productId"), ctx.Param("id")); err != nil {
			writeMetricsError(ctx, err)
			return
		}
		binding, err := service.UpdateServiceBinding(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), strings.TrimSpace(ctx.Param("bindingId")), body)
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.OK(ctx, binding, gin.H{})
	}
}

func probeMetricsServiceBindingHandler(service metrics.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := metricsSubject(ctx)
		if !ok {
			return
		}
		if err := service.ValidateServiceScope(ctx.Request.Context(), ctx.Param("productId"), ctx.Param("id")); err != nil {
			writeMetricsError(ctx, err)
			return
		}
		binding, err := service.ProbeServiceBinding(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), strings.TrimSpace(ctx.Param("bindingId")))
		if err != nil {
			writeMetricsError(ctx, err)
			return
		}
		response.OK(ctx, binding, gin.H{})
	}
}

func metricsSubject(ctx *gin.Context) (platformrbac.Subject, bool) {
	subject, ok := authctx.SubjectFrom(ctx.Request.Context())
	if !ok || subject.ID == "" || subject.Type == "" {
		response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
		return platformrbac.Subject{}, false
	}
	return subject, true
}

func writeMetricsError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, metrics.ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权管理该服务的指标配置")
	default:
		writeError(ctx, err)
	}
}
