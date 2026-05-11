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
	"novaobs/internal/onboarding"
	"novaobs/internal/opamp"
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
	collectorSvc := collectormanagement.NewService(store.CollectorGroups(), store.CollectorInstances(), collectormanagement.WithConfigVersionStore(store.CollectorConfigVersions()))
	onboardingSvc := onboarding.NewService(store.Onboardings(), store.IngestionIdentities(), svcRepo, collectorSvc)
	collectorConfigSvc := collectorconfig.NewService(
		store.CollectorPlatformTemplates(),
		store.CollectorGroupOverrides(),
		store.ServiceEnrichmentPatches(),
		store.ServiceParserRules(),
		store.ServicePipelinePatches(),
		store.CollectorAdditionalConfigs(),
		collectorSvc,
		svcRepo,
	)
	alertSvc := alerting.NewService(store.AlertRules())
	logQuerySvc := logquery.NewService()
	opampMgr := opamp.NewManager()

	return httpapi.NewRouter(httpapi.Dependencies{
		Store:                  store,
		ServiceRepo:            svcRepo,
		CollectorConfigService: collectorConfigSvc,
		CollectorService:       collectorSvc,
		OnboardingService:      onboardingSvc,
		LogQueryService:        logQuerySvc,
		AlertService:           alertSvc,
		OpAMPManager:           opampMgr,
		CollectorTemplate:      cfg.CollectorTemplate,
	}), nil
}
