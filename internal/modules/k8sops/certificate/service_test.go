package certificate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceListsCertificates(t *testing.T) {
	svc := NewService(NewMemoryRepository([]Certificate{
		{ID: "cert-1", ClusterID: "prod", Namespace: "ingress", Name: "wildcard-prod", Status: "valid"},
	}))

	items, err := svc.List(context.Background(), ListFilter{ClusterID: "prod"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "wildcard-prod", items[0].Name)
}

func TestCertificatesDoNotExposePrivateMaterial(t *testing.T) {
	svc := NewService(NewMemoryRepository([]Certificate{
		{ID: "cert-1", Name: "wildcard-prod", PrivateKey: "secret-key-material"},
	}))

	items, err := svc.List(context.Background(), ListFilter{})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Empty(t, items[0].PrivateKey)

	payload, err := json.Marshal(items[0])
	require.NoError(t, err)

	require.NotContains(t, certificateJSONTags(), "private")
	require.NotContains(t, string(payload), "secret-key-material")
	require.NotContains(t, string(payload), "private_key")
}
