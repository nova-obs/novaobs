package servicecatalog

import (
	"context"
	"strings"
	"time"

	"novaobs/internal/database"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Repository struct {
	store database.ServiceStore
}

func NewRepository(store database.ServiceStore) Repository {
	return Repository{store: store}
}

func (r Repository) List(ctx context.Context, filters ...ListFilter) ([]Service, error) {
	var services []Service
	if err := r.store.FindAll(ctx, &services); err != nil {
		return nil, err
	}
	if len(filters) == 0 {
		return applyFilter(services, ListFilter{}), nil
	}
	return applyFilter(services, filters[0]), nil
}

func (r Repository) Get(ctx context.Context, id string) (Service, error) {
	var service Service
	err := r.store.FindByID(ctx, id, &service)
	return service, err
}

func (r Repository) Create(ctx context.Context, service Service) (Service, error) {
	if err := validateService(service); err != nil {
		return Service{}, err
	}
	if service.ID == "" {
		service.ID = primitive.NewObjectID().Hex()
	}
	now := time.Now().UTC()
	if service.CreatedAt.IsZero() {
		service.CreatedAt = now
	}
	service.UpdatedAt = now
	service = normalizeService(service)
	if err := r.ensureUnique(ctx, service); err != nil {
		return Service{}, err
	}
	if err := r.store.Insert(ctx, service); err != nil {
		return Service{}, err
	}
	return service, nil
}

func (r Repository) Update(ctx context.Context, id string, req UpdateRequest) (Service, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return Service{}, err
	}
	updated := applyUpdate(current, req)
	if err := validateService(updated); err != nil {
		return Service{}, err
	}
	updated = normalizeService(updated)
	updated.ID = current.ID
	updated.CreatedAt = current.CreatedAt
	updated.LastSyncedAt = current.LastSyncedAt
	updated.UpdatedAt = time.Now().UTC()
	if err := r.ensureUnique(ctx, updated); err != nil {
		return Service{}, err
	}
	if err := r.store.Update(ctx, id, updated); err != nil {
		return Service{}, err
	}
	return updated, nil
}

func (r Repository) Delete(ctx context.Context, id string, deps DeleteDependencies) (Service, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return Service{}, err
	}
	if deps.LogRouteRefs > 0 {
		return Service{}, apperr.Conflict("服务仍有关联日志路由，不能删除")
	}
	if deps.AgentRefs > 0 {
		return Service{}, apperr.Conflict("服务仍有关联采集 Agent，不能删除")
	}
	if deps.OnboardingRefs > 0 {
		return Service{}, apperr.Conflict("服务仍有关联接入配置，不能删除")
	}
	if current.Status == "deleted" {
		return current, nil
	}
	current.Status = "deleted"
	current.UpdatedAt = time.Now().UTC()
	if err := r.store.Update(ctx, id, current); err != nil {
		return Service{}, err
	}
	return current, nil
}

func (r Repository) ensureUnique(ctx context.Context, service Service) error {
	services, err := r.List(ctx)
	if err != nil {
		return err
	}
	for _, existing := range services {
		if existing.ID == service.ID {
			continue
		}
		if existing.Status == "deleted" {
			continue
		}
		if service.CMDBServiceID != "" && existing.CMDBServiceID == service.CMDBServiceID {
			return apperr.Conflict("CMDB 服务 ID 已存在")
		}
		if sameServiceIdentity(existing, service) {
			return apperr.Conflict("相同名称、环境、集群和 Namespace 的服务已存在")
		}
	}
	return nil
}

func validateService(service Service) error {
	if strings.TrimSpace(service.Name) == "" {
		return apperr.InvalidRequest("服务名称不能为空")
	}
	if strings.TrimSpace(service.Environment) == "" {
		return apperr.InvalidRequest("服务环境不能为空")
	}
	if service.Source != "" && service.Source != "manual" && service.Source != "cmdb" && service.Source != "k8s" {
		return apperr.InvalidRequest("服务来源只能是 manual、cmdb 或 k8s")
	}
	if service.Status != "" && service.Status != "pending" && service.Status != "active" && service.Status != "degraded" && service.Status != "deleted" {
		return apperr.InvalidRequest("服务状态只能是 pending、active、degraded 或 deleted")
	}
	if service.SyncStatus != "" && service.SyncStatus != "local" && service.SyncStatus != "synced" && service.SyncStatus != "stale" {
		return apperr.InvalidRequest("服务同步状态只能是 local、synced 或 stale")
	}
	if service.IdentityType != "" && service.IdentityType != "k8s_workload" && service.IdentityType != "host_process" {
		return apperr.InvalidRequest("服务身份类型只能是 k8s_workload 或 host_process")
	}
	return nil
}

func normalizeService(service Service) Service {
	service.Name = strings.TrimSpace(service.Name)
	service.Environment = strings.TrimSpace(service.Environment)
	service.Cluster = strings.TrimSpace(service.Cluster)
	service.Namespace = strings.TrimSpace(service.Namespace)
	service.CMDBServiceID = strings.TrimSpace(service.CMDBServiceID)
	service.BusinessID = strings.TrimSpace(service.BusinessID)
	service.ApplicationID = strings.TrimSpace(service.ApplicationID)
	service.OwnerTeam = strings.TrimSpace(service.OwnerTeam)
	service.Owner = strings.TrimSpace(service.Owner)
	service.AlertRoute = strings.TrimSpace(service.AlertRoute)
	service.SLOLevel = strings.TrimSpace(service.SLOLevel)
	service.IdentityType = strings.TrimSpace(service.IdentityType)
	service.ServiceType = strings.TrimSpace(service.ServiceType)
	if service.IdentityType == "" {
		service.IdentityType = "k8s_workload"
	}
	if service.ServiceType == "" && service.IdentityType == "k8s_workload" {
		service.ServiceType = "k8s业务"
	}
	if service.DisplayName == "" {
		service.DisplayName = service.Name
	}
	if service.Status == "" {
		service.Status = "pending"
	}
	if service.Source == "" {
		service.Source = "manual"
	}
	if service.SyncStatus == "" {
		if service.Source == "cmdb" {
			service.SyncStatus = "synced"
		} else {
			service.SyncStatus = "local"
		}
	}
	return service
}

func applyUpdate(service Service, req UpdateRequest) Service {
	if req.CMDBServiceID != nil {
		service.CMDBServiceID = *req.CMDBServiceID
	}
	if req.BusinessID != nil {
		service.BusinessID = *req.BusinessID
	}
	if req.ApplicationID != nil {
		service.ApplicationID = *req.ApplicationID
	}
	if req.Name != nil {
		service.Name = *req.Name
	}
	if req.DisplayName != nil {
		service.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		service.Description = *req.Description
	}
	if req.Environment != nil {
		service.Environment = *req.Environment
	}
	if req.Cluster != nil {
		service.Cluster = *req.Cluster
	}
	if req.Namespace != nil {
		service.Namespace = *req.Namespace
	}
	if req.OwnerTeam != nil {
		service.OwnerTeam = *req.OwnerTeam
	}
	if req.Owner != nil {
		service.Owner = *req.Owner
	}
	if req.AlertRoute != nil {
		service.AlertRoute = *req.AlertRoute
	}
	if req.SLOLevel != nil {
		service.SLOLevel = *req.SLOLevel
	}
	if req.IdentityType != nil {
		service.IdentityType = *req.IdentityType
	}
	if req.ServiceType != nil {
		service.ServiceType = *req.ServiceType
	}
	if req.Status != nil {
		service.Status = *req.Status
	}
	if req.Source != nil {
		service.Source = *req.Source
	}
	if req.SyncStatus != nil {
		service.SyncStatus = *req.SyncStatus
	}
	return service
}

func applyFilter(services []Service, filter ListFilter) []Service {
	out := make([]Service, 0, len(services))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, service := range services {
		if filter.Status == "" && service.Status == "deleted" {
			continue
		}
		if filter.Environment != "" && service.Environment != filter.Environment {
			continue
		}
		if filter.Status != "" && service.Status != filter.Status {
			continue
		}
		if filter.Source != "" && service.Source != filter.Source {
			continue
		}
		if query != "" && !serviceMatchesQuery(service, query) {
			continue
		}
		out = append(out, service)
	}
	return out
}

func serviceMatchesQuery(service Service, query string) bool {
	values := []string{
		service.Name,
		service.DisplayName,
		service.CMDBServiceID,
		service.BusinessID,
		service.ApplicationID,
		service.OwnerTeam,
		service.Owner,
		service.AlertRoute,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func sameServiceIdentity(a Service, b Service) bool {
	return a.Name == b.Name &&
		a.Environment == b.Environment &&
		a.Cluster == b.Cluster &&
		a.Namespace == b.Namespace
}
