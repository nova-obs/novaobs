package httpapi

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"novaapm/internal/collectorconfig"
	"novaapm/internal/collectormanagement"
	"novaapm/internal/opamp"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

type collectorGroupConfigStatus struct {
	CollectorGroup    collectormanagement.CollectorGroup          `json:"collector_group"`
	DesiredConfigHash string                                      `json:"desired_config_hash"`
	LatestVersion     *collectormanagement.CollectorConfigVersion `json:"latest_version"`
	Agents            []collectorGroupAgentConfigStatus           `json:"agents"`
}

type collectorGroupAgentConfigStatus struct {
	InstanceUID         string `json:"instance_uid"`
	RuntimeStatus       string `json:"runtime_status"`
	Online              bool   `json:"online"`
	Healthy             bool   `json:"healthy"`
	RemoteConfigCapable bool   `json:"remote_config_capable"`
	RemoteConfigStatus  string `json:"remote_config_status"`
	LastConfigHash      string `json:"last_config_hash"`
	EffectiveConfigHash string `json:"effective_config_hash"`
	InSync              bool   `json:"in_sync"`
	LastError           string `json:"last_error"`
	LastSeenAt          string `json:"last_seen_at"`
}

type groupOverrideRequest struct {
	OverrideYAML string `json:"override_yaml"`
}

func getCollectorGroupConfigSourcesHandler(service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		sources, err := service.ConfigSources(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 配置来源不存在"))
			return
		}
		response.OK(ctx, sources, gin.H{})
	}
}

func putCollectorGroupOverrideHandler(service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		var body groupOverrideRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("Group Override 请求无效"))
			return
		}
		override, err := service.UpsertGroupOverride(bg, id, body.OverrideYAML)
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, override, gin.H{})
	}
}

func validateCollectorGroupConfigHandler(service collectorconfig.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		validation, err := service.ValidateGroup(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		if !validation.Valid {
			writeError(ctx, apperr.InvalidRequest(strings.Join(validation.Errors, "; ")))
			return
		}
		response.OK(ctx, validation, gin.H{})
	}
}

func publishCollectorGroupConfigHandler(configService collectorconfig.Service, collectorService collectormanagement.Service, manager *opamp.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseID(ctx)
		if !ok {
			return
		}
		group, err := collectorService.GetGroup(bg, id)
		if err != nil {
			writeError(ctx, normalizeNotFound(err, "Collector Group 不存在"))
			return
		}
		validation, err := configService.ValidateGroup(bg, id)
		if err != nil {
			writeError(ctx, err)
			return
		}
		if !validation.Valid {
			writeError(ctx, apperr.InvalidRequest(strings.Join(validation.Errors, "; ")))
			return
		}
		publishMessage := "无在线可下发 Collector，等待 Collector 实例拉取配置"
		activeDeliveryCount := 0
		deployment := collectorconfig.RemoteConfigDeployment{
			ID:                   fmt.Sprintf("collector-group:%s:%d", group.ID, group.ConfigVersion+1),
			CollectorInstanceUID: fmt.Sprintf("group:%s", group.ID),
			CollectorGroupID:     group.ID,
			Version:              group.ConfigVersion + 1,
			ConfigHash:           validation.ConfigHash,
			CollectorYAML:        validation.RenderedYAML,
			Status:               "pending",
		}
		if manager != nil {
			activeDeliveryCount, err = manager.SendGroupDeployment(bg, group.ID, deployment)
			if err != nil {
				writeError(ctx, err)
				return
			}
			if activeDeliveryCount > 0 {
				publishMessage = fmt.Sprintf("Remote Config 已发送给 %d 个在线 Collector，等待应用回执", activeDeliveryCount)
			}
		}
		group, err = collectorService.MarkGroupPublishPending(bg, group.ID, validation.ConfigHash, publishMessage)
		if err != nil {
			writeError(ctx, err)
			return
		}
		version, err := collectorService.CreateConfigVersion(bg, collectormanagement.CollectorConfigVersion{
			CollectorGroupID: group.ID,
			Version:          group.ConfigVersion,
			ConfigHash:       validation.ConfigHash,
			CollectorYAML:    validation.RenderedYAML,
			Status:           "pending",
			CreatedBy:        "system",
			Message:          publishMessage,
		})
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, version, gin.H{"active_delivery_count": activeDeliveryCount})
	}
}

func getCollectorGroupConfigStatusHandler(service collectormanagement.Service) gin.HandlerFunc {
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
		instances, err := service.ListInstances(bg, id)
		if err != nil {
			writeError(ctx, err)
			return
		}
		var latest *collectormanagement.CollectorConfigVersion
		if version, err := service.LatestConfigVersion(bg, id); err == nil {
			latest = &version
		}
		agents := make([]collectorGroupAgentConfigStatus, 0, len(instances))
		for _, instance := range instances {
			agents = append(agents, collectorGroupAgentStatus(group.DesiredConfigHash, instance))
		}
		sort.Slice(agents, func(i, j int) bool {
			return agents[i].InstanceUID < agents[j].InstanceUID
		})
		response.OK(ctx, collectorGroupConfigStatus{
			CollectorGroup:    group,
			DesiredConfigHash: group.DesiredConfigHash,
			LatestVersion:     latest,
			Agents:            agents,
		}, gin.H{})
	}
}

func collectorGroupAgentStatus(desiredHash string, instance collectormanagement.CollectorInstance) collectorGroupAgentConfigStatus {
	inSync := strings.TrimSpace(desiredHash) != "" &&
		(instance.LastConfigHash == desiredHash || instance.EffectiveConfigHash == desiredHash)
	lastSeenAt := ""
	if !instance.LastSeenAt.IsZero() {
		lastSeenAt = instance.LastSeenAt.Format(time.RFC3339)
	}
	return collectorGroupAgentConfigStatus{
		InstanceUID:         instance.InstanceUID,
		RuntimeStatus:       instance.RuntimeStatus,
		Online:              instance.Online,
		Healthy:             instance.Healthy,
		RemoteConfigCapable: instance.RemoteConfigCapable,
		RemoteConfigStatus:  instance.RemoteConfigStatus,
		LastConfigHash:      instance.LastConfigHash,
		EffectiveConfigHash: instance.EffectiveConfigHash,
		InSync:              inSync,
		LastError:           instance.LastError,
		LastSeenAt:          lastSeenAt,
	}
}
