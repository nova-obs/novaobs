package cluster

import (
	"errors"
	"log/slog"
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
			Query:    ctx.Query("q"),
			Page:     parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
			PageSize: parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
			Sort:     ctx.DefaultQuery("sort", "name"),
			Order:    ctx.DefaultQuery("order", "asc"),
		}
		items, err := service.List(ctx.Request.Context(), filter)
		if err != nil {
			slog.Warn("K8s 集群列表查询失败", "error", err)
			response.Error(ctx, http.StatusInternalServerError, "k8s_cluster_list_failed", "集群列表查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func CreateHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body UpsertRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		item, err := service.Create(ctx.Request.Context(), body)
		if err != nil {
			writeClusterError(ctx, err)
			return
		}
		response.Created(ctx, item)
	}
}

func writeClusterError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidClusterRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "集群 ID 和名称不能为空")
	case errors.Is(err, ErrClusterRepositoryWrite):
		response.Error(ctx, http.StatusInternalServerError, "k8s_cluster_write_unavailable", "集群仓储暂不支持写入")
	default:
		response.Error(ctx, http.StatusInternalServerError, "k8s_cluster_operation_failed", "集群操作失败")
	}
}

func ListCredentialHandler(service CredentialService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.List(ctx.Request.Context(), CredentialListFilter{ClusterID: ctx.Query("cluster_id")})
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_cluster_credentials_failed", "集群凭据查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func CreateCredentialHandler(service CredentialService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body UpsertCredentialRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Create(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeCredentialError(ctx, err)
			return
		}
		response.Created(ctx, result)
	}
}

func RotateCredentialHandler(service CredentialService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body UpsertCredentialRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Rotate(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeCredentialError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeCredentialError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrCredentialPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权管理集群凭据")
	case errors.Is(err, ErrInvalidCredentialRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "集群凭据请求参数不完整")
	default:
		response.Error(ctx, http.StatusInternalServerError, "k8s_cluster_credential_failed", "集群凭据操作失败")
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
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
