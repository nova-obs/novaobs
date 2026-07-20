package images

import (
	"errors"
	"net/http"

	"novaapm/internal/platform/authctx"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		items, err := service.ListAuthorized(ctx.Request.Context(), subject)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func UpsertHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		var req UpsertRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "镜像配置参数无效")
			return
		}
		item, err := service.UpsertAuthorized(ctx.Request.Context(), subject, req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, item, gin.H{})
	}
}

func writeError(ctx *gin.Context, err error) {
	if errors.Is(err, ErrPermissionDenied) {
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权管理平台运行时镜像")
		return
	}
	var appErr apperr.Error
	if errors.As(err, &appErr) {
		response.Error(ctx, appErr.Status, appErr.Code, appErr.Message)
		return
	}
	response.Error(ctx, http.StatusInternalServerError, "internal_error", "镜像配置操作失败")
}
