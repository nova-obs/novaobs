package kubeclient

import (
	"context"
	"testing"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestProviderUsesAnonymousSubjectWhenContextHasNoSubject(t *testing.T) {
	reader := staticCredentialReader{kubeconfig: minimalKubeconfig()}
	provider := NewProvider(&reader)

	_, err := provider.Clientset(context.Background(), "prod")

	require.NoError(t, err)
	require.Equal(t, "anonymous", reader.subject.ID)
	require.Equal(t, "anonymous", reader.subject.Type)
}

func TestProviderReadsKubeconfigWithContextSubject(t *testing.T) {
	reader := staticCredentialReader{kubeconfig: minimalKubeconfig()}
	provider := NewProvider(&reader)
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	_, err := provider.Clientset(ctx, "prod")

	require.NoError(t, err)
	require.Equal(t, "prod", reader.clusterID)
	require.Equal(t, "user-1", reader.subject.ID)
}

type staticCredentialReader struct {
	kubeconfig []byte
	subject    platformrbac.Subject
	clusterID  string
}

func (r *staticCredentialReader) Kubeconfig(_ context.Context, subject platformrbac.Subject, clusterID string) ([]byte, error) {
	r.subject = subject
	r.clusterID = clusterID
	return r.kubeconfig, nil
}

func minimalKubeconfig() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- name: prod
  cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
users:
- name: novaobs
  user:
    token: test-token
contexts:
- name: prod
  context:
    cluster: prod
    user: novaobs
current-context: prod
`)
}
