package cluster

import (
	"context"
	"errors"
	"testing"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"
	"novaobs/internal/platform/secret"

	"github.com/stretchr/testify/require"
)

func TestCredentialServiceReadsClusterKubeconfigWithRBACAndAudit(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	_, err := secretSvc.Create(context.Background(), secret.CreateRequest{
		Name:      "prod-readonly",
		Type:      ClusterCredentialSecretType,
		Scope:     secret.Scope{ClusterID: "prod"},
		Plaintext: []byte("apiVersion: v1\nclusters: []"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)
	auditStore := audit.NewMemoryStore()
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialRepo()), audit.NewService(auditStore))

	plaintext, err := svc.Kubeconfig(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, "prod")

	require.NoError(t, err)
	require.Equal(t, []byte("apiVersion: v1\nclusters: []"), plaintext)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "k8s.cluster-credential", events[0].ResourceType)
	require.Equal(t, "read", events[0].Action)
	require.Equal(t, "prod", events[0].RequestSummary["cluster_id"])
	require.NotContains(t, events[0].RequestSummary, "kubeconfig")
}

func TestCredentialServiceRequiresClusterCredentialPermission(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	svc := NewCredentialService(secretSvc, platformrbac.NewService(testRBACRepo{}), audit.NewService(audit.NewMemoryStore()))

	_, err := svc.Kubeconfig(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, "prod")

	require.ErrorIs(t, err, ErrCredentialPermissionDenied)
}

func clusterCredentialRepo() testRBACRepo {
	return testRBACRepo{
		roles: map[string]platformrbac.Role{
			"role-cluster-credential-reader": {
				ID: "role-cluster-credential-reader",
				Permissions: []platformrbac.Permission{
					{Resource: "k8s.cluster-credential", Action: "read", ScopeMode: "cluster"},
				},
			},
		},
		bindings: []platformrbac.Binding{
			{ID: "binding-1", SubjectID: "user-1", SubjectType: "user", RoleID: "role-cluster-credential-reader", Scope: platformrbac.Scope{ClusterID: "prod"}},
		},
	}
}

type testRBACRepo struct {
	roles    map[string]platformrbac.Role
	bindings []platformrbac.Binding
}

func (r testRBACRepo) SaveRole(role platformrbac.Role) error { return nil }

func (r testRBACRepo) GetRole(id string) (platformrbac.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return platformrbac.Role{}, errors.New("role not found")
	}
	return role, nil
}

func (r testRBACRepo) SaveBinding(binding platformrbac.Binding) error { return nil }

func (r testRBACRepo) ListBindingsBySubject(subjectID string, subjectType string) ([]platformrbac.Binding, error) {
	out := make([]platformrbac.Binding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		if binding.SubjectID == subjectID && binding.SubjectType == subjectType {
			out = append(out, binding)
		}
	}
	return out, nil
}
