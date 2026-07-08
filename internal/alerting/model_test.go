package alerting

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuleSpecValidateAccepts一期查询式日志规则(t *testing.T) {
	err := validRuleSpec().Validate()
	require.NoError(t, err)
}

func TestRuleSpecNormalizeDefaultsSignalTypeToLogs(t *testing.T) {
	spec := validRuleSpec()
	require.Empty(t, spec.SignalType)

	normalized := spec.Normalize()

	require.Equal(t, SignalTypeLogs, normalized.SignalType)
	require.NoError(t, normalized.Validate())
}

func TestRuleSpecValidateAcceptsMetricsRule(t *testing.T) {
	spec := validMetricsRuleSpec()

	require.NoError(t, spec.Validate())
}

func TestRuleSpecValidateRejectsLogsQueryModeForMetrics(t *testing.T) {
	spec := validMetricsRuleSpec()
	spec.Query.Mode = QueryModeContains

	err := spec.Validate()

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "query.mode")
}

func TestRuleSpecValidateRejectsDashboardVariablesForMetrics(t *testing.T) {
	spec := validMetricsRuleSpec()
	spec.Query.Expression = `sum(rate(http_requests_total[$__interval]))`

	err := spec.Validate()

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "Dashboard 变量")
}

func TestRuleSpecValidateRejectsDerivedMetricForMetrics(t *testing.T) {
	spec := validMetricsRuleSpec()
	spec.DerivedMetric = &DerivedMetricSpec{Enabled: true, Signal: "match_count"}

	err := spec.Validate()

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "指标告警不支持日志派生指标")
}

func TestRuleSpecValidateRejectsMetricsRuleWithoutBasePromQL(t *testing.T) {
	spec := validMetricsRuleSpec()
	spec.Scope.BasePromQL = ""

	err := spec.Validate()

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "base_promql")
}

func TestRuleSpecValidateAcceptsExternalLogTargetScope(t *testing.T) {
	spec := validRuleSpec()
	spec.Scope.LogRouteID = ""
	spec.Scope.LogTargetID = "target-orders"
	spec.Scope.BaseFilter = `"stream":"orders"`

	require.NoError(t, spec.Validate())
}

func TestRuleSpecValidateRejects严格连续语义(t *testing.T) {
	spec := validRuleSpec()
	spec.Trigger.Mode = TriggerModeConsecutive

	err := spec.Validate()

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "严格连续")
}

func TestRuleSpecValidateRejectsUnboundedGrouping(t *testing.T) {
	spec := validRuleSpec()
	spec.Grouping.Fields = []string{"service.name", "environment", "region", "pod"}

	err := spec.Validate()

	require.ErrorIs(t, err, ErrInvalidSpec)
	require.Contains(t, err.Error(), "最多 3")
}

func TestRuleSpecValidateRejectsInvalidDurationsAndThreshold(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RuleSpec)
	}{
		{name: "窗口过长", mutate: func(spec *RuleSpec) { spec.Trigger.Window = "2h" }},
		{name: "评估间隔大于窗口", mutate: func(spec *RuleSpec) { spec.Trigger.EvaluationInterval = "2m" }},
		{name: "计数阈值小于一", mutate: func(spec *RuleSpec) { spec.Trigger.Threshold = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validRuleSpec()
			tt.mutate(&spec)
			require.ErrorIs(t, spec.Validate(), ErrInvalidSpec)
		})
	}
}

func TestRuleSpecInputHashIsStableAndSensitive(t *testing.T) {
	spec := validRuleSpec()
	first, err := spec.InputHash()
	require.NoError(t, err)
	second, err := spec.InputHash()
	require.NoError(t, err)
	require.Equal(t, first, second)

	changed := spec
	changed.Trigger.Threshold = 4
	changedHash, err := changed.InputHash()
	require.NoError(t, err)
	require.NotEqual(t, first, changedHash)
}

func TestRuleSpecNormalizeDoesNotMutateCaller(t *testing.T) {
	spec := validRuleSpec()
	spec.Grouping.Fields = []string{" z ", "a"}
	normalized := spec.Normalize()
	require.Equal(t, []string{" z ", "a"}, spec.Grouping.Fields)
	require.Equal(t, []string{"a", "z"}, normalized.Grouping.Fields)
}

func TestValidationErrorSupportsErrorsIs(t *testing.T) {
	err := invalidSpec("name", "规则名称不能为空")
	require.True(t, errors.Is(err, ErrInvalidSpec))
}

func TestRuleSpecValidateRejectsUnboundedDerivedMetricLabels(t *testing.T) {
	spec := validRuleSpec()
	spec.DerivedMetric = &DerivedMetricSpec{Enabled: true, Signal: "match_count", Labels: map[string]string{"request_id": "dynamic"}}
	require.ErrorIs(t, spec.Validate(), ErrInvalidSpec)
}

func TestRuleSpecValidateRejectsGroupedDerivedMetric(t *testing.T) {
	spec := validRuleSpec()
	spec.DerivedMetric = &DerivedMetricSpec{Enabled: true, Signal: "match_count"}
	require.ErrorIs(t, spec.Validate(), ErrInvalidSpec)
	spec.Grouping.Fields = nil
	require.NoError(t, spec.Validate())
}

func validRuleSpec() RuleSpec {
	return RuleSpec{
		Name: "payment-failed",
		Scope: RuleScope{
			ServiceID:   "service-payment",
			ServiceName: "payment-service",
			LogRouteID:  "route-prod",
			EndpointID:  "vl-prod",
			AccountID:   "1001",
			ProjectID:   "2001",
		},
		Query: QuerySpec{Mode: QueryModeContains, Expression: "payment failed"},
		Trigger: TriggerSpec{
			Mode:               TriggerModeWindow,
			Aggregation:        AggregationCount,
			Operator:           OperatorGTE,
			Threshold:          3,
			Window:             "1m",
			EvaluationInterval: "30s",
			EvaluationDelay:    "5s",
		},
		Grouping: GroupingSpec{Fields: []string{"deployment.environment"}, MaxInstances: 100},
		Notification: NotificationSpec{
			PolicyID:  "pay-team-oncall",
			Severity:  SeverityCritical,
			OwnerTeam: "pay-team",
			Receiver:  "pay-oncall",
		},
	}
}

func validMetricsRuleSpec() RuleSpec {
	return RuleSpec{
		SignalType: SignalTypeMetrics,
		Name:       "orders-request-rate",
		Scope: RuleScope{
			ServiceID:        "service-orders",
			ServiceName:      "orders-api",
			EndpointID:       "vm-prod",
			MetricsBindingID: "binding-orders",
			AccountID:        "1001",
			ProjectID:        "2001",
			BasePromQL:       `service:requests:rate5m{service="orders-api"}`,
		},
		Query: QuerySpec{Mode: QueryModePromQL, Expression: `sum(rate(http_requests_total{status=~"5.."}[5m]))`},
		Trigger: TriggerSpec{
			Mode:               TriggerModeWindow,
			Aggregation:        AggregationCount,
			Operator:           OperatorGTE,
			Threshold:          10,
			Window:             "5m",
			EvaluationInterval: "1m",
			EvaluationDelay:    "0s",
		},
		Grouping: GroupingSpec{MaxInstances: 20},
		Notification: NotificationSpec{
			PolicyID:  "orders-oncall",
			Severity:  SeverityWarning,
			OwnerTeam: "orders-team",
			Receiver:  "orders-oncall",
		},
	}
}
