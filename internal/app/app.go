package app

import (
	"context"
	"fmt"

	"novaobs/internal/alerting"
	"novaobs/internal/collectorconfig"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/config"
	"novaobs/internal/database/mongo"
	"novaobs/internal/httpapi"
	"novaobs/internal/logquery"
	"novaobs/internal/modules/k8sops"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/namespace"
	"novaobs/internal/onboarding"
	"novaobs/internal/opamp"
	"novaobs/internal/platform/audit"
	"novaobs/internal/platform/rbac"
	"novaobs/internal/platform/secret"
	"novaobs/internal/servicecatalog"

	"github.com/gin-gonic/gin"
)

func New(cfg config.Config) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)

	ctx := context.Background()
	store, err := mongo.NewStore(ctx, cfg.Database.URI)
	if err != nil {
		return nil, fmt.Errorf("连接 MongoDB 失败: %w", err)
	}

	svcRepo := servicecatalog.NewRepository(store.Services())
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
	alertSvc := alerting.NewService(store.AlertRules())
	logQuerySvc := logquery.NewService()
	rbacRepo := rbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	if cfg.Server.Mode != gin.ReleaseMode {
		if err := rbac.EnsureK8sOpsDefaults(rbacRepo, rbac.DevAdminSubject(), rbac.DevK8sOpsScope()); err != nil {
			return nil, fmt.Errorf("初始化 K8s 运维 RBAC 失败: %w", err)
		}
	}
	rbacSvc := rbac.NewService(rbacRepo)
	auditSvc := audit.NewService(audit.NewStoreRepository(store.AuditEvents()))
	secretSvc := secret.NewService(secret.NewStoreRepository(store.Secrets()), secret.NewAESGCMEncryptor([]byte(cfg.Secret.Key)))
	k8sOpsModule := k8sops.NewModuleWithSecurity(
		rbacSvc,
		auditSvc,
		secretSvc,
		cluster.NewStoreRepository(store.K8sClusters()),
		namespace.NewStoreRepository(store.K8sNamespaces()),
	)
	opampMgr := opamp.NewManager()

	deps := httpapi.Dependencies{
		Store:                  store,
		ServiceRepo:            svcRepo,
		ServiceTargetRepo:      targetRepo,
		CollectorConfigService: collectorConfigSvc,
		CollectorService:       collectorSvc,
		OnboardingService:      onboardingSvc,
		LogQueryService:        logQuerySvc,
		AlertService:           alertSvc,
		K8sOpsModule:           k8sOpsModule,
		OpAMPManager:           opampMgr,
		CollectorTemplate:      cfg.CollectorTemplate,
	}
	if cfg.Server.Mode != gin.ReleaseMode {
		deps.DefaultSubject = rbac.DevAdminSubject()
	}
	return httpapi.NewRouter(deps), nil
}
