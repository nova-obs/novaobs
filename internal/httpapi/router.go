package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"novaapm/internal/alerting"
	"novaapm/internal/collectorconfig"
	"novaapm/internal/collectormanagement"
	"novaapm/internal/database"
	"novaapm/internal/logs"
	"novaapm/internal/metrics"
	"novaapm/internal/modules/k8sops"
	k8sopscertificate "novaapm/internal/modules/k8sops/certificate"
	k8sopscluster "novaapm/internal/modules/k8sops/cluster"
	k8sopsdashboard "novaapm/internal/modules/k8sops/dashboard"
	k8sopsdeployment "novaapm/internal/modules/k8sops/deployment"
	k8sopskubeconfig "novaapm/internal/modules/k8sops/kubeconfig"
	k8sopsnamespace "novaapm/internal/modules/k8sops/namespace"
	k8sopsplatformaccess "novaapm/internal/modules/k8sops/platformaccess"
	k8sopsrbac "novaapm/internal/modules/k8sops/rbac"
	k8sopsresource "novaapm/internal/modules/k8sops/resource"
	k8sopsserviceaccount "novaapm/internal/modules/k8sops/serviceaccount"
	k8sopstemplate "novaapm/internal/modules/k8sops/template"
	k8sopsterminal "novaapm/internal/modules/k8sops/terminal"
	obsendpoint "novaapm/internal/observability/endpoint"
	"novaapm/internal/onboarding"
	"novaapm/internal/opamp"
	platformauth "novaapm/internal/platform/auth"
	"novaapm/internal/platform/authctx"
	"novaapm/internal/platform/iam"
	platformimages "novaapm/internal/platform/images"
	platformrbac "novaapm/internal/platform/rbac"
	"novaapm/internal/servicecatalog"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"
)

// bg is used as background context for store operations
var bg = context.Background()

type Dependencies struct {
	Store                  database.Store
	ProductRepo            servicecatalog.ProductRepository
	ServiceRepo            servicecatalog.Repository
	ServiceTargetRepo      servicecatalog.TargetRepository
	CollectorConfigService collectorconfig.Service
	CollectorService       collectormanagement.Service
	OnboardingService      onboarding.Service
	LogsService            logs.Service
	ObservabilityEndpoints obsendpoint.Service
	MetricsService         metrics.Service
	AlertRuntimeService    alerting.LogRuntimeService
	MetricsRuntimeService  alerting.MetricsRuntimeService
	AlertService           alerting.Service
	AlertEventIngestor     alerting.EventIngestor
	AlertPolicyService     alerting.PolicyService
	PlatformIAMService     iam.Service
	PlatformImageService   platformimages.Service
	K8sOpsModule           k8sops.Module
	OpAMPManager           *opamp.Manager
	CollectorTemplate      string
	PlatformAuthService    *platformauth.Service
	DefaultSubject         platformrbac.Subject
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), errorLogMiddleware())
	if deps.OpAMPManager != nil {
		deps.OpAMPManager.SetStateSink(func(ctx context.Context, state opamp.AgentState) {
			_, _ = deps.CollectorService.UpsertInstance(ctx, state.InstanceUID, state.CollectorGroupID, collectormanagement.InstanceStatus{
				ServiceID:           state.ServiceID,
				OpAMPInstanceUID:    state.OpAMPInstanceUID,
				RuntimeIdentity:     state.RuntimeIdentity,
				ClusterID:           state.ClusterID,
				Namespace:           state.Namespace,
				AgentNamespace:      state.AgentNamespace,
				Hostname:            state.Hostname,
				PodUID:              state.PodUID,
				PodName:             state.PodName,
				NodeName:            state.NodeName,
				PodIP:               state.PodIP,
				Version:             state.Version,
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
	api.POST("/auth/login", platformauth.LoginHandler(deps.PlatformAuthService))
	api.POST("/auth/logout", platformauth.LogoutHandler())
	api.POST("/alerts/ingest", alertIngestHandler(deps.AlertEventIngestor))

	vmalertNotifierAPI := router.Group("/api/v2")
	vmalertNotifierAPI.POST("/alerts", alertIngestHandler(deps.AlertEventIngestor))

	api = router.Group("/api/v1")
	if deps.PlatformAuthService != nil {
		api.Use(platformauth.SessionMiddleware(deps.PlatformAuthService))
	} else if deps.DefaultSubject.ID != "" && deps.DefaultSubject.Type != "" {
		api.Use(defaultSubjectMiddleware(deps.DefaultSubject))
	}

	api.GET("/auth/session", platformauth.SessionHandler())
	api.GET("/overview", overviewHandler(deps))
	api.GET("/products", listProductsHandler(deps.ProductRepo))
	api.POST("/products", createProductHandler(deps.ProductRepo))
	api.GET("/products/:productId", getProductHandler(deps.ProductRepo))
	api.GET("/services", listServicesHandler(deps.ServiceRepo))
	api.POST("/products/:productId/services", createServiceHandler(deps.ServiceRepo))
	api.GET("/services/:id", getServiceHandler(deps.ServiceRepo))
	api.PATCH("/services/:id", updateServiceHandler(deps.ServiceRepo))
	api.DELETE("/services/:id", deleteServiceHandler(deps.ServiceRepo, deps.CollectorService, deps.Store))
	api.GET("/services/:id/observability-graph", getServiceObservabilityGraphHandler(deps))
	api.GET("/services/:id/targets", listServiceTargetsHandler(deps.ServiceRepo, deps.ServiceTargetRepo))
	api.POST("/services/:id/targets", createServiceTargetHandler(deps.ServiceRepo, deps.ServiceTargetRepo))
	api.GET("/services/:id/agents", listServiceAgentsHandler(deps.ServiceRepo, deps.CollectorService))
	api.GET("/services/:id/onboarding", getOnboardingHandler(deps.ServiceRepo, deps.OnboardingService))
	api.POST("/services/:id/onboarding", upsertOnboardingHandler(deps.ServiceRepo, deps.OnboardingService))
	api.POST("/services/:id/onboarding/check", checkOnboardingHandler(deps.ServiceRepo, deps.OnboardingService))
	api.GET("/collector-groups", listCollectorGroupsHandler(deps.CollectorService))
	api.POST("/collector-groups", createCollectorGroupHandler(deps.CollectorService))
	api.GET("/collector-groups/:id", getCollectorGroupHandler(deps.CollectorService))
	api.PATCH("/collector-groups/:id", updateCollectorGroupHandler(deps.CollectorService))
	api.POST("/collector-groups/:id/activate", activateCollectorGroupHandler(deps.CollectorService, deps.CollectorConfigService))
	api.DELETE("/collector-groups/:id", deleteCollectorGroupHandler(deps.CollectorService, deps.Store.Onboardings(), deps.Store.ServicePipelinePatches(), deps.Store.LogRoutes()))
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
	api.GET("/products/:productId/services/:id/logs/workspace", getLogsOnboardingWorkspaceHandler(deps.LogsService))
	api.GET("/logs/onboarding/k8s/workloads", getLogsK8sWorkloadsHandler(deps.LogsService))
	api.POST("/products/:productId/logs/onboarding/k8s/sync-services", syncLogsK8sServicesHandler(deps.LogsService))
	api.GET("/logs/endpoints", listLogsEndpointsHandler(deps.LogsService))
	api.POST("/logs/endpoints", createLogsEndpointHandler(deps.LogsService))
	api.PATCH("/logs/endpoints/:id", updateLogsEndpointHandler(deps.LogsService))
	api.POST("/logs/endpoints/:id/vmalert-runtime/publish", publishLogsEndpointVmalertRuntimeHandler(deps.AlertRuntimeService))
	api.GET("/logs/targets", listLogsTargetsHandler(deps.LogsService))
	api.POST("/logs/targets", createLogsTargetHandler(deps.LogsService))
	api.PATCH("/logs/targets/:id", updateLogsTargetHandler(deps.LogsService))
	api.POST("/logs/targets/:id/probe", probeLogsTargetHandler(deps.LogsService))
	api.POST("/logs/parse-preview", previewLogsParseRulesHandler(deps.LogsService))
	api.POST("/logs/routes/preview", previewLogsRouteHandler(deps.LogsService))
	api.POST("/logs/routes", createLogsRouteHandler(deps.LogsService))
	api.PATCH("/logs/routes/:id", updateLogsRouteHandler(deps.LogsService))
	api.GET("/logs/routes/:id/collector-config", getLogsRouteCollectorConfigHandler(deps.LogsService))
	api.POST("/logs/routes/:id/probe", probeLogsRouteHandler(deps.LogsService))
	api.DELETE("/logs/routes/:id", deleteLogsRouteHandler(deps.LogsService))
	api.POST("/logs/routes/:id/publish", publishLogsRouteHandler(deps.LogsService))
	api.GET("/observability/endpoints", listObservabilityEndpointsHandler(deps.ObservabilityEndpoints))
	api.POST("/observability/endpoints/:id/test", testObservabilityEndpointHandler(deps.ObservabilityEndpoints))
	api.GET("/observability/runtimes/logs-collector/status", getLogsCollectorRuntimeStatusHandler(deps.LogsService))
	api.POST("/observability/runtimes/logs-collector/publish", publishLogsCollectorRuntimeHandler(deps.LogsService))
	api.GET("/metrics/endpoints", listMetricsEndpointsHandler(deps.MetricsService))
	api.POST("/metrics/endpoints/:id/vmalert-runtime/publish", publishMetricsEndpointVmalertRuntimeHandler(deps.MetricsRuntimeService))
	api.GET("/products/:productId/services/:id/metrics/workspace", getMetricsWorkspaceHandler(deps.MetricsService))
	api.GET("/products/:productId/services/:id/metrics/bindings", listMetricsServiceBindingsHandler(deps.MetricsService))
	api.POST("/products/:productId/services/:id/metrics/bindings", createMetricsServiceBindingHandler(deps.MetricsService))
	api.PATCH("/products/:productId/services/:id/metrics/bindings/:bindingId", updateMetricsServiceBindingHandler(deps.MetricsService))
	api.POST("/products/:productId/services/:id/metrics/bindings/:bindingId/probe", probeMetricsServiceBindingHandler(deps.MetricsService))
	api.GET("/metrics/alert-rules", listMetricsAlertRulesHandler(deps.AlertService))
	api.POST("/metrics/alert-rules/test", testMetricsAlertRuleHandler(deps.AlertService))
	api.POST("/metrics/alert-rules", createMetricsAlertRuleHandler(deps.AlertService))
	api.GET("/alerts/rules", listAlertRulesHandler(deps.AlertService))
	api.POST("/alerts/rules/test", testAlertRuleHandler(deps.AlertService))
	api.POST("/alerts/rules", createAlertRuleHandler(deps.AlertService))
	api.GET("/alerts/rules/:id", getAlertRuleHandler(deps.AlertService))
	api.PUT("/alerts/rules/:id", updateAlertRuleHandler(deps.AlertService))
	api.POST("/alerts/rules/:id/disable", disableAlertRuleHandler(deps.AlertService))
	api.GET("/alerts/rules/:id/updates", listAlertRuleUpdatesHandler(deps.AlertService))
	api.POST("/alerts/rules/:id/rollback", rollbackAlertRuleHandler(deps.AlertService))
	api.GET("/alerts/instances", listAlertInstancesHandler(deps.AlertService))
	api.GET("/alerts/events", listAlertEventsHandler(deps.AlertService))
	api.GET("/alerts/notification-policies", listNotificationPoliciesHandler(deps.AlertPolicyService))
	api.POST("/alerts/notification-policies", createNotificationPolicyHandler(deps.AlertPolicyService))
	api.PUT("/alerts/notification-policies/:id", updateNotificationPolicyHandler(deps.AlertPolicyService))
	api.GET("/platform/me", iam.MeHandler(deps.PlatformIAMService))
	api.GET("/platform/subjects", iam.ListSubjectsHandler(deps.PlatformIAMService))
	api.GET("/platform/users", iam.ListUsersHandler(deps.PlatformIAMService))
	api.POST("/platform/users", iam.CreateUserHandler(deps.PlatformIAMService))
	api.DELETE("/platform/users/:id", iam.DeleteUserHandler(deps.PlatformIAMService))
	api.GET("/platform/groups", iam.ListGroupsHandler(deps.PlatformIAMService))
	api.POST("/platform/groups", iam.CreateGroupHandler(deps.PlatformIAMService))
	api.DELETE("/platform/groups/:id", iam.DeleteGroupHandler(deps.PlatformIAMService))
	api.GET("/platform/group-memberships", iam.ListMembershipsHandler(deps.PlatformIAMService))
	api.POST("/platform/group-memberships", iam.CreateMembershipHandler(deps.PlatformIAMService))
	api.DELETE("/platform/group-memberships/:id", iam.DeleteMembershipHandler(deps.PlatformIAMService))
	api.GET("/platform/service-accounts", iam.ListServiceAccountsHandler(deps.PlatformIAMService))
	api.POST("/platform/service-accounts", iam.CreateServiceAccountHandler(deps.PlatformIAMService))
	api.DELETE("/platform/service-accounts/:id", iam.DeleteServiceAccountHandler(deps.PlatformIAMService))
	api.GET("/platform/roles", iam.ListRolesHandler(deps.PlatformIAMService))
	api.POST("/platform/roles", iam.CreateRoleHandler(deps.PlatformIAMService))
	api.DELETE("/platform/roles/:id", iam.DeleteRoleHandler(deps.PlatformIAMService))
	api.GET("/platform/bindings", iam.ListBindingsHandler(deps.PlatformIAMService))
	api.POST("/platform/bindings", iam.CreateBindingHandler(deps.PlatformIAMService))
	api.DELETE("/platform/bindings/:id", iam.DeleteBindingHandler(deps.PlatformIAMService))
	api.GET("/platform/effective-permissions", iam.EffectivePermissionsHandler(deps.PlatformIAMService))
	api.GET("/platform/images", platformimages.ListHandler(deps.PlatformImageService))
	api.PUT("/platform/images", platformimages.UpsertHandler(deps.PlatformImageService))
	api.GET("/k8sops/dashboard", getK8sOpsDashboardHandler(deps.K8sOpsModule.Dashboard))
	api.GET("/k8s/clusters", k8sopscluster.ListHandler(deps.K8sOpsModule.Cluster))
	api.POST("/k8s/clusters", k8sopscluster.CreateHandler(deps.K8sOpsModule.Cluster))
	api.GET("/k8s/clusters/:id/capabilities", k8sopscluster.CapabilityHandler(deps.K8sOpsModule.ClusterCaps))
	api.POST("/k8s/clusters/:id/probe", k8sopscluster.ProbeHandler(deps.K8sOpsModule.Cluster, deps.K8sOpsModule.ClusterCaps))
	api.DELETE("/k8s/clusters/:id", k8sopscluster.DeleteHandler(deps.K8sOpsModule.Cluster))
	api.GET("/k8s/cluster-credentials", k8sopscluster.ListCredentialHandler(deps.K8sOpsModule.ClusterCred))
	api.POST("/k8s/cluster-credentials", k8sopscluster.CreateCredentialHandler(deps.K8sOpsModule.ClusterCred))
	api.POST("/k8s/cluster-credentials/rotate", k8sopscluster.RotateCredentialHandler(deps.K8sOpsModule.ClusterCred))
	api.POST("/k8s/cluster-credentials/rollback", k8sopscluster.RollbackCredentialHandler(deps.K8sOpsModule.ClusterCred))
	api.GET("/k8s/namespaces", k8sopsnamespace.ListHandler(deps.K8sOpsModule.Namespace))
	api.POST("/k8s/namespaces", k8sopsnamespace.CreateHandler(deps.K8sOpsModule.Namespace))
	api.DELETE("/k8s/namespaces", k8sopsnamespace.DeleteHandler(deps.K8sOpsModule.Namespace))
	api.GET("/k8s/resources", k8sopsresource.ListHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/resources/detail", k8sopsresource.DetailHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/resources/yaml", k8sopsresource.YAMLHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/pod-logs", k8sopsresource.PodLogsHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/runtime-groups", k8sopsresource.RuntimeGroupsHandler(deps.K8sOpsModule.Resource))
	api.GET("/k8s/platform-access/bindings", k8sopsplatformaccess.ListBindingsHandler(deps.K8sOpsModule.PlatformAccess))
	api.POST("/k8s/platform-access/bindings", k8sopsplatformaccess.CreateBindingHandler(deps.K8sOpsModule.PlatformAccess))
	api.DELETE("/k8s/platform-access/bindings/:id", k8sopsplatformaccess.DeleteBindingHandler(deps.K8sOpsModule.PlatformAccess))
	api.GET("/k8s/platform-access/permissions", k8sopsplatformaccess.PermissionsHandler(deps.K8sOpsModule.PlatformAccess))
	api.GET("/k8s/platform-access/profiles", k8sopsplatformaccess.ProfilesHandler(deps.K8sOpsModule.PlatformAccess))
	api.GET("/k8s/platform-access/subjects", k8sopsplatformaccess.ListSubjectsHandler(deps.K8sOpsModule.PlatformAccess))
	api.GET("/k8s/deployment-history", k8sopsdeployment.HistoryHandler(deps.K8sOpsModule.Deploy))
	api.GET("/k8s/audit-events", k8sopsdeployment.AuditEventsHandler(deps.K8sOpsModule.Deploy))
	api.POST("/k8s/deployments/preview", k8sopsdeployment.PreviewHandler(deps.K8sOpsModule.Deploy))
	api.POST("/k8s/deployments/delete-preview", k8sopsdeployment.PreviewDeleteHandler(deps.K8sOpsModule.Deploy))
	api.POST("/k8s/deployments", k8sopsdeployment.ApplyHandler(deps.K8sOpsModule.Deploy))
	api.DELETE("/k8s/deployments", k8sopsdeployment.DeleteHandler(deps.K8sOpsModule.Deploy))
	api.POST("/k8s/deployments/rollback", k8sopsdeployment.RollbackHandler(deps.K8sOpsModule.Deploy))
	api.GET("/k8s/certificates", k8sopscertificate.ListHandler(deps.K8sOpsModule.Cert))
	api.POST("/k8s/certificates", k8sopscertificate.CreateHandler(deps.K8sOpsModule.Cert))
	api.DELETE("/k8s/certificates/:id", k8sopscertificate.DeleteHandler(deps.K8sOpsModule.Cert))
	api.GET("/k8s/service-accounts", k8sopsserviceaccount.ListHandler(deps.K8sOpsModule.ServiceAccount))
	api.POST("/k8s/service-accounts", k8sopsserviceaccount.CreateHandler(deps.K8sOpsModule.ServiceAccount))
	api.DELETE("/k8s/service-accounts", k8sopsserviceaccount.DeleteHandler(deps.K8sOpsModule.ServiceAccount))
	api.GET("/k8s/rbac/roles", k8sopsrbac.ListRolesHandler(deps.K8sOpsModule.RBAC))
	api.POST("/k8s/rbac/roles", k8sopsrbac.CreateRoleHandler(deps.K8sOpsModule.RBAC))
	api.PUT("/k8s/rbac/roles", k8sopsrbac.UpdateRoleHandler(deps.K8sOpsModule.RBAC))
	api.DELETE("/k8s/rbac/roles", k8sopsrbac.DeleteRoleHandler(deps.K8sOpsModule.RBAC))
	api.GET("/k8s/rbac/bindings", k8sopsrbac.ListBindingsHandler(deps.K8sOpsModule.RBAC))
	api.POST("/k8s/rbac/bindings", k8sopsrbac.CreateBindingHandler(deps.K8sOpsModule.RBAC))
	api.DELETE("/k8s/rbac/bindings", k8sopsrbac.DeleteBindingHandler(deps.K8sOpsModule.RBAC))
	api.POST("/k8s/kubeconfigs", k8sopskubeconfig.CreateHandler(deps.K8sOpsModule.Kubeconfig))
	api.POST("/k8s/kubeconfigs/export", k8sopskubeconfig.ExportHandler(deps.K8sOpsModule.Kubeconfig))
	api.GET("/k8s/templates", k8sopstemplate.ListHandler(deps.K8sOpsModule.Template))
	api.GET("/k8s/templates/base", k8sopstemplate.BaseTemplateHandler())
	api.POST("/k8s/templates", k8sopstemplate.CreateHandler(deps.K8sOpsModule.Template))
	api.PUT("/k8s/templates", k8sopstemplate.UpdateHandler(deps.K8sOpsModule.Template))
	api.DELETE("/k8s/templates/:id", k8sopstemplate.DeleteHandler(deps.K8sOpsModule.Template))
	api.POST("/k8s/templates/render", k8sopstemplate.RenderHandler(deps.K8sOpsModule.Template))
	api.POST("/k8s/terminal/exec", k8sopsterminal.ExecHandler(deps.K8sOpsModule.Terminal))
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

func defaultSubjectMiddleware(subject platformrbac.Subject) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if _, ok := authctx.SubjectFrom(ctx.Request.Context()); !ok {
			ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), subject))
		}
		ctx.Next()
	}
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
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		rules, _ := deps.AlertService.List(ctx.Request.Context(), subject, alerting.RuleFilter{})
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

func listProductsHandler(repo servicecatalog.ProductRepository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		products, err := repo.List(ctx.Request.Context())
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, products, gin.H{"total": len(products)})
	}
}

func createProductHandler(repo servicecatalog.ProductRepository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body servicecatalog.Product
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("产品创建请求无效"))
			return
		}
		product, err := repo.Create(ctx.Request.Context(), body)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.Created(ctx, product)
	}
}

func getProductHandler(repo servicecatalog.ProductRepository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		product, err := repo.Get(ctx.Request.Context(), strings.TrimSpace(ctx.Param("productId")))
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "产品不存在"))
			return
		}
		response.OK(ctx, product, gin.H{})
	}
}

func createServiceHandler(repo servicecatalog.Repository) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body servicecatalog.Service
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("服务创建请求无效"))
			return
		}
		if strings.TrimSpace(body.ProductID) == "" {
			body.ProductID = strings.TrimSpace(ctx.Param("productId"))
		}
		if body.ProductID != strings.TrimSpace(ctx.Param("productId")) {
			writeError(ctx, apperr.InvalidRequest("product_id 与路径产品不一致"))
			return
		}
		service, err := repo.Create(ctx.Request.Context(), body)
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

func deleteServiceHandler(repo servicecatalog.Repository, collectorService collectormanagement.Service, store database.Store) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		deps, err := serviceDeleteDependencies(bg, id, collectorService, store)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if _, err := repo.Delete(bg, id, deps); err != nil {
			writeError(ctx, normalizeNotFound(err, "服务不存在"))
			return
		}
		ctx.Status(http.StatusNoContent)
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

func deleteCollectorGroupHandler(service collectormanagement.Service, onboardingStore database.OnboardingStore, pipelinePatchStore database.ServicePipelinePatchStore, logRouteStore database.LogRouteStore) gin.HandlerFunc {
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
		if logRouteStore != nil {
			var refs []logs.LogRoute
			if err := logRouteStore.FindByAgentGroup(bg, id, &refs); err != nil {
				writeError(ctx, err)
				return
			}
			deps.ConfigRefs += len(refs)
		}
		_, err := service.DeleteGroup(bg, id, deps)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		ctx.Status(http.StatusNoContent)
	}
}

func serviceDeleteDependencies(ctx context.Context, serviceID string, collectorService collectormanagement.Service, store database.Store) (servicecatalog.DeleteDependencies, error) {
	deps := servicecatalog.DeleteDependencies{}
	if store != nil {
		var routes []logs.LogRoute
		if err := store.LogRoutes().FindByService(ctx, serviceID, &routes); err != nil {
			return deps, err
		}
		deps.LogRouteRefs = len(routes)
		var onboardingState onboarding.ServiceOnboarding
		if err := store.Onboardings().FindByService(ctx, serviceID, &onboardingState); err == nil {
			deps.OnboardingRefs = 1
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return deps, err
		}
	}
	instances, err := collectorService.ListInstancesByService(ctx, serviceID)
	if err != nil {
		return deps, err
	}
	deps.AgentRefs = len(instances)
	return deps, nil
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
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		signalType, ok := alertRuleSignalFilter(ctx)
		if !ok {
			return
		}
		rules, err := service.List(ctx.Request.Context(), subject, alerting.RuleFilter{
			ServiceID:  strings.TrimSpace(ctx.Query("service_id")),
			State:      strings.TrimSpace(ctx.Query("state")),
			SignalType: signalType,
		})
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, rules, gin.H{"total": len(rules)})
	}
}

func createAlertRuleHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.EnableRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "告警规则请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		result, err := service.Enable(ctx.Request.Context(), subject, body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.Created(ctx, result)
	}
}

func writeAlertingError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, alerting.ErrInvalidSpec):
		response.Error(ctx, http.StatusUnprocessableEntity, "invalid_alert_rule", err.Error())
	case errors.Is(err, alerting.ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权管理该范围的告警资源")
	case errors.Is(err, alerting.ErrNotFound):
		response.Error(ctx, http.StatusNotFound, "alert_rule_not_found", "告警规则不存在")
	case errors.Is(err, alerting.ErrConflict):
		response.Error(ctx, http.StatusConflict, "alert_rule_conflict", "告警规则已被其他操作更新，请刷新后重试")
	case errors.Is(err, alerting.ErrQueryFailed):
		response.Error(ctx, http.StatusBadGateway, "alert_query_failed", "VictoriaLogs 查询失败")
	case errors.Is(err, alerting.ErrUnavailable):
		response.Error(ctx, http.StatusServiceUnavailable, "alert_service_unavailable", "告警服务暂不可用")
	case errors.Is(err, alerting.ErrTestRequired):
		response.Error(ctx, http.StatusPreconditionFailed, "alert_test_required", "规则内容已变化，请重新测试后再启用")
	default:
		response.ErrorWithCause(ctx, http.StatusInternalServerError, "alert_rule_failed", "告警规则操作失败", err)
	}
}

func alertSubject(ctx *gin.Context) (platformrbac.Subject, bool) {
	subject, ok := authctx.SubjectFrom(ctx.Request.Context())
	if !ok || subject.ID == "" || subject.Type == "" {
		response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
		return platformrbac.Subject{}, false
	}
	return subject, true
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

func normalizeNotFound(err error, message string) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return apperr.NotFound(message)
	}
	return err
}

func writeError(ctx *gin.Context, err error) {
	var appError apperr.Error
	if errors.As(err, &appError) {
		response.ErrorWithCause(ctx, appError.Status, appError.Code, appError.Message, err)
		return
	}
	response.ErrorWithCause(ctx, http.StatusInternalServerError, "internal_error", "服务处理失败", err)
}
