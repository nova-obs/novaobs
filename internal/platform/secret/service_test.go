package secret

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceStoresCiphertextAndReturnsMetadata(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))

	created, err := svc.Create(context.Background(), CreateRequest{
		Name:      "orders-admin",
		Type:      "kubeconfig",
		Scope:     Scope{ClusterID: "prod", Namespace: "orders"},
		Plaintext: []byte("apiVersion: v1\nclusters: []"),
		CreatedBy: "user-1",
	})

	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.NotEmpty(t, created.Fingerprint)
	require.Empty(t, created.Ciphertext)

	stored, err := repo.Get(context.Background(), created.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Ciphertext)
	require.NotContains(t, stored.Ciphertext, "apiVersion")
}
