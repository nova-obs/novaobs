package platformaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"
)

var (
	ErrPermissionDenied   = errors.New("permission_denied")
	ErrInvalidRequest     = errors.New("invalid_request")
	ErrUnsupportedPerm    = errors.New("k8s_platform_permission_unsupported")
	ErrPermissionScope    = errors.New("k8s_platform_permission_scope_invalid")
	ErrBindingNotFound    = errors.New("k8s_platform_binding_not_found")
	platformAccessRequest = platformrbac.Request{Resource: "k8s.platform-access", Action: "manage", Scope: platformrbac.Scope{Global: true}}
)

type Repository interface {
	SaveRole(role platformrbac.Role) error
	GetRole(id string) (platformrbac.Role, error)
	SaveBinding(binding platformrbac.Binding) error
	ListBindings() ([]platformrbac.Binding, error)
	DeleteBinding(id string) error
}

type SubjectRepository interface {
	ListSubjects(ctx context.Context) ([]SubjectRecord, error)
}

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Service struct {
	repo        Repository
	subjectRepo SubjectRepository
	authorizer  Authorizer
	auditor     Auditor
}

func NewService(repo Repository, authorizer Authorizer, auditor Auditor, dependencies ...any) Service {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	if auditor == nil {
		auditor = noopAuditor{}
	}
	service := Service{repo: repo, subjectRepo: NewMemorySubjectRepository(), authorizer: authorizer, auditor: auditor}
	for _, dependency := range dependencies {
		if value, ok := dependency.(SubjectRepository); ok && value != nil {
			service.subjectRepo = value
		}
	}
	return service
}

func (s Service) ListBindings(ctx context.Context, subject platformrbac.Subject) ([]Binding, error) {
	if !s.allowed(subject) {
		return nil, ErrPermissionDenied
	}
	items, err := s.repo.ListBindings()
	if err != nil {
		return nil, err
	}
	out := make([]Binding, 0, len(items))
	for _, item := range items {
		role, err := s.repo.GetRole(item.RoleID)
		if err != nil || !hasK8sPermission(role.Permissions) {
			continue
		}
		out = append(out, bindingView(item, role))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s Service) Permissions(ctx context.Context, subject platformrbac.Subject) ([]PermissionOption, error) {
	if !s.allowed(subject) {
		return nil, ErrPermissionDenied
	}
	return PermissionOptions(), nil
}

func (s Service) ListSubjects(ctx context.Context, subject platformrbac.Subject) ([]SubjectRecord, error) {
	if !s.allowed(subject) {
		return nil, ErrPermissionDenied
	}
	stored, err := s.subjectRepo.ListSubjects(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := s.repo.ListBindings()
	if err != nil {
		return nil, err
	}
	merged := map[string]SubjectRecord{}
	for _, item := range stored {
		item = normalizeSubjectRecord(item)
		if item.ID == "" {
			continue
		}
		item.BindingRefs = 0
		merged[item.ID] = item
	}
	for _, binding := range bindings {
		role, err := s.repo.GetRole(binding.RoleID)
		if err != nil || !hasK8sPermission(role.Permissions) {
			continue
		}
		id := subjectRecordID(binding.SubjectType, binding.SubjectID)
		if id == "" {
			continue
		}
		item, ok := merged[id]
		if !ok {
			item = SubjectRecord{
				ID:          id,
				SubjectID:   binding.SubjectID,
				SubjectType: binding.SubjectType,
				DisplayName: binding.SubjectID,
				Source:      "binding",
				CreatedAt:   binding.CreatedAt,
				UpdatedAt:   binding.UpdatedAt,
			}
		}
		item.BindingRefs++
		if item.UpdatedAt.Before(binding.UpdatedAt) {
			item.UpdatedAt = binding.UpdatedAt
		}
		merged[id] = item
	}
	out := make([]SubjectRecord, 0, len(merged))
	for _, item := range merged {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SubjectType == out[j].SubjectType {
			return out[i].SubjectID < out[j].SubjectID
		}
		return out[i].SubjectType < out[j].SubjectType
	})
	return out, nil
}

func (s Service) CreateBinding(ctx context.Context, subject platformrbac.Subject, req CreateBindingRequest) (WriteResult, error) {
	if !s.allowed(subject) {
		return WriteResult{}, ErrPermissionDenied
	}
	req = normalizeCreateRequest(req)
	if req.SubjectID == "" || req.SubjectType == "" || len(req.PermissionIDs) == 0 {
		return WriteResult{}, ErrInvalidRequest
	}
	permissions, err := expandPermissionIDs(req.PermissionIDs)
	if err != nil {
		return WriteResult{}, err
	}
	scope, err := bindingScope(req, permissions)
	if err != nil {
		return WriteResult{}, err
	}
	now := time.Now().UTC()
	role := platformrbac.Role{
		ID:          roleID(req.PermissionIDs),
		Name:        "K8s 平台授权组合",
		Description: "由 NovaObs 平台访问授权页生成的 K8s 运维权限组合",
		Permissions: permissions,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	binding := platformrbac.Binding{
		ID:          bindingID(req, scope),
		SubjectID:   req.SubjectID,
		SubjectType: req.SubjectType,
		RoleID:      role.ID,
		Scope:       scope,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.SaveRole(role); err != nil {
		return WriteResult{}, err
	}
	if err := s.repo.SaveBinding(binding); err != nil {
		return WriteResult{}, err
	}
	event, err := s.record(ctx, subject, "create", binding.ID, scope, map[string]any{
		"subject_id":     binding.SubjectID,
		"subject_type":   binding.SubjectType,
		"role_id":        binding.RoleID,
		"permission_ids": req.PermissionIDs,
	})
	if err != nil {
		return WriteResult{}, err
	}
	view := bindingView(binding, role)
	return WriteResult{Item: &view, Status: "created", AuditID: event.ID}, nil
}

func (s Service) DeleteBinding(ctx context.Context, subject platformrbac.Subject, id string) (WriteResult, error) {
	if !s.allowed(subject) {
		return WriteResult{}, ErrPermissionDenied
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return WriteResult{}, ErrInvalidRequest
	}
	before, err := s.ListBindings(ctx, subject)
	if err != nil {
		return WriteResult{}, err
	}
	var target *Binding
	for _, item := range before {
		if item.ID == id {
			copied := item
			target = &copied
			break
		}
	}
	if target == nil {
		return WriteResult{}, ErrBindingNotFound
	}
	if err := s.repo.DeleteBinding(id); err != nil {
		return WriteResult{}, err
	}
	event, err := s.record(ctx, subject, "delete", id, target.Scope, map[string]any{
		"subject_id":   target.SubjectID,
		"subject_type": target.SubjectType,
		"role_id":      target.RoleID,
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: "deleted", AuditID: event.ID}, nil
}

func PermissionOptions() []PermissionOption {
	return []PermissionOption{
		{ID: "k8s.resource:read", Label: "资源只读", Description: "查看资源列表、详情和 YAML", Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		{ID: "k8s.credential:manage", Label: "凭据托管", Description: "读取、录入与轮换集群 kubeconfig", Resource: "k8s.cluster-credential", Action: "manage", ScopeMode: "cluster"},
		{ID: "k8s.deploy:apply", Label: "部署 Apply", Description: "执行 dry-run 后的资源发布", Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"},
		{ID: "k8s.deploy:delete", Label: "部署 Delete", Description: "执行确认后的资源删除", Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"},
		{ID: "k8s.terminal:exec", Label: "受控终端", Description: "执行受策略限制的只读命令", Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"},
		{ID: "k8s.rbac:manage", Label: "K8s RBAC 管理", Description: "管理集群内 Role 与 Binding", Resource: "k8s.rbac", Action: "manage", ScopeMode: "namespace"},
	}
}

func expandPermissionIDs(ids []string) ([]platformrbac.Permission, error) {
	unique := uniqueStrings(ids)
	out := make([]platformrbac.Permission, 0, len(unique))
	for _, id := range unique {
		switch id {
		case "k8s.resource:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"})
		case "k8s.credential:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "read", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "create", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "rotate", ScopeMode: "cluster"},
			)
		case "k8s.deploy:apply":
			out = append(out, platformrbac.Permission{Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"})
		case "k8s.deploy:delete":
			out = append(out, platformrbac.Permission{Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"})
		case "k8s.terminal:exec":
			out = append(out, platformrbac.Permission{Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"})
		case "k8s.rbac:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.rbac", Action: "read", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "create", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "update", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "delete", ScopeMode: "namespace"},
			)
		default:
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedPerm, id)
		}
	}
	return out, nil
}

func bindingScope(req CreateBindingRequest, permissions []platformrbac.Permission) (platformrbac.Scope, error) {
	scope := platformrbac.Scope{Global: req.Global, ClusterID: req.ClusterID, Namespace: req.Namespace}
	if scope.Global {
		return platformrbac.Scope{Global: true}, nil
	}
	needsCluster := false
	needsNamespace := false
	for _, permission := range permissions {
		switch permission.ScopeMode {
		case "cluster":
			needsCluster = true
		case "namespace":
			needsCluster = true
			needsNamespace = true
		}
	}
	if needsCluster && scope.ClusterID == "" {
		return platformrbac.Scope{}, ErrPermissionScope
	}
	if needsNamespace && scope.Namespace == "" {
		return platformrbac.Scope{}, ErrPermissionScope
	}
	return scope, nil
}

func (s Service) allowed(subject platformrbac.Subject) bool {
	decision := s.authorizer.Authorize(subject, platformAccessRequest)
	return decision.Allowed
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, action string, name string, scope platformrbac.Scope, summary map[string]any) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:          audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:       audit.Resource{Type: "k8s.platform-access", Name: name},
		ResourceType:   "k8s.platform-access",
		ResourceName:   name,
		Action:         action,
		Scope:          scopeString(scope),
		RequestSummary: summary,
		Result:         "success",
	})
}

func bindingView(binding platformrbac.Binding, role platformrbac.Role) Binding {
	return Binding{
		ID:            binding.ID,
		SubjectID:     binding.SubjectID,
		SubjectType:   binding.SubjectType,
		RoleID:        binding.RoleID,
		RoleName:      role.Name,
		Scope:         binding.Scope,
		PermissionIDs: permissionIDsForPermissions(role.Permissions),
		Permissions:   role.Permissions,
		CreatedAt:     binding.CreatedAt,
		UpdatedAt:     binding.UpdatedAt,
	}
}

func permissionIDsForPermissions(permissions []platformrbac.Permission) []string {
	seen := map[string]bool{}
	for _, permission := range permissions {
		switch {
		case permission.Resource == "k8s.resource" && permission.Action == "read":
			seen["k8s.resource:read"] = true
		case permission.Resource == "k8s.cluster-credential":
			seen["k8s.credential:manage"] = true
		case permission.Resource == "k8s.deployment" && permission.Action == "deploy":
			seen["k8s.deploy:apply"] = true
		case permission.Resource == "k8s.deployment" && permission.Action == "delete":
			seen["k8s.deploy:delete"] = true
		case permission.Resource == "k8s.terminal" && permission.Action == "exec":
			seen["k8s.terminal:exec"] = true
		case permission.Resource == "k8s.rbac":
			seen["k8s.rbac:manage"] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func hasK8sPermission(permissions []platformrbac.Permission) bool {
	for _, permission := range permissions {
		if strings.HasPrefix(permission.Resource, "k8s.") {
			return true
		}
	}
	return false
}

func normalizeCreateRequest(req CreateBindingRequest) CreateBindingRequest {
	req.SubjectID = strings.TrimSpace(req.SubjectID)
	req.SubjectType = strings.TrimSpace(req.SubjectType)
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.PermissionIDs = uniqueStrings(req.PermissionIDs)
	return req
}

func normalizeSubjectRecord(item SubjectRecord) SubjectRecord {
	item.SubjectID = strings.TrimSpace(item.SubjectID)
	item.SubjectType = strings.TrimSpace(item.SubjectType)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.Email = strings.TrimSpace(item.Email)
	item.Source = strings.TrimSpace(item.Source)
	if item.DisplayName == "" {
		item.DisplayName = item.SubjectID
	}
	if item.Source == "" {
		item.Source = "manual"
	}
	item.ID = subjectRecordID(item.SubjectType, item.SubjectID)
	return item
}

func subjectRecordID(subjectType string, subjectID string) string {
	subjectType = strings.TrimSpace(subjectType)
	subjectID = strings.TrimSpace(subjectID)
	if subjectType == "" || subjectID == "" {
		return ""
	}
	return subjectType + ":" + subjectID
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func roleID(permissionIDs []string) string {
	return "role-k8s-platform-access-" + digest(strings.Join(uniqueStrings(permissionIDs), ","))
}

func bindingID(req CreateBindingRequest, scope platformrbac.Scope) string {
	parts := []string{req.SubjectType, req.SubjectID, scope.ClusterID, scope.Namespace, fmt.Sprintf("%t", scope.Global), strings.Join(req.PermissionIDs, ",")}
	return "binding-k8s-platform-access-" + digest(strings.Join(parts, "|"))
}

func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])[:16]
}

func scopeString(scope platformrbac.Scope) string {
	if scope.Global {
		return "global"
	}
	parts := []string{}
	if scope.ClusterID != "" {
		parts = append(parts, "cluster="+scope.ClusterID)
	}
	if scope.Namespace != "" {
		parts = append(parts, "namespace="+scope.Namespace)
	}
	return strings.Join(parts, " ")
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(_ context.Context, event audit.Event) (audit.Event, error) {
	if event.ID == "" {
		event.ID = "audit-disabled"
	}
	return event, nil
}
