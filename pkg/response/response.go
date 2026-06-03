package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	ErrorStatusKey  = "novaobs.error.status"
	ErrorCodeKey    = "novaobs.error.code"
	ErrorMessageKey = "novaobs.error.message"
)

type Envelope struct {
	Success bool       `json:"success"`
	Data    any        `json:"data"`
	Error   *ErrorBody `json:"error"`
	Meta    any        `json:"meta"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(ctx *gin.Context, data any, meta any) {
	ctx.JSON(http.StatusOK, Envelope{
		Success: true,
		Data:    data,
		Error:   nil,
		Meta:    meta,
	})
}

func Created(ctx *gin.Context, data any) {
	ctx.JSON(http.StatusCreated, Envelope{
		Success: true,
		Data:    data,
		Error:   nil,
		Meta:    gin.H{},
	})
}

func Error(ctx *gin.Context, status int, code string, message string) {
	ctx.Set(ErrorStatusKey, status)
	ctx.Set(ErrorCodeKey, code)
	ctx.Set(ErrorMessageKey, message)
	ctx.JSON(status, Envelope{
		Success: false,
		Data:    nil,
		Error: &ErrorBody{
			Code:    code,
			Message: message,
		},
		Meta: gin.H{},
	})
}

func ErrorWithCause(ctx *gin.Context, status int, code string, message string, cause error) {
	if cause != nil {
		_ = ctx.Error(cause)
	}
	Error(ctx, status, code, message)
}
