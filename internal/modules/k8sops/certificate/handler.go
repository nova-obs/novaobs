package certificate

import (
	"errors"
	"net/http"
	"strconv"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := ListFilter{
			ClusterID: ctx.Query("cluster_id"),
			Namespace: ctx.Query("namespace"),
			Query:     ctx.Query("q"),
			Page:      parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
			PageSize:  parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
			Sort:      ctx.DefaultQuery("sort", "not_after"),
			Order:     ctx.DefaultQuery("order", "asc"),
		}
		items, err := service.List(ctx.Request.Context(), filter)
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_certificate_list_failed", "证书列表查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func CreateHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body CreateRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		item, event, err := service.Create(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeCertificateError(ctx, err)
			return
		}
		response.Created(ctx, gin.H{"item": item, "audit_id": event.ID})
	}
}

func DeleteHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		event, err := service.Delete(ctx.Request.Context(), subjectFromRequest(ctx), DeleteRequest{ID: ctx.Param("id")})
		if err != nil {
			writeCertificateError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"status": "deleted", "audit_id": event.ID}, gin.H{})
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func writeCertificateError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行证书操作")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "证书请求参数不完整")
	case errors.Is(err, ErrNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "证书不存在")
	case errors.Is(err, ErrAlreadyExists):
		response.Error(ctx, http.StatusConflict, "already_exists", "证书已存在")
	default:
		response.Error(ctx, http.StatusInternalServerError, "k8s_certificate_operation_failed", "证书操作失败")
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}
