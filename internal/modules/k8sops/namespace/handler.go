package namespace

import (
	"net/http"
	"strconv"

	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := ListFilter{
			ClusterID: ctx.Query("cluster_id"),
			Query:     ctx.Query("q"),
			Page:      parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
			PageSize:  parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
			Sort:      ctx.DefaultQuery("sort", "name"),
			Order:     ctx.DefaultQuery("order", "asc"),
		}
		items, err := service.List(ctx.Request.Context(), filter)
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_namespace_list_failed", "命名空间列表查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
