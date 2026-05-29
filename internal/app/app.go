package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"novaobs/internal/alerting"
	"novaobs/internal/collectorconfig"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/config"
	"novaobs/internal/database/mongo"
	"novaobs/internal/httpapi"
	"novaobs/internal/logs"
	"novaobs/internal/modules/k8sops"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/deployment"
	"novaobs/internal/modules/k8sops/kubeclient"
	"novaobs/internal/modules/k8sops/platformaccess"
	"novaobs/internal/modules/k8sops/terminal"
	"novaobs/internal/onboarding"
	"novaobs/internal/opamp"
	"novaobs/internal/platform/audit"
	platformauth "novaobs/internal/platform/auth"
	"novaobs/internal/platform/iam"
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
	iamRBACRepo := iam.NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings())
	iamSvc := iam.NewService(iamRepo, iamRBACRepo, rbacSvc)
	if cfg.Server.Mode != gin.ReleaseMode {
		_, err := iamSvc.CreateUser(ctx, rbac.DevAdminSubject(), iam.CreateUserRequest{
			Username:    rbac.DevAdminSubject().ID,
			DisplayName: "开发管理员",
			Password:    os.Getenv("NOVAOBS_DEV_ADMIN_PASSWORD"),
		})
		if err != nil {
			return nil, fmt.Errorf("初始化开发管理员用户失败: %w", err)
		}
	} else if bootstrapAdmin.ID != "" {
		bootstrapPassword := os.Getenv("NOVAOBS_BOOTSTRAP_ADMIN_PASSWORD")
		if strings.TrimSpace(bootstrapPassword) == "" {
			return nil, fmt.Errorf("NOVAOBS_BOOTSTRAP_ADMIN_PASSWORD 不能为空")
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
			BinaryPath: os.Getenv("NOVAOBS_KUBECTL_PATH"),
			TempDir:    os.Getenv("NOVAOBS_KUBECTL_TEMP_DIR"),
		}),
	)
	opampMgr := opamp.NewManager()
	logsSvc := logs.NewService(
		store.LogEndpoints(),
		store.LogSources(),
		store.LogRoutes(),
		store.LogAgentPlans(),
		svcRepo,
		targetRepo,
		collectorSvc,
		k8sOpsModule.Cluster,
		k8sOpsModule.Resource,
		k8sOpsModule.Deploy,
	)

	deps := httpapi.Dependencies{
		Store:                  store,
		ServiceRepo:            svcRepo,
		ServiceTargetRepo:      targetRepo,
		CollectorConfigService: collectorConfigSvc,
		CollectorService:       collectorSvc,
		OnboardingService:      onboardingSvc,
		LogsService:            logsSvc,
		AlertService:           alertSvc,
		PlatformIAMService:     iamSvc,
		K8sOpsModule:           k8sOpsModule,
		OpAMPManager:           opampMgr,
		CollectorTemplate:      cfg.CollectorTemplate,
		PlatformAuthService:    authSvc,
	}
	return httpapi.NewRouter(deps), nil
}

func platformBootstrapAdmin() rbac.Subject {
	username := strings.TrimSpace(os.Getenv("NOVAOBS_BOOTSTRAP_ADMIN_USERNAME"))
	if username == "" {
		return rbac.Subject{}
	}
	displayName := strings.TrimSpace(os.Getenv("NOVAOBS_BOOTSTRAP_ADMIN_DISPLAY_NAME"))
	if displayName == "" {
		displayName = username
	}
	return rbac.Subject{ID: username, Type: iam.SubjectTypeUser, DisplayName: displayName}
}
