package terminal

import (
	"errors"
	"net/http"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func ExecHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body ExecRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "请求体格式不正确")
			return
		}
		result, err := service.Exec(ctx.Request.Context(), subjectFromRequest(ctx), body)
		if err != nil {
			writeTerminalError(ctx, err, result)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeTerminalError(ctx *gin.Context, err error, result ExecResult) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权执行 K8s 终端命令")
	case errors.Is(err, ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "K8s 终端请求参数不完整")
	case errors.Is(err, ErrCommandBlocked):
		ctx.JSON(http.StatusBadRequest, response.Envelope{
			Success: false,
			Data:    result,
			Error:   &response.ErrorBody{Code: "k8s_terminal_command_blocked", Message: "命令不符合 NovaObs 终端安全策略"},
			Meta:    gin.H{},
		})
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "k8s_terminal_exec_failed", "K8s 终端命令执行失败", err)
	}
}

func subjectFromRequest(ctx *gin.Context) platformrbac.Subject {
	subject, _ := authctx.SubjectFrom(ctx.Request.Context())
	return subject
}
