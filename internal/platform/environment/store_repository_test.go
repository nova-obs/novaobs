package environment

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestStoreRepositoryPersistsEnvironmentAndGloballyUniqueResourceBinding(t *testing.T) {
	store := memstore.NewStore()
	repo := NewStoreRepository(store.Environments(), store.EnvironmentResourceBindings())
	now := time.Now().UTC()
	first := Environment{ID: "env-1", Name: "生产环境", Stage: StageProduction, Status: StatusActive, CreatedAt: now, UpdatedAt: now}
	second := Environment{ID: "env-2", Name: "预发环境", Stage: StageStaging, Status: StatusActive, CreatedAt: now, UpdatedAt: now}

	require.NoError(t, repo.CreateEnvironment(context.Background(), first))
	require.NoError(t, repo.CreateEnvironment(context.Background(), second))
	require.NoError(t, repo.CreateResourceBinding(context.Background(), ResourceBinding{
		ID: "binding-1", EnvironmentID: first.ID, ResourceKind: ResourceKindK8sCluster, ResourceRef: "cluster-1", CreatedAt: now,
	}))

	err := repo.CreateResourceBinding(context.Background(), ResourceBinding{
		ID: "binding-2", EnvironmentID: second.ID, ResourceKind: ResourceKindK8sCluster, ResourceRef: "cluster-1", CreatedAt: now,
	})
	require.ErrorIs(t, err, ErrResourceAlreadyBound)

	view, err := repo.GetEnvironment(context.Background(), first.ID)
	require.NoError(t, err)
	require.Equal(t, first.Name, view.Name)
	bindings, err := repo.ListResourceBindings(context.Background(), first.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
}
