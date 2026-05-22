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
