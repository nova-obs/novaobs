package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"novaobs/internal/collectorconfig"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/opamp"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

type serviceBaseConfigRequest struct {
	BaseYAML string `json:"base_yaml"`
}

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

func putServicePipelineBaseHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		var body serviceBaseConfigRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("公共处理片段请求无效"))
			return
		}
		template, err := service.UpsertServiceBaseConfig(bg, svc.ID, body.BaseYAML)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, template, gin.H{})
	}
}

func regenerateServicePipelineEnrichmentHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		patch, err := service.RegenerateEnrichmentPatch(bg, svc.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, patch, gin.H{})
	}
}

func putServicePipelineParserRuleHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		var body collectorconfig.ServiceParserRule
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("服务解析规则请求无效"))
			return
		}
		rule, err := service.UpsertParserRule(bg, svc.ID, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, rule, gin.H{})
	}
}

func previewServicePipelineParserRuleHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		var body collectorconfig.ParserPreviewRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("解析预览请求无效"))
			return
		}
		result, err := service.PreviewParserRule(bg, svc.ID, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if !result.Valid && len(result.Errors) > 0 {
			writeError(ctx, apperr.InvalidRequest(result.Errors[0]))
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func generateServicePipelineParserPatchHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		patch, err := service.GeneratePipelinePatch(bg, svc.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, patch, gin.H{})
	}
}

func getServicePipelineSourcesHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		sources, err := service.ServiceConfigSources(bg, svc.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, sources, gin.H{})
	}
}

func publishServicePipelineHandler(repo servicecatalog.Repository, configService collectorconfig.Service, collectorService collectormanagement.Service, manager *opamp.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		validation, err := configService.ValidateServicePipeline(bg, svc.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if !validation.Valid {
			writeError(ctx, apperr.InvalidRequest(strings.Join(validation.Errors, "; ")))
			return
		}
		instances, err := collectorService.ListInstancesByService(bg, svc.ID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		deployment := collectorconfig.RemoteConfigDeployment{
			ID:                   fmt.Sprintf("service:%s:%s", svc.ID, validation.ConfigHash),
			CollectorInstanceUID: fmt.Sprintf("service:%s", svc.ID),
			Version:              1,
			ConfigHash:           validation.ConfigHash,
			CollectorYAML:        validation.RenderedYAML,
			Status:               "pending",
		}
		activeDeliveryCount := 0
		if manager != nil {
			activeDeliveryCount, err = manager.SendServiceDeployment(bg, svc.ID, deployment)
			if err != nil {
				writeError(ctx, err)
				return
			}
		}
		if activeDeliveryCount == 0 {
			for _, instance := range instances {
				if instance.Online && instance.RemoteConfigCapable {
					activeDeliveryCount++
					if manager != nil {
						item := deployment
						item.CollectorInstanceUID = instance.InstanceUID
						manager.QueueDeployment(item)
					}
				}
			}
		}
		response.OK(ctx, gin.H{
			"service_id":            svc.ID,
			"config_hash":           validation.ConfigHash,
			"rendered_yaml":         validation.RenderedYAML,
			"active_delivery_count": activeDeliveryCount,
			"agent_count":           len(instances),
		}, gin.H{})
	}
}
