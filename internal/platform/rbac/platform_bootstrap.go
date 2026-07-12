package rbac

import "time"

const PlatformAdminRoleID = "role-platform-admin"

func EnsurePlatformDefaults(repo Repository, subject Subject) error {
	now := time.Now().UTC()
	role := Role{
		ID:          PlatformAdminRoleID,
		Name:        "平台管理员",
		Description: "NovaAPM 平台用户、角色、授权和全局管理默认角色",
		Permissions: []Permission{
			{Resource: "logs.external-tenant", Action: "manage", ScopeMode: "global"},
			{Resource: "observability.endpoint", Action: "read", ScopeMode: "global"},
			{Resource: "observability.endpoint", Action: "manage", ScopeMode: "global"},
			{Resource: "metrics.integration", Action: "read", ScopeMode: "global"},
			{Resource: "metrics.integration", Action: "manage", ScopeMode: "global"},
			{Resource: "metrics.deployment", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.image", Action: "read", ScopeMode: "global"},
			{Resource: "platform.image", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.environment", Action: "read", ScopeMode: "global"},
			{Resource: "platform.environment", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.iam", Action: "read", ScopeMode: "global"},
			{Resource: "platform.iam", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.subject", Action: "read", ScopeMode: "global"},
			{Resource: "platform.user", Action: "read", ScopeMode: "global"},
			{Resource: "platform.user", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.group", Action: "read", ScopeMode: "global"},
			{Resource: "platform.group", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.service-account", Action: "read", ScopeMode: "global"},
			{Resource: "platform.service-account", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.role", Action: "read", ScopeMode: "global"},
			{Resource: "platform.role", Action: "manage", ScopeMode: "global"},
			{Resource: "platform.binding", Action: "read", ScopeMode: "global"},
			{Resource: "platform.binding", Action: "manage", ScopeMode: "global"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.SaveRole(role); err != nil {
		return err
	}
	if subject.ID == "" || subject.Type == "" {
		return nil
	}
	return repo.SaveBinding(Binding{
		ID:          "binding-platform-admin-" + subject.Type + "-" + subject.ID,
		SubjectID:   subject.ID,
		SubjectType: subject.Type,
		RoleID:      role.ID,
		Scope:       Scope{Global: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}
