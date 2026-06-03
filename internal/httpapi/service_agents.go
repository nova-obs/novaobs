package httpapi

import (
	"net/http"
	"strings"

	"novaobs/internal/collectormanagement"
	"novaobs/internal/opamp"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func listServiceAgentsHandler(repo servicecatalog.Repository, collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		instances, err := collectorService.ListInstancesByService(bg, service.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, instances, gin.H{"total": len(instances)})
	}
}

func assignOpAMPInstanceServiceHandler(manager *opamp.Manager, serviceRepo servicecatalog.Repository, collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := strings.TrimSpace(ctx.Param("uid"))
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}
		var body struct {
			ServiceID string `json:"service_id"`
		}
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("请求 JSON 无效"))
			return
		}
		if strings.TrimSpace(body.ServiceID) == "" {
			writeError(ctx, apperr.InvalidRequest("service_id 不能为空"))
			return
		}
		if _, err := serviceRepo.Get(bg, body.ServiceID); err != nil {
			writeError(ctx, normalizeNotFound(err, "服务不存在"))
			return
		}
		instance, err := collectorService.AssignInstanceService(bg, uid, body.ServiceID)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Instance 不存在"))
			return
		}
		if manager != nil {
			manager.RegisterInstanceService(uid, body.ServiceID)
		}
		response.OK(ctx, instance, gin.H{})
	}
}

func unassignOpAMPInstanceServiceHandler(manager *opamp.Manager, collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := strings.TrimSpace(ctx.Param("uid"))
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}
		_, err := collectorService.UnassignInstanceService(bg, uid)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Instance 不存在"))
			return
		}
		if manager != nil {
			manager.UnregisterInstanceService(uid)
		}
		ctx.Status(http.StatusNoContent)
	}
}
