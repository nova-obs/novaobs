package rbac

import (
	"context"

	"novaobs/internal/database"
)

type StoreRepository struct {
	roles    database.RBACRoleStore
	bindings database.RBACBindingStore
}

func NewStoreRepository(roles database.RBACRoleStore, bindings database.RBACBindingStore) StoreRepository {
	return StoreRepository{roles: roles, bindings: bindings}
}

func (r StoreRepository) SaveRole(role Role) error {
	return r.roles.Upsert(context.Background(), role.ID, role)
}

func (r StoreRepository) ListRoles() ([]Role, error) {
	var roles []Role
	err := r.roles.FindAll(context.Background(), &roles)
	return roles, err
}

func (r StoreRepository) GetRole(id string) (Role, error) {
	var role Role
	err := r.roles.FindByID(context.Background(), id, &role)
	return role, err
}

func (r StoreRepository) DeleteRole(id string) error {
	return r.roles.Delete(context.Background(), id)
}

func (r StoreRepository) SaveBinding(binding Binding) error {
	return r.bindings.Upsert(context.Background(), binding.ID, binding)
}

func (r StoreRepository) ListBindings() ([]Binding, error) {
	var bindings []Binding
	err := r.bindings.FindAll(context.Background(), &bindings)
	return bindings, err
}

func (r StoreRepository) ListBindingsBySubject(subjectID string, subjectType string) ([]Binding, error) {
	var bindings []Binding
	err := r.bindings.FindBySubject(context.Background(), subjectID, subjectType, &bindings)
	return bindings, err
}

func (r StoreRepository) DeleteBinding(id string) error {
	return r.bindings.Delete(context.Background(), id)
}
