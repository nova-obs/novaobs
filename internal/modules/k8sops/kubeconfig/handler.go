package kubeconfig

import (
	"errors"
	"net/http"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func CreateHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body CreateRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Create(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeKubeconfigError(ctx, err)
			return
		}
		response.Created(ctx, result)
	}
}

func ExportHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body ExportRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Export(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeKubeconfigError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeKubeconfigError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权导出 Kubeconfig")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "Kubeconfig 请求参数不完整")
	default:
		response.Error(ctx, http.StatusInternalServerError, "k8s_kubeconfig_operation_failed", "Kubeconfig 操作失败")
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}
