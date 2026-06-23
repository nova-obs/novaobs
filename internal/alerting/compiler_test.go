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
