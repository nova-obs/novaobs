package httpapi

import (
	"net/http"
	"strings"

	"novaapm/internal/alerting"
	"novaapm/internal/platform/authctx"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func publishMetricsAlertRuntimeHandler(service alerting.MetricsRuntimeService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var request alerting.LogRuntimePublishRequest
		if err := ctx.ShouldBindJSON(&request); err != nil && ctx.Request.ContentLength > 0 {
			writeError(ctx, apperr.InvalidRequest("指标告警运行时发布请求无效"))
			return
		}
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := service.Publish(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), request)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}
