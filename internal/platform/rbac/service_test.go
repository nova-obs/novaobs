package rbac

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceAllowsRoleBindingPermission(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-k8s-reader",
		Name: "K8s Reader",
		Permissions: []Permission{
			{Resource: "k8s.cluster", Action: "read", ScopeMode: "cluster"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-1",
		SubjectID:   "user-1",
		SubjectType: "user",
		RoleID:      "role-k8s-reader",
		Scope:       Scope{ClusterID: "prod"},
	}))
	svc := NewService(repo)

	decision := svc.Authorize(Subject{ID: "user-1", Type: "user"}, Request{
		Resource: "k8s.cluster",
		Action:   "read",
		Scope:    Scope{ClusterID: "prod"},
	})

	require.True(t, decision.Allowed)
	require.Empty(t, decision.Reason)
}

func TestServiceDeniesDifferentNamespace(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-orders-deployer",
		Name: "Orders Deployer",
		Permissions: []Permission{
			{Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-1",
		SubjectID:   "user-1",
		SubjectType: "user",
		RoleID:      "role-orders-deployer",
		Scope:       Scope{ClusterID: "prod", Namespace: "orders"},
	}))
	svc := NewService(repo)

	decision := svc.Authorize(Subject{ID: "user-1", Type: "user"}, Request{
		Resource: "k8s.deployment",
		Action:   "deploy",
		Scope:    Scope{ClusterID: "prod", Namespace: "billing"},
	})

	require.False(t, decision.Allowed)
	require.Equal(t, "permission_denied", decision.Reason)
}

func TestServiceAllowsGlobalAdmin(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-admin",
		Name: "Admin",
		Permissions: []Permission{
			{Resource: "*", Action: "*", ScopeMode: "global"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-admin",
		SubjectID:   "admin",
		SubjectType: "user",
		RoleID:      "role-admin",
		Scope:       Scope{Global: true},
	}))
	svc := NewService(repo)

	decision := svc.Authorize(Subject{ID: "admin", Type: "user"}, Request{
		Resource: "k8s.kubeconfig",
		Action:   "export",
		Scope:    Scope{ClusterID: "prod", Namespace: "orders"},
	})

	require.True(t, decision.Allowed)
}
