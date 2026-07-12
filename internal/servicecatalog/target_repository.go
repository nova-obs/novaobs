package servicecatalog

import (
	"context"
	"strings"
	"time"

	"novaapm/internal/database"
	"novaapm/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type TargetRepository struct {
	store database.ServiceTargetStore
}

func NewTargetRepository(store database.ServiceTargetStore) TargetRepository {
	return TargetRepository{store: store}
}

func (r TargetRepository) Create(ctx context.Context, target ObservedTarget) (ObservedTarget, error) {
	if err := validateTarget(target); err != nil {
		return ObservedTarget{}, err
	}
	if target.ID == "" {
		target.ID = primitive.NewObjectID().Hex()
	}
	now := time.Now().UTC()
	if target.CreatedAt.IsZero() {
		target.CreatedAt = now
	}
	target.UpdatedAt = now
	target = normalizeTarget(target)
	if err := r.store.Insert(ctx, target); err != nil {
		return ObservedTarget{}, err
	}
	return target, nil
}

func (r TargetRepository) ListByService(ctx context.Context, serviceID string) ([]ObservedTarget, error) {
	var targets []ObservedTarget
	if err := r.store.FindByService(ctx, serviceID, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func validateTarget(target ObservedTarget) error {
	if strings.TrimSpace(target.ServiceID) == "" {
		return apperr.InvalidRequest("服务目标必须关联服务")
	}
	if strings.TrimSpace(target.TargetType) == "" {
		return apperr.InvalidRequest("服务目标类型不能为空")
	}
	switch target.TargetType {
	case "cloud_native_workload", "host_process", "physical_or_network_device":
	default:
		return apperr.InvalidRequest("服务目标类型只能是 cloud_native_workload、host_process 或 physical_or_network_device")
	}
	if strings.TrimSpace(target.EnvironmentID) == "" {
		return apperr.InvalidRequest("服务目标环境不能为空")
	}
	if target.Source != "" && target.Source != "manual" && target.Source != "cmdb" && target.Source != "discovered" {
		return apperr.InvalidRequest("服务目标来源只能是 manual、cmdb 或 discovered")
	}
	if target.SyncStatus != "" && target.SyncStatus != "local" && target.SyncStatus != "synced" && target.SyncStatus != "stale" && target.SyncStatus != "conflict" {
		return apperr.InvalidRequest("服务目标同步状态只能是 local、synced、stale 或 conflict")
	}
	return nil
}

func normalizeTarget(target ObservedTarget) ObservedTarget {
	target.ServiceID = strings.TrimSpace(target.ServiceID)
	target.TargetType = strings.TrimSpace(target.TargetType)
	target.EnvironmentID = strings.TrimSpace(target.EnvironmentID)
	target.DisplayName = strings.TrimSpace(target.DisplayName)
	if target.IdentityAttributes == nil {
		target.IdentityAttributes = map[string]string{}
	}
	if target.MatchRules == nil {
		target.MatchRules = map[string]string{}
	}
	if target.Source == "" {
		target.Source = "manual"
	}
	if target.SyncStatus == "" {
		if target.Source == "manual" {
			target.SyncStatus = "local"
		} else {
			target.SyncStatus = "synced"
		}
	}
	if target.DisplayName == "" {
		target.DisplayName = target.TargetType
	}
	return target
}
