package httpapi

import (
	"strings"

	"novaobs/internal/collectorconfig"
	"novaobs/internal/collectormanagement"
	"novaobs/internal/database"
	"novaobs/internal/onboarding"
	"novaobs/internal/opamp"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

type opAMPAgentDetail struct {
	InstanceUID    string                                `json:"instance_uid"`
	Runtime        collectormanagement.CollectorInstance `json:"runtime"`
	Agent          opamp.AgentRuntimeDetail              `json:"agent"`
	CollectorGroup *collectormanagement.CollectorGroup   `json:"collector_group,omitempty"`
	Services       []servicecatalog.Service              `json:"services"`
	Onboardings    []onboarding.ServiceOnboarding        `json:"onboardings"`
	Configuration  opAMPAgentDetailConfiguration         `json:"configuration"`
}

type opAMPAgentDetailConfiguration struct {
	EffectiveConfig        string                                    `json:"effective_config"`
	EffectiveConfigFiles   map[string]string                         `json:"effective_config_files"`
	EffectiveConfigHash    string                                    `json:"effective_config_hash"`
	LastRemoteConfig       string                                    `json:"last_remote_config"`
	LastRemoteConfigFiles  map[string]string                         `json:"last_remote_config_files"`
	LastRemoteConfigHash   string                                    `json:"last_remote_config_hash"`
	ExpectedRenderedConfig string                                    `json:"expected_rendered_config"`
	ExpectedConfigHash     string                                    `json:"expected_config_hash"`
	InSync                 bool                                      `json:"in_sync"`
	ApplyStatus            string                                    `json:"apply_status"`
	ConfigSources          collectorconfig.ConfigSources             `json:"config_sources"`
	AdditionalConfig       collectorconfig.CollectorAdditionalConfig `json:"additional_config"`
}

type additionalConfigRequest struct {
	YAMLPatch string `json:"yaml_patch"`
	Send      bool   `json:"send"`
}

func getOpAMPAgentDetailHandler(deps Dependencies) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := strings.TrimSpace(ctx.Param("uid"))
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}

		runtime, runtimeFound, err := findCollectorInstanceByUID(deps.CollectorService, uid)
		if err != nil {
			writeError(ctx, err)
			return
		}

		var runtimeDetail opamp.AgentRuntimeDetail
		detailFound := false
		if deps.OpAMPManager != nil {
			runtimeDetail, detailFound = deps.OpAMPManager.GetAgentDetail(uid)
		}
		if !runtimeFound && !detailFound {
			writeError(ctx, apperr.NotFound("OpAMP Agent 不存在"))
			return
		}
		if !runtimeFound {
			runtime = collectormanagement.CollectorInstance{
				ID:                  uid,
				InstanceUID:         uid,
				CollectorGroupID:    runtimeDetail.State.CollectorGroupID,
				Online:              runtimeDetail.State.Online,
				Healthy:             runtimeDetail.State.Healthy,
				Capabilities:        runtimeDetail.State.Capabilities,
				RemoteConfigCapable: runtimeDetail.State.RemoteConfigCapable,
				EffectiveConfigHash: runtimeDetail.State.EffectiveConfigHash,
				RemoteConfigStatus:  runtimeDetail.State.RemoteConfigStatus,
				LastConfigHash:      runtimeDetail.State.LastConfigHash,
				LastError:           runtimeDetail.State.LastError,
				LastSeenAt:          runtimeDetail.State.LastSeenAt,
				UpdatedAt:           runtimeDetail.State.LastSeenAt,
			}
			runtime = deps.CollectorService.ApplyRuntimeStatus(runtime)
		}

		group, groupFound, err := findCollectorGroup(deps.CollectorService, runtime.CollectorGroupID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		onboardings, err := onboardingsForGroup(deps.Store.Onboardings(), runtime.CollectorGroupID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		services, err := servicesForOnboardings(deps.ServiceRepo, onboardings)
		if err != nil {
			writeError(ctx, err)
			return
		}

		configuration := opAMPAgentConfiguration(deps.CollectorService, deps.CollectorConfigService, uid, runtime.CollectorGroupID, runtimeDetail)
		response.OK(ctx, opAMPAgentDetail{
			InstanceUID:    uid,
			Runtime:        runtime,
			Agent:          runtimeDetail,
			CollectorGroup: optionalCollectorGroup(group, groupFound),
			Services:       services,
			Onboardings:    onboardings,
			Configuration:  configuration,
		}, gin.H{})
	}
}

func findCollectorInstanceByUID(service collectormanagement.Service, uid string) (collectormanagement.CollectorInstance, bool, error) {
	instances, err := service.ListInstances(bg, "")
	if err != nil {
		return collectormanagement.CollectorInstance{}, false, err
	}
	for _, instance := range instances {
		if instance.InstanceUID == uid {
			return instance, true, nil
		}
	}
	return collectormanagement.CollectorInstance{}, false, nil
}

func findCollectorGroup(service collectormanagement.Service, groupID string) (collectormanagement.CollectorGroup, bool, error) {
	if strings.TrimSpace(groupID) == "" {
		return collectormanagement.CollectorGroup{}, false, nil
	}
	group, err := service.GetGroup(bg, groupID)
	if err != nil {
		return collectormanagement.CollectorGroup{}, false, err
	}
	return group, true, nil
}

func optionalCollectorGroup(group collectormanagement.CollectorGroup, ok bool) *collectormanagement.CollectorGroup {
	if !ok {
		return nil
	}
	return &group
}

func onboardingsForGroup(store database.OnboardingStore, groupID string) ([]onboarding.ServiceOnboarding, error) {
	if store == nil || strings.TrimSpace(groupID) == "" {
		return []onboarding.ServiceOnboarding{}, nil
	}
	var onboardings []onboarding.ServiceOnboarding
	if err := store.FindByCollectorGroup(bg, groupID, &onboardings); err != nil {
		return nil, err
	}
	return onboardings, nil
}

func servicesForOnboardings(repo servicecatalog.Repository, onboardings []onboarding.ServiceOnboarding) ([]servicecatalog.Service, error) {
	seen := map[string]struct{}{}
	serviceIDs := make([]string, 0, len(onboardings))
	for _, item := range onboardings {
		if item.ServiceID == "" {
			continue
		}
		if _, ok := seen[item.ServiceID]; ok {
			continue
		}
		seen[item.ServiceID] = struct{}{}
		serviceIDs = append(serviceIDs, item.ServiceID)
	}
	services := make([]servicecatalog.Service, 0, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		service, err := repo.Get(bg, serviceID)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, nil
}

func opAMPAgentConfiguration(service collectormanagement.Service, configService collectorconfig.Service, instanceUID string, groupID string, runtime opamp.AgentRuntimeDetail) opAMPAgentDetailConfiguration {
	config := opAMPAgentDetailConfiguration{
		EffectiveConfig:       runtime.EffectiveConfig,
		EffectiveConfigFiles:  runtime.EffectiveConfigFiles,
		EffectiveConfigHash:   runtime.State.EffectiveConfigHash,
		LastRemoteConfig:      runtime.LastRemoteConfig,
		LastRemoteConfigFiles: runtime.LastRemoteConfigFiles,
		LastRemoteConfigHash:  runtime.LastRemoteConfigHash,
	}
	if strings.TrimSpace(groupID) == "" {
		return config
	}
	if additional, err := configService.GetAdditionalConfig(bg, instanceUID); err == nil {
		config.AdditionalConfig = additional
	}
	if sources, err := configService.ConfigSources(bg, groupID); err == nil {
		config.ConfigSources = sources
		config.ExpectedRenderedConfig = sources.RenderedYAML
		config.ExpectedConfigHash = sources.ConfigHash
	}
	version, err := service.LatestConfigVersion(bg, groupID)
	if err == nil {
		if config.ExpectedRenderedConfig == "" {
			config.ExpectedRenderedConfig = version.CollectorYAML
			config.ExpectedConfigHash = version.ConfigHash
		}
	}
	config.InSync = strings.TrimSpace(config.ExpectedConfigHash) != "" && config.ExpectedConfigHash == runtime.State.EffectiveConfigHash
	config.ApplyStatus = runtime.State.RemoteConfigStatus
	return config
}

func putOpAMPAgentAdditionalConfigHandler(deps Dependencies) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uid := strings.TrimSpace(ctx.Param("uid"))
		if uid == "" {
			writeError(ctx, apperr.InvalidRequest("instance_uid 不能为空"))
			return
		}
		runtime, runtimeFound, err := findCollectorInstanceByUID(deps.CollectorService, uid)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if !runtimeFound {
			writeError(ctx, apperr.NotFound("OpAMP Agent 不存在"))
			return
		}
		var body additionalConfigRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("Additional Configuration 请求无效"))
			return
		}
		cfg, err := deps.CollectorConfigService.UpsertAdditionalConfig(bg, uid, body.YAMLPatch, runtime.CollectorGroupID)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if body.Send {
			sources, err := deps.CollectorConfigService.ConfigSources(bg, runtime.CollectorGroupID)
			if err != nil {
				writeError(ctx, err)
				return
			}
			files := map[string]string{
				"collector.yaml": sources.RenderedYAML,
				"":               cfg.YAMLPatch,
			}
			hash := opamp.HashConfigFiles(files)
			deployment := collectorconfig.RemoteConfigDeployment{
				ID:                   "agent-additional:" + uid,
				CollectorInstanceUID: uid,
				CollectorGroupID:     runtime.CollectorGroupID,
				Version:              cfg.Version,
				ConfigHash:           hash,
				CollectorYAML:        sources.RenderedYAML,
				ConfigFiles:          files,
				Status:               "pending",
			}
			if deps.OpAMPManager != nil {
				deps.OpAMPManager.QueueDeployment(deployment)
			}
			if updated, err := deps.CollectorConfigService.MarkAdditionalConfigPending(bg, uid, hash); err == nil {
				cfg = updated
			}
		}
		response.OK(ctx, cfg, gin.H{})
	}
}
