package template

import (
	"errors"
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
			Type:     ctx.Query("type"),
			Query:    ctx.Query("q"),
			Page:     parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
			PageSize: parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
		}
		items, err := service.List(ctx.Request.Context(), filter)
		if err != nil {
			response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_template_list_failed", "模板列表查询失败", err)
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
		item, event, err := service.Create(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeTemplateError(ctx, err)
			return
		}
		response.Created(ctx, gin.H{"item": item, "audit_id": event.ID})
	}
}

func UpdateHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body UpsertRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		item, event, err := service.Update(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeTemplateError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"item": item, "audit_id": event.ID}, gin.H{})
	}
}

func DeleteHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		event, err := service.Delete(ctx.Request.Context(), subjectFromRequest(ctx), DeleteRequest{ID: ctx.Param("id")})
		if err != nil {
			writeTemplateError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"status": "deleted", "audit_id": event.ID}, gin.H{})
	}
}

func RenderHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body RenderRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Render(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeTemplateError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func BaseTemplateHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		result, err := BaseTemplate(ctx.Query("type"))
		if err != nil {
			writeTemplateError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeTemplateError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行模板操作")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "模板请求参数不完整")
	case errors.Is(err, ErrNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "模板不存在")
	case errors.Is(err, ErrAlreadyExists):
		response.Error(ctx, http.StatusConflict, "already_exists", "模板已存在")
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_template_operation_failed", "模板操作失败", err)
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
