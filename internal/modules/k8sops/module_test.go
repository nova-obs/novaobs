package k8sops

import (
	"context"
	"testing"
	"time"

	"novaobs/internal/database/memstore"
	"novaobs/internal/modules/k8sops/cluster"
	"novaobs/internal/modules/k8sops/namespace"

	"github.com/stretchr/testify/require"
)

func TestModuleUsesInjectedClusterAndNamespaceRepositories(t *testing.T) {
	store := memstore.NewStore()
	ctx := context.Background()
	clusterRepo := cluster.NewStoreRepository(store.K8sClusters())
	namespaceRepo := namespace.NewStoreRepository(store.K8sNamespaces())
	_, err := clusterRepo.Upsert(ctx, cluster.Cluster{ID: "stage", Name: "stage-core", Region: "cn-beijing"})
	require.NoError(t, err)
	_, err = namespaceRepo.Upsert(ctx, namespace.Namespace{
		ClusterID: "stage",
		Name:      "platform",
		Owner:     "platform-team",
		UpdatedAt: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	module := NewModuleWithSecurity(nil, nil, nil, clusterRepo, namespaceRepo)

	clusters, err := module.Cluster.List(ctx, cluster.ListFilter{})
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	require.Equal(t, "stage-core", clusters[0].Name)

	namespaces, err := module.Namespace.List(ctx, namespace.ListFilter{ClusterID: "stage"})
	require.NoError(t, err)
	require.Len(t, namespaces, 1)
	require.Equal(t, "platform", namespaces[0].Name)
}
