package rbac

import (
	"errors"
	"net/http"
	"strconv"

	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListRolesHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := listFilterFromQuery(ctx)
		items, err := service.ListRoles(ctx.Request.Context(), filter)
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func CreateRoleHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body RoleRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		item, event, err := service.CreateRole(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.Created(ctx, gin.H{"item": item, "audit_id": event.ID})
	}
}

func UpdateRoleHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body RoleRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		item, event, err := service.UpdateRole(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"item": item, "audit_id": event.ID}, gin.H{})
	}
}

func DeleteRoleHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		event, err := service.DeleteRole(ctx.Request.Context(), subjectFromRequest(ctx), deleteRequestFromQuery(ctx))
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"status": "deleted", "audit_id": event.ID}, gin.H{})
	}
}

func ListBindingsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := listFilterFromQuery(ctx)
		items, err := service.ListBindings(ctx.Request.Context(), filter)
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func CreateBindingHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body BindingRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		item, event, err := service.CreateBinding(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.Created(ctx, gin.H{"item": item, "audit_id": event.ID})
	}
}

func DeleteBindingHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		event, err := service.DeleteBinding(ctx.Request.Context(), subjectFromRequest(ctx), deleteRequestFromQuery(ctx))
		if err != nil {
			writeRBACError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"status": "deleted", "audit_id": event.ID}, gin.H{})
	}
}

func writeRBACError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行 K8s RBAC 操作")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "K8s RBAC 请求参数不完整")
	case errors.Is(err, ErrNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "K8s RBAC 资源不存在")
	case errors.Is(err, ErrAlreadyExists):
		response.Error(ctx, http.StatusConflict, "already_exists", "K8s RBAC 资源已存在")
	case errors.Is(err, ErrWriteUnavailable):
		response.Error(ctx, http.StatusConflict, "k8s_rbac_write_unavailable", "真实集群 K8s RBAC 写操作尚未启用")
	case errors.Is(err, cluster.ErrClusterReadOnly):
		response.Error(ctx, http.StatusForbidden, "k8s_cluster_read_only", "当前集群为只读接入，已阻断 K8s RBAC 写操作")
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_rbac_operation_failed", "K8s RBAC 操作失败", err)
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}

func listFilterFromQuery(ctx *gin.Context) ListFilter {
	return ListFilter{
		ClusterID: ctx.Query("cluster_id"),
		Namespace: ctx.Query("namespace"),
		Page:      parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
		PageSize:  parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
	}
}

func deleteRequestFromQuery(ctx *gin.Context) DeleteRequest {
	return DeleteRequest{
		ClusterID: ctx.Query("cluster_id"),
		Namespace: ctx.Query("namespace"),
		Kind:      ctx.Query("kind"),
		Name:      ctx.Query("name"),
		UID:       ctx.Query("uid"),
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
