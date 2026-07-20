package environment

import (
	"context"
	"strings"

	"novaapm/internal/database"
	"novaapm/pkg/apperr"
)

type StoreResourceValidator struct {
	clusters   database.K8sClusterStore
	hostGroups database.CollectorGroupStore
}

func NewStoreResourceValidator(clusters database.K8sClusterStore, hostGroups database.CollectorGroupStore) StoreResourceValidator {
	return StoreResourceValidator{clusters: clusters, hostGroups: hostGroups}
}

func (v StoreResourceValidator) Validate(ctx context.Context, resourceKind string, resourceRef string) error {
	resourceRef = strings.TrimSpace(resourceRef)
	switch resourceKind {
	case ResourceKindK8sCluster:
		record, found := v.findCluster(ctx, resourceRef)
		if !found {
			return apperr.InvalidRequest("K8s 集群不存在")
		}
		return validateResourceStatus(record.Status)
	case ResourceKindHostGroup:
		var record resourceRecord
		if v.hostGroups == nil || v.hostGroups.FindByID(ctx, resourceRef, &record) != nil {
			return apperr.InvalidRequest("主机组不存在")
		}
		return validateResourceStatus(record.Status)
	default:
		return apperr.InvalidRequest("环境资源类型无效")
	}

}

type resourceRecord struct {
	ID     string `json:"id" bson:"_id"`
	Status string `json:"status" bson:"status"`
}

func (v StoreResourceValidator) findCluster(ctx context.Context, resourceRef string) (resourceRecord, bool) {
	if v.clusters == nil {
		return resourceRecord{}, false
	}
	var records []resourceRecord
	if err := v.clusters.FindAll(ctx, &records); err != nil {
		return resourceRecord{}, false
	}
	for _, record := range records {
		if record.ID == resourceRef {
			return record, true
		}
	}
	return resourceRecord{}, false
}

func validateResourceStatus(status string) error {
	if status == "deleted" || status == "disabled" {
		return apperr.InvalidRequest("环境资源不可用")
	}
	return nil
}
