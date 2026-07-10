package images

import (
	"errors"
	"net/http"

	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.List(ctx.Request.Context())
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func UpsertHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UpsertRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "镜像配置参数无效")
			return
		}
		item, err := service.Upsert(ctx.Request.Context(), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, item, gin.H{})
	}
}

func writeError(ctx *gin.Context, err error) {
	var appErr apperr.Error
	if errors.As(err, &appErr) {
		response.Error(ctx, appErr.Status, appErr.Code, appErr.Message)
		return
	}
	response.Error(ctx, http.StatusInternalServerError, "internal_error", "镜像配置操作失败")
}
