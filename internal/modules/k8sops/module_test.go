package k8sops

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/database/memstore"
	"novaapm/internal/modules/k8sops/certificate"
	"novaapm/internal/modules/k8sops/cluster"
	"novaapm/internal/modules/k8sops/dashboard"
	"novaapm/internal/modules/k8sops/deployment"
	"novaapm/internal/modules/k8sops/namespace"
	k8srbac "novaapm/internal/modules/k8sops/rbac"
	"novaapm/internal/modules/k8sops/resource"
	"novaapm/internal/modules/k8sops/serviceaccount"
	k8stemplate "novaapm/internal/modules/k8sops/template"

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

func TestModuleDoesNotSeedDemoK8sTemplates(t *testing.T) {
	module := NewModuleWithSecurity(nil, nil, nil)

	templates, err := module.Template.List(context.Background(), k8stemplate.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, templates)
}

func TestModuleDoesNotSeedDemoK8sOpsData(t *testing.T) {
	module := NewModuleWithSecurity(nil, nil, nil)
	ctx := context.Background()

	namespaces, err := module.Namespace.List(ctx, namespace.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, namespaces)

	resources, err := module.Resource.List(ctx, resource.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, resources)

	serviceAccounts, err := module.ServiceAccount.List(ctx, serviceaccount.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, serviceAccounts)

	roles, err := module.RBAC.ListRoles(ctx, k8srbac.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, roles)

	bindings, err := module.RBAC.ListBindings(ctx, k8srbac.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, bindings)

	certificates, err := module.Cert.List(ctx, certificate.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, certificates)

	history, err := module.Deploy.ListHistory(ctx, deployment.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, history)

	auditEvents, err := module.Deploy.ListAuditEvents(ctx, deployment.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, auditEvents)

	snapshot, err := module.Dashboard.Get(ctx, dashboard.Query{})
	require.NoError(t, err)
	require.Empty(t, snapshot.Signals)
	require.NotEqual(t, "startorch", snapshot.Sync.Source)
}
