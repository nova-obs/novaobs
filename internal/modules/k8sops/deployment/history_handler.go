package deployment

import (
	"errors"
	"net/http"
	"strconv"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func HistoryHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := listFilterFromQuery(ctx)
		items, err := service.ListHistory(ctx.Request.Context(), filter)
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_deployment_history_failed", "部署历史查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func AuditEventsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := listFilterFromQuery(ctx)
		items, err := service.ListAuditEvents(ctx.Request.Context(), filter)
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_audit_events_failed", "操作审计查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func PreviewHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body OperationRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Preview(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeDeploymentError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func ApplyHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body OperationRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Apply(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeDeploymentError(ctx, err)
			return
		}
		response.Created(ctx, result)
	}
}

func DeleteHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body DeleteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Delete(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeDeploymentError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func RollbackHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body RollbackRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Rollback(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeDeploymentError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func listFilterFromQuery(ctx *gin.Context) ListFilter {
	return ListFilter{
		ClusterID: ctx.Query("cluster_id"),
		Namespace: ctx.Query("namespace"),
		Query:     ctx.Query("q"),
		Page:      parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
		PageSize:  parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
		Sort:      ctx.DefaultQuery("sort", "created_at"),
		Order:     ctx.DefaultQuery("order", "desc"),
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func writeDeploymentError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行发布部署操作")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "发布部署请求参数不完整")
	default:
		response.Error(ctx, http.StatusInternalServerError, "k8s_deployment_operation_failed", "发布部署操作失败")
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}
