package alerting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompileAlertQueryScopesServiceAndAppliesThreshold(t *testing.T) {
	spec := validRuleSpec()
	query, err := CompileAlertQuery(spec)

	require.NoError(t, err)
	require.Equal(t, `_time:1m AND "service.name":="payment-service" AND ("payment failed") | stats by (deployment.environment) count() as matches | filter matches:>=3 | fields deployment.environment, matches`, query)
}

func TestCompileTestQueryReturnsCountsWithoutThresholdFilter(t *testing.T) {
	spec := validRuleSpec()
	query, err := CompileTestQuery(spec)

	require.NoError(t, err)
	require.Equal(t, `"service.name":="payment-service" AND ("payment failed") | stats by (deployment.environment) count() as matches`, query)
}

func TestCompileTestQueryUsesLogTargetBaseFilterWhenPresent(t *testing.T) {
	spec := validRuleSpec()
	spec.Scope.LogRouteID = ""
	spec.Scope.LogTargetID = "target-orders"
	spec.Scope.BaseFilter = `"stream":"orders" AND "env":"prod"`
	query, err := CompileTestQuery(spec)

	require.NoError(t, err)
	require.Equal(t, `("stream":"orders" AND "env":"prod") AND ("payment failed") | stats by (deployment.environment) count() as matches`, query)
	require.NotContains(t, query, `"service.name":=`)
}

func TestCompileMetricsQueryInheritsBasePromQLAndThreshold(t *testing.T) {
	spec := validMetricsRuleSpec()
	query, err := CompileAlertQuery(spec)

	require.NoError(t, err)
	require.Equal(t, `((sum(rate(http_requests_total{status=~"5.."}[5m]))) and (service:requests:rate5m{service="orders-api"})) >= 10`, query)
}

func TestCompileMetricsTestQueryDoesNotApplyThreshold(t *testing.T) {
	spec := validMetricsRuleSpec()
	query, err := CompileTestQuery(spec)

	require.NoError(t, err)
	require.Equal(t, `(sum(rate(http_requests_total{status=~"5.."}[5m]))) and (service:requests:rate5m{service="orders-api"})`, query)
}

func TestCompileQueryEscapesUserString(t *testing.T) {
	spec := validRuleSpec()
	spec.Query.Expression = `failed "card"`
	query, err := CompileTestQuery(spec)

	require.NoError(t, err)
	require.Contains(t, query, `"failed \"card\""`)
}

func TestLogsQLFilterRejectsPipesAndTimeOverrides(t *testing.T) {
	for _, expression := range []string{"error | stats count()", "_time:24h error", `error) OR ("service.name":*)`} {
		spec := validRuleSpec()
		spec.Query.Mode = QueryModeLogsQL
		spec.Query.Expression = expression
		require.ErrorIs(t, spec.Validate(), ErrInvalidSpec)
	}
}
