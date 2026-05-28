package rbac

import "strings"

type LegacyDefaultRepository interface {
	ListRoles() ([]Role, error)
	DeleteRole(id string) error
	ListBindings() ([]Binding, error)
	DeleteBinding(id string) error
}

func CleanupLegacyDefaults(repo LegacyDefaultRepository) error {
	bindings, err := repo.ListBindings()
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if IsLegacyDefaultBindingID(binding.ID) || IsLegacyDefaultRoleID(binding.RoleID) {
			if err := repo.DeleteBinding(binding.ID); err != nil {
				return err
			}
		}
	}
	roles, err := repo.ListRoles()
	if err != nil {
		return err
	}
	for _, role := range roles {
		if IsLegacyDefaultRoleID(role.ID) {
			if err := repo.DeleteRole(role.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func IsLegacyDefaultRoleID(id string) bool {
	switch strings.TrimSpace(id) {
	case PlatformAdminRoleID,
		K8sOpsAdminRoleID,
		K8sOpsGlobalAdminRoleID,
		K8sOpsGlobalReaderRoleID,
		K8sOpsGlobalCredentialManagerRoleID:
		return true
	default:
		return false
	}
}

func IsLegacyDefaultBindingID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.HasPrefix(id, "binding-platform-admin-") || strings.HasPrefix(id, "binding-k8s-ops-admin-")
}
