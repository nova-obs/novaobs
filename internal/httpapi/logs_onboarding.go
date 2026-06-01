package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"novaobs/internal/logs"
	k8sopscluster "novaobs/internal/modules/k8sops/cluster"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/platform/authctx"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func getLogsOnboardingWorkspaceHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		workspace, err := service.Workspace(ctx.Request.Context())
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, workspace, gin.H{})
	}
}

func getLogsK8sWorkloadsHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListK8sWorkloads(ctx.Request.Context(), ctx.Query("cluster_id"), ctx.Query("namespace"))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func syncLogsK8sServicesHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.SyncK8sNamespaceRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("K8s 服务同步请求无效"))
			return
		}
		result, err := service.SyncK8sNamespaceServices(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{"total": result.Total})
	}
}

func createLogsEndpointHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.LogEndpoint
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("VictoriaLogs 端点请求无效"))
			return
		}
		endpoint, err := service.CreateEndpoint(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.Created(ctx, endpoint)
	}
}

func listLogsEndpointsHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		endpoints, err := service.ListEndpoints(ctx.Request.Context())
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, endpoints, gin.H{"total": len(endpoints)})
	}
}

func previewLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.UpsertRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志接入预览请求无效"))
			return
		}
		preview, err := service.PreviewRoute(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, preview, gin.H{})
	}
}

func previewLogsParseRulesHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.ParsePreviewRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志解析预览请求无效"))
			return
		}
		result, err := service.PreviewParseRules(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func createLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.UpsertRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志接入路由请求无效"))
			return
		}
		route, err := service.CreateRoute(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.Created(ctx, route)
	}
}

func probeLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		result, err := service.ProbeRoute(ctx.Request.Context(), strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func publishLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.PublishRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil && ctx.Request.ContentLength > 0 {
			writeError(ctx, apperr.InvalidRequest("日志接入发布请求无效"))
			return
		}
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := service.PublishRoute(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeLogsError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, k8sopscluster.ErrClusterReadOnly):
		response.Error(ctx, http.StatusForbidden, "k8s_cluster_read_only", "当前集群为只读接入，只能生成配置预览，不能发布 Agent")
	case errors.Is(err, k8sopsdeployment.ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权发布 K8s 日志采集 Agent")
	case errors.Is(err, k8sopsdeployment.ErrConfirmationMismatch):
		response.Error(ctx, http.StatusBadRequest, "confirmation_mismatch", "预览确认已失效，请重新预览后再执行")
	case errors.Is(err, k8sopsdeployment.ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "K8s Agent 发布请求无效")
	default:
		writeError(ctx, err)
	}
}
