package cluster

import (
	"context"
	"testing"

	"novaobs/internal/modules/k8sops/kubeclient"

	"github.com/stretchr/testify/require"
)

func TestServiceListsClusters(t *testing.T) {
	repo := NewMemoryRepository([]Cluster{
		{ID: "prod", Name: "prod", Region: "cn-shanghai", Status: "active"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod", items[0].ID)
	require.Equal(t, "active", items[0].Status)
}

func TestMemoryRepositoryFiltersClustersByQuery(t *testing.T) {
	repo := NewMemoryRepository([]Cluster{
		{ID: "prod", Name: "prod-core", Region: "cn-shanghai", Status: "active"},
		{ID: "staging", Name: "staging-lab", Region: "cn-beijing", Status: "paused"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{Query: "core"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod", items[0].ID)
}

func TestMemoryRepositorySortsAndPaginatesClusters(t *testing.T) {
	repo := NewMemoryRepository([]Cluster{
		{ID: "prod-a", Name: "prod-a", Region: "cn-shanghai", Status: "active"},
		{ID: "prod-c", Name: "prod-c", Region: "cn-beijing", Status: "active"},
		{ID: "prod-b", Name: "prod-b", Region: "cn-guangzhou", Status: "active"},
	})
	svc := NewService(repo)

	items, err := svc.List(context.Background(), ListFilter{Sort: "name", Order: "desc", Page: 2, PageSize: 1})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "prod-b", items[0].ID)
}

func TestCapabilityServiceReturnsProviderSnapshot(t *testing.T) {
	svc := NewCapabilityService(staticCapabilityProvider{snapshot: kubeclient.CapabilitySnapshot{
		ClusterID:     "prod",
		ServerVersion: "v1.30.2",
		Resources: []kubeclient.APIResource{
			{Group: "networking.istio.io", Version: "v1", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
		},
	}})

	snapshot, err := svc.Get(context.Background(), " prod ")

	require.NoError(t, err)
	require.Equal(t, "prod", snapshot.ClusterID)
	require.Equal(t, "v1.30.2", snapshot.ServerVersion)
	require.True(t, snapshot.Supports("networking.istio.io", "v1", "virtualservices"))
}

func TestCapabilityServiceRejectsMissingProviderAndClusterID(t *testing.T) {
	svc := NewCapabilityService(nil)

	_, err := svc.Get(context.Background(), "")
	require.ErrorIs(t, err, ErrInvalidClusterRequest)

	_, err = svc.Get(context.Background(), "prod")
	require.ErrorIs(t, err, ErrClusterCapabilityUnavailable)
}

type staticCapabilityProvider struct {
	snapshot kubeclient.CapabilitySnapshot
}

func (p staticCapabilityProvider) Capabilities(_ context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error) {
	p.snapshot.ClusterID = clusterID
	return p.snapshot, nil
}
