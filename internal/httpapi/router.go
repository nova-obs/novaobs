package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"novaobs/internal/alerting"
	"novaobs/internal/collectorconfig"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/database"
	"novaobs/internal/logquery"
	"novaobs/internal/modules/k8sops"
	k8sopscertificate "novaobs/internal/modules/k8sops/certificate"
	k8sopscluster "novaobs/internal/modules/k8sops/cluster"
	k8sopsdashboard "novaobs/internal/modules/k8sops/dashboard"
	k8sopsdeployment "novaobs/internal/modules/k8sops/deployment"
	k8sopsnamespace "novaobs/internal/modules/k8sops/namespace"
	k8sopsresource "novaobs/internal/modules/k8sops/resource"
	"novaobs/internal/onboarding"
	"novaobs/internal/opamp"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"
)

// bg is used as background context for store operations
var bg = context.Background()

type Dependencies struct {
	Store                  database.Store
	ServiceRepo            servicecatalog.Repository
	ServiceTargetRepo      servicecatalog.TargetRepository
	CollectorConfigService collectorconfig.Service
	CollectorService       collectormanagement.Service
	OnboardingService      onboarding.Service
	LogQueryService        logquery.Service
	AlertService           alerting.Service
	K8sOpsModule           k8sops.Module
	OpAMPManager           *opamp.Manager
	CollectorTemplate      string
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	if deps.OpAMPManager != nil {
		deps.OpAMPManager.SetStateSink(func(ctx context.Context, state opamp.AgentState) {
			_, _ = deps.CollectorService.UpsertInstance(ctx, state.InstanceUID, state.CollectorGroupID, collectormanagement.InstanceStatus{
				ServiceID:           state.ServiceID,
				Online:              state.Online,
				Healthy:             state.Healthy,
				HealthSet:           state.HealthSet,
				Capabilities:        state.Capabilities,
				RemoteConfigCapable: state.RemoteConfigCapable,
				EffectiveConfigHash: state.EffectiveConfigHash,
				RemoteConfigStatus:  state.RemoteConfigStatus,
				LastConfigHash:      state.LastConfigHash,
				LastError:           state.LastError,
				LastSeenAt:          state.LastSeenAt,
			})
			if state.CollectorGroupID != "" && state.LastConfigHash != "" {
				_, _ = deps.CollectorService.MarkGroupConfigStatus(ctx, state.CollectorGroupID, state.LastConfigHash, state.RemoteConfigStatus, state.LastError)
			}
		})
	}

	api := router.Group("/api/v1")
	api.GET("/health", healthHandler)
	api.GET("/overview", overviewHandler(deps))
	api.GET("/services", listServicesHandler(deps.ServiceRepo))
	api.POST("/services", createServiceHandler(deps.ServiceRepo))
	api.GET("/services/:id", getServiceHandler(deps.ServiceRepo))
	api.PATCH("/services/:id", updateServiceHandler(deps.ServiceRepo))
	api.GET("/services/:id/observability-graph", getServiceObservabilityGraphHandler(deps))
	api.GET("/services/:id/targets", listServiceTargetsHandler(deps.ServiceRepo, deps.ServiceTargetRepo))
	api.POST("/services/:id/targets", createServiceTargetHandler(deps.ServiceRepo, deps.ServiceTargetRepo))
	api.GET("/services/:id/agents", listServiceAgentsHandler(deps.ServiceRepo, deps.CollectorService))
	api.PUT("/services/:id/pipeline/base", putServicePipelineBaseHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/pipeline/enrichment/regenerate", regenerateServicePipelineEnrichmentHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.PUT("/services/:id/pipeline/parser-rule", putServicePipelineParserRuleHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/pipeline/parser-rule/preview", previewServicePipelineParserRuleHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/pipeline/parser-rule/generate-patch", generateServicePipelineParserPatchHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.GET("/services/:id/pipeline/sources", getServicePipelineSourcesHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/pipeline/publish", publishServicePipelineHandler(deps.ServiceRepo, deps.CollectorConfigService, deps.CollectorService, deps.OpAMPManager))
	api.GET("/services/:id/onboarding", getOnboardingHandler(deps.ServiceRepo, deps.OnboardingService))
	api.POST("/services/:id/onboarding", upsertOnboardingHandler(deps.ServiceRepo, deps.OnboardingService))
	api.POST("/services/:id/onboarding/check", checkOnboardingHandler(deps.ServiceRepo, deps.OnboardingService))
	api.GET("/collector-groups", listCollectorGroupsHandler(deps.CollectorService))
	api.POST("/collector-groups", createCollectorGroupHandler(deps.CollectorService))
	api.GET("/collector-groups/:id", getCollectorGroupHandler(deps.CollectorService))
	api.PATCH("/collector-groups/:id", updateCollectorGroupHandler(deps.CollectorService))
	api.POST("/collector-groups/:id/activate", activateCollectorGroupHandler(deps.CollectorService, deps.CollectorConfigService))
	api.DELETE("/collector-groups/:id", deleteCollectorGroupHandler(deps.CollectorService, deps.Store.Onboardings(), deps.Store.ServicePipelinePatches()))
	api.GET("/collector-groups/:id/instances", listCollectorInstancesHandler(deps.CollectorService))
	api.GET("/collector-groups/:id/config-versions", listCollectorGroupConfigVersionsHandler(deps.CollectorService))
	api.GET("/collector-groups/:id/config/latest.yaml", getCollectorGroupLatestConfigYAMLHandler(deps.CollectorService))
	api.GET("/collector-groups/:id/config/sources", getCollectorGroupConfigSourcesHandler(deps.CollectorConfigService))
	api.PUT("/collector-groups/:id/config/override", putCollectorGroupOverrideHandler(deps.CollectorConfigService))
	api.POST("/collector-groups/:id/config/validate", validateCollectorGroupConfigHandler(deps.CollectorConfigService))
	api.POST("/collector-groups/:id/config/publish", publishCollectorGroupConfigHandler(deps.CollectorConfigService, deps.CollectorService, deps.OpAMPManager))
	api.GET("/collector-groups/:id/config/status", getCollectorGroupConfigStatusHandler(deps.CollectorService))
	api.GET("/collector-platform-templates", listCollectorPlatformTemplatesHandler(deps.CollectorConfigService))
	api.POST("/collector-platform-templates/import-from-agent", importCollectorPlatformTemplateHandler(deps.CollectorConfigService, deps.OpAMPManager))
	api.GET("/collector-platform-templates/:id", getCollectorPlatformTemplateHandler(deps.CollectorConfigService))
	api.PUT("/collector-platform-templates/:id", updateCollectorPlatformTemplateHandler(deps.CollectorConfigService))
	api.GET("/services/:id/enrichment-patch", getServiceEnrichmentPatchHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/enrichment-patch/regenerate", regenerateServiceEnrichmentPatchHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.GET("/services/:id/parser-rule", getServiceParserRuleHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.PUT("/services/:id/parser-rule", putServiceParserRuleHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/parser-rule/preview", previewServiceParserRuleHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.POST("/services/:id/parser-rule/generate-patch", generateServicePipelinePatchHandler(deps.ServiceRepo, deps.CollectorConfigService))
	api.GET("/logs", searchLogsHandler(deps.LogQueryService))
	api.GET("/alert-rules", listAlertRulesHandler(deps.AlertService))
	api.POST("/alert-rules", createAlertRuleHandler(deps.AlertService))
	api.GET("/k8sops/dashboard", getK8sOpsDashboardHandler(deps.K8sOpsModule.Dashboard))
	api.GET("/k8s/clusters", k8sopscluster.ListHandler(deps.K8sOpsModule.Cluster))
	api.GET("/k8s/namespaces", k8sopsnamespace.ListHandler(deps.K8sOpsModule.Namespace))
	api.GET("/k8s/resources", k8sopsresource.ListHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/resources/detail", k8sopsresource.DetailHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/resources/yaml", k8sopsresource.YAMLHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/pod-logs", k8sopsresource.PodLogsHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/deployment-history", k8sopsdeployment.HistoryHandler(deps.K8sOpsModule.Deploy))
	api.GET("/k8s/audit-events", k8sopsdeployment.AuditEventsHandler(deps.K8sOpsModule.Deploy))
	api.GET("/k8s/certificates", k8sopscertificate.ListHandler(deps.K8sOpsModule.Cert))
	api.GET("/opamp/agents", listOpAMPAgentsHandler(deps.OpAMPManager, deps.CollectorService))
	api.GET("/opamp/agents/:uid", getOpAMPAgentDetailHandler(deps))
	api.POST("/opamp/instances/:uid/group", registerOpAMPInstanceGroupHandler(deps.OpAMPManager, deps.CollectorService))
	api.POST("/opamp/instances/:uid/service", assignOpAMPInstanceServiceHandler(deps.OpAMPManager, deps.ServiceRepo, deps.CollectorService))
	api.DELETE("/opamp/instances/:uid/service", unassignOpAMPInstanceServiceHandler(deps.OpAMPManager, deps.CollectorService))
	api.DELETE("/opamp/instances/:uid/group", unassignOpAMPInstanceGroupHandler(deps.OpAMPManager, deps.CollectorService))
	api.DELETE("/opamp/instances/:uid", deleteOpAMPInstanceHandler(deps.CollectorService))

	if deps.OpAMPManager != nil {
		router.Any("/v1/opamp", gin.WrapF(deps.OpAMPManager.ServeHTTP))
	}

	router.NoRoute(func(ctx *gin.Context) {
		response.Error(ctx, http.StatusNotFound, "not_found", "资源不存在")
	})

	return router
}

func getK8sOpsDashboardHandler(service k8sopsdashboard.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		snapshot, err := service.Get(ctx.Request.Context(), k8sopsdashboard.Query{
			ClusterID: strings.TrimSpace(ctx.Query("cluster_id")),
		})
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, snapshot, gin.H{"source": "k8sops"})
	}
}

func healthHandler(ctx *gin.Context) {
	response.OK(ctx, gin.H{"status": "ok"}, gin.H{})
}

func overviewHandler(deps Dependencies) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		services, _ := deps.ServiceRepo.List(bg)
		rules, _ := deps.AlertService.List(bg)
		onlineAgents := 0
		if instances, err := deps.CollectorService.ListInstances(ctx.Request.Context(), ""); err == nil {
			for _, instance := range instances {
				if instance.RuntimeStatus == "online" {
					onlineAgents++
				}
			}
		}
		healthyPipelines := 0
		if groups, err := deps.CollectorService.ListGroups(ctx.Request.Context()); err == nil {
			for _, group := range groups {
				if group.LastPublishStatus == "applied" {
					healthyPipelines++
				}
			}
		}
		response.OK(ctx, gin.H{
			"services":               len(services),
			"healthy_pipeline_count": healthyPipelines,
			"alerts":                 len(rules),
			"online_collectors":      onlineAgents,
			"modules": []gin.H{
				{"name": "logs", "status": "active"},
				{"name": "metrics", "status": "planned"},
				{"name": "traces", "status": "planned"},
			},
		}, gin.H{})
	}
}

func listServicesHandler(repo servicecatalog.Repository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		services, err := repo.List(bg, servicecatalog.ListFilter{
			Query:       ctx.Query("q"),
			Environment: ctx.Query("environment"),
			Status:      ctx.Query("status"),
			Source:      ctx.Query("source"),
		})
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, services, gin.H{"total": len(services)})
	}
}

func createServiceHandler(repo servicecatalog.Repository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body servicecatalog.Service
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("服务创建请求无效"))
			return
		}
		service, err := repo.Create(bg, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.Created(ctx, service)
	}
}

func getServiceHandler(repo servicecatalog.Repository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		service, err := repo.Get(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "服务不存在"))
			return
		}
		response.OK(ctx, service, gin.H{})
	}
}

func updateServiceHandler(repo servicecatalog.Repository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		var body servicecatalog.UpdateRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("服务更新请求无效"))
			return
		}
		service, err := repo.Update(bg, id, body)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "服务不存在"))
			return
		}
		response.OK(ctx, service, gin.H{})
	}
}

func getOnboardingHandler(repo servicecatalog.Repository, onboardingService onboarding.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		guide, err := onboardingService.Get(bg, service)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, guide, gin.H{})
	}
}

func upsertOnboardingHandler(repo servicecatalog.Repository, onboardingService onboarding.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		var body onboarding.UpsertRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("服务接入请求无效"))
			return
		}
		result, err := onboardingService.Upsert(bg, service, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func checkOnboardingHandler(repo servicecatalog.Repository, onboardingService onboarding.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		service, ok := getServiceFromPath(ctx, repo)
		if !ok {
			return
		}
		result, err := onboardingService.Check(bg, service)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func listCollectorGroupsHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		groups, err := service.ListGroups(bg, collectormanagement.ListGroupFilter{
			Query:           ctx.Query("q"),
			Environment:     ctx.Query("environment"),
			Cluster:         ctx.Query("cluster"),
			Namespace:       ctx.Query("namespace"),
			Mode:            ctx.Query("mode"),
			Status:          ctx.Query("status"),
			ReceiverProfile: ctx.Query("receiver_profile"),
		})
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, groups, gin.H{"total": len(groups)})
	}
}

func createCollectorGroupHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body collectormanagement.CollectorGroup
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("Collector Group 创建请求无效"))
			return
		}
		if body.Name == "" || body.Mode == "" {
			writeError(ctx, apperr.InvalidRequest("Collector Group 名称和模式不能为空"))
			return
		}
		group, err := service.CreateGroup(bg, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.Created(ctx, group)
	}
}

func getCollectorGroupHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		group, err := service.GetGroup(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		response.OK(ctx, group, gin.H{})
	}
}

func updateCollectorGroupHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		var body collectormanagement.UpdateGroupRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("Collector Group 更新请求无效"))
			return
		}
		group, err := service.UpdateGroup(bg, id, body)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		response.OK(ctx, group, gin.H{})
	}
}

func activateCollectorGroupHandler(service collectormanagement.Service, configService collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		validation, err := configService.ValidateGroup(bg, id)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if !validation.Valid {
			writeError(ctx, apperr.InvalidRequest("Collector Group 配置校验未通过: "+strings.Join(validation.Errors, "; ")))
			return
		}
		group, err := service.ActivateGroup(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		response.OK(ctx, group, gin.H{})
	}
}

func deleteCollectorGroupHandler(service collectormanagement.Service, onboardingStore database.OnboardingStore, pipelinePatchStore database.ServicePipelinePatchStore) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		deps := collectormanagement.DeleteGroupDependencies{}
		if onboardingStore != nil {
			var onboardings []onboarding.ServiceOnboarding
			if err := onboardingStore.FindByCollectorGroup(bg, id, &onboardings); err == nil {
				deps.OnboardingRefs = len(onboardings)
			}
		}
		if pipelinePatchStore != nil {
			var refs []any
			if err := pipelinePatchStore.FindByCollectorGroup(bg, id, &refs); err == nil {
				deps.ConfigRefs += len(refs)
			}
		}
		_, err := service.DeleteGroup(bg, id, deps)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		ctx.Status(http.StatusNoContent)
	}
}

func listCollectorInstancesHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		instances, err := service.ListInstances(bg, id)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, instances, gin.H{"total": len(instances)})
	}
}

func listCollectorGroupConfigVersionsHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		versions, err := service.ListConfigVersions(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		response.OK(ctx, versions, gin.H{"total": len(versions)})
	}
}

func getCollectorGroupLatestConfigYAMLHandler(service collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		version, err := service.LatestConfigVersion(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 尚未发布配置"))
			return
		}
		if version.CollectorYAML == "" {
			writeError(ctx, apperr.NotFound("Collector Group 最新配置为空"))
			return
		}
		ctx.Header("X-Collector-Config-Version", fmt.Sprintf("%d", version.Version))
		ctx.Header("X-Collector-Config-Hash", version.ConfigHash)
		ctx.Data(http.StatusOK, "application/x-yaml; charset=utf-8", []byte(version.CollectorYAML))
	}
}

func searchLogsHandler(service logquery.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		start, ok := parseOptionalTime(ctx, "start")
		if !ok {
			return
		}
		end, ok := parseOptionalTime(ctx, "end")
		if !ok {
			return
		}
		result := service.Search(logquery.Query{
			Service:     ctx.Query("service"),
			Environment: ctx.Query("environment"),
			Level:       ctx.Query("level"),
			Keyword:     ctx.Query("keyword"),
			TraceID:     ctx.Query("trace_id"),
			RequestID:   ctx.Query("request_id"),
			Start:       start,
			End:         end,
		})
		response.OK(ctx, result.Items, gin.H{"total": result.Total})
	}
}

func registerOpAMPInstanceGroupHandler(manager *opamp.Manager, collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := ctx.Param("uid")
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}
		var body struct {
			GroupID string `json:"group_id"`
		}
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("请求 JSON 无效"))
			return
		}
		if body.GroupID == "" {
			writeError(ctx, apperr.InvalidRequest("group_id 不能为空"))
			return
		}
		instance, err := collectorService.AssignInstanceGroup(bg, uid, body.GroupID)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		if manager != nil {
			manager.RegisterInstanceGroup(uid, body.GroupID)
		}
		response.OK(ctx, instance, gin.H{})
	}
}

func unassignOpAMPInstanceGroupHandler(manager *opamp.Manager, collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := ctx.Param("uid")
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}
		_, err := collectorService.UnassignInstance(bg, uid)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Instance 不存在"))
			return
		}
		if manager != nil {
			manager.UnregisterInstanceGroup(uid)
		}
		ctx.Status(http.StatusNoContent)
	}
}

func deleteOpAMPInstanceHandler(collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := ctx.Param("uid")
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}
		if err := collectorService.DeleteInstance(bg, uid); err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Instance 不存在"))
			return
		}
		ctx.Status(http.StatusNoContent)
	}
}

func listOpAMPAgentsHandler(manager *opamp.Manager, collectorService collectormanagement.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		instances, err := collectorService.ListInstances(bg, "")
		if err != nil {
			writeError(ctx, err)
			return
		}
		byUID := map[string]collectormanagement.CollectorInstance{}
		for _, instance := range instances {
			byUID[instance.InstanceUID] = instance
		}
		if manager != nil {
			for _, agent := range manager.ListAgents() {
				instance := byUID[agent.InstanceUID]
				if instance.ID == "" {
					instance.ID = agent.InstanceUID
					instance.InstanceUID = agent.InstanceUID
					instance.CreatedAt = agent.LastSeenAt
				}
				instance.CollectorGroupID = agent.CollectorGroupID
				instance.Online = agent.Online
				instance.Healthy = agent.Healthy
				instance.Capabilities = agent.Capabilities
				instance.RemoteConfigCapable = agent.RemoteConfigCapable
				instance.EffectiveConfigHash = agent.EffectiveConfigHash
				instance.RemoteConfigStatus = agent.RemoteConfigStatus
				instance.LastConfigHash = agent.LastConfigHash
				instance.LastError = agent.LastError
				instance.LastSeenAt = agent.LastSeenAt
				instance.UpdatedAt = agent.LastSeenAt
				if instance.Online {
					instance.RuntimeStatus = "online"
				}
				byUID[agent.InstanceUID] = instance
			}
		}
		agents := make([]collectormanagement.CollectorInstance, 0, len(byUID))
		for _, instance := range byUID {
			instance = collectorService.ApplyRuntimeStatus(instance)
			if instance.RuntimeStatus != "online" {
				continue
			}
			agents = append(agents, instance)
		}
		response.OK(ctx, agents, gin.H{"total": len(agents)})
	}
}

func listAlertRulesHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		rules, err := service.List(bg)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, rules, gin.H{"total": len(rules)})
	}
}

func createAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.Rule
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("告警规则创建请求无效"))
			return
		}
		if body.Name == "" || body.RuleType == "" {
			writeError(ctx, apperr.InvalidRequest("告警规则名称和类型不能为空"))
			return
		}
		rule, err := service.Create(bg, body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.Created(ctx, rule)
	}
}

func getServiceFromPath(ctx *gin.Context, repo servicecatalog.Repository) (servicecatalog.Service, bool) {
	id, ok := parseID(ctx)
	if !ok {
		return servicecatalog.Service{}, false
	}
	service, err := repo.Get(bg, id)
	if err != nil {
		writeError(ctx, normalizeNotFound(err, "服务不存在"))
		return servicecatalog.Service{}, false
	}
	return service, true
}

func parseID(ctx *gin.Context) (string, bool) {
	id := ctx.Param("id")
	if id == "" {
		writeError(ctx, apperr.InvalidRequest("资源 ID 不能为空"))
		return "", false
	}
	return id, true
}

func parseOptionalTime(ctx *gin.Context, key string) (*time.Time, bool) {
	value := ctx.Query(key)
	if value == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		writeError(ctx, apperr.InvalidRequest(fmt.Sprintf("%s 必须是 RFC3339 时间", key)))
		return nil, false
	}
	return &parsed, true
}

func normalizeNotFound(err error, message string) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return apperr.NotFound(message)
	}
	return err
}

func writeError(ctx *gin.Context, err error) {
	var appError apperr.Error
	if errors.As(err, &appError) {
		response.Error(ctx, appError.Status, appError.Code, appError.Message)
		return
	}
	response.Error(ctx, http.StatusInternalServerError, "internal_error", "服务处理失败")
}
