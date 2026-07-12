package environment

import (
	"errors"
	"net/http"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(api *gin.RouterGroup, service Service) {
	api.GET("/platform/environments", listHandler(service))
	api.POST("/platform/environments", createHandler(service))
	api.GET("/platform/environments/:environmentId", getHandler(service))
	api.PATCH("/platform/environments/:environmentId", updateHandler(service))
	api.POST("/platform/environments/:environmentId/resource-bindings", bindResourceHandler(service))
	api.DELETE("/platform/environments/:environmentId/resource-bindings/:bindingId", unbindResourceHandler(service))
}

func listHandler(service Service) gin.HandlerFunc {
	return withSubject(func(ctx *gin.Context) (any, error) {
		items, err := service.List(ctx.Request.Context(), mustSubject(ctx))
		if err != nil {
			return nil, err
		}
		return items, nil
	})
}

func createHandler(service Service) gin.HandlerFunc {
	return withSubject(func(ctx *gin.Context) (any, error) {
		var request CreateRequest
		if err := ctx.ShouldBindJSON(&request); err != nil {
			return nil, apperr.InvalidRequest("环境参数无效")
		}
		return service.Create(ctx.Request.Context(), mustSubject(ctx), request)
	})
}

func getHandler(service Service) gin.HandlerFunc {
	return withSubject(func(ctx *gin.Context) (any, error) {
		return service.Get(ctx.Request.Context(), mustSubject(ctx), ctx.Param("environmentId"))
	})
}

func updateHandler(service Service) gin.HandlerFunc {
	return withSubject(func(ctx *gin.Context) (any, error) {
		var request UpdateRequest
		if err := ctx.ShouldBindJSON(&request); err != nil {
			return nil, apperr.InvalidRequest("环境参数无效")
		}
		return service.Update(ctx.Request.Context(), mustSubject(ctx), ctx.Param("environmentId"), request)
	})
}

func bindResourceHandler(service Service) gin.HandlerFunc {
	return withSubject(func(ctx *gin.Context) (any, error) {
		var request BindResourceRequest
		if err := ctx.ShouldBindJSON(&request); err != nil {
			return nil, apperr.InvalidRequest("环境资源绑定参数无效")
		}
		return service.BindResource(ctx.Request.Context(), mustSubject(ctx), ctx.Param("environmentId"), request)
	})
}

func unbindResourceHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		if err := service.UnbindResource(ctx.Request.Context(), subject, ctx.Param("environmentId"), ctx.Param("bindingId")); err != nil {
			writeHandlerError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"deleted": true}, gin.H{})
	}
}

func withSubject(operation func(*gin.Context) (any, error)) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if _, ok := authctx.SubjectFrom(ctx.Request.Context()); !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := operation(ctx)
		if err != nil {
			writeHandlerError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func mustSubject(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}

func writeHandlerError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权访问平台环境")
	case errors.Is(err, ErrEnvironmentNotFound), errors.Is(err, ErrBindingNotFound):
		response.Error(ctx, http.StatusNotFound, "not_found", "环境或资源绑定不存在")
	case errors.Is(err, ErrResourceAlreadyBound):
		response.Error(ctx, http.StatusConflict, "conflict", "该资源已归属其他环境")
	case errors.Is(err, ErrEnvironmentArchived):
		response.Error(ctx, http.StatusConflict, "conflict", "已归档环境不能绑定新资源")
	default:
		var appError apperr.Error
		if errors.As(err, &appError) {
			response.Error(ctx, appError.Status, appError.Code, appError.Message)
			return
		}
		response.Error(ctx, http.StatusInternalServerError, "internal_error", "平台环境操作失败")
	}
}
