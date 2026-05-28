package secret

import (
	"context"
	"testing"
	"time"

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

func TestServiceDecryptsLatestSecretByTypeAndScope(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	_, err := svc.Create(context.Background(), CreateRequest{
		Name:      "prod-readonly-v1",
		Type:      "k8s.cluster-credential",
		Scope:     Scope{ClusterID: "prod"},
		Plaintext: []byte("apiVersion: v1\nclusters: []\nversion: old"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)
	rotatedAt := time.Now().UTC().Add(time.Minute)
	_, err = svc.Create(context.Background(), CreateRequest{
		Name:      "prod-readonly-v2",
		Type:      "k8s.cluster-credential",
		Scope:     Scope{ClusterID: "prod"},
		Plaintext: []byte("apiVersion: v1\nclusters: []\nversion: new"),
		CreatedBy: "platform",
		RotatedAt: rotatedAt,
	})
	require.NoError(t, err)

	plaintext, metadata, err := svc.PlaintextByTypeAndScope(context.Background(), "k8s.cluster-credential", Scope{ClusterID: "prod"})

	require.NoError(t, err)
	require.Equal(t, []byte("apiVersion: v1\nclusters: []\nversion: new"), plaintext)
	require.Equal(t, "prod-readonly-v2", metadata.Name)
	require.Equal(t, rotatedAt, metadata.RotatedAt)
}

func TestServiceDecryptsStoredSecretByID(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))

	created, err := svc.Create(context.Background(), CreateRequest{
		Name:      "orders-admin",
		Type:      "kubeconfig",
		Scope:     Scope{ClusterID: "prod", Namespace: "orders"},
		Plaintext: []byte("apiVersion: v1\nusers: []"),
		CreatedBy: "user-1",
	})
	require.NoError(t, err)

	plaintext, metadata, err := svc.Plaintext(context.Background(), created.ID)

	require.NoError(t, err)
	require.Equal(t, []byte("apiVersion: v1\nusers: []"), plaintext)
	require.Equal(t, created.ID, metadata.ID)
	require.Empty(t, metadata.Ciphertext)
}

func TestServiceDecryptsStoredSecretByTypeAndScope(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))

	_, err := svc.Create(context.Background(), CreateRequest{
		Name:      "prod-readonly",
		Type:      "k8s.cluster-credential",
		Scope:     Scope{ClusterID: "prod"},
		Plaintext: []byte("apiVersion: v1\nclusters: []"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)

	plaintext, metadata, err := svc.PlaintextByTypeAndScope(context.Background(), "k8s.cluster-credential", Scope{ClusterID: "prod"})

	require.NoError(t, err)
	require.Equal(t, []byte("apiVersion: v1\nclusters: []"), plaintext)
	require.Equal(t, "prod-readonly", metadata.Name)
	require.Empty(t, metadata.Ciphertext)
}

func TestServiceListsSecretMetadataByType(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, NewAESGCMEncryptor([]byte("12345678901234567890123456789012")))
	_, err := svc.Create(context.Background(), CreateRequest{
		Name:      "prod-readonly",
		Type:      "k8s.cluster-credential",
		Scope:     Scope{ClusterID: "prod"},
		Plaintext: []byte("apiVersion: v1\nclusters: []"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), CreateRequest{
		Name:      "orders-reader",
		Type:      "kubeconfig",
		Scope:     Scope{ClusterID: "prod", Namespace: "orders"},
		Plaintext: []byte("apiVersion: v1\nusers: []"),
		CreatedBy: "platform",
	})
	require.NoError(t, err)

	items, err := svc.ListByType(context.Background(), "k8s.cluster-credential")

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod-readonly", items[0].Name)
	require.Empty(t, items[0].Ciphertext)
}
