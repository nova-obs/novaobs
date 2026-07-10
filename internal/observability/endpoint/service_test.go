package endpoint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"novaapm/internal/database/memstore"
	"novaapm/internal/logs"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

var endpointTestSubject = platformrbac.Subject{ID: "endpoint-admin", Type: "user", DisplayName: "Endpoint Admin"}

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

func TestLogEndpointFacadeTestReturnsConfigurationResultForNonMetricsEndpoint(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	require.NoError(t, store.LogEndpoints().Insert(ctx, logs.LogEndpoint{
		ID:          "vl-prod",
		Name:        "vl-prod",
		Kind:        "victorialogs",
		SignalTypes: []string{"logs"},
		QueryURL:    "http://victorialogs:9428/select/logsql/query",
		Status:      "active",
	}))
	service := NewLogEndpointFacade(store.LogEndpoints())

	result, err := service.Test(ctx, "vl-prod")

	require.NoError(t, err)
	require.Equal(t, "pending", result.Status)
	require.Contains(t, result.Message, "配置完整")
	require.False(t, result.CheckedAt.IsZero())
}

func TestLogEndpointFacadeCreatesAndUpdatesVictoriaMetricsEndpoint(t *testing.T) {
	ctx := context.Background()
	store := memstore.NewStore()
	service := NewLogEndpointFacade(store.LogEndpoints(), WithAuthorizer(allowEndpointAuthorizer{}))

	created, err := service.CreateForSubject(ctx, endpointTestSubject, Endpoint{
		Name:        "vm-prod",
		Description: "生产 VictoriaMetrics 集群",
		Kind:        KindVictoriaMetrics,
		SignalTypes: []string{SignalTypeMetrics},
		Scope:       EndpointScope{Type: "global"},
		URLs: EndpointURLs{
			RemoteWriteURL: "http://vminsert:8480/insert/0/prometheus/api/v1/write",
			QueryURL:       "http://vmselect:8481/select/0/prometheus",
			UIURL:          "http://vmselect:8481/select/0/vmui/",
		},
		Status: "active",
	})

	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, KindVictoriaMetrics, created.Kind)
	require.Equal(t, []string{SignalTypeMetrics}, created.SignalTypes)
	require.Equal(t, "http://vminsert:8480/insert/0/prometheus/api/v1/write", created.URLs.RemoteWriteURL)

	created.Name = "vm-production"
	created.Description = "生产 VMS"
	updated, err := service.UpdateForSubject(ctx, endpointTestSubject, created.ID, created)
	require.NoError(t, err)
	require.Equal(t, "vm-production", updated.Name)

	listed, err := service.List(ctx, ListFilter{SignalType: SignalTypeMetrics})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)
}

func TestLogEndpointFacadeRejectsVictoriaMetricsURLWithoutTenantPlaceholder(t *testing.T) {
	service := NewLogEndpointFacade(memstore.NewStore().LogEndpoints(), WithAuthorizer(allowEndpointAuthorizer{}))

	_, err := service.CreateForSubject(context.Background(), endpointTestSubject, Endpoint{
		Name:        "vm-single",
		Kind:        KindVictoriaMetrics,
		SignalTypes: []string{SignalTypeMetrics},
		Scope:       EndpointScope{Type: "global"},
		URLs: EndpointURLs{
			RemoteWriteURL: "http://victoriametrics:8428/api/v1/write",
			QueryURL:       "http://victoriametrics:8428/api/v1/query",
			UIURL:          "http://victoriametrics:8428/vmui/",
		},
		Status: "active",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "/insert/0/prometheus")
}

func TestLogEndpointFacadeProbesVictoriaMetricsAndPersistsHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/select/0/prometheus/api/v1/query", request.URL.Path)
		require.Equal(t, "vector(1)", request.URL.Query().Get("query"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	store := memstore.NewStore()
	service := NewLogEndpointFacade(
		store.LogEndpoints(),
		WithAuthorizer(allowEndpointAuthorizer{}),
		WithHTTPClient(server.Client()),
	)
	created, err := service.CreateForSubject(context.Background(), endpointTestSubject, Endpoint{
		Name:        "vm-probe",
		Kind:        KindVictoriaMetrics,
		SignalTypes: []string{SignalTypeMetrics},
		Scope:       EndpointScope{Type: "global"},
		URLs: EndpointURLs{
			RemoteWriteURL: server.URL + "/insert/0/prometheus/api/v1/write",
			QueryURL:       server.URL + "/select/0/prometheus",
			UIURL:          server.URL + "/select/0/vmui/",
		},
		Status: "active",
	})
	require.NoError(t, err)

	result, err := service.TestForSubject(context.Background(), endpointTestSubject, created.ID)
	require.NoError(t, err)
	require.Equal(t, "healthy", result.Status)
	require.Contains(t, result.Message, "VictoriaMetrics")

	persisted, err := service.Get(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "healthy", persisted.Health.Status)
	require.False(t, persisted.Health.CheckedAt.IsZero())
}

func TestLogEndpointFacadeRequiresUnifiedManagePermissionForMutation(t *testing.T) {
	service := NewLogEndpointFacade(memstore.NewStore().LogEndpoints(), WithAuthorizer(readOnlyEndpointAuthorizer{}))
	_, err := service.CreateForSubject(context.Background(), endpointTestSubject, Endpoint{
		Name:        "vm-denied",
		Kind:        KindVictoriaMetrics,
		SignalTypes: []string{SignalTypeMetrics},
		Scope:       EndpointScope{Type: "global"},
		URLs: EndpointURLs{
			RemoteWriteURL: "http://vminsert:8480/insert/0/prometheus/api/v1/write",
			QueryURL:       "http://vmselect:8481/select/0/prometheus",
			UIURL:          "http://vmselect:8481/select/0/vmui/",
		},
	})
	require.ErrorIs(t, err, ErrPermissionDenied)
}

type allowEndpointAuthorizer struct{}

func (allowEndpointAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type readOnlyEndpointAuthorizer struct{}

func (readOnlyEndpointAuthorizer) Authorize(_ platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: req.Action == "read"}
}
