package metrics

import (
	"errors"
	"net/http"

	"novaapm/internal/platform/authctx"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func RegisterIntegrationRoutes(api *gin.RouterGroup, service IntegrationService) {
	api.GET("/metrics/integrations", integrationListHandler(service))
	api.GET("/metrics/overview", integrationOverviewHandler(service))
	api.POST("/metrics/integrations", integrationCreateHandler(service))
	api.GET("/metrics/integrations/:integrationId", integrationGetHandler(service))
	api.PATCH("/metrics/integrations/:integrationId", integrationUpdateHandler(service))
	api.POST("/metrics/integrations/:integrationId/reconcile-sources", integrationReconcileSourcesHandler(service))
	api.POST("/metrics/integrations/:integrationId/verify", integrationVerifyHandler(service))
	api.POST("/metrics/integrations/:integrationId/log-derived-source", logDerivedSourceHandler(service))
	api.GET("/metrics/write-destinations/options", writeDestinationOptionsHandler(service))
	api.GET("/metrics/dashboard-options", dashboardOptionsHandler(service))
	api.PATCH("/metrics/source-accesses/:sourceAccessId", sourceAccessUpdateHandler(service))
	api.GET("/metrics/source-accesses/:sourceAccessId/handoff", sourceHandoffHandler(service))
	api.POST("/metrics/source-accesses/:sourceAccessId/managed-release/preview", managedReleasePreviewHandler(service))
	api.POST("/metrics/source-accesses/:sourceAccessId/managed-release/apply", managedReleaseApplyHandler(service))
	api.POST("/metrics/source-accesses/:sourceAccessId/assess", sourceAssessmentHandler(service))
}

func integrationUpdateHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		var request UpdateIntegrationRequest
		if err := ctx.ShouldBindJSON(&request); err != nil {
			return nil, apperr.InvalidRequest("环境指标接入更新参数无效")
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.UpdateIntegration(ctx.Request.Context(), subject, ctx.Param("integrationId"), request)
	})
}

func integrationReconcileSourcesHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.ReconcileSources(ctx.Request.Context(), subject, ctx.Param("integrationId"))
	})
}

func dashboardOptionsHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.ListDashboardOptions(ctx.Request.Context(), subject, ctx.Query("environment_id"))
	})
}

func logDerivedSourceHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.EnableLogDerivedSource(ctx.Request.Context(), subject, ctx.Param("integrationId"))
	})
}

func sourceAssessmentHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.AssessSource(ctx.Request.Context(), subject, ctx.Param("sourceAccessId"))
	})
}

func managedReleasePreviewHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		var request PreviewCollectorReleaseRequest
		if err := ctx.ShouldBindJSON(&request); err != nil && ctx.Request.ContentLength > 0 {
			return nil, apperr.InvalidRequest("受管采集器预览参数无效")
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.PreviewManagedCollector(ctx.Request.Context(), subject, ctx.Param("sourceAccessId"), request)
	})
}

func managedReleaseApplyHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.ApplyManagedCollector(ctx.Request.Context(), subject, ctx.Param("sourceAccessId"))
	})
}

func integrationOverviewHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.ListOverview(ctx.Request.Context(), subject)
	})
}

func integrationVerifyHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.VerifyIntegration(ctx.Request.Context(), subject, ctx.Param("integrationId"))
	})
}

func sourceHandoffHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.GetSourceHandoff(ctx.Request.Context(), subject, ctx.Param("sourceAccessId"))
	})
}

func writeDestinationOptionsHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.ListWriteDestinationOptions(ctx.Request.Context(), subject, ctx.Query("environment_id"))
	})
}

func integrationListHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.ListIntegrations(ctx.Request.Context(), subject)
	})
}

func integrationCreateHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		var request CreateIntegrationRequest
		if err := ctx.ShouldBindJSON(&request); err != nil {
			return nil, apperr.InvalidRequest("环境指标接入参数无效")
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.CreateIntegration(ctx.Request.Context(), subject, request)
	})
}

func integrationGetHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.GetIntegration(ctx.Request.Context(), subject, ctx.Param("integrationId"))
	})
}

func sourceAccessUpdateHandler(service IntegrationService) gin.HandlerFunc {
	return integrationOperation(func(ctx *gin.Context) (any, error) {
		var request UpdateSourceAccessRequest
		if err := ctx.ShouldBindJSON(&request); err != nil {
			return nil, apperr.InvalidRequest("指标来源接入参数无效")
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		return service.UpdateSourceAccess(ctx.Request.Context(), subject, ctx.Param("sourceAccessId"), request)
	})
}

func integrationOperation(operation func(*gin.Context) (any, error)) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if _, ok := authctx.SubjectFrom(ctx.Request.Context()); !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		value, err := operation(ctx)
		if err != nil {
			writeIntegrationError(ctx, err)
			return
		}
		response.OK(ctx, value, gin.H{})
	}
}

func writeIntegrationError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权访问该环境的指标接入")
	case errors.Is(err, ErrIntegrationNotFound), errors.Is(err, ErrSourceAccessNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "指标接入或来源不存在")
	case errors.Is(err, ErrIntegrationAlreadyExists):
		response.Error(ctx, http.StatusConflict, "conflict", "该环境已经存在指标接入")
	case errors.Is(err, ErrEnvironmentUnavailable):
		response.Error(ctx, http.StatusConflict, "environment_unavailable", "环境不存在或已归档")
	case errors.Is(err, ErrDestinationUnavailable):
		response.Error(ctx, http.StatusBadRequest, "destination_unavailable", "写入目标不是可用的 VictoriaMetrics 目标")
	case errors.Is(err, ErrManagedCollectorUnsupported):
		response.Error(ctx, http.StatusConflict, "managed_collector_unsupported", "该来源不支持平台受管采集器")
	case errors.Is(err, ErrCollectorReleaseMismatch):
		response.Error(ctx, http.StatusConflict, "collector_release_mismatch", "受管采集器预览已失效，请重新预览")
	case errors.Is(err, ErrCollectorAlreadyPresent):
		response.Error(ctx, http.StatusConflict, "collector_already_present", "集群已存在 Prometheus 或 vmagent，请复用现有采集器")
	default:
		var appError apperr.Error
		if errors.As(err, &appError) {
			response.Error(ctx, appError.Status, appError.Code, appError.Message)
			return
		}
		response.Error(ctx, http.StatusInternalServerError, "internal_error", "环境指标接入操作失败")
	}
}
