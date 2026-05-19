package deployment

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"gopkg.in/yaml.v3"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_k8s_deployment_request")
)

type Reader interface {
	ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error)
	ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error)
}

type Service struct {
	reader     Reader
	authorizer Authorizer
	auditor    Auditor
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

func NewService(reader Reader, security ...any) Service {
	service := Service{reader: reader, authorizer: denyAuthorizer{}, auditor: noopAuditor{}}
	for _, item := range security {
		switch value := item.(type) {
		case Authorizer:
			if value != nil {
				service.authorizer = value
			}
		case Auditor:
			if value != nil {
				service.auditor = value
			}
		}
	}
	return service
}

func (s Service) ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error) {
	return s.reader.ListHistory(ctx, filter)
}

func (s Service) ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	return s.reader.ListAuditEvents(ctx, filter)
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
	event, err := s.record(ctx, subject, "preview", req.ClusterID, identities, map[string]any{
		"cluster_id": req.ClusterID,
		"resources":  identitySummaries(identities),
		"yaml_bytes": len(req.YAMLContent),
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Status: "previewed", Message: "部署预览已生成", AuditID: event.ID, Resources: identities}, nil
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
	event, err := s.record(ctx, subject, "deploy", req.ClusterID, identities, map[string]any{
		"cluster_id": req.ClusterID,
		"resources":  identitySummaries(identities),
		"yaml_bytes": len(req.YAMLContent),
		"revision":   primitive.NewObjectID().Hex(),
	})
	if err != nil {
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
	event, err := s.record(ctx, subject, "delete", identity.ClusterID, []ResourceIdentity{identity}, map[string]any{
		"resource": identitySummary(identity),
	})
	if err != nil {
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
	event, err := s.record(ctx, subject, "rollback", identity.ClusterID, []ResourceIdentity{identity}, map[string]any{
		"history_id": req.HistoryID,
		"resource":   identitySummary(identity),
	})
	if err != nil {
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
	sort.SliceStable(out, func(left, right int) bool {
		less := out[left].CreatedAt.Before(out[right].CreatedAt)
		if strings.EqualFold(filter.Order, "asc") {
			return less
		}
		return !less
	})
	return paginateAudit(out, filter.Page, filter.PageSize), nil
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
		if identity.Namespace == "" || identity.APIVersion == "" || identity.Kind == "" || identity.Name == "" {
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

func completeDestructiveIdentity(identity ResourceIdentity) bool {
	return identity.ClusterID != "" && identity.Namespace != "" && identity.APIVersion != "" && identity.Kind != "" && identity.Name != "" && identity.UID != ""
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
