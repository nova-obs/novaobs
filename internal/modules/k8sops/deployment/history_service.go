package deployment

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/kubeclient"
	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"gopkg.in/yaml.v3"
)

var (
	ErrPermissionDenied     = errors.New("permission_denied")
	ErrInvalidRequest       = errors.New("invalid_k8s_deployment_request")
	ErrConfirmationMismatch = errors.New("confirmation_mismatch")
	ErrClusterReadOnly      = cluster.ErrClusterReadOnly
)

type Reader interface {
	ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error)
	ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error)
}

type Service struct {
	reader        Reader
	authorizer    Authorizer
	auditor       Auditor
	auditReader   AuditEventReader
	capabilities  CapabilityProvider
	dryRunner     OperationDryRunner
	applier       OperationApplier
	deleter       OperationDeleter
	inventory     InventoryRepository
	history       HistoryWriter
	clusterPolicy ClusterPolicy
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type AuditEventReader interface {
	List(ctx context.Context) ([]audit.Event, error)
}

type CapabilityProvider interface {
	Capabilities(ctx context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error)
}

type OperationDryRunner interface {
	DryRunApply(ctx context.Context, req kubeclient.ClusterDryRunApplyRequest) (kubeclient.DryRunApplyResult, error)
}

type OperationApplier interface {
	Apply(ctx context.Context, req kubeclient.ClusterApplyRequest) (kubeclient.ResourceOperationResult, error)
}

type OperationDeleter interface {
	Delete(ctx context.Context, req kubeclient.ClusterDeleteRequest) (kubeclient.ResourceOperationResult, error)
}

type ClusterPolicy = cluster.ReadOnlyPolicy

func NewService(reader Reader, security ...any) Service {
	service := Service{reader: reader, authorizer: denyAuthorizer{}, auditor: noopAuditor{}}
	for _, item := range security {
		if value, ok := item.(Authorizer); ok && value != nil {
			service.authorizer = value
		}
		if value, ok := item.(Auditor); ok && value != nil {
			service.auditor = value
		}
		if value, ok := item.(AuditEventReader); ok && value != nil {
			service.auditReader = value
		}
		if value, ok := item.(CapabilityProvider); ok && value != nil {
			service.capabilities = value
		}
		if value, ok := item.(OperationDryRunner); ok && value != nil {
			service.dryRunner = value
		}
		if value, ok := item.(OperationApplier); ok && value != nil {
			service.applier = value
		}
		if value, ok := item.(OperationDeleter); ok && value != nil {
			service.deleter = value
		}
		if value, ok := item.(InventoryRepository); ok && value != nil {
			service.inventory = value
		}
		if value, ok := item.(HistoryWriter); ok && value != nil {
			service.history = value
		}
		if value, ok := item.(ClusterPolicy); ok && value != nil {
			service.clusterPolicy = value
		}
	}
	return service
}

func (s Service) ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error) {
	return s.reader.ListHistory(ctx, filter)
}

func (s Service) ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	unpaged := filter
	unpaged.Page = 0
	unpaged.PageSize = 0
	items, err := s.reader.ListAuditEvents(ctx, unpaged)
	if err != nil {
		return nil, err
	}
	if s.auditReader != nil {
		events, err := s.auditReader.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			item, ok := platformAuditToK8sEvent(event, filter)
			if ok {
				items = append(items, item)
			}
		}
	}
	sortAuditEvents(items, filter.Order)
	return paginateAudit(items, filter.Page, filter.PageSize), nil
}

func (s Service) Preview(ctx context.Context, subject platformrbac.Subject, req OperationRequest) (OperationResult, error) {
	req = normalizeOperationRequest(req)
	identities, err := parseResourceIdentities(req)
	if err != nil {
		return OperationResult{}, err
	}
	if !s.allowedAll(subject, "preview", identities) {
		return OperationResult{}, ErrPermissionDenied
	}
	if err := s.ensureClusterWritable(ctx, req.ClusterID); err != nil {
		return OperationResult{}, err
	}
	warnings := []string{}
	if s.dryRunner != nil {
		dryRun, err := s.dryRunner.DryRunApply(ctx, kubeclient.ClusterDryRunApplyRequest{ClusterID: req.ClusterID, YAMLContent: req.YAMLContent, ForceConflicts: req.ForceConflicts})
		if err != nil {
			return OperationResult{}, deploymentOperationError(err)
		}
		identities = dryRunObjectsToIdentities(req.ClusterID, dryRun.Objects)
		warnings = append(warnings, dryRun.Warnings...)
		if !s.allowedAll(subject, "preview", identities) {
			return OperationResult{}, ErrPermissionDenied
		}
		plan := buildPreviewPlanFromOperationObjects(req.ClusterID, dryRun.Objects, warnings)
		event, err := s.record(ctx, subject, "preview", req.ClusterID, identities, map[string]any{
			"cluster_id": req.ClusterID,
			"preview_id": plan.ID,
			"resources":  identitySummaries(plan.Resources),
			"diff_count": len(plan.Diffs),
			"yaml_bytes": len(req.YAMLContent),
		})
		if err != nil {
			return OperationResult{}, err
		}
		return OperationResult{
			Status:            "previewed",
			Message:           "部署预览已生成",
			AuditID:           event.ID,
			Resources:         plan.Resources,
			PreviewID:         plan.ID,
			ConfirmationToken: plan.ConfirmationToken,
			Diffs:             plan.Diffs,
			Warnings:          plan.Warnings,
		}, nil
	} else {
		identities, err = s.resolveResourceVersions(ctx, req.ClusterID, identities)
		if err != nil {
			return OperationResult{}, err
		}
	}
	plan := buildPreviewPlan(req.ClusterID, identities, warnings)
	event, err := s.record(ctx, subject, "preview", req.ClusterID, identities, map[string]any{
		"cluster_id": req.ClusterID,
		"preview_id": plan.ID,
		"resources":  identitySummaries(plan.Resources),
		"diff_count": len(plan.Diffs),
		"yaml_bytes": len(req.YAMLContent),
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Status:            "previewed",
		Message:           "部署预览已生成",
		AuditID:           event.ID,
		Resources:         plan.Resources,
		PreviewID:         plan.ID,
		ConfirmationToken: plan.ConfirmationToken,
		Diffs:             plan.Diffs,
		Warnings:          plan.Warnings,
	}, nil
}

func (s Service) PreviewDelete(ctx context.Context, subject platformrbac.Subject, req DeleteRequest) (OperationResult, error) {
	identity := normalizeIdentity(req.Identity)
	if !completeDestructiveIdentity(identity) {
		return OperationResult{}, ErrInvalidRequest
	}
	if !s.allowedAll(subject, "delete", []ResourceIdentity{identity}) {
		return OperationResult{}, ErrPermissionDenied
	}
	if err := s.ensureClusterWritable(ctx, identity.ClusterID); err != nil {
		return OperationResult{}, err
	}
	plan := buildDeletePlan(identity)
	event, err := s.record(ctx, subject, "delete_preview", identity.ClusterID, []ResourceIdentity{identity}, map[string]any{
		"cluster_id": identity.ClusterID,
		"preview_id": plan.ID,
		"resource":   identitySummary(identity),
		"diff_count": len(plan.Diffs),
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Status:            "preview",
		Message:           "删除预览已生成",
		AuditID:           event.ID,
		Resources:         plan.Resources,
		PreviewID:         plan.ID,
		ConfirmationToken: plan.ConfirmationToken,
		Diffs:             plan.Diffs,
	}, nil
}

func (s Service) Apply(ctx context.Context, subject platformrbac.Subject, req OperationRequest) (OperationResult, error) {
	req = normalizeOperationRequest(req)
	identities, err := parseResourceIdentities(req)
	if err != nil {
		return OperationResult{}, err
	}
	if !s.allowedAll(subject, "deploy", identities) {
		return OperationResult{}, ErrPermissionDenied
	}
	if err := s.ensureClusterWritable(ctx, req.ClusterID); err != nil {
		return OperationResult{}, err
	}
	if s.applier != nil {
		plan, err := s.previewPlanForRequest(ctx, subject, req, "deploy", identities)
		if err != nil {
			return OperationResult{}, err
		}
		if !matchingConfirmation(req, plan) {
			return OperationResult{}, fmt.Errorf("%w: %w", ErrInvalidRequest, ErrConfirmationMismatch)
		}
		applied, err := s.applier.Apply(ctx, kubeclient.ClusterApplyRequest{
			ClusterID:      req.ClusterID,
			Mode:           kubeclient.OperationModeApply,
			YAMLContent:    req.YAMLContent,
			ForceConflicts: req.ForceConflicts,
		})
		if err != nil {
			return OperationResult{}, deploymentOperationError(err)
		}
		appliedIdentities := dryRunObjectsToIdentities(req.ClusterID, applied.Objects)
		if len(appliedIdentities) == 0 {
			appliedIdentities = plan.Resources
		}
		if !s.allowedAll(subject, "deploy", appliedIdentities) {
			return OperationResult{}, ErrPermissionDenied
		}
		if err := s.upsertInventory(ctx, plan); err != nil {
			return OperationResult{}, err
		}
		event, err := s.record(ctx, subject, "deploy", req.ClusterID, appliedIdentities, map[string]any{
			"cluster_id": req.ClusterID,
			"preview_id": plan.ID,
			"resources":  identitySummaries(appliedIdentities),
			"diff_count": len(plan.Diffs),
			"yaml_bytes": len(req.YAMLContent),
			"revision":   primitive.NewObjectID().Hex(),
		})
		if err != nil {
			return OperationResult{}, err
		}
		if err := s.recordHistory(ctx, subject, "deploy", "applied", event.ID, appliedIdentities); err != nil {
			return OperationResult{}, err
		}
		return OperationResult{Status: "applied", Message: "部署已应用到 Kubernetes API", AuditID: event.ID, Resources: appliedIdentities, PreviewID: plan.ID, Diffs: plan.Diffs, Warnings: plan.Warnings}, nil
	}
	identities, err = s.resolveResourceVersions(ctx, req.ClusterID, identities)
	if err != nil {
		return OperationResult{}, err
	}
	event, err := s.record(ctx, subject, "deploy", req.ClusterID, identities, map[string]any{
		"cluster_id": req.ClusterID,
		"resources":  identitySummaries(identities),
		"yaml_bytes": len(req.YAMLContent),
		"revision":   primitive.NewObjectID().Hex(),
	})
	if err != nil {
		return OperationResult{}, err
	}
	if err := s.recordHistory(ctx, subject, "deploy", "accepted", event.ID, identities); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Status: "accepted", Message: "部署请求已通过 NovaObs 校验", AuditID: event.ID, Resources: identities}, nil
}

func (s Service) Delete(ctx context.Context, subject platformrbac.Subject, req DeleteRequest) (OperationResult, error) {
	identity := normalizeIdentity(req.Identity)
	if !completeDestructiveIdentity(identity) {
		return OperationResult{}, ErrInvalidRequest
	}
	if !s.allowedAll(subject, "delete", []ResourceIdentity{identity}) {
		return OperationResult{}, ErrPermissionDenied
	}
	if err := s.ensureClusterWritable(ctx, identity.ClusterID); err != nil {
		return OperationResult{}, err
	}
	if s.deleter != nil {
		plan := buildDeletePlan(identity)
		if !matchingDeleteConfirmation(req, plan) {
			return OperationResult{}, fmt.Errorf("%w: %w", ErrInvalidRequest, ErrConfirmationMismatch)
		}
		deleted, err := s.deleter.Delete(ctx, kubeclient.ClusterDeleteRequest{
			ClusterID: identity.ClusterID,
			Mode:      kubeclient.OperationModeDelete,
			Identity: kubeclient.OperationObject{
				APIVersion: identity.APIVersion,
				Kind:       identity.Kind,
				Namespace:  identity.Namespace,
				Name:       identity.Name,
			},
		})
		if err != nil {
			return OperationResult{}, deploymentOperationError(err)
		}
		deletedIdentities := dryRunObjectsToIdentities(identity.ClusterID, deleted.Objects)
		if len(deletedIdentities) == 0 {
			deletedIdentities = []ResourceIdentity{identity}
		}
		if !s.allowedAll(subject, "delete", deletedIdentities) {
			return OperationResult{}, ErrPermissionDenied
		}
		if err := s.removeInventory(ctx, deletedIdentities); err != nil {
			return OperationResult{}, err
		}
		event, err := s.record(ctx, subject, "delete", identity.ClusterID, deletedIdentities, map[string]any{
			"cluster_id": identity.ClusterID,
			"preview_id": plan.ID,
			"resource":   identitySummary(identity),
			"diff_count": len(plan.Diffs),
		})
		if err != nil {
			return OperationResult{}, err
		}
		if err := s.recordHistory(ctx, subject, "delete", "deleted", event.ID, deletedIdentities); err != nil {
			return OperationResult{}, err
		}
		return OperationResult{Status: "deleted", Message: "资源已从 Kubernetes API 删除", AuditID: event.ID, Resources: deletedIdentities, PreviewID: plan.ID, Diffs: plan.Diffs}, nil
	}
	event, err := s.record(ctx, subject, "delete", identity.ClusterID, []ResourceIdentity{identity}, map[string]any{
		"resource": identitySummary(identity),
	})
	if err != nil {
		return OperationResult{}, err
	}
	if err := s.recordHistory(ctx, subject, "delete", "deleted", event.ID, []ResourceIdentity{identity}); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Status: "deleted", Message: "删除请求已通过 NovaObs 校验", AuditID: event.ID, Resources: []ResourceIdentity{identity}}, nil
}

func (s Service) Rollback(ctx context.Context, subject platformrbac.Subject, req RollbackRequest) (OperationResult, error) {
	req.HistoryID = strings.TrimSpace(req.HistoryID)
	identity := normalizeIdentity(req.Identity)
	if req.HistoryID == "" || !completeDestructiveIdentity(identity) {
		return OperationResult{}, ErrInvalidRequest
	}
	if !s.allowedAll(subject, "rollback", []ResourceIdentity{identity}) {
		return OperationResult{}, ErrPermissionDenied
	}
	if err := s.ensureClusterWritable(ctx, identity.ClusterID); err != nil {
		return OperationResult{}, err
	}
	exists, err := s.historyRecordExists(ctx, req.HistoryID, identity)
	if err != nil {
		return OperationResult{}, err
	}
	if !exists {
		return OperationResult{}, ErrInvalidRequest
	}
	event, err := s.record(ctx, subject, "rollback", identity.ClusterID, []ResourceIdentity{identity}, map[string]any{
		"history_id": req.HistoryID,
		"resource":   identitySummary(identity),
	})
	if err != nil {
		return OperationResult{}, err
	}
	if err := s.recordHistory(ctx, subject, "rollback", "requested", event.ID, []ResourceIdentity{identity}); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Status: "rollback_requested", Message: "回滚请求已通过 NovaObs 校验", AuditID: event.ID, Resources: []ResourceIdentity{identity}}, nil
}

type MemoryReader struct {
	history []HistoryRecord
	audits  []AuditEvent
}

func NewMemoryReader(history []HistoryRecord, audits ...[]AuditEvent) MemoryReader {
	copiedHistory := make([]HistoryRecord, len(history))
	copy(copiedHistory, history)
	copiedAudits := []AuditEvent{}
	if len(audits) > 0 {
		copiedAudits = make([]AuditEvent, len(audits[0]))
		copy(copiedAudits, audits[0])
	}
	return MemoryReader{history: copiedHistory, audits: copiedAudits}
}

func (r MemoryReader) ListHistory(_ context.Context, filter ListFilter) ([]HistoryRecord, error) {
	out := make([]HistoryRecord, 0, len(r.history))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.history {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Workload), query) && !strings.Contains(strings.ToLower(item.Action), query) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		less := out[left].StartedAt.Before(out[right].StartedAt)
		if strings.EqualFold(filter.Order, "asc") {
			return less
		}
		return !less
	})
	return paginateHistory(out, filter.Page, filter.PageSize), nil
}

func (r MemoryReader) ListAuditEvents(_ context.Context, filter ListFilter) ([]AuditEvent, error) {
	out := make([]AuditEvent, 0, len(r.audits))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.audits {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.ResourceName), query) && !strings.Contains(strings.ToLower(item.Action), query) {
			continue
		}
		out = append(out, item)
	}
	sortAuditEvents(out, filter.Order)
	return paginateAudit(out, filter.Page, filter.PageSize), nil
}

func sortAuditEvents(items []AuditEvent, order string) {
	sort.SliceStable(items, func(left, right int) bool {
		less := items[left].CreatedAt.Before(items[right].CreatedAt)
		if strings.EqualFold(order, "asc") {
			return less
		}
		return !less
	})
}

func platformAuditToK8sEvent(event audit.Event, filter ListFilter) (AuditEvent, bool) {
	resourceType := event.ResourceType
	if resourceType == "" {
		resourceType = event.Resource.Type
	}
	if !strings.HasPrefix(resourceType, "k8s.") {
		return AuditEvent{}, false
	}
	clusterID := summaryString(event.RequestSummary, "cluster_id")
	if clusterID == "" {
		clusterID = scopeValue(event.Scope, "cluster")
	}
	namespace := summaryString(event.RequestSummary, "namespace")
	if namespace == "" {
		namespace = scopeValue(event.Scope, "namespace")
	}
	if filter.ClusterID != "" && clusterID != filter.ClusterID {
		return AuditEvent{}, false
	}
	if filter.Namespace != "" && namespace != filter.Namespace {
		return AuditEvent{}, false
	}

	resourceKind := strings.TrimPrefix(resourceType, "k8s.")
	resourceName := event.ResourceName
	if resourceName == "" {
		resourceName = event.Resource.Name
	}
	if resourceKind == "deployment" {
		resourceKind = summaryResourceString(event.RequestSummary, "kind", resourceKind)
		resourceName = summaryResourceString(event.RequestSummary, "name", resourceName)
	}
	actor := event.Actor.Name
	if actor == "" {
		actor = event.ActorName
	}
	if actor == "" {
		actor = event.Actor.ID
	}
	status := event.Result
	if status == "" {
		status = "success"
	}
	traceID := event.TraceID
	if traceID == "" {
		traceID = event.Trace
	}
	item := AuditEvent{
		ID:           event.ID,
		ClusterID:    clusterID,
		Namespace:    namespace,
		ResourceKind: resourceKind,
		ResourceName: resourceName,
		Action:       event.Action,
		Actor:        actor,
		Status:       status,
		TraceID:      traceID,
		CreatedAt:    event.CreatedAt,
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query != "" && !strings.Contains(strings.ToLower(item.ResourceName), query) &&
		!strings.Contains(strings.ToLower(item.ResourceKind), query) &&
		!strings.Contains(strings.ToLower(item.Action), query) &&
		!strings.Contains(strings.ToLower(item.Actor), query) {
		return AuditEvent{}, false
	}
	return item, true
}

func summaryString(summary map[string]any, key string) string {
	if summary == nil {
		return ""
	}
	value, _ := summary[key].(string)
	return value
}

func summaryResourceString(summary map[string]any, key string, fallback string) string {
	if summary == nil {
		return fallback
	}
	if resource, ok := summary["resource"].(map[string]string); ok {
		if value := resource[key]; value != "" {
			return value
		}
	}
	if resource, ok := summary["resource"].(map[string]any); ok {
		if value, _ := resource[key].(string); value != "" {
			return value
		}
	}
	if resources, ok := summary["resources"].([]map[string]string); ok && len(resources) > 0 {
		if value := resources[0][key]; value != "" {
			return value
		}
	}
	if resources, ok := summary["resources"].([]any); ok && len(resources) > 0 {
		if resource, ok := resources[0].(map[string]any); ok {
			if value, _ := resource[key].(string); value != "" {
				return value
			}
		}
	}
	return fallback
}

func scopeValue(scope string, key string) string {
	for _, field := range strings.Fields(scope) {
		name, value, ok := strings.Cut(field, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

func paginateHistory(items []HistoryRecord, page int, pageSize int) []HistoryRecord {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []HistoryRecord{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func paginateAudit(items []AuditEvent, page int, pageSize int) []AuditEvent {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []AuditEvent{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

type yamlResourceDocument struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
		UID       string `yaml:"uid"`
	} `yaml:"metadata"`
}

func parseResourceIdentities(req OperationRequest) ([]ResourceIdentity, error) {
	if req.ClusterID == "" || req.YAMLContent == "" {
		return nil, ErrInvalidRequest
	}
	docs := splitYAMLDocuments(req.YAMLContent)
	if len(docs) == 0 {
		return nil, ErrInvalidRequest
	}
	identities := make([]ResourceIdentity, 0, len(docs))
	for _, doc := range docs {
		var parsed yamlResourceDocument
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			return nil, fmt.Errorf("%w: yaml parse failed", ErrInvalidRequest)
		}
		identity := normalizeIdentity(ResourceIdentity{
			ClusterID:  req.ClusterID,
			Namespace:  parsed.Metadata.Namespace,
			APIVersion: parsed.APIVersion,
			Kind:       parsed.Kind,
			Name:       parsed.Metadata.Name,
			UID:        parsed.Metadata.UID,
		})
		if !completeResourceIdentity(identity) {
			return nil, ErrInvalidRequest
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func splitYAMLDocuments(value string) []string {
	out := []string{}
	for _, doc := range strings.Split(value, "---") {
		trimmed := strings.TrimSpace(doc)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (s Service) resolveResourceVersions(ctx context.Context, clusterID string, identities []ResourceIdentity) ([]ResourceIdentity, error) {
	if s.capabilities == nil || len(identities) == 0 {
		return identities, nil
	}
	snapshot, err := s.capabilities.Capabilities(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	resolver := kubeclient.NewResourceVersionResolver(snapshot)
	out := make([]ResourceIdentity, 0, len(identities))
	for _, identity := range identities {
		resolved, err := resolver.Resolve(kubeclient.ResourceVersionRequest{APIVersion: identity.APIVersion, Kind: identity.Kind})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		identity.APIVersion = resolved.APIVersion
		identity.Kind = resolved.Kind
		out = append(out, identity)
	}
	return out, nil
}

func (s Service) previewPlanForRequest(ctx context.Context, subject platformrbac.Subject, req OperationRequest, action string, identities []ResourceIdentity) (PreviewPlan, error) {
	warnings := []string{}
	if s.dryRunner != nil {
		dryRun, err := s.dryRunner.DryRunApply(ctx, kubeclient.ClusterDryRunApplyRequest{ClusterID: req.ClusterID, YAMLContent: req.YAMLContent, ForceConflicts: req.ForceConflicts})
		if err != nil {
			return PreviewPlan{}, deploymentOperationError(err)
		}
		identities = dryRunObjectsToIdentities(req.ClusterID, dryRun.Objects)
		warnings = append(warnings, dryRun.Warnings...)
		if !s.allowedAll(subject, action, identities) {
			return PreviewPlan{}, ErrPermissionDenied
		}
		return buildPreviewPlanFromOperationObjects(req.ClusterID, dryRun.Objects, warnings), nil
	} else {
		var err error
		identities, err = s.resolveResourceVersions(ctx, req.ClusterID, identities)
		if err != nil {
			return PreviewPlan{}, err
		}
	}
	return buildPreviewPlan(req.ClusterID, identities, warnings), nil
}

func matchingConfirmation(req OperationRequest, plan PreviewPlan) bool {
	return strings.TrimSpace(req.PreviewID) != "" &&
		strings.TrimSpace(req.ConfirmationToken) != "" &&
		strings.TrimSpace(req.PreviewID) == plan.ID &&
		strings.TrimSpace(req.ConfirmationToken) == plan.ConfirmationToken
}

func (s Service) ensureClusterWritable(ctx context.Context, clusterID string) error {
	if s.clusterPolicy == nil {
		return nil
	}
	readOnly, err := s.clusterPolicy.IsReadOnly(ctx, clusterID)
	if err != nil {
		return err
	}
	if readOnly {
		return ErrClusterReadOnly
	}
	return nil
}

func matchingDeleteConfirmation(req DeleteRequest, plan PreviewPlan) bool {
	return strings.TrimSpace(req.PreviewID) != "" &&
		strings.TrimSpace(req.ConfirmationToken) != "" &&
		strings.TrimSpace(req.PreviewID) == plan.ID &&
		strings.TrimSpace(req.ConfirmationToken) == plan.ConfirmationToken
}

func (s Service) upsertInventory(ctx context.Context, plan PreviewPlan) error {
	if s.inventory == nil {
		return nil
	}
	now := time.Now().UTC()
	hashes := map[string]string{}
	for _, diff := range plan.Diffs {
		hashes[diffIdentityKey(diff)] = diff.AfterHash
	}
	for _, resource := range plan.Resources {
		_, err := s.inventory.Upsert(ctx, InventoryRecord{
			ClusterID:     resource.ClusterID,
			Namespace:     resource.Namespace,
			APIVersion:    resource.APIVersion,
			Kind:          resource.Kind,
			Name:          resource.Name,
			FieldManager:  kubeclient.DefaultFieldManager,
			LastApplyHash: hashes[resourceIdentityKey(resource)],
			LastPreviewID: plan.ID,
			UpdatedAt:     now,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Service) removeInventory(ctx context.Context, identities []ResourceIdentity) error {
	if s.inventory == nil {
		return nil
	}
	for _, identity := range identities {
		err := s.inventory.Remove(ctx, identity)
		if err != nil && !errors.Is(err, ErrInventoryRecordNotFound) {
			return err
		}
	}
	return nil
}

func (s Service) recordHistory(ctx context.Context, subject platformrbac.Subject, action string, status string, revision string, identities []ResourceIdentity) error {
	if s.history == nil || len(identities) == 0 {
		return nil
	}
	first := identities[0]
	now := time.Now().UTC()
	actor := subject.DisplayName
	if actor == "" {
		actor = subject.ID
	}
	_, err := s.history.CreateHistory(ctx, HistoryRecord{
		ClusterID:  first.ClusterID,
		Namespace:  first.Namespace,
		Workload:   first.Name,
		Action:     action,
		Status:     status,
		Revision:   revision,
		Actor:      actor,
		StartedAt:  now,
		FinishedAt: now,
	})
	return err
}

func (s Service) historyRecordExists(ctx context.Context, historyID string, identity ResourceIdentity) (bool, error) {
	items, err := s.reader.ListHistory(ctx, ListFilter{ClusterID: identity.ClusterID, Namespace: identity.Namespace})
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.ID == historyID && (item.Workload == "" || item.Workload == identity.Name) {
			return true, nil
		}
	}
	return false, nil
}

func resourceIdentityKey(identity ResourceIdentity) string {
	return strings.Join([]string{identity.ClusterID, identity.Namespace, identity.APIVersion, strings.ToLower(identity.Kind), identity.Name}, "\x00")
}

func diffIdentityKey(diff ResourceDiff) string {
	return strings.Join([]string{diff.ClusterID, diff.Namespace, diff.APIVersion, strings.ToLower(diff.Kind), diff.Name}, "\x00")
}

func dryRunObjectsToIdentities(clusterID string, objects []kubeclient.OperationObject) []ResourceIdentity {
	out := make([]ResourceIdentity, 0, len(objects))
	for _, object := range objects {
		out = append(out, normalizeIdentity(ResourceIdentity{
			ClusterID:  clusterID,
			Namespace:  object.Namespace,
			APIVersion: object.APIVersion,
			Kind:       object.Kind,
			Name:       object.Name,
		}))
	}
	return out
}

func deploymentOperationError(err error) error {
	switch {
	case errors.Is(err, kubeclient.ErrClusterRequired),
		errors.Is(err, kubeclient.ErrResourceOperationInvalid),
		errors.Is(err, kubeclient.ErrResourceVersionRequestInvalid),
		errors.Is(err, kubeclient.ErrResourceVersionUnsupported):
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	default:
		return err
	}
}

func (s Service) allowedAll(subject platformrbac.Subject, action string, identities []ResourceIdentity) bool {
	for _, identity := range identities {
		decision := s.authorizer.Authorize(subject, platformrbac.Request{
			Resource: "k8s.deployment",
			Action:   action,
			Scope:    platformrbac.Scope{ClusterID: identity.ClusterID, Namespace: identity.Namespace},
		})
		if !decision.Allowed {
			return false
		}
	}
	return true
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, action string, clusterID string, identities []ResourceIdentity, summary map[string]any) (audit.Event, error) {
	resourceName := clusterID
	if len(identities) == 1 {
		resourceName = identities[0].Name
	}
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.deployment", Name: resourceName},
		ResourceType:   "k8s.deployment",
		ResourceName:   resourceName,
		Action:         action,
		Scope:          fmt.Sprintf("cluster=%s", clusterID),
		Result:         "success",
		RequestSummary: summary,
		CreatedAt:      time.Now().UTC(),
	})
}

func normalizeOperationRequest(req OperationRequest) OperationRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.YAMLContent = strings.TrimSpace(req.YAMLContent)
	return req
}

func normalizeIdentity(identity ResourceIdentity) ResourceIdentity {
	identity.ClusterID = strings.TrimSpace(identity.ClusterID)
	identity.Namespace = strings.TrimSpace(identity.Namespace)
	identity.APIVersion = strings.TrimSpace(identity.APIVersion)
	identity.Kind = strings.TrimSpace(identity.Kind)
	identity.Name = strings.TrimSpace(identity.Name)
	identity.UID = strings.TrimSpace(identity.UID)
	return identity
}

func completeResourceIdentity(identity ResourceIdentity) bool {
	return identity.ClusterID != "" &&
		identity.APIVersion != "" &&
		identity.Kind != "" &&
		identity.Name != "" &&
		(identity.Namespace != "" || clusterScopedKind(identity.APIVersion, identity.Kind))
}

func completeDestructiveIdentity(identity ResourceIdentity) bool {
	return completeResourceIdentity(identity) && identity.UID != ""
}

func clusterScopedKind(apiVersion string, kind string) bool {
	group := apiGroup(apiVersion)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "namespace", "node", "persistentvolume":
		return group == ""
	case "clusterrole", "clusterrolebinding":
		return group == "rbac.authorization.k8s.io"
	case "customresourcedefinition":
		return group == "apiextensions.k8s.io"
	case "mutatingwebhookconfiguration", "validatingwebhookconfiguration":
		return group == "admissionregistration.k8s.io"
	case "apiservice":
		return group == "apiregistration.k8s.io"
	case "certificatesigningrequest":
		return group == "certificates.k8s.io"
	case "storageclass", "csidriver", "csinode", "volumeattachment":
		return group == "storage.k8s.io"
	case "priorityclass":
		return group == "scheduling.k8s.io"
	case "runtimeclass":
		return group == "node.k8s.io"
	case "podsecuritypolicy":
		return group == "policy"
	case "gatewayclass":
		return group == "gateway.networking.k8s.io"
	case "meshpolicy":
		return group == "authentication.istio.io"
	case "clusterrbacconfig":
		return group == "rbac.istio.io"
	default:
		return false
	}
}

func apiGroup(apiVersion string) string {
	group, _, ok := strings.Cut(strings.TrimSpace(apiVersion), "/")
	if !ok {
		return ""
	}
	return strings.ToLower(group)
}

func identitySummaries(identities []ResourceIdentity) []map[string]string {
	out := make([]map[string]string, 0, len(identities))
	for _, identity := range identities {
		out = append(out, identitySummary(identity))
	}
	return out
}

func identitySummary(identity ResourceIdentity) map[string]string {
	return map[string]string{
		"cluster_id":  identity.ClusterID,
		"namespace":   identity.Namespace,
		"api_version": identity.APIVersion,
		"kind":        identity.Kind,
		"name":        identity.Name,
		"uid":         identity.UID,
	}
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
