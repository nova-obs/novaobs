package alerting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCompileVmalertArtifactProducesTenantScopedVLogsGroups(t *testing.T) {
	rule := Rule{ID: "rule-abc", Spec: validRuleSpec(), State: RuleStateEnabled}
	artifact, err := CompileVmalertArtifact("vmalert-logs:vl-prod", []Rule{rule}, time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC))

	require.NoError(t, err)
	require.NotEmpty(t, artifact.Hash)
	require.Equal(t, []string{"rule-abc"}, artifact.RuleIDs)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(artifact.Content), &document))
	groups := document["groups"].([]any)
	group := groups[0].(map[string]any)
	require.Equal(t, "vlogs", group["type"])
	require.Equal(t, "30s", group["interval"])
	require.Equal(t, 100, group["limit"])
	require.ElementsMatch(t, []any{"AccountID: 1001", "ProjectID: 2001"}, group["headers"])
	rules := group["rules"].([]any)
	compiled := rules[0].(map[string]any)
	require.Contains(t, compiled["expr"], "_time:1m")
	require.Equal(t, "logs", compiled["labels"].(map[string]any)["signal_type"])
	require.Equal(t, "critical", compiled["labels"].(map[string]any)["severity"])
	require.Equal(t, "pay-oncall", compiled["labels"].(map[string]any)["notification_receiver"])
}

func TestCompileVmalertArtifactProducesMetricsGroupsWithoutVLogsHeaders(t *testing.T) {
	spec := validMetricsRuleSpec()
	rule := Rule{ID: "rule-metrics", Spec: spec, State: RuleStateEnabled}

	artifact, err := CompileVmalertArtifact("vmalert-metrics:vm-prod", []Rule{rule}, time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC))

	require.NoError(t, err)
	require.Equal(t, []string{"rule-metrics"}, artifact.RuleIDs)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(artifact.Content), &document))
	group := document["groups"].([]any)[0].(map[string]any)
	require.Equal(t, "novaapm_metrics_rule_metrics", group["name"])
	require.NotContains(t, group, "type")
	require.NotContains(t, group, "headers")
	rules := group["rules"].([]any)
	compiled := rules[0].(map[string]any)
	require.Equal(t, "NovaAPMMetricAlert_rule_metrics", compiled["alert"])
	require.Contains(t, compiled["expr"], "http_requests_total")
	require.Equal(t, "metrics", compiled["labels"].(map[string]any)["signal_type"])
	require.Equal(t, "vm-prod", compiled["labels"].(map[string]any)["endpoint_id"])
	require.Equal(t, "env-prod", compiled["labels"].(map[string]any)["novaapm_environment_id"])
}

func TestCompileVmalertArtifactIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	first := Rule{ID: "rule-a", Spec: validRuleSpec(), State: RuleStateEnabled}
	second := Rule{ID: "rule-b", Spec: validRuleSpec(), State: RuleStateEnabled}
	a, err := CompileVmalertArtifact("runtime", []Rule{first, second}, time.Now())
	require.NoError(t, err)
	b, err := CompileVmalertArtifact("runtime", []Rule{second, first}, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, a.Hash, b.Hash)
	require.Equal(t, a.Content, b.Content)
}

func TestCompileVmalertArtifactExcludesDisabledRules(t *testing.T) {
	artifact, err := CompileVmalertArtifact("runtime", []Rule{{ID: "disabled", Spec: validRuleSpec(), State: RuleStateDisabled}}, time.Now())
	require.NoError(t, err)
	require.Empty(t, artifact.RuleIDs)
	require.Contains(t, artifact.Content, "groups: []")
}

func TestCompileVmalertArtifactAddsBoundedRecordingRuleWhenDerivedMetricEnabled(t *testing.T) {
	spec := validRuleSpec()
	spec.Grouping.Fields = nil
	spec.Scope.EnvironmentID = "env-prod"
	spec.DerivedMetric = &DerivedMetricSpec{Enabled: true, Signal: "match_count", Labels: map[string]string{"environment": "prod"}}
	artifact, err := CompileVmalertArtifact("runtime", []Rule{{ID: "rule-a", Spec: spec, State: RuleStateEnabled}}, time.Now())
	require.NoError(t, err)

	var document map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(artifact.Content), &document))
	group := document["groups"].([]any)[0].(map[string]any)
	rules := group["rules"].([]any)
	require.Len(t, rules, 2)
	recording := rules[1].(map[string]any)
	require.Equal(t, "novaapm_log_matches", recording["record"])
	require.Contains(t, recording["expr"], "_time:1m")
	require.NotContains(t, recording["expr"], "filter matches")
	require.Equal(t, "rule-a", recording["labels"].(map[string]any)["novaapm_rule_id"])
	require.Equal(t, "env-prod", recording["labels"].(map[string]any)["novaapm_environment_id"])
}
