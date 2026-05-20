package rbac

import "time"

const (
	K8sOpsAdminRoleID        = "role-k8s-ops-admin"
	K8sOpsGlobalAdminRoleID  = "role-k8s-ops-global-admin"
	k8sOpsAdminBindingPrefix = "binding-k8s-ops-admin-"
)

func EnsureK8sOpsDefaults(repo Repository, subject Subject, scope Scope) error {
	now := time.Now().UTC()
	namespaceRole := Role{
		ID:          K8sOpsAdminRoleID,
		Name:        "K8s 运维管理员",
		Description: "NovaObs K8s 运维开发与运维闭环默认角色",
		Permissions: []Permission{
			{Resource: "k8s.service-account", Action: "create", ScopeMode: "namespace"},
			{Resource: "k8s.service-account", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "create", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "update", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.kubeconfig", Action: "export", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "preview", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "apply", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "rollback", ScopeMode: "namespace"},
			{Resource: "k8s.certificate", Action: "create", ScopeMode: "namespace"},
			{Resource: "k8s.certificate", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"},
			{Resource: "k8s.cluster-credential", Action: "read", ScopeMode: "cluster"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	globalRole := Role{
		ID:          K8sOpsGlobalAdminRoleID,
		Name:        "K8s 运维全局管理员",
		Description: "NovaObs K8s 运维全局资源默认角色",
		Permissions: []Permission{
			{Resource: "k8s.template", Action: "create", ScopeMode: "global"},
			{Resource: "k8s.template", Action: "update", ScopeMode: "global"},
			{Resource: "k8s.template", Action: "delete", ScopeMode: "global"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.SaveRole(namespaceRole); err != nil {
		return err
	}
	if err := repo.SaveRole(globalRole); err != nil {
		return err
	}
	if subject.ID == "" || subject.Type == "" {
		return nil
	}
	namespaceBinding := Binding{
		ID:          k8sOpsAdminBindingPrefix + subject.Type + "-" + subject.ID,
		SubjectID:   subject.ID,
		SubjectType: subject.Type,
		RoleID:      K8sOpsAdminRoleID,
		Scope:       scope,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := repo.SaveBinding(namespaceBinding); err != nil {
		return err
	}
	globalBinding := Binding{
		ID:          k8sOpsAdminBindingPrefix + "global-" + subject.Type + "-" + subject.ID,
		SubjectID:   subject.ID,
		SubjectType: subject.Type,
		RoleID:      K8sOpsGlobalAdminRoleID,
		Scope:       Scope{Global: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return repo.SaveBinding(globalBinding)
}

func DevAdminSubject() Subject {
	return Subject{ID: "dev-admin", Type: "user", DisplayName: "开发管理员"}
}

func DevK8sOpsScope() Scope {
	return Scope{ClusterID: "prod", Namespace: "orders"}
}
