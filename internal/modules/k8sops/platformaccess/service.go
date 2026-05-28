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
	ErrRiskConfirmation   = errors.New("k8s_platform_risk_confirmation_required")
	ErrSubjectNotFound    = errors.New("k8s_platform_subject_not_found")
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

func (s Service) Profiles(ctx context.Context, subject platformrbac.Subject) ([]PermissionProfile, error) {
	if !s.allowed(subject) {
		return nil, ErrPermissionDenied
	}
	return PermissionProfiles(), nil
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
	if ok, err := s.subjectExists(ctx, req.SubjectType, req.SubjectID); err != nil {
		return WriteResult{}, err
	} else if !ok {
		return WriteResult{}, ErrSubjectNotFound
	}
	permissions, err := expandPermissionIDs(req.PermissionIDs)
	if err != nil {
		return WriteResult{}, err
	}
	scopes, err := bindingScopes(req, permissions)
	if err != nil {
		return WriteResult{}, err
	}
	if needsRiskAcceptance(req) && !req.RiskAccepted {
		return WriteResult{}, ErrRiskConfirmation
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
	if err := s.repo.SaveRole(role); err != nil {
		return WriteResult{}, err
	}
	bindings := make([]platformrbac.Binding, 0, len(scopes))
	for _, scope := range scopes {
		binding := platformrbac.Binding{
			ID:          bindingID(req, scope),
			SubjectID:   req.SubjectID,
			SubjectType: req.SubjectType,
			RoleID:      role.ID,
			Scope:       scope,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.repo.SaveBinding(binding); err != nil {
			return WriteResult{}, err
		}
		bindings = append(bindings, binding)
	}
	bindingIDs := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		bindingIDs = append(bindingIDs, binding.ID)
	}
	eventScope := bindings[0].Scope
	event, err := s.record(ctx, subject, "create", strings.Join(bindingIDs, ","), eventScope, map[string]any{
		"subject_id":     req.SubjectID,
		"subject_type":   req.SubjectType,
		"role_id":        role.ID,
		"permission_ids": req.PermissionIDs,
		"binding_ids":    bindingIDs,
		"namespaces":     effectiveNamespaces(req),
		"all_namespaces": req.AllNamespaces,
		"global":         req.Global,
	})
	if err != nil {
		for _, binding := range bindings {
			_ = s.repo.DeleteBinding(binding.ID)
		}
		return WriteResult{}, err
	}
	views := make([]Binding, 0, len(bindings))
	for _, binding := range bindings {
		views = append(views, bindingView(binding, role))
	}
	return WriteResult{Item: &views[0], Items: views, Status: "created", AuditID: event.ID}, nil
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
		{ID: "k8s.namespace:read", Label: "命名空间只读", Description: "查看集群命名空间和工作台基础信息", Resource: "k8s.namespace", Action: "read", ScopeMode: "cluster"},
		{ID: "k8s.resource:read", Label: "资源只读", Description: "查看命名空间内资源列表、详情和 YAML", Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		{ID: "k8s.service-account:read", Label: "ServiceAccount 只读", Description: "查看命名空间内服务身份", Resource: "k8s.service-account", Action: "read", ScopeMode: "namespace"},
		{ID: "k8s.rbac:read", Label: "命名空间 RBAC 只读", Description: "查看命名空间内 Role 与 RoleBinding", Resource: "k8s.rbac", Action: "read", ScopeMode: "namespace"},
		{ID: "k8s.certificate:read", Label: "证书只读", Description: "查看命名空间内证书元数据", Resource: "k8s.certificate", Action: "read", ScopeMode: "namespace"},
		{ID: "k8s.namespace:manage", Label: "命名空间管理", Description: "查看、创建与删除集群命名空间", Resource: "k8s.namespace", Action: "manage", ScopeMode: "cluster"},
		{ID: "k8s.service-account:manage", Label: "ServiceAccount 管理", Description: "创建与删除命名空间内服务身份", Resource: "k8s.service-account", Action: "manage", ScopeMode: "namespace"},
		{ID: "k8s.credential:manage", Label: "凭据托管", Description: "读取、录入、轮换与回滚集群 kubeconfig", Resource: "k8s.cluster-credential", Action: "manage", ScopeMode: "cluster"},
		{ID: "k8s.kubeconfig:export", Label: "Kubeconfig 导出", Description: "生成与导出命名空间 ServiceAccount kubeconfig 明文", Resource: "k8s.kubeconfig", Action: "export", ScopeMode: "namespace"},
		{ID: "k8s.deploy:apply", Label: "部署 Apply", Description: "执行 dry-run 后的资源发布", Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"},
		{ID: "k8s.deploy:delete", Label: "部署 Delete", Description: "执行确认后的资源删除", Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"},
		{ID: "k8s.deploy:rollback", Label: "部署 Rollback", Description: "请求经过审计的部署回滚", Resource: "k8s.deployment", Action: "rollback", ScopeMode: "namespace"},
		{ID: "k8s.certificate:manage", Label: "证书管理", Description: "创建与删除命名空间内 TLS 证书", Resource: "k8s.certificate", Action: "manage", ScopeMode: "namespace"},
		{ID: "k8s.terminal:exec", Label: "受控终端", Description: "执行受策略限制的只读命令", Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"},
		{ID: "k8s.rbac:manage", Label: "命名空间 RBAC 管理", Description: "管理命名空间内 Role 与 RoleBinding", Resource: "k8s.rbac", Action: "manage", ScopeMode: "namespace"},
		{ID: "k8s.cluster-rbac:read", Label: "集群 RBAC 只读", Description: "查看 ClusterRole 与 ClusterRoleBinding", Resource: "k8s.rbac", Action: "read", ScopeMode: "cluster"},
		{ID: "k8s.cluster-rbac:manage", Label: "集群 RBAC 管理", Description: "管理 ClusterRole 与 ClusterRoleBinding", Resource: "k8s.rbac", Action: "manage", ScopeMode: "cluster"},
		{ID: "k8s.template:manage", Label: "模板管理", Description: "创建、更新、删除与渲染全局 K8s 模板", Resource: "k8s.template", Action: "manage", ScopeMode: "global"},
	}
}

func PermissionProfiles() []PermissionProfile {
	return []PermissionProfile{
		{
			ID:                     "k8s-readonly",
			Label:                  "只读观察者",
			Description:            "查看命名空间、资源列表、详情、YAML 和日志，不包含任何写操作。",
			Risk:                   "low",
			ScopeMode:              "namespace",
			RecommendedSubjectType: "group",
			PermissionIDs:          []string{"k8s.namespace:read", "k8s.resource:read", "k8s.service-account:read", "k8s.rbac:read", "k8s.certificate:read"},
		},
		{
			ID:                     "k8s-release-operator",
			Label:                  "发布执行者",
			Description:            "在命名空间内查看资源并执行经过预览的部署 Apply。",
			Risk:                   "medium",
			ScopeMode:              "namespace",
			RecommendedSubjectType: "group",
			PermissionIDs:          []string{"k8s.namespace:read", "k8s.resource:read", "k8s.service-account:read", "k8s.rbac:read", "k8s.certificate:read", "k8s.deploy:apply"},
		},
		{
			ID:                     "k8s-namespace-admin",
			Label:                  "命名空间管理员",
			Description:            "管理集群命名空间并查看命名空间内资源。",
			Risk:                   "high",
			ScopeMode:              "mixed",
			RecommendedSubjectType: "group",
			PermissionIDs:          []string{"k8s.namespace:read", "k8s.resource:read", "k8s.service-account:read", "k8s.rbac:read", "k8s.certificate:read", "k8s.namespace:manage"},
		},
		{
			ID:                     "k8s-access-admin",
			Label:                  "集群接入管理员",
			Description:            "负责集群 kubeconfig 凭据录入、读取、轮换与回滚。",
			Risk:                   "high",
			ScopeMode:              "cluster",
			RecommendedSubjectType: "group",
			PermissionIDs:          []string{"k8s.namespace:read", "k8s.credential:manage"},
		},
		{
			ID:                     "k8s-cluster-admin",
			Label:                  "集群运维管理员",
			Description:            "覆盖当前集群运维写操作，包含资源查看、SA、证书、发布、回滚、删除、终端、RBAC 和 kubeconfig 导出。",
			Risk:                   "critical",
			ScopeMode:              "mixed",
			RecommendedSubjectType: "group",
			PermissionIDs: []string{
				"k8s.namespace:read",
				"k8s.resource:read",
				"k8s.service-account:read",
				"k8s.service-account:manage",
				"k8s.rbac:read",
				"k8s.cluster-rbac:read",
				"k8s.certificate:read",
				"k8s.certificate:manage",
				"k8s.namespace:manage",
				"k8s.credential:manage",
				"k8s.kubeconfig:export",
				"k8s.deploy:apply",
				"k8s.deploy:delete",
				"k8s.deploy:rollback",
				"k8s.terminal:exec",
				"k8s.rbac:manage",
				"k8s.cluster-rbac:manage",
			},
		},
		{
			ID:                     "k8s-template-admin",
			Label:                  "模板管理员",
			Description:            "管理全局 K8s YAML 模板与渲染能力，不包含集群资源写操作。",
			Risk:                   "high",
			ScopeMode:              "global",
			RecommendedSubjectType: "group",
			PermissionIDs:          []string{"k8s.template:manage"},
		},
	}
}

func expandPermissionIDs(ids []string) ([]platformrbac.Permission, error) {
	unique := uniqueStrings(ids)
	out := make([]platformrbac.Permission, 0, len(unique))
	for _, id := range unique {
		switch id {
		case "k8s.namespace:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.namespace", Action: "read", ScopeMode: "cluster"})
		case "k8s.resource:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"})
		case "k8s.service-account:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.service-account", Action: "read", ScopeMode: "namespace"})
		case "k8s.rbac:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.rbac", Action: "read", ScopeMode: "namespace"})
		case "k8s.certificate:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.certificate", Action: "read", ScopeMode: "namespace"})
		case "k8s.namespace:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.namespace", Action: "read", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.namespace", Action: "create", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.namespace", Action: "delete", ScopeMode: "cluster"},
			)
		case "k8s.service-account:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.service-account", Action: "read", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.service-account", Action: "create", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.service-account", Action: "delete", ScopeMode: "namespace"},
			)
		case "k8s.credential:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "read", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "create", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "rotate", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.cluster-credential", Action: "rollback", ScopeMode: "cluster"},
			)
		case "k8s.kubeconfig:export":
			out = append(out, platformrbac.Permission{Resource: "k8s.kubeconfig", Action: "export", ScopeMode: "namespace"})
		case "k8s.deploy:apply":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.deployment", Action: "preview", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"},
			)
		case "k8s.deploy:delete":
			out = append(out, platformrbac.Permission{Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"})
		case "k8s.deploy:rollback":
			out = append(out, platformrbac.Permission{Resource: "k8s.deployment", Action: "rollback", ScopeMode: "namespace"})
		case "k8s.certificate:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.certificate", Action: "read", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.certificate", Action: "create", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.certificate", Action: "delete", ScopeMode: "namespace"},
			)
		case "k8s.terminal:exec":
			out = append(out, platformrbac.Permission{Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"})
		case "k8s.rbac:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.rbac", Action: "read", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "create", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "update", ScopeMode: "namespace"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "delete", ScopeMode: "namespace"},
			)
		case "k8s.cluster-rbac:read":
			out = append(out, platformrbac.Permission{Resource: "k8s.rbac", Action: "read", ScopeMode: "cluster"})
		case "k8s.cluster-rbac:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.rbac", Action: "read", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "create", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "update", ScopeMode: "cluster"},
				platformrbac.Permission{Resource: "k8s.rbac", Action: "delete", ScopeMode: "cluster"},
			)
		case "k8s.template:manage":
			out = append(out,
				platformrbac.Permission{Resource: "k8s.template", Action: "create", ScopeMode: "global"},
				platformrbac.Permission{Resource: "k8s.template", Action: "update", ScopeMode: "global"},
				platformrbac.Permission{Resource: "k8s.template", Action: "delete", ScopeMode: "global"},
				platformrbac.Permission{Resource: "k8s.template", Action: "render", ScopeMode: "global"},
			)
		default:
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedPerm, id)
		}
	}
	return out, nil
}

func bindingScopes(req CreateBindingRequest, permissions []platformrbac.Permission) ([]platformrbac.Scope, error) {
	needsCluster := false
	needsNamespace := false
	needsGlobal := false
	for _, permission := range permissions {
		switch permission.ScopeMode {
		case "global":
			needsGlobal = true
		case "cluster":
			needsCluster = true
		case "namespace":
			needsCluster = true
			needsNamespace = true
		}
	}
	if req.Global {
		if needsCluster || needsNamespace || !needsGlobal {
			return nil, ErrPermissionScope
		}
		return []platformrbac.Scope{{Global: true}}, nil
	}
	if needsGlobal {
		return nil, ErrPermissionScope
	}
	if needsCluster && req.ClusterID == "" {
		return nil, ErrPermissionScope
	}
	if !needsNamespace {
		return []platformrbac.Scope{{ClusterID: req.ClusterID}}, nil
	}
	if req.AllNamespaces {
		return []platformrbac.Scope{{ClusterID: req.ClusterID, AllNamespaces: true}}, nil
	}
	namespaces := effectiveNamespaces(req)
	if len(namespaces) == 0 {
		return nil, ErrPermissionScope
	}
	return []platformrbac.Scope{{ClusterID: req.ClusterID, Namespaces: namespaces}}, nil
}

func effectiveNamespaces(req CreateBindingRequest) []string {
	namespaces := append([]string{}, req.Namespaces...)
	if req.Namespace != "" {
		namespaces = append(namespaces, req.Namespace)
	}
	return uniqueStrings(namespaces)
}

func needsRiskAcceptance(req CreateBindingRequest) bool {
	hasHighRisk := false
	hasClusterWideHighRisk := false
	for _, id := range req.PermissionIDs {
		if highRiskPermission(id) {
			hasHighRisk = true
		}
		if clusterWideHighRiskPermission(id) {
			hasClusterWideHighRisk = true
		}
	}
	if !hasHighRisk {
		return false
	}
	return wideNamespaceGrant(req) || hasClusterWideHighRisk
}

func wideNamespaceGrant(req CreateBindingRequest) bool {
	if req.AllNamespaces {
		return true
	}
	return len(effectiveNamespaces(req)) > 1
}

func highRiskPermission(id string) bool {
	switch id {
	case "k8s.namespace:manage",
		"k8s.service-account:manage",
		"k8s.kubeconfig:export",
		"k8s.deploy:apply",
		"k8s.deploy:delete",
		"k8s.deploy:rollback",
		"k8s.certificate:manage",
		"k8s.terminal:exec",
		"k8s.rbac:manage",
		"k8s.cluster-rbac:manage",
		"k8s.credential:manage":
		return true
	default:
		return false
	}
}

func clusterWideHighRiskPermission(id string) bool {
	switch id {
	case "k8s.namespace:manage",
		"k8s.credential:manage",
		"k8s.cluster-rbac:manage":
		return true
	default:
		return false
	}
}

func (s Service) allowed(subject platformrbac.Subject) bool {
	decision := s.authorizer.Authorize(subject, platformAccessRequest)
	return decision.Allowed
}

func (s Service) subjectExists(ctx context.Context, subjectType string, subjectID string) (bool, error) {
	subjects, err := s.subjectRepo.ListSubjects(ctx)
	if err != nil {
		return false, err
	}
	target := subjectRecordID(subjectType, subjectID)
	for _, subject := range subjects {
		if subjectRecordID(subject.SubjectType, subject.SubjectID) == target {
			return true, nil
		}
	}
	return false, nil
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
	rbacNamespace := false
	rbacCluster := false
	serviceAccountRead := false
	serviceAccountWrite := false
	certificateRead := false
	certificateWrite := false
	templateManage := false
	for _, permission := range permissions {
		switch {
		case permission.Resource == "k8s.namespace" && permission.Action == "read":
			seen["k8s.namespace:read"] = true
		case permission.Resource == "k8s.resource" && permission.Action == "read":
			seen["k8s.resource:read"] = true
		case permission.Resource == "k8s.service-account" && permission.Action == "read":
			serviceAccountRead = true
		case permission.Resource == "k8s.service-account" && (permission.Action == "create" || permission.Action == "delete"):
			serviceAccountWrite = true
		case permission.Resource == "k8s.cluster-credential":
			seen["k8s.credential:manage"] = true
		case permission.Resource == "k8s.kubeconfig" && permission.Action == "export":
			seen["k8s.kubeconfig:export"] = true
		case permission.Resource == "k8s.deployment" && permission.Action == "deploy":
			seen["k8s.deploy:apply"] = true
		case permission.Resource == "k8s.deployment" && permission.Action == "delete":
			seen["k8s.deploy:delete"] = true
		case permission.Resource == "k8s.deployment" && permission.Action == "rollback":
			seen["k8s.deploy:rollback"] = true
		case permission.Resource == "k8s.certificate" && permission.Action == "read":
			certificateRead = true
		case permission.Resource == "k8s.certificate" && (permission.Action == "create" || permission.Action == "delete"):
			certificateWrite = true
		case permission.Resource == "k8s.terminal" && permission.Action == "exec":
			seen["k8s.terminal:exec"] = true
		case permission.Resource == "k8s.rbac" && permission.ScopeMode == "namespace":
			if permission.Action == "read" {
				rbacNamespace = true
			}
			if permission.Action == "create" || permission.Action == "update" || permission.Action == "delete" {
				seen["k8s.rbac:manage"] = true
			}
		case permission.Resource == "k8s.rbac" && permission.ScopeMode == "cluster":
			if permission.Action == "read" {
				rbacCluster = true
			}
			if permission.Action == "create" || permission.Action == "update" || permission.Action == "delete" {
				seen["k8s.cluster-rbac:manage"] = true
			}
		case permission.Resource == "k8s.template":
			templateManage = true
		}
	}
	if serviceAccountWrite {
		seen["k8s.service-account:manage"] = true
	} else if serviceAccountRead {
		seen["k8s.service-account:read"] = true
	}
	if rbacNamespace && !seen["k8s.rbac:manage"] {
		seen["k8s.rbac:read"] = true
	}
	if rbacCluster && !seen["k8s.cluster-rbac:manage"] {
		seen["k8s.cluster-rbac:read"] = true
	}
	if certificateWrite {
		seen["k8s.certificate:manage"] = true
	} else if certificateRead {
		seen["k8s.certificate:read"] = true
	}
	if templateManage {
		seen["k8s.template:manage"] = true
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
	req.Namespaces = uniqueStrings(req.Namespaces)
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
	parts := []string{req.SubjectType, req.SubjectID, scope.ClusterID, scope.Namespace, strings.Join(scope.Namespaces, ","), fmt.Sprintf("%t", scope.AllNamespaces), fmt.Sprintf("%t", scope.Global), strings.Join(req.PermissionIDs, ",")}
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
	if scope.AllNamespaces {
		parts = append(parts, "namespaces=*")
	}
	if len(scope.Namespaces) > 0 {
		parts = append(parts, "namespaces="+strings.Join(scope.Namespaces, ","))
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
