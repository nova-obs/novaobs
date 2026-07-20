package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	obsendpoint "novaapm/internal/observability/endpoint"

	"github.com/stretchr/testify/require"
)

func TestVictoriaMetricsHealthVerifierSeparatesDestinationFlowAndCoverage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query().Get("query")
		value := "1"
		if query == `time() - max(timestamp({novaapm_environment_id="env-prod"}))` {
			value = "30"
		}
		_, _ = response.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"` + value + `"]}]}}`))
	}))
	defer server.Close()
	verifier := NewVictoriaMetricsHealthVerifier(server.Client())

	destination, flow, sources, signals := verifier.Verify(context.Background(), obsendpoint.Endpoint{URLs: obsendpoint.EndpointURLs{QueryURL: server.URL + "/select/0/prometheus"}}, "env-prod", []SourceAccess{{ID: "source-k8s", SourceKind: SourceKindKubernetesInfra}}, time.Now())

	require.Equal(t, HealthHealthy, destination.Status)
	require.Equal(t, HealthHealthy, flow.Status)
	require.Equal(t, HealthHealthy, sources[0].Status)
	require.Contains(t, sources[0].Message, "3/3")
	require.Len(t, signals, 4)
}

func TestBuildMetricsQueryURLKeepsExistingQueryEndpoint(t *testing.T) {
	value, err := buildMetricsQueryURL("https://vm.example/api/v1/query", "vector(1)")

	require.NoError(t, err)
	require.Contains(t, value, "/api/v1/query?")
	require.NotContains(t, value, "/api/v1/query/api/v1/query")
}
