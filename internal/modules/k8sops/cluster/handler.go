package cluster

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/response"

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
			response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_cluster_list_failed", "集群列表查询失败", err)
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

func DeleteHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if err := service.Delete(ctx.Request.Context(), ctx.Param("id")); err != nil {
			writeClusterError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"deleted": true}, gin.H{})
	}
}

func CapabilityHandler(service CapabilityService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		snapshot, err := service.Get(ctx.Request.Context(), ctx.Param("id"))
		if err != nil {
			writeCapabilityError(ctx, err)
			return
		}
		response.OK(ctx, snapshot, gin.H{"total": len(snapshot.Resources)})
	}
}

func ProbeHandler(clusterService Service, capabilityService CapabilityService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		cluster, err := clusterService.Get(ctx.Request.Context(), ctx.Param("id"))
		if err != nil {
			writeClusterError(ctx, err)
			return
		}
		result, err := capabilityService.Probe(ctx.Request.Context(), cluster)
		if err != nil {
			writeCapabilityError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{"source": "k8s.capabilities"})
	}
}

func writeClusterError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidClusterRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "集群 ID 和名称不能为空")
	case errors.Is(err, ErrClusterRepositoryWrite):
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_cluster_write_unavailable", "集群仓储暂不支持写入", err)
	case errors.Is(err, ErrClusterNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "集群不存在")
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_cluster_operation_failed", "集群操作失败", err)
	}
}

func writeCapabilityError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidClusterRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "集群 ID 不能为空")
	case errors.Is(err, ErrClusterCapabilityUnavailable):
		response.ErrorWithCause(ctx, http.StatusServiceUnavailable, "k8s_cluster_capability_unavailable", "集群能力解析服务尚未接入", err)
	case errors.Is(err, ErrCredentialPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权读取集群能力")
	case errors.Is(err, ErrCredentialNotFound):
		response.Error(ctx, http.StatusConflict, "k8s_cluster_credential_required", "当前集群尚未录入可用 kubeconfig")
	default:
		slog.Warn("K8s 集群能力查询失败", "error", err)
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_cluster_capability_failed", "集群能力查询失败", err)
	}
}

func ListCredentialHandler(service CredentialService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.List(ctx.Request.Context(), CredentialListFilter{ClusterID: ctx.Query("cluster_id")})
		if err != nil {
			response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_cluster_credentials_failed", "集群凭据查询失败", err)
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

func RollbackCredentialHandler(service CredentialService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body RollbackCredentialRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Rollback(ctx.Request.Context(), subjectFromRequest(ctx), body)
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
	case errors.Is(err, ErrCredentialNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "集群凭据版本不存在")
	case errors.Is(err, ErrCredentialValidationFailed):
		response.Error(ctx, http.StatusConflict, "k8s_cluster_credential_validation_failed", "集群凭据连接探测失败")
	case errors.Is(err, ErrInvalidCredentialRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "集群凭据请求参数不完整")
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_cluster_credential_failed", "集群凭据操作失败", err)
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
