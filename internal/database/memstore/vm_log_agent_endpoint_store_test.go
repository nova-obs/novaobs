package memstore

import (
	"context"
	"testing"

	"novaapm/internal/database"

	"github.com/stretchr/testify/require"
)

type testVMLogAgentEndpoint struct {
	ID      string `json:"id"`
	RouteID string `json:"route_id"`
	Address string `json:"address"`
}

func TestVMLogAgentEndpointStoreEnforcesRouteAddressUniquenessAndDeleteByRoute(t *testing.T) {
	ctx := context.Background()
	store := NewStore().VMLogAgentEndpoints()
	require.NoError(t, store.Insert(ctx, testVMLogAgentEndpoint{ID: "node-1", RouteID: "route-1", Address: "10.0.0.8:13133"}))
	require.ErrorIs(t, store.Insert(ctx, testVMLogAgentEndpoint{ID: "node-2", RouteID: "route-1", Address: "10.0.0.8:13133"}), database.ErrConflict)
	require.NoError(t, store.Insert(ctx, testVMLogAgentEndpoint{ID: "node-3", RouteID: "route-2", Address: "10.0.0.8:13133"}))

	var routeOne []testVMLogAgentEndpoint
	require.NoError(t, store.FindByRoute(ctx, "route-1", &routeOne))
	require.Len(t, routeOne, 1)
	require.NoError(t, store.DeleteByRoute(ctx, "route-1"))
	routeOne = nil
	require.NoError(t, store.FindByRoute(ctx, "route-1", &routeOne))
	require.Empty(t, routeOne)
	var routeTwo []testVMLogAgentEndpoint
	require.NoError(t, store.FindByRoute(ctx, "route-2", &routeTwo))
	require.Len(t, routeTwo, 1)
}
