package httpapi

import (
	"errors"
	"net/http"
	"strings"

	obsendpoint "novaapm/internal/observability/endpoint"
	"novaapm/internal/platform/authctx"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func listObservabilityEndpointsHandler(service obsendpoint.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		endpoints, err := service.ListForSubject(ctx.Request.Context(), subject, obsendpoint.ListFilter{
			SignalType: strings.TrimSpace(ctx.Query("signal_type")),
			Kind:       strings.TrimSpace(ctx.Query("kind")),
		})
		if err != nil {
			writeObservabilityEndpointError(ctx, err)
			return
		}
		response.OK(ctx, endpoints, gin.H{"total": len(endpoints)})
	}
}

func testObservabilityEndpointHandler(service obsendpoint.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := service.TestForSubject(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeObservabilityEndpointError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func writeObservabilityEndpointError(ctx *gin.Context, err error) {
	if errors.Is(err, obsendpoint.ErrPermissionDenied) {
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权查看观测端点")
		return
	}
	writeError(ctx, err)
}
