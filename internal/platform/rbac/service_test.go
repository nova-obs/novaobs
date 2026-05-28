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

func TestServiceAllowsNamespaceListScope(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-multi-namespace-deployer",
		Name: "Multi Namespace Deployer",
		Permissions: []Permission{
			{Resource: "k8s.deployment", Action: "deploy", ScopeMode: "namespace"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-multi-namespace",
		SubjectID:   "group-sre",
		SubjectType: "group",
		RoleID:      "role-multi-namespace-deployer",
		Scope:       Scope{ClusterID: "prod", Namespaces: []string{"orders", "payments"}},
	}))
	svc := NewService(repo)

	orders := svc.Authorize(Subject{ID: "group-sre", Type: "group"}, Request{
		Resource: "k8s.deployment",
		Action:   "deploy",
		Scope:    Scope{ClusterID: "prod", Namespace: "orders"},
	})
	billing := svc.Authorize(Subject{ID: "group-sre", Type: "group"}, Request{
		Resource: "k8s.deployment",
		Action:   "deploy",
		Scope:    Scope{ClusterID: "prod", Namespace: "billing"},
	})

	require.True(t, orders.Allowed)
	require.False(t, billing.Allowed)
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

func TestServiceAllowsConfiguredSuperSubjectWithoutRoles(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, WithSuperSubjects(Subject{ID: "dev-admin", Type: "user"}))

	decision := svc.Authorize(Subject{ID: "dev-admin", Type: "user"}, Request{
		Resource: "k8s.kubeconfig",
		Action:   "export",
		Scope:    Scope{ClusterID: "test03-02", Namespace: "logplatform"},
	})

	require.True(t, decision.Allowed)
	require.Empty(t, decision.Reason)
	require.Empty(t, repo.roles)
	require.Empty(t, repo.bindings)
}

func TestServiceDoesNotAllowUnconfiguredSubjectAsSuperSubject(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, WithSuperSubjects(Subject{ID: "dev-admin", Type: "user"}))

	decision := svc.Authorize(Subject{ID: "operator-1", Type: "user"}, Request{
		Resource: "platform.iam",
		Action:   "manage",
		Scope:    Scope{Global: true},
	})

	require.False(t, decision.Allowed)
	require.Equal(t, "permission_denied", decision.Reason)
}

func TestServiceScopeDoesNotExpandAcrossClusters(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-prod-service",
		Name: "Prod Service Operator",
		Permissions: []Permission{
			{Resource: "k8s.deployment", Action: "deploy", ScopeMode: "service"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-service",
		SubjectID:   "user-1",
		SubjectType: "user",
		RoleID:      "role-prod-service",
		Scope:       Scope{ClusterID: "prod", Namespace: "orders", ServiceID: "orders-api"},
	}))
	svc := NewService(repo)

	decision := svc.Authorize(Subject{ID: "user-1", Type: "user"}, Request{
		Resource: "k8s.deployment",
		Action:   "deploy",
		Scope:    Scope{ClusterID: "staging", Namespace: "orders", ServiceID: "orders-api"},
	})

	require.False(t, decision.Allowed)
	require.Equal(t, "permission_denied", decision.Reason)
}

func TestServiceDeniesUnknownScopeMode(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-broken",
		Name: "Broken Role",
		Permissions: []Permission{
			{Resource: "k8s.cluster", Action: "read", ScopeMode: "clusetr"},
			{Resource: "k8s.namespace", Action: "read"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-broken",
		SubjectID:   "user-1",
		SubjectType: "user",
		RoleID:      "role-broken",
		Scope:       Scope{ClusterID: "prod"},
	}))
	svc := NewService(repo)

	clusterDecision := svc.Authorize(Subject{ID: "user-1", Type: "user"}, Request{
		Resource: "k8s.cluster",
		Action:   "read",
		Scope:    Scope{ClusterID: "prod"},
	})
	namespaceDecision := svc.Authorize(Subject{ID: "user-1", Type: "user"}, Request{
		Resource: "k8s.namespace",
		Action:   "read",
		Scope:    Scope{ClusterID: "prod"},
	})

	require.False(t, clusterDecision.Allowed)
	require.False(t, namespaceDecision.Allowed)
}

func TestServiceDeniesUnknownScopeModeWithGlobalBinding(t *testing.T) {
	repo := NewMemoryRepository()
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-broken-global",
		Name: "Broken Global Role",
		Permissions: []Permission{
			{Resource: "k8s.cluster", Action: "read", ScopeMode: "clusetr"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-broken-global",
		SubjectID:   "admin",
		SubjectType: "user",
		RoleID:      "role-broken-global",
		Scope:       Scope{Global: true},
	}))
	svc := NewService(repo)

	decision := svc.Authorize(Subject{ID: "admin", Type: "user"}, Request{
		Resource: "k8s.cluster",
		Action:   "read",
		Scope:    Scope{ClusterID: "prod"},
	})

	require.False(t, decision.Allowed)
	require.Equal(t, "permission_denied", decision.Reason)
}
