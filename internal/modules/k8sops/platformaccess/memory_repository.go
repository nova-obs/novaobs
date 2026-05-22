package platformaccess

import (
	"errors"

	platformrbac "novaobs/internal/platform/rbac"
)

type MemoryRepository struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{roles: map[string]platformrbac.Role{}}
}

func (r *MemoryRepository) SaveRole(role platformrbac.Role) error {
	r.roles[role.ID] = role
	return nil
}

func (r *MemoryRepository) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r *MemoryRepository) SaveBinding(binding platformrbac.Binding) error {
	for index, item := range r.bindings {
		if item.ID == binding.ID {
			r.bindings[index] = binding
			return nil
		}
	}
	r.bindings = append(r.bindings, binding)
	return nil
}

func (r *MemoryRepository) ListBindings() ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, len(r.bindings))
	copy(out, r.bindings)
	return out, nil
}

func (r *MemoryRepository) DeleteBinding(id string) error {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.ID != id {
			out = append(out, binding)
		}
	}
	r.bindings = out
	return nil
}
