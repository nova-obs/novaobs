package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"novaapm/internal/alerting"
	"novaapm/internal/collectorconfig"
	"novaapm/internal/collectormanagement"
	"novaapm/internal/config"
	"novaapm/internal/database/mongo"
	"novaapm/internal/httpapi"
	"novaapm/internal/logs"
	"novaapm/internal/metrics"
	"novaapm/internal/modules/k8sops"
	"novaapm/internal/modules/k8sops/cluster"
	"novaapm/internal/modules/k8sops/deployment"
	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/modules/k8sops/platformaccess"
	"novaapm/internal/modules/k8sops/terminal"
	obsendpoint "novaapm/internal/observability/endpoint"
	"novaapm/internal/onboarding"
	"novaapm/internal/opamp"
	"novaapm/internal/platform/audit"
	platformauth "novaapm/internal/platform/auth"
	"novaapm/internal/platform/iam"
	platformimages "novaapm/internal/platform/images"
	"novaapm/internal/platform/rbac"
	"novaapm/internal/platform/secret"
	"novaapm/internal/servicecatalog"

	"github.com/gin-gonic/gin"
)

func New(cfg config.Config) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)

	ctx := context.Background()
	store, err := mongo.NewStore(ctx, cfg.Database.URI)
	if err != nil {
		return nil, fmt.Errorf("连接 MongoDB 失败: %w", err)
	}

	svcRepo := servicecatalog.NewRepository(store.Services(), store.Products())
	productRepo := servicecatalog.NewProductRepository(store.Products())
	targetRepo := servicecatalog.NewTargetRepository(store.ServiceTargets())
	collectorSvc := collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances(), collectormanagement.WithConfigVersionStore(store.CollectorConfigVersions()))
	onboardingSvc := onboarding.NewService(store.Onboardings(), store.IngestionIdentities(), svcRepo, collectorSvc)
	collectorConfigSvc := collectorconfig.NewService(
		store.CollectorPlatformTemplates(),
		store.CollectorGroupOverrides(),
		store.ServiceEnrichmentPatches(),
		store.ServiceParserRules(),
		store.ServicePipelinePatches(),
		collectorSvc,
		svcRepo,
	)
	rbacRepo := rbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	iamRepo := iam.NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	if cfg.Server.Mode != gin.ReleaseMode {
		if err := rbac.CleanupLegacyDefaults(rbacRepo); err != nil {
			return nil, fmt.Errorf("清理开发态历史内置角色失败: %w", err)
		}
	}
	bootstrapAdmin := platformBootstrapAdmin()
	superSubjects := []rbac.Subject{}
	if cfg.Server.Mode != gin.ReleaseMode {
		superSubjects = append(superSubjects, rbac.DevAdminSubject())
	} else if bootstrapAdmin.ID != "" {
		superSubjects = append(superSubjects, bootstrapAdmin)
	}
	rbacSvc := rbac.NewService(rbacRepo, rbac.WithSubjectResolver(iam.NewSubjectResolver(iamRepo)), rbac.WithSuperSubjects(superSubjects...))
	alertScopeResolver := alerting.NewSignalAwareStoreScopeResolver(store.Services(), store.LogRoutes(), store.LogTargets(), store.LogEndpoints(), store.MetricsServiceBindings(), store.Products())
	alertRepository := alerting.NewStoreRepository(store.Alerting())
	alertPolicyResolver := alerting.NewStorePolicyResolver(alertRepository)
	alertPolicySvc := alerting.NewPolicyService(alerting.PolicyDependencies{Repository: alertRepository, Authorizer: rbacSvc})
	alertSvc := alerting.NewService(alerting.Dependencies{
		Repository:      alertRepository,
		Authorizer:      rbacSvc,
		ScopeResolver:   alertScopeResolver,
		Tester:          alerting.NewSignalAwareTester(alerting.NewVictoriaLogsTester(alertScopeResolver, nil), alerting.MetricsCompileOnlyTester{}),
		ReceiptSigner:   alerting.NewHMACTestReceiptSigner([]byte(cfg.Secret.Key)),
		EventRepository: alertRepository,
		PolicyResolver:  alertPolicyResolver,
	})
	alertWebhookToken := strings.TrimSpace(os.Getenv("NOVAAPM_ALERT_INGEST_TOKEN"))
	if alertWebhookToken == "" {
		if cfg.Server.Mode == gin.ReleaseMode {
			return nil, fmt.Errorf("NOVAAPM_ALERT_INGEST_TOKEN 不能为空")
		}
		alertWebhookToken = cfg.Secret.Key
	}
	alertEventIngestor := alerting.NewEventIngestor(alertRepository, alertRepository, alertWebhookToken, nil)
	iamRBACRepo := iam.NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings())
	iamSvc := iam.NewService(iamRepo, iamRBACRepo, rbacSvc)
	if cfg.Server.Mode != gin.ReleaseMode {
		_, err := iamSvc.CreateUser(ctx, rbac.DevAdminSubject(), iam.CreateUserRequest{
			Username:    rbac.DevAdminSubject().ID,
			DisplayName: "开发管理员",
			Password:    os.Getenv("NOVAAPM_DEV_ADMIN_PASSWORD"),
		})
		if err != nil {
			return nil, fmt.Errorf("初始化开发管理员用户失败: %w", err)
		}
	} else if bootstrapAdmin.ID != "" {
		bootstrapPassword := os.Getenv("NOVAAPM_BOOTSTRAP_ADMIN_PASSWORD")
		if strings.TrimSpace(bootstrapPassword) == "" {
			return nil, fmt.Errorf("NOVAAPM_BOOTSTRAP_ADMIN_PASSWORD 不能为空")
		}
		_, err := iamSvc.CreateUser(ctx, bootstrapAdmin, iam.CreateUserRequest{
			Username:    bootstrapAdmin.ID,
			DisplayName: bootstrapAdmin.DisplayName,
			Password:    bootstrapPassword,
		})
		if err != nil {
			return nil, fmt.Errorf("初始化平台管理员用户失败: %w", err)
		}
	}
	authSvc := platformauth.NewService(
		iamRepo,
		[]byte(cfg.Secret.Key),
		platformauth.WithPasswordlessLocalUsers(cfg.Server.Mode != gin.ReleaseMode),
	)
	auditSvc := audit.NewService(audit.NewStoreRepository(store.AuditEvents()))
	secretSvc := secret.NewService(secret.NewStoreRepository(store.Secrets()), secret.NewAESGCMEncryptor([]byte(cfg.Secret.Key)))
	imageSvc := platformimages.NewService(
		platformimages.NewStoreRepository(store.PlatformImages()),
		platformimages.WithAuthorizer(rbacSvc),
		platformimages.WithAuditor(auditSvc),
	)
	clusterCredentialSvc := cluster.NewCredentialService(secretSvc, rbacSvc, auditSvc)
	k8sClientProvider := kubeclient.NewProvider(clusterCredentialSvc)
	k8sOpsModule := k8sops.NewModuleWithSecurity(
		rbacSvc,
		auditSvc,
		secretSvc,
		cluster.NewStoreRepository(store.K8sClusters()),
		deployment.NewStoreInventoryRepository(store.K8sDeploymentInventory()),
		deployment.NewStoreHistoryRepository(store.K8sDeploymentHistory()),
		rbacRepo,
		platformaccess.NewIAMSubjectRepository(iamSvc),
		k8sClientProvider,
		terminal.NewKubectlExecutor(clusterCredentialSvc, terminal.KubectlExecutorConfig{
			BinaryPath: os.Getenv("NOVAAPM_KUBECTL_PATH"),
			TempDir:    os.Getenv("NOVAAPM_KUBECTL_TEMP_DIR"),
		}),
	)
	opampMgr := opamp.NewManager()
	logsSvc := logs.NewService(
		store.LogEndpoints(),
		store.LogSources(),
		store.LogRoutes(),
		store.LogCollectorConfigVersions(),
		store.LogDeploymentManifestVersions(),
		store.LogCollectorClusterConfigs(),
		svcRepo,
		targetRepo,
		collectorSvc,
		k8sOpsModule.Cluster,
		k8sOpsModule.Resource,
		k8sOpsModule.Deploy,
		logs.WithAgentOpAMPEndpoint(os.Getenv("NOVAAPM_LOGS_AGENT_OPAMP_ENDPOINT")),
		logs.WithImageTemplateValues(imageSvc),
		logs.WithLogTargets(store.LogTargets()),
		logs.WithObservabilityRuntimes(store.ObservabilityRuntimes()),
		logs.WithAuthorizer(rbacSvc),
		logs.WithEndpointAuditor(auditSvc),
	)
	endpointSvc := obsendpoint.NewLogEndpointFacade(store.LogEndpoints(), obsendpoint.WithAuthorizer(rbacSvc))
	metricsSvc := metrics.NewService(metrics.Dependencies{
		Bindings:       store.MetricsServiceBindings(),
		Routes:         store.MetricsRoutes(),
		Runtimes:       store.ObservabilityRuntimes(),
		Endpoints:      endpointSvc,
		Services:       svcRepo,
		K8sResources:   k8sOpsModule.Resource,
		K8sDeployments: k8sOpsModule.Deploy,
		ImageTemplates: imageSvc,
		Authorizer:     rbacSvc,
	})
	alertRuntimeSvc := alerting.NewLogRuntimeService(alerting.LogRuntimeDependencies{
		Endpoints:             store.LogEndpoints(),
		Runtimes:              store.ObservabilityRuntimes(),
		Repository:            alertRepository,
		ScopeResolver:         alertScopeResolver,
		K8sDeployments:        k8sOpsModule.Deploy,
		ImageTemplates:        imageSvc,
		DefaultAlertIngestURL: strings.TrimSpace(os.Getenv("NOVAAPM_ALERT_INGEST_URL")),
	})
	metricsRuntimeSvc := alerting.NewMetricsRuntimeService(alerting.MetricsRuntimeDependencies{
		Endpoints:             store.LogEndpoints(),
		Runtimes:              store.ObservabilityRuntimes(),
		Repository:            alertRepository,
		K8sDeployments:        k8sOpsModule.Deploy,
		ImageTemplates:        imageSvc,
		DefaultAlertIngestURL: strings.TrimSpace(os.Getenv("NOVAAPM_ALERT_INGEST_URL")),
	})

	deps := httpapi.Dependencies{
		Store:                  store,
		ProductRepo:            productRepo,
		ServiceRepo:            svcRepo,
		ServiceTargetRepo:      targetRepo,
		CollectorConfigService: collectorConfigSvc,
		CollectorService:       collectorSvc,
		OnboardingService:      onboardingSvc,
		LogsService:            logsSvc,
		ObservabilityEndpoints: endpointSvc,
		MetricsService:         metricsSvc,
		AlertRuntimeService:    alertRuntimeSvc,
		MetricsRuntimeService:  metricsRuntimeSvc,
		AlertService:           alertSvc,
		AlertEventIngestor:     alertEventIngestor,
		AlertPolicyService:     alertPolicySvc,
		PlatformIAMService:     iamSvc,
		PlatformImageService:   imageSvc,
		K8sOpsModule:           k8sOpsModule,
		OpAMPManager:           opampMgr,
		CollectorTemplate:      cfg.CollectorTemplate,
		PlatformAuthService:    authSvc,
	}
	return httpapi.NewRouter(deps), nil
}

func platformBootstrapAdmin() rbac.Subject {
	username := strings.TrimSpace(os.Getenv("NOVAAPM_BOOTSTRAP_ADMIN_USERNAME"))
	if username == "" {
		return rbac.Subject{}
	}
	displayName := strings.TrimSpace(os.Getenv("NOVAAPM_BOOTSTRAP_ADMIN_DISPLAY_NAME"))
	if displayName == "" {
		displayName = username
	}
	return rbac.Subject{ID: username, Type: iam.SubjectTypeUser, DisplayName: displayName}
}
