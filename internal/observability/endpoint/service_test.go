package endpoint

import (
	"context"
	"testing"

	"novaobs/internal/database/memstore"
	"novaobs/internal/logs"

	"github.com/stretchr/testify/require"
)

func TestLogEndpointFacadeMapsVictoriaLogsEndpointToLogsSignal(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:       "vl-prod",
		Name:     "vl-prod",
		SinkType: logs.EndpointSinkVL,
		WriteURL: "http://victorialogs:9428/insert/opentelemetry/v1/logs",
		QueryURL: "http://victorialogs:9428/select/logsql/query",
		VMUIURL:  "http://victorialogs:9428/select/vmui",
		Status:   "active",
	}))

	service := NewLogEndpointFacade(store.LogEndpoints())

	all, err := service.List(ctx, ListFilter{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "victorialogs", all[0].Kind)
	require.Equal(t, []string{"logs"}, all[0].SignalTypes)
	require.Equal(t, "http://victorialogs:9428/select/vmui", all[0].URLs.UIURL)

	metricsEndpoints, err := service.List(ctx, ListFilter{SignalType: "metrics"})
	require.NoError(t, err)
	require.Empty(t, metricsEndpoints)
}

func TestLogEndpointFacadeListsMetricsEndpointStoredDuringCompatibilityPhase(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:          "vm-prod",
		Name:        "vm-prod",
		Kind:        "victoriametrics",
		SignalTypes: []string{"metrics"},
		WriteURL:    "http://victoriametrics:8428/api/v1/write",
		QueryURL:    "http://victoriametrics:8428/api/v1/query",
		VMUIURL:     "http://victoriametrics:8428/vmui",
		Status:      "active",
	}))

	service := NewLogEndpointFacade(store.LogEndpoints())

	endpoints, err := service.List(ctx, ListFilter{SignalType: "metrics", Kind: "victoriametrics"})
	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	require.Equal(t, "vm-prod", endpoints[0].ID)
	require.Equal(t, []string{"metrics"}, endpoints[0].SignalTypes)
}

func TestLogEndpointFacadeTestReturnsConfigurationResultWithoutNetwork(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:          "vm-prod",
		Name:        "vm-prod",
		Kind:        "victoriametrics",
		SignalTypes: []string{"metrics"},
		QueryURL:    "http://victoriametrics:8428/api/v1/query",
		Status:      "active",
	}))
	service := NewLogEndpointFacade(store.LogEndpoints())

	result, err := service.Test(ctx, "vm-prod")

	require.NoError(t, err)
	require.Equal(t, "pending", result.Status)
	require.Contains(t, result.Message, "配置完整")
	require.False(t, result.CheckedAt.IsZero())
}
