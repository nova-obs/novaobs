package httpapi

import (
	"log/slog"
	"sort"

	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func errorLogMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Next()
		status := ctx.Writer.Status()
		if status < 400 {
			return
		}
		attrs := []any{
			"status", status,
			"method", ctx.Request.Method,
			"path", requestLogPath(ctx),
		}
		if code, ok := ctx.Get(response.ErrorCodeKey); ok {
			attrs = append(attrs, "code", code)
		}
		if message, ok := ctx.Get(response.ErrorMessageKey); ok {
			attrs = append(attrs, "message", message)
		}
		if requestID := ctx.GetHeader("X-Request-ID"); requestID != "" {
			attrs = append(attrs, "request_id", requestID)
		}
		if keys := queryKeys(ctx); len(keys) > 0 {
			attrs = append(attrs, "query_keys", keys)
		}
		if len(ctx.Errors) > 0 {
			attrs = append(attrs, "error", ctx.Errors.Last().Err)
		}
		switch {
		case status >= 500:
			slog.Error("HTTP 请求处理失败", attrs...)
		case status == 403 || status == 409:
			slog.Warn("HTTP 请求被业务策略阻断", attrs...)
		default:
			slog.Debug("HTTP 请求返回客户端错误", attrs...)
		}
	}
}

func requestLogPath(ctx *gin.Context) string {
	if fullPath := ctx.FullPath(); fullPath != "" {
		return fullPath
	}
	return ctx.Request.URL.Path
}

func queryKeys(ctx *gin.Context) []string {
	query := ctx.Request.URL.Query()
	if len(query) == 0 {
		return nil
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
