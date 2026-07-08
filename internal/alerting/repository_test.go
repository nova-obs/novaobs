package alerting

import (
	"context"
	"testing"
	"time"

	"novaobs/internal/database/memstore"
	"novaobs/internal/platform/audit"

	"github.com/stretchr/testify/require"
)

func TestStoreRepositoryEnrichesRuntimeRulesWithManagedReceiver(t *testing.T) {
	store := memstore.NewStore()
	repository := NewStoreRepository(store.Alerting())
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	policy := validNotificationPolicy()
	policy.CreatedAt, policy.UpdatedAt = now, now
	require.NoError(t, repository.SavePolicy(context.Background(), time.Time{}, policy, audit.Event{ID: "audit-policy"}))
	spec := validRuleSpec()
	spec.Notification.PolicyID = policy.ID
	spec.Notification.Receiver = ""
	rule := Rule{ID: "rule-a", Spec: spec, State: RuleStateEnabled, CurrentUpdateID: "update-a", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, repository.SaveChange(context.Background(), ChangeSet{
		Rule: rule, Update: UpdateRecord{ID: "update-a", RuleID: rule.ID}, Audit: audit.Event{ID: "audit-rule"},
	}))

	rules, err := repository.ListRuntimeRules(context.Background(), "vmalert-logs:"+spec.Scope.EndpointID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, policy.Receiver, rules[0].Spec.Notification.Receiver)
}

func TestStoreRepositoryMarksRuntimeRulesApplied(t *testing.T) {
	store := memstore.NewStore()
	repository := NewStoreRepository(store.Alerting())
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	spec := validRuleSpec()
	rule := Rule{ID: "rule-a", Spec: spec, State: RuleStateDisabled, ApplyStatus: ApplyStatusPending, CurrentUpdateID: "update-a", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, repository.SaveChange(context.Background(), ChangeSet{
		Rule: rule, Update: UpdateRecord{ID: "update-a", RuleID: rule.ID}, Audit: audit.Event{ID: "audit-rule"},
	}))

	applied, err := repository.MarkRuntimeRulesApplied(context.Background(), "vmalert-logs:"+spec.Scope.EndpointID, now.Add(time.Minute))

	require.NoError(t, err)
	require.Equal(t, 1, applied)
	var stored Rule
	require.NoError(t, store.Alerting().FindRuleByID(context.Background(), rule.ID, &stored))
	require.Equal(t, ApplyStatusApplied, stored.ApplyStatus)
	require.Equal(t, rule.CurrentUpdateID, stored.AppliedUpdateID)
}

func TestStoreRepositoryRuntimeRulesAreIsolatedBySignalType(t *testing.T) {
	store := memstore.NewStore()
	repository := NewStoreRepository(store.Alerting())
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	policy := validNotificationPolicy()
	policy.CreatedAt, policy.UpdatedAt = now, now
	require.NoError(t, repository.SavePolicy(context.Background(), time.Time{}, policy, audit.Event{ID: "audit-policy"}))
	logsSpec := validRuleSpec()
	logsSpec.Notification.PolicyID = policy.ID
	metricsSpec := validMetricsRuleSpec()
	metricsSpec.Scope.EndpointID = logsSpec.Scope.EndpointID
	metricsSpec.Notification.PolicyID = policy.ID
	require.NoError(t, repository.SaveChange(context.Background(), ChangeSet{
		Rule:   Rule{ID: "rule-logs", Spec: logsSpec, State: RuleStateEnabled, CurrentUpdateID: "update-logs", CreatedAt: now, UpdatedAt: now},
		Update: UpdateRecord{ID: "update-logs", RuleID: "rule-logs"},
		Audit:  audit.Event{ID: "audit-logs"},
	}))
	require.NoError(t, repository.SaveChange(context.Background(), ChangeSet{
		Rule:   Rule{ID: "rule-metrics", Spec: metricsSpec, State: RuleStateEnabled, CurrentUpdateID: "update-metrics", CreatedAt: now, UpdatedAt: now},
		Update: UpdateRecord{ID: "update-metrics", RuleID: "rule-metrics"},
		Audit:  audit.Event{ID: "audit-metrics"},
	}))

	rules, err := repository.ListRuntimeRules(context.Background(), "vmalert-logs:"+logsSpec.Scope.EndpointID)

	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, "rule-logs", rules[0].ID)
	applied, err := repository.MarkRuntimeRulesApplied(context.Background(), "vmalert-logs:"+logsSpec.Scope.EndpointID, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, applied)
	var metricsRule Rule
	require.NoError(t, store.Alerting().FindRuleByID(context.Background(), "rule-metrics", &metricsRule))
	require.Empty(t, metricsRule.AppliedUpdateID)
}
