package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceListsResourcesByIdentityScope(t *testing.T) {
	svc := NewService(NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
		{
			Identity: Identity{ClusterID: "prod", Namespace: "payment", APIVersion: "apps/v1", Kind: "Deployment", Name: "payment-gateway", UID: "uid-payment"},
			Status:   "healthy",
		},
	}))

	items, err := svc.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: "Deployment"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "uid-orders", items[0].Identity.UID)
	require.Equal(t, "orders-api", items[0].Identity.Name)
}

func TestServiceReturnsDetailAndYAMLByIdentity(t *testing.T) {
	reader := NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
	})
	svc := NewService(reader)

	detail, err := svc.GetDetail(context.Background(), DetailQuery{Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"}})
	require.NoError(t, err)
	require.Equal(t, "orders-api", detail.Identity.Name)

	yaml, err := svc.GetYAML(context.Background(), DetailQuery{Identity: detail.Identity})
	require.NoError(t, err)
	require.Contains(t, yaml.YAML, "kind: Deployment")
	require.Contains(t, yaml.YAML, "name: orders-api")
}

func TestServiceRequiresAPIVersionForDetailIdentity(t *testing.T) {
	reader := NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
	})
	svc := NewService(reader)

	_, err := svc.GetDetail(context.Background(), DetailQuery{Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"}})

	require.Error(t, err)
}

func TestServiceRequiresUIDForDetailAndYAMLIdentity(t *testing.T) {
	reader := NewMemoryReader([]ResourceSummary{
		{
			Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-orders"},
			Status:   "warning",
		},
	})
	svc := NewService(reader)
	withoutUID := DetailQuery{Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api"}}
	wrongUID := DetailQuery{Identity: Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "Deployment", Name: "orders-api", UID: "uid-replaced"}}

	_, err := svc.GetDetail(context.Background(), withoutUID)
	require.Error(t, err)

	_, err = svc.GetYAML(context.Background(), wrongUID)
	require.Error(t, err)
}

func TestServiceDoesNotReturnPodLogsForMissingPod(t *testing.T) {
	svc := NewService(NewMemoryReader(nil))

	_, err := svc.GetPodLogs(context.Background(), PodLogQuery{ClusterID: "prod", Namespace: "orders", Pod: "orders-api-6f7d", Container: "app"})

	require.Error(t, err)
}

func TestServiceListRuntimeGroupsRequiresClusterAndNamespace(t *testing.T) {
	svc := NewService(NewMemoryReader(nil))

	_, err := svc.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{Namespace: "orders"})
	require.ErrorIs(t, err, ErrClusterRequired)

	_, err = svc.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{ClusterID: "prod"})
	require.ErrorIs(t, err, ErrNamespaceRequired)

	_, err = svc.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{ClusterID: "prod", Namespace: "*"})
	require.ErrorIs(t, err, ErrNamespaceRequired)
}

func TestServiceListRuntimeGroupsDelegatesToReader(t *testing.T) {
	reader := runtimeGroupsReaderStub{
		result: RuntimeGroupsResponse{
			ClusterID: "prod",
			Namespace: "orders",
			Summary:   RuntimeGroupsSummary{GroupCount: 1},
			Groups: []RuntimeGroup{{
				Key:         "orders",
				DisplayName: "orders",
				Summary:     RuntimeGroupSummary{ServicesTotal: 1},
			}},
		},
	}
	svc := NewService(reader)

	result, err := svc.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{ClusterID: "prod", Namespace: "orders"})

	require.NoError(t, err)
	require.Equal(t, uint64(1), result.Summary.GroupCount)
	require.Len(t, result.Groups, 1)
}

type runtimeGroupsReaderStub struct {
	MemoryReader
	result RuntimeGroupsResponse
}

func (r runtimeGroupsReaderStub) ListRuntimeGroups(_ context.Context, _ RuntimeGroupsQuery) (RuntimeGroupsResponse, error) {
	return r.result, nil
}
