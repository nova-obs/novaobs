package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"

	"novaobs/internal/database"
	"novaobs/internal/logs"
	obsruntime "novaobs/internal/observability/runtime"
	"novaobs/internal/platform/authctx"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"
)

func listObservabilityRuntimesHandler(store database.ObservabilityRuntimeStore) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if _, ok := authctx.SubjectFrom(ctx.Request.Context()); !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		if store == nil {
			response.OK(ctx, []obsruntime.Runtime{}, gin.H{})
			return
		}
		var runtimes []obsruntime.Runtime
		if err := store.FindAll(ctx.Request.Context(), &runtimes); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, database.ErrNotFound) {
				response.OK(ctx, []obsruntime.Runtime{}, gin.H{})
				return
			}
			writeError(ctx, err)
			return
		}
		response.OK(ctx, runtimes, gin.H{})
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
