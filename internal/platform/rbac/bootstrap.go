package rbac

import "time"

const (
	K8sOpsAdminRoleID                   = "role-k8s-ops-admin"
	K8sOpsGlobalAdminRoleID             = "role-k8s-ops-global-admin"
	K8sOpsGlobalReaderRoleID            = "role-k8s-ops-global-reader"
	K8sOpsGlobalCredentialManagerRoleID = "role-k8s-ops-global-credential-manager"
	k8sOpsAdminBindingPrefix            = "binding-k8s-ops-admin-"
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
			{Resource: "k8s.service-account", Action: "read", ScopeMode: "namespace"},
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "create", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "update", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "read", ScopeMode: "namespace"},
			{Resource: "k8s.kubeconfig", Action: "export", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "preview", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "apply", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.deployment", Action: "rollback", ScopeMode: "namespace"},
			{Resource: "k8s.certificate", Action: "create", ScopeMode: "namespace"},
			{Resource: "k8s.certificate", Action: "delete", ScopeMode: "namespace"},
			{Resource: "k8s.terminal", Action: "exec", ScopeMode: "namespace"},
			{Resource: "k8s.namespace", Action: "read", ScopeMode: "cluster"},
			{Resource: "k8s.cluster-credential", Action: "read", ScopeMode: "cluster"},
			{Resource: "k8s.cluster-credential", Action: "create", ScopeMode: "cluster"},
			{Resource: "k8s.cluster-credential", Action: "rotate", ScopeMode: "cluster"},
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
	globalReaderRole := Role{
		ID:          K8sOpsGlobalReaderRoleID,
		Name:        "K8s 运维全局只读",
		Description: "NovaObs K8s 运维开发态真实集群只读接入默认角色",
		Permissions: []Permission{
			{Resource: "k8s.namespace", Action: "read", ScopeMode: "cluster"},
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
			{Resource: "k8s.service-account", Action: "read", ScopeMode: "namespace"},
			{Resource: "k8s.rbac", Action: "read", ScopeMode: "namespace"},
			{Resource: "k8s.cluster-credential", Action: "read", ScopeMode: "cluster"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	globalCredentialManagerRole := Role{
		ID:          K8sOpsGlobalCredentialManagerRoleID,
		Name:        "K8s 运维全局凭据托管",
		Description: "NovaObs K8s 运维开发态真实集群 kubeconfig 托管默认角色",
		Permissions: []Permission{
			{Resource: "k8s.cluster-credential", Action: "create", ScopeMode: "cluster"},
			{Resource: "k8s.cluster-credential", Action: "rotate", ScopeMode: "cluster"},
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
	if err := repo.SaveRole(globalReaderRole); err != nil {
		return err
	}
	if err := repo.SaveRole(globalCredentialManagerRole); err != nil {
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
	if err := repo.SaveBinding(globalBinding); err != nil {
		return err
	}
	globalReaderBinding := Binding{
		ID:          k8sOpsAdminBindingPrefix + "global-reader-" + subject.Type + "-" + subject.ID,
		SubjectID:   subject.ID,
		SubjectType: subject.Type,
		RoleID:      K8sOpsGlobalReaderRoleID,
		Scope:       Scope{Global: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := repo.SaveBinding(globalReaderBinding); err != nil {
		return err
	}
	globalCredentialManagerBinding := Binding{
		ID:          k8sOpsAdminBindingPrefix + "global-credential-manager-" + subject.Type + "-" + subject.ID,
		SubjectID:   subject.ID,
		SubjectType: subject.Type,
		RoleID:      K8sOpsGlobalCredentialManagerRoleID,
		Scope:       Scope{Global: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return repo.SaveBinding(globalCredentialManagerBinding)
}

func DevAdminSubject() Subject {
	return Subject{ID: "dev-admin", Type: "user", DisplayName: "开发管理员"}
}

func DevK8sOpsScope() Scope {
	return Scope{ClusterID: "prod", Namespace: "orders"}
}
