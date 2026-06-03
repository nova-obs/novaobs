package iam

import (
	"context"

	"novaobs/internal/database"
	platformrbac "novaobs/internal/platform/rbac"
)

type Repository interface {
	SaveUser(ctx context.Context, user User) error
	ListUsers(ctx context.Context) ([]User, error)
	GetUser(ctx context.Context, id string) (User, error)
	DeleteUser(ctx context.Context, id string) error
	SaveGroup(ctx context.Context, group Group) error
	ListGroups(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, id string) (Group, error)
	DeleteGroup(ctx context.Context, id string) error
	SaveMembership(ctx context.Context, membership Membership) error
	ListMemberships(ctx context.Context) ([]Membership, error)
	ListMembershipsByGroup(ctx context.Context, groupID string) ([]Membership, error)
	ListMembershipsBySubject(ctx context.Context, subjectID string, subjectType string) ([]Membership, error)
	DeleteMembership(ctx context.Context, id string) error
	SaveServiceAccount(ctx context.Context, serviceAccount ServiceAccount) error
	ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error)
	GetServiceAccount(ctx context.Context, id string) (ServiceAccount, error)
	DeleteServiceAccount(ctx context.Context, id string) error
}

type RBACRepository interface {
	SaveRole(role platformrbac.Role) error
	GetRole(id string) (platformrbac.Role, error)
	ListRoles(ctx context.Context) ([]platformrbac.Role, error)
	DeleteRole(id string) error
	SaveBinding(binding platformrbac.Binding) error
	ListBindings(ctx context.Context) ([]platformrbac.Binding, error)
	DeleteBinding(id string) error
}

type StoreRepository struct {
	users           database.IAMUserStore
	groups          database.IAMGroupStore
	memberships     database.IAMMembershipStore
	serviceAccounts database.IAMServiceAccountStore
}

func NewStoreRepository(users database.IAMUserStore, groups database.IAMGroupStore, memberships database.IAMMembershipStore, serviceAccounts database.IAMServiceAccountStore) StoreRepository {
	return StoreRepository{users: users, groups: groups, memberships: memberships, serviceAccounts: serviceAccounts}
}

func (r StoreRepository) SaveUser(ctx context.Context, user User) error {
	return r.users.Upsert(ctx, user.ID, user)
}

func (r StoreRepository) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	err := r.users.FindAll(ctx, &users)
	return users, err
}

func (r StoreRepository) GetUser(ctx context.Context, id string) (User, error) {
	var user User
	err := r.users.FindByID(ctx, id, &user)
	return user, err
}

func (r StoreRepository) DeleteUser(ctx context.Context, id string) error {
	return r.users.Delete(ctx, id)
}

func (r StoreRepository) SaveGroup(ctx context.Context, group Group) error {
	return r.groups.Upsert(ctx, group.ID, group)
}

func (r StoreRepository) ListGroups(ctx context.Context) ([]Group, error) {
	var groups []Group
	err := r.groups.FindAll(ctx, &groups)
	return groups, err
}

func (r StoreRepository) GetGroup(ctx context.Context, id string) (Group, error) {
	var group Group
	err := r.groups.FindByID(ctx, id, &group)
	return group, err
}

func (r StoreRepository) DeleteGroup(ctx context.Context, id string) error {
	return r.groups.Delete(ctx, id)
}

func (r StoreRepository) SaveMembership(ctx context.Context, membership Membership) error {
	return r.memberships.Upsert(ctx, membership.ID, membership)
}

func (r StoreRepository) ListMemberships(ctx context.Context) ([]Membership, error) {
	var memberships []Membership
	err := r.memberships.FindAll(ctx, &memberships)
	return memberships, err
}

func (r StoreRepository) ListMembershipsByGroup(ctx context.Context, groupID string) ([]Membership, error) {
	var memberships []Membership
	err := r.memberships.FindByGroup(ctx, groupID, &memberships)
	return memberships, err
}

func (r StoreRepository) ListMembershipsBySubject(ctx context.Context, subjectID string, subjectType string) ([]Membership, error) {
	var memberships []Membership
	err := r.memberships.FindBySubject(ctx, subjectID, subjectType, &memberships)
	return memberships, err
}

func (r StoreRepository) DeleteMembership(ctx context.Context, id string) error {
	return r.memberships.Delete(ctx, id)
}

func (r StoreRepository) SaveServiceAccount(ctx context.Context, serviceAccount ServiceAccount) error {
	return r.serviceAccounts.Upsert(ctx, serviceAccount.ID, serviceAccount)
}

func (r StoreRepository) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	var serviceAccounts []ServiceAccount
	err := r.serviceAccounts.FindAll(ctx, &serviceAccounts)
	return serviceAccounts, err
}

func (r StoreRepository) GetServiceAccount(ctx context.Context, id string) (ServiceAccount, error) {
	var serviceAccount ServiceAccount
	err := r.serviceAccounts.FindByID(ctx, id, &serviceAccount)
	return serviceAccount, err
}

func (r StoreRepository) DeleteServiceAccount(ctx context.Context, id string) error {
	return r.serviceAccounts.Delete(ctx, id)
}

type StoreRBACRepository struct {
	roles    database.RBACRoleStore
	bindings database.RBACBindingStore
}

func NewStoreRBACRepository(roles database.RBACRoleStore, bindings database.RBACBindingStore) StoreRBACRepository {
	return StoreRBACRepository{roles: roles, bindings: bindings}
}

func (r StoreRBACRepository) SaveRole(role platformrbac.Role) error {
	return r.roles.Upsert(context.Background(), role.ID, role)
}

func (r StoreRBACRepository) GetRole(id string) (platformrbac.Role, error) {
	var role platformrbac.Role
	err := r.roles.FindByID(context.Background(), id, &role)
	return role, err
}

func (r StoreRBACRepository) ListRoles(ctx context.Context) ([]platformrbac.Role, error) {
	var roles []platformrbac.Role
	err := r.roles.FindAll(ctx, &roles)
	return roles, err
}

func (r StoreRBACRepository) DeleteRole(id string) error {
	return r.roles.Delete(context.Background(), id)
}

func (r StoreRBACRepository) SaveBinding(binding platformrbac.Binding) error {
	return r.bindings.Upsert(context.Background(), binding.ID, binding)
}

func (r StoreRBACRepository) ListBindings(ctx context.Context) ([]platformrbac.Binding, error) {
	var bindings []platformrbac.Binding
	err := r.bindings.FindAll(ctx, &bindings)
	return bindings, err
}

func (r StoreRBACRepository) DeleteBinding(id string) error {
	return r.bindings.Delete(context.Background(), id)
}
