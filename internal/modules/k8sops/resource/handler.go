package resource

import (
	"net/http"
	"strconv"

	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func ListHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := ListFilter{
			ClusterID:  ctx.Query("cluster_id"),
			Namespace:  ctx.Query("namespace"),
			APIVersion: ctx.Query("api_version"),
			Kind:       ctx.Query("kind"),
			Query:      ctx.Query("q"),
			Page:       parsePositiveInt(ctx.DefaultQuery("page", "1"), 1),
			PageSize:   parsePositiveInt(ctx.DefaultQuery("page_size", "20"), 20),
			Sort:       ctx.DefaultQuery("sort", "name"),
			Order:      ctx.DefaultQuery("order", "asc"),
		}
		items, err := service.List(ctx.Request.Context(), filter)
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_resource_list_failed", "资源列表查询失败")
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items), "page": filter.Page, "page_size": filter.PageSize})
	}
}

func DetailHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		detail, err := service.GetDetail(ctx.Request.Context(), DetailQuery{Identity: identityFromQuery(ctx)})
		if err != nil {
			response.Error(ctx, http.StatusNotFound, "k8s_resource_not_found", "资源不存在")
			return
		}
		response.OK(ctx, detail, gin.H{})
	}
}

func YAMLHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		yaml, err := service.GetYAML(ctx.Request.Context(), DetailQuery{Identity: identityFromQuery(ctx)})
		if err != nil {
			response.Error(ctx, http.StatusNotFound, "k8s_resource_not_found", "资源不存在")
			return
		}
		response.OK(ctx, yaml, gin.H{})
	}
}

func PodLogsHandler(service Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		logs, err := service.GetPodLogs(ctx.Request.Context(), PodLogQuery{
			ClusterID: ctx.Query("cluster_id"),
			Namespace: ctx.Query("namespace"),
			Pod:       ctx.Query("pod"),
			Container: ctx.Query("container"),
		})
		if err != nil {
			response.Error(ctx, http.StatusInternalServerError, "k8s_pod_logs_failed", "Pod 日志查询失败")
			return
		}
		response.OK(ctx, logs, gin.H{})
	}
}

func identityFromQuery(ctx *gin.Context) Identity {
	return Identity{
		ClusterID:  ctx.Query("cluster_id"),
		Namespace:  ctx.Query("namespace"),
		APIVersion: ctx.Query("api_version"),
		Kind:       ctx.Query("kind"),
		Name:       ctx.Query("name"),
		UID:        ctx.Query("uid"),
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
