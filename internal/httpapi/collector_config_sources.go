package httpapi

import (
	"novaapm/internal/collectorconfig"
	"novaapm/internal/opamp"
	"novaapm/internal/servicecatalog"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func listCollectorPlatformTemplatesHandler(service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		templates, err := service.ListTemplates(bg)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, templates, gin.H{"total": len(templates)})
	}
}

func importCollectorPlatformTemplateHandler(service collectorconfig.Service, manager *opamp.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body collectorconfig.ImportTemplateRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("平台模板导入请求无效"))
			return
		}
		if body.BaseYAML == "" && manager != nil && body.SourceAgentUID != "" {
			if detail, ok := manager.GetAgentDetail(body.SourceAgentUID); ok {
				body.BaseYAML = detail.EffectiveConfig
			}
		}
		template, err := service.ImportTemplate(bg, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.Created(ctx, template)
	}
}

func getCollectorPlatformTemplateHandler(service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		template, err := service.GetTemplate(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "平台模板不存在"))
			return
		}
		response.OK(ctx, template, gin.H{})
	}
}

func updateCollectorPlatformTemplateHandler(service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		var body collectorconfig.CollectorPlatformTemplate
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("平台模板更新请求无效"))
			return
		}
		template, err := service.UpdateTemplate(bg, id, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, template, gin.H{})
	}
}

func getServiceEnrichmentPatchHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		patch, err := service.GetEnrichmentPatch(bg, svc.ID)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "服务基础属性补齐 patch 尚未生成"))
			return
		}
		response.OK(ctx, patch, gin.H{})
	}
}

func regenerateServiceEnrichmentPatchHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		var body struct {
			CollectorGroupID string `json:"collector_group_id"`
		}
		_ = ctx.ShouldBindJSON(&body)
		var patch collectorconfig.ServiceEnrichmentPatch
		var err error
		if body.CollectorGroupID != "" {
			patch, err = service.BindServiceToCollectorGroup(bg, svc.ID, body.CollectorGroupID)
		} else {
			patch, err = service.RegenerateEnrichmentPatch(bg, svc.ID)
		}
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, patch, gin.H{})
	}
}

func getServiceParserRuleHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		svc, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		rule, err := service.GetParserRule(bg, svc.ID)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "服务解析规则尚未配置"))
			return
		}
		response.OK(ctx, rule, gin.H{})
	}
}

func putServiceParserRuleHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
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

func previewServiceParserRuleHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
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
		if !result.Valid {
			writeError(ctx, apperr.InvalidRequest(result.Errors[0]))
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func generateServicePipelinePatchHandler(repo servicecatalog.Repository, service collectorconfig.Service) gin.HandlerFunc {
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
