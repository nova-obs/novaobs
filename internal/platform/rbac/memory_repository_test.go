package rbac

import "errors"

type MemoryRepository struct {
	roles    map[string]Role
	bindings []Binding
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{roles: map[string]Role{}}
}

func (r *MemoryRepository) SaveRole(role Role) error {
	r.roles[role.ID] = role
	return nil
}

func (r *MemoryRepository) ListRoles() ([]Role, error) {
	out := make([]Role, 0, len(r.roles))
	for _, role := range r.roles {
		out = append(out, role)
	}
	return out, nil
}

func (r *MemoryRepository) GetRole(id string) (Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r *MemoryRepository) DeleteRole(id string) error {
	delete(r.roles, id)
	return nil
}

func (r *MemoryRepository) SaveBinding(binding Binding) error {
	r.bindings = append(r.bindings, binding)
	return nil
}

func (r *MemoryRepository) ListBindings() ([]Binding, error) {
	out := make([]Binding, len(r.bindings))
	copy(out, r.bindings)
	return out, nil
}

func (r *MemoryRepository) ListBindingsBySubject(subjectID string, subjectType string) ([]Binding, error) {
	out := make([]Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (r *MemoryRepository) DeleteBinding(id string) error {
	out := make([]Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.ID != id {
			out = append(out, binding)
		}
	}
	r.bindings = out
	return nil
}
