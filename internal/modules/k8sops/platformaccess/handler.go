package platformaccess

import (
	"errors"
	"net/http"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListBindingsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListBindings(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func PermissionsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.Permissions(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func ProfilesHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.Profiles(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func ListSubjectsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListSubjects(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func CreateBindingHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req CreateBindingRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "授权绑定参数无效")
			return
		}
		result, err := service.CreateBinding(ctx.Request.Context(), subjectFromRequest(ctx), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func DeleteBindingHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		result, err := service.DeleteBinding(ctx.Request.Context(), subjectFromRequest(ctx), ctx.Param("id"))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权管理平台 K8s 授权")
	case errors.Is(err, ErrRiskConfirmation):
		response.Error(ctx, http.StatusBadRequest, "k8s_platform_risk_confirmation_required", "高风险 K8s 授权需要显式确认")
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrPermissionScope), errors.Is(err, ErrUnsupportedPerm):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台 K8s 授权参数无效")
	case errors.Is(err, ErrSubjectNotFound):
		response.Error(ctx, http.StatusNotFound, "k8s_platform_subject_not_found", "平台 K8s 授权主体不存在")
	case errors.Is(err, ErrBindingNotFound):
		response.Error(ctx, http.StatusNotFound, "k8s_platform_binding_not_found", "平台 K8s 授权绑定不存在")
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_platform_access_failed", "平台 K8s 授权操作失败", err)
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}
