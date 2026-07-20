package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"novaapm/internal/logs"
	"novaapm/internal/platform/authctx"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"
)

func getLogsCollectorRuntimeStatusHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if _, ok := authctx.SubjectFrom(ctx.Request.Context()); !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := service.CheckK8sCollectorRuntimeStatus(ctx.Request.Context(), logs.K8sCollectorRuntimeStatusRequest{
			ClusterID: ctx.Query("cluster_id"),
			Namespace: ctx.Query("namespace"),
		})
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func publishLogsCollectorRuntimeHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.K8sCollectorRuntimePublishRequest
		if err := ctx.ShouldBindJSON(&body); err != nil && ctx.Request.ContentLength > 0 {
			writeError(ctx, apperr.InvalidRequest("logs_collector 运行时发布请求无效"))
			return
		}
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := service.PublishK8sCollectorRuntime(ctx.Request.Context(), subject, body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}
