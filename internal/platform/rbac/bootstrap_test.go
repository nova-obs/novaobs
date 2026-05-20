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

func TestEnsureK8sOpsDefaultsCoversK8sWritePermissions(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), DevK8sOpsScope())

	require.NoError(t, err)
	svc := NewService(repo)
	for _, req := range []Request{
		{Resource: "k8s.service-account", Action: "create", Scope: Scope{ClusterID: "prod", Namespace: "orders"}},
		{Resource: "k8s.rbac", Action: "delete", Scope: Scope{ClusterID: "prod", Namespace: "orders"}},
		{Resource: "k8s.kubeconfig", Action: "export", Scope: Scope{ClusterID: "prod", Namespace: "orders"}},
		{Resource: "k8s.deployment", Action: "rollback", Scope: Scope{ClusterID: "prod", Namespace: "orders"}},
		{Resource: "k8s.certificate", Action: "create", Scope: Scope{ClusterID: "prod", Namespace: "orders"}},
		{Resource: "k8s.template", Action: "update", Scope: Scope{Global: true}},
	} {
		decision := svc.Authorize(DevAdminSubject(), req)
		require.True(t, decision.Allowed, "%s:%s", req.Resource, req.Action)
	}
}

func TestEnsureK8sOpsDefaultsDoesNotExpandNamespaceScope(t *testing.T) {
	repo := NewMemoryRepository()

	err := EnsureK8sOpsDefaults(repo, DevAdminSubject(), DevK8sOpsScope())

	require.NoError(t, err)
	svc := NewService(repo)
	decision := svc.Authorize(DevAdminSubject(), Request{
		Resource: "k8s.terminal",
		Action:   "exec",
		Scope:    Scope{ClusterID: "prod", Namespace: "billing"},
	})
	require.False(t, decision.Allowed)
}
