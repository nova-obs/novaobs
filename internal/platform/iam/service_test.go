package iam

import (
	"context"
	"testing"

	"novaobs/internal/database/memstore"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestServiceCreatesUsersAndExposesSubjectDirectory(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	service := NewService(
		NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts()),
		NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		platformrbac.NewService(rbacRepo),
	)

	result, err := service.CreateUser(ctx, admin, CreateUserRequest{
		Username:    "operator-1",
		DisplayName: "一线运维",
		Email:       "operator@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, "operator-1", result.Item.ID)

	subjects, err := service.Subjects(ctx, admin)
	require.NoError(t, err)
	require.Len(t, subjects, 2)
	require.Contains(t, subjectIDs(subjects), "user:admin")
	require.Contains(t, subjectIDs(subjects), "user:operator-1")
}

func TestServiceDeniesUserCreationWithoutPlatformPermission(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	service := NewService(
		NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts()),
		NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		platformrbac.NewService(rbacRepo),
	)

	_, err := service.CreateUser(ctx, platformrbac.Subject{ID: "guest", Type: "user"}, CreateUserRequest{
		Username:    "operator-1",
		DisplayName: "一线运维",
	})
	require.ErrorIs(t, err, ErrPermissionDenied)
}

func subjectIDs(subjects []SubjectView) []string {
	out := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		out = append(out, subject.ID)
	}
	return out
}

func TestServiceCreatesPlatformBindingForExistingRole(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	require.NoError(t, rbacRepo.SaveRole(platformrbac.Role{
		ID:   "role-k8s-reader",
		Name: "K8s 只读",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
	}))
	service := NewService(
		NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts()),
		NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		platformrbac.NewService(rbacRepo),
	)
	_, err := service.CreateUser(ctx, admin, CreateUserRequest{Username: "operator-1", DisplayName: "一线运维"})
	require.NoError(t, err)

	result, err := service.CreateBinding(ctx, admin, CreateBindingRequest{
		SubjectID:   "operator-1",
		SubjectType: "user",
		RoleID:      "role-k8s-reader",
		Scope:       platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.NoError(t, err)
	require.Equal(t, "role-k8s-reader", result.Item.RoleID)

	decision := platformrbac.NewService(rbacRepo).Authorize(platformrbac.Subject{ID: "operator-1", Type: "user"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.True(t, decision.Allowed)
}

func TestServiceRejectsPlatformBindingForUnknownSubject(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	require.NoError(t, rbacRepo.SaveRole(platformrbac.Role{
		ID:   "role-k8s-reader",
		Name: "K8s 只读",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
	}))
	service := NewService(
		NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts()),
		NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		platformrbac.NewService(rbacRepo),
	)

	_, err := service.CreateBinding(ctx, admin, CreateBindingRequest{
		SubjectID:   "missing-user",
		SubjectType: "user",
		RoleID:      "role-k8s-reader",
		Scope:       platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGroupBindingIsInheritedByUserMember(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	iamRepo := NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	require.NoError(t, rbacRepo.SaveRole(platformrbac.Role{
		ID:   "role-k8s-reader",
		Name: "K8s 只读",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.namespace", Action: "read", ScopeMode: "cluster"},
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
	}))
	rbacSvc := platformrbac.NewService(rbacRepo, platformrbac.WithSubjectResolver(NewSubjectResolver(iamRepo)))
	service := NewService(
		iamRepo,
		NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()),
		rbacSvc,
	)

	_, err := service.CreateUser(ctx, admin, CreateUserRequest{Username: "operator-1", DisplayName: "一线运维"})
	require.NoError(t, err)
	_, err = service.CreateGroup(ctx, admin, CreateGroupRequest{Name: "sre", DisplayName: "SRE"})
	require.NoError(t, err)
	_, err = service.CreateMembership(ctx, admin, CreateMembershipRequest{GroupID: "sre", SubjectID: "operator-1", SubjectType: "user"})
	require.NoError(t, err)
	_, err = service.CreateBinding(ctx, admin, CreateBindingRequest{
		SubjectID:   "sre",
		SubjectType: "group",
		RoleID:      "role-k8s-reader",
		Scope:       platformrbac.Scope{ClusterID: "test03-02", Namespace: "cattle-system"},
	})
	require.NoError(t, err)

	decision := rbacSvc.Authorize(platformrbac.Subject{ID: "operator-1", Type: "user"}, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: "test03-02", Namespace: "cattle-system"},
	})
	require.True(t, decision.Allowed)
	effective, err := service.EffectivePermissions(ctx, admin, "user", "operator-1")
	require.NoError(t, err)
	require.Len(t, effective, 1)
	require.Equal(t, "group", effective[0].GrantedVia)
	require.Equal(t, "sre", effective[0].GrantedToSubjectID)
}

func TestServiceRejectsNestedGroupMembership(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	iamRepo := NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	service := NewService(iamRepo, NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()), platformrbac.NewService(rbacRepo))

	_, err := service.CreateGroup(ctx, admin, CreateGroupRequest{Name: "admin", DisplayName: "管理员组"})
	require.NoError(t, err)

	_, err = service.CreateMembership(ctx, admin, CreateMembershipRequest{
		GroupID:     "admin",
		SubjectID:   "admin",
		SubjectType: "group",
	})
	require.ErrorIs(t, err, ErrUnsupportedMembershipSubject)
}

func TestServiceDeletesUserAndCascadesMembershipsAndBindings(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	iamRepo := NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	require.NoError(t, rbacRepo.SaveRole(platformrbac.Role{
		ID:   "role-k8s-reader",
		Name: "K8s 只读",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
	}))
	service := NewService(iamRepo, NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()), platformrbac.NewService(rbacRepo, platformrbac.WithSubjectResolver(NewSubjectResolver(iamRepo))))

	_, err := service.CreateUser(ctx, admin, CreateUserRequest{Username: "operator-1", DisplayName: "一线运维"})
	require.NoError(t, err)
	_, err = service.CreateGroup(ctx, admin, CreateGroupRequest{Name: "sre", DisplayName: "SRE"})
	require.NoError(t, err)
	_, err = service.CreateMembership(ctx, admin, CreateMembershipRequest{GroupID: "sre", SubjectID: "operator-1", SubjectType: "user"})
	require.NoError(t, err)
	binding, err := service.CreateBinding(ctx, admin, CreateBindingRequest{
		SubjectID:   "operator-1",
		SubjectType: "user",
		RoleID:      "role-k8s-reader",
		Scope:       platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.NoError(t, err)

	result, err := service.DeleteUser(ctx, admin, "operator-1")
	require.NoError(t, err)
	require.Equal(t, "deleted", result.Status)
	require.Equal(t, "operator-1", result.Item.ID)

	_, err = iamRepo.GetUser(ctx, "operator-1")
	require.Error(t, err)
	memberships, err := iamRepo.ListMemberships(ctx)
	require.NoError(t, err)
	require.Empty(t, memberships)
	_, err = rbacRepo.GetRole("role-k8s-reader")
	require.NoError(t, err)
	bindings, err := service.ListBindings(ctx, admin)
	require.NoError(t, err)
	for _, item := range bindings {
		require.NotEqual(t, binding.Item.ID, item.ID)
		require.NotEqual(t, "operator-1", item.SubjectID)
	}
}

func TestServiceProtectsCurrentUserButAllowsDeletingLegacyBuiltInRBAC(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	iamRepo := NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	service := NewService(iamRepo, NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()), platformrbac.NewService(rbacRepo, platformrbac.WithSuperSubjects(admin)))

	_, err := service.CreateUser(ctx, admin, CreateUserRequest{Username: "admin", DisplayName: "平台管理员"})
	require.NoError(t, err)
	_, err = service.DeleteUser(ctx, admin, "admin")
	require.ErrorIs(t, err, ErrProtectedResource)

	bindings, err := service.ListBindings(ctx, admin)
	require.NoError(t, err)
	require.NotEmpty(t, bindings)

	bindingResult, err := service.DeleteBinding(ctx, admin, bindings[0].ID)
	require.NoError(t, err)
	require.Equal(t, "deleted", bindingResult.Status)

	roleResult, err := service.DeleteRole(ctx, admin, platformrbac.PlatformAdminRoleID)
	require.NoError(t, err)
	require.Equal(t, "deleted", roleResult.Status)
}

func TestServiceDeletesCustomRoleAndDependentBindings(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	iamRepo := NewStoreRepository(store.IAMUsers(), store.IAMGroups(), store.IAMMemberships(), store.IAMServiceAccounts())
	rbacRepo := platformrbac.NewStoreRepository(store.RBACRoles(), store.RBACBindings())
	admin := platformrbac.Subject{ID: "admin", Type: "user", DisplayName: "平台管理员"}
	require.NoError(t, platformrbac.EnsurePlatformDefaults(rbacRepo, admin))
	service := NewService(iamRepo, NewStoreRBACRepository(store.RBACRoles(), store.RBACBindings()), platformrbac.NewService(rbacRepo))

	_, err := service.CreateUser(ctx, admin, CreateUserRequest{Username: "operator-1", DisplayName: "一线运维"})
	require.NoError(t, err)
	_, err = service.CreateRole(ctx, admin, CreateRoleRequest{
		ID:   "role-k8s-custom",
		Name: "K8s 自定义",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
	})
	require.NoError(t, err)
	binding, err := service.CreateBinding(ctx, admin, CreateBindingRequest{
		SubjectID:   "operator-1",
		SubjectType: "user",
		RoleID:      "role-k8s-custom",
		Scope:       platformrbac.Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.NoError(t, err)

	result, err := service.DeleteRole(ctx, admin, "role-k8s-custom")
	require.NoError(t, err)
	require.Equal(t, "deleted", result.Status)
	require.Equal(t, "role-k8s-custom", result.Item.ID)
	_, err = rbacRepo.GetRole("role-k8s-custom")
	require.Error(t, err)

	bindings, err := service.ListBindings(ctx, admin)
	require.NoError(t, err)
	for _, item := range bindings {
		require.NotEqual(t, binding.Item.ID, item.ID)
		require.NotEqual(t, "role-k8s-custom", item.RoleID)
	}
}
