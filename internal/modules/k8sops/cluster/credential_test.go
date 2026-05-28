package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"novaobs/internal/modules/k8sops/kubeclient"
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
		Plaintext: []byte("apiVersion: v1\nkind: Config\nclusters: []"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)
	auditStore := audit.NewMemoryStore()
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialRepo()), audit.NewService(auditStore))

	plaintext, err := svc.Kubeconfig(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}, "prod")

	require.NoError(t, err)
	require.Equal(t, []byte("apiVersion: v1\nkind: Config\nclusters: []"), plaintext)
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

func TestCredentialServiceReportsMissingClusterCredential(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialRepo()), audit.NewService(audit.NewMemoryStore()))

	_, err := svc.Kubeconfig(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, "prod")

	require.ErrorIs(t, err, ErrCredentialNotFound)
}

func TestCredentialServiceReportsInvalidStoredCredential(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	_, err := secretSvc.Create(context.Background(), secret.CreateRequest{
		Name:      "prod-broken",
		Type:      ClusterCredentialSecretType,
		Scope:     secret.Scope{ClusterID: "prod"},
		Plaintext: []byte("not a kubeconfig"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialRepo()), audit.NewService(audit.NewMemoryStore()))

	_, err = svc.Kubeconfig(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, "prod")

	require.ErrorIs(t, err, ErrInvalidCredentialRequest)
}

func TestCredentialServiceCreatesAndRotatesClusterCredentialMetadata(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	auditStore := audit.NewMemoryStore()
	validator := &credentialValidationStub{serverVersion: "v1.30.2", resourceCount: 18}
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialAdminRepo()), audit.NewService(auditStore), validator)
	subject := platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}

	created, err := svc.Create(context.Background(), subject, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "apiVersion: v1\nkind: Config\nclusters: []\n",
	})
	require.NoError(t, err)
	rotated, err := svc.Rotate(context.Background(), subject, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "apiVersion: v1\nkind: Config\nclusters: []\nusers: []\n",
	})
	require.NoError(t, err)
	items, err := svc.List(context.Background(), CredentialListFilter{ClusterID: "prod"})
	require.NoError(t, err)

	require.NotEmpty(t, created.AuditID)
	require.NotEqual(t, created.Item.SecretID, rotated.Item.SecretID)
	require.Equal(t, "active", rotated.Item.Status)
	require.Equal(t, "v1.30.2", rotated.Probe.ServerVersion)
	require.Equal(t, 18, rotated.Probe.ResourceCount)
	require.Len(t, items, 2)
	require.Equal(t, "active", items[0].Status)
	require.True(t, items[0].Active)
	require.Equal(t, "superseded", items[1].Status)
	require.False(t, items[1].Active)
	require.Equal(t, 2, validator.calls)
	require.NotContains(t, fmt.Sprintf("%+v", items), "apiVersion")
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "create", events[0].Action)
	require.Equal(t, "rotate", events[1].Action)
	require.NotContains(t, fmt.Sprintf("%+v", events), "clusters")
}

func TestCredentialServiceRejectsRotationWhenProbeValidationFails(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	validator := &credentialValidationStub{serverVersion: "v1.30.2", resourceCount: 1}
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialAdminRepo()), audit.NewService(audit.NewMemoryStore()), validator)
	subject := platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}

	_, err := svc.Create(context.Background(), subject, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "apiVersion: v1\nkind: Config\nclusters: []\n",
	})
	require.NoError(t, err)
	validator.err = errors.New("tls handshake failed")

	_, err = svc.Rotate(context.Background(), subject, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "apiVersion: v1\nkind: Config\nclusters: []\nusers: []\n",
	})
	items, listErr := svc.List(context.Background(), CredentialListFilter{ClusterID: "prod"})
	require.NoError(t, listErr)

	require.ErrorIs(t, err, ErrCredentialValidationFailed)
	require.Len(t, items, 1)
	require.Equal(t, "active", items[0].Status)
}

func TestCredentialServiceRollbackPromotesHistoricalSecretAsNewActiveVersion(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	auditStore := audit.NewMemoryStore()
	validator := &credentialValidationStub{serverVersion: "v1.30.2", resourceCount: 3}
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialAdminRepo()), audit.NewService(auditStore), validator)
	subject := platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}

	created, err := svc.Create(context.Background(), subject, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "apiVersion: v1\nkind: Config\nclusters: []\nusers:\n- name: old\n",
	})
	require.NoError(t, err)
	_, err = svc.Rotate(context.Background(), subject, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "apiVersion: v1\nkind: Config\nclusters: []\nusers:\n- name: new\n",
	})
	require.NoError(t, err)

	rolledBack, err := svc.Rollback(context.Background(), subject, RollbackCredentialRequest{
		ClusterID: "prod",
		SecretID:  created.Item.SecretID,
	})
	items, listErr := svc.List(context.Background(), CredentialListFilter{ClusterID: "prod"})
	require.NoError(t, listErr)

	require.NoError(t, err)
	require.NotEqual(t, created.Item.SecretID, rolledBack.Item.SecretID)
	require.Equal(t, created.Item.Fingerprint, rolledBack.Item.Fingerprint)
	require.Equal(t, "active", items[0].Status)
	require.Equal(t, rolledBack.Item.SecretID, items[0].SecretID)
	require.Len(t, items, 3)
	events, err := auditStore.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, "rollback", events[2].Action)
	require.Equal(t, "[redacted]", events[2].RequestSummary["source_secret_id"])
	require.Equal(t, created.Item.Fingerprint, events[2].RequestSummary["source_fingerprint"])
	require.NotContains(t, fmt.Sprintf("%+v", events), "users:")
}

func TestCredentialServiceRejectsInvalidKubeconfig(t *testing.T) {
	secretSvc := secret.NewService(secret.NewMemoryRepository(), secret.NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	svc := NewCredentialService(secretSvc, platformrbac.NewService(clusterCredentialAdminRepo()), audit.NewService(audit.NewMemoryStore()))

	_, err := svc.Create(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, UpsertCredentialRequest{
		ClusterID:  "prod",
		Name:       "prod-readonly",
		Kubeconfig: "not a kubeconfig",
	})

	require.ErrorIs(t, err, ErrInvalidCredentialRequest)
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

func clusterCredentialAdminRepo() testRBACRepo {
	repo := clusterCredentialRepo()
	repo.roles["role-cluster-credential-admin"] = platformrbac.Role{
		ID: "role-cluster-credential-admin",
		Permissions: []platformrbac.Permission{
			{Resource: "k8s.cluster-credential", Action: "create", ScopeMode: "cluster"},
			{Resource: "k8s.cluster-credential", Action: "rotate", ScopeMode: "cluster"},
			{Resource: "k8s.cluster-credential", Action: "rollback", ScopeMode: "cluster"},
		},
	}
	repo.bindings = append(repo.bindings, platformrbac.Binding{ID: "binding-admin", SubjectID: "user-1", SubjectType: "user", RoleID: "role-cluster-credential-admin", Scope: platformrbac.Scope{ClusterID: "prod"}})
	return repo
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

type credentialValidationStub struct {
	serverVersion string
	resourceCount int
	err           error
	calls         int
}

func (v *credentialValidationStub) ValidateCredential(_ context.Context, clusterID string, kubeconfig []byte) (kubeclient.CapabilitySnapshot, error) {
	v.calls++
	if v.err != nil {
		return kubeclient.CapabilitySnapshot{}, v.err
	}
	resources := make([]kubeclient.APIResource, v.resourceCount)
	return kubeclient.CapabilitySnapshot{
		ClusterID:     clusterID,
		ServerVersion: v.serverVersion,
		Resources:     resources,
	}, nil
}
