package serviceaccount

import (
	"errors"
	"net/http"
	"strconv"

	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/platform/authctx"
	"novaobs/internal/platform/rbac"
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
		}
		items, err := service.List(ctx.Request.Context(), filter)
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_service_account_list_failed", "ServiceAccount 列表查询失败")
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
			writeServiceAccountError(ctx, err)
			return
		}
		response.Created(ctx, gin.H{"item": item, "audit_id": event.ID})
	}
}

func DeleteHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		event, err := service.Delete(ctx.Request.Context(), subjectFromRequest(ctx), DeleteRequest{
			ClusterID: ctx.Query("cluster_id"),
			Namespace: ctx.Query("namespace"),
			Name:      ctx.Query("name"),
			UID:       ctx.Query("uid"),
		})
		if err != nil {
			writeServiceAccountError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"status": "deleted", "audit_id": event.ID}, gin.H{})
	}
}

func writeServiceAccountError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行 ServiceAccount 操作")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "ServiceAccount 请求参数不完整")
	case errors.Is(err, ErrNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "ServiceAccount 不存在")
	case errors.Is(err, ErrWriteUnavailable):
		response.Error(ctx, http.StatusConflict, "k8s_service_account_write_unavailable", "真实集群 ServiceAccount 写操作尚未启用")
	case errors.Is(err, cluster.ErrClusterReadOnly):
		response.Error(ctx, http.StatusForbidden, "k8s_cluster_read_only", "当前集群为只读接入，已阻断 ServiceAccount 写操作")
	default:
		response.Error(ctx, http.StatusInternalServerError, "k8s_service_account_operation_failed", "ServiceAccount 操作失败")
	}
}

func subjectFromRequest(ctx *gin.Context) rbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
