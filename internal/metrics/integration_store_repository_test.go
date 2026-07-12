package metrics

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestStoreIntegrationRepositoryEnforcesOneIntegrationPerEnvironment(t *testing.T) {
	store := memstore.NewStore()
	repository := NewStoreIntegrationRepository(store.MetricsIntegrations(), store.MetricsSourceAccesses(), store.MetricsHealthSnapshots(), store.MetricsCollectorReleases())
	now := time.Now().UTC()

	require.NoError(t, repository.CreateIntegration(context.Background(), Integration{ID: "integration-1", EnvironmentID: "env-1", DestinationRef: "vm-1", CreatedAt: now, UpdatedAt: now}))
	err := repository.CreateIntegration(context.Background(), Integration{ID: "integration-2", EnvironmentID: "env-1", DestinationRef: "vm-2", CreatedAt: now, UpdatedAt: now})

	require.ErrorIs(t, err, ErrIntegrationAlreadyExists)
	stored, err := repository.FindIntegrationByEnvironment(context.Background(), "env-1")
	require.NoError(t, err)
	require.Equal(t, "integration-1", stored.ID)
}
