package deployment

import (
	"net/http"
	"strconv"

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
