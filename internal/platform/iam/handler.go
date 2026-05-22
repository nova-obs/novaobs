package iam

import (
	"errors"
	"net/http"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func MeHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		item, err := service.Me(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, item, gin.H{})
	}
}

func ListUsersHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListUsers(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func CreateUserHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req CreateUserRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台用户参数无效")
			return
		}
		result, err := service.CreateUser(ctx.Request.Context(), subjectFromRequest(ctx), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func ListGroupsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListGroups(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func CreateGroupHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req CreateGroupRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台用户组参数无效")
			return
		}
		result, err := service.CreateGroup(ctx.Request.Context(), subjectFromRequest(ctx), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func ListServiceAccountsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListServiceAccounts(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func CreateServiceAccountHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req CreateServiceAccountRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台服务账号参数无效")
			return
		}
		result, err := service.CreateServiceAccount(ctx.Request.Context(), subjectFromRequest(ctx), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func ListSubjectsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.Subjects(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func ListRolesHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListRoles(ctx.Request.Context(), subjectFromRequest(ctx))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func CreateRoleHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req CreateRoleRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台角色参数无效")
			return
		}
		result, err := service.CreateRole(ctx.Request.Context(), subjectFromRequest(ctx), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

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

func CreateBindingHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req CreateBindingRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台授权绑定参数无效")
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

func writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权管理平台用户权限")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "平台用户权限参数无效")
	case errors.Is(err, ErrNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "平台用户权限资源不存在")
	default:
		response.Error(ctx, http.StatusInternalServerError, "platform_iam_failed", "平台用户权限操作失败")
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}
