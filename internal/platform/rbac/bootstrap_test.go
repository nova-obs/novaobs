package rbac

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsureK8sOpsDefaultsAllowsDevAdminTerminal(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), DevK8sOpsScope())

	require.NoError(t, err)
	svc := NewService(repo)
	decision := svc.Authorize(DevAdminSubject(), Request{
		Resource: "k8s.terminal",
		Action:   "exec",
		Scope:    Scope{ClusterID: "prod", Namespace: "orders"},
	})
	require.True(t, decision.Allowed)
}

func TestDevK8sOpsScopeDoesNotSeedDemoClusterNamespace(t *testing.T) {
	scope := DevK8sOpsScope()

	require.False(t, scope.Global)
	require.Empty(t, scope.ClusterID)
	require.Empty(t, scope.Namespace)
}

func TestCleanupLegacyDefaultsRemovesSeededRolesAndBindings(t *testing.T) {
	repo := NewMemoryRepository()
	subject := DevAdminSubject()
	require.NoError(t, EnsurePlatformDefaults(repo, subject))
	require.NoError(t, EnsureK8sOpsDefaults(repo, subject, DevK8sOpsScope()))
	require.NoError(t, repo.SaveRole(Role{
		ID:   "role-custom",
		Name: "自定义角色",
		Permissions: []Permission{
			{Resource: "k8s.resource", Action: "read", ScopeMode: "namespace"},
		},
	}))
	require.NoError(t, repo.SaveBinding(Binding{
		ID:          "binding-custom",
		SubjectID:   subject.ID,
		SubjectType: subject.Type,
		RoleID:      "role-custom",
		Scope:       Scope{Global: true},
	}))

	require.NoError(t, CleanupLegacyDefaults(repo))

	roles, err := repo.ListRoles()
	require.NoError(t, err)
	require.Len(t, roles, 1)
	require.Equal(t, "role-custom", roles[0].ID)
	bindings, err := repo.ListBindings()
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	require.Equal(t, "binding-custom", bindings[0].ID)
}

func TestEnsureK8sOpsDefaultsCoversK8sWritePermissions(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), Scope{ClusterID: "stage", Namespace: "default"})

	require.NoError(t, err)
	svc := NewService(repo)
	for _, req := range []Request{
		{Resource: "k8s.service-account", Action: "create", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.rbac", Action: "read", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.rbac", Action: "delete", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.kubeconfig", Action: "export", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.deployment", Action: "rollback", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.certificate", Action: "read", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.certificate", Action: "create", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.namespace", Action: "create", Scope: Scope{ClusterID: "stage"}},
		{Resource: "k8s.namespace", Action: "delete", Scope: Scope{ClusterID: "stage"}},
		{Resource: "k8s.template", Action: "update", Scope: Scope{Global: true}},
	} {
		decision := svc.Authorize(DevAdminSubject(), req)
		require.True(t, decision.Allowed, "%s:%s", req.Resource, req.Action)
	}
}

func TestEnsureK8sOpsDefaultsDoesNotExpandNamespaceWriteScope(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), Scope{ClusterID: "stage", Namespace: "default"})

	require.NoError(t, err)
	svc := NewService(repo)
	decision := svc.Authorize(DevAdminSubject(), Request{
		Resource: "k8s.deployment",
		Action:   "rollback",
		Scope:    Scope{ClusterID: "stage", Namespace: "billing"},
	})
	require.False(t, decision.Allowed)
}

func TestEnsureK8sOpsDefaultsAllowsGlobalK8sReadOnly(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), DevK8sOpsScope())

	require.NoError(t, err)
	svc := NewService(repo)
	for _, req := range []Request{
		{Resource: "k8s.namespace", Action: "read", Scope: Scope{ClusterID: "stage"}},
		{Resource: "k8s.resource", Action: "read", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.service-account", Action: "read", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.rbac", Action: "read", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.certificate", Action: "read", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.cluster-credential", Action: "read", Scope: Scope{ClusterID: "stage"}},
		{Resource: "k8s.terminal", Action: "exec", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
	} {
		decision := svc.Authorize(DevAdminSubject(), req)
		require.True(t, decision.Allowed, "%s:%s", req.Resource, req.Action)
	}
}

func TestEnsureK8sOpsDefaultsAllowsGlobalClusterCredentialManagement(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), DevK8sOpsScope())

	require.NoError(t, err)
	svc := NewService(repo)
	for _, req := range []Request{
		{Resource: "k8s.cluster-credential", Action: "create", Scope: Scope{ClusterID: "stage"}},
		{Resource: "k8s.cluster-credential", Action: "rotate", Scope: Scope{ClusterID: "stage"}},
	} {
		decision := svc.Authorize(DevAdminSubject(), req)
		require.True(t, decision.Allowed, "%s:%s", req.Resource, req.Action)
	}
}

func TestEnsureK8sOpsDefaultsAllowsDevAdminGlobalK8sWrite(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), DevK8sOpsScope())

	require.NoError(t, err)
	svc := NewService(repo)
	for _, req := range []Request{
		{Resource: "k8s.deployment", Action: "rollback", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.rbac", Action: "delete", Scope: Scope{ClusterID: "stage", Namespace: "default"}},
		{Resource: "k8s.namespace", Action: "delete", Scope: Scope{ClusterID: "stage"}},
	} {
		decision := svc.Authorize(DevAdminSubject(), req)
		require.True(t, decision.Allowed, "%s:%s", req.Resource, req.Action)
	}
}
