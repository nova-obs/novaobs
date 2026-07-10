package alerting

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventIngestorCreatesIdempotentInstanceTransition(t *testing.T) {
	repository := &fakeEventRepository{}
	ingestor := NewEventIngestor(repository, &fakeEventRuleResolver{rule: Rule{ID: "rule-a", Spec: RuleSpec{Scope: RuleScope{ServiceID: "service-a"}}, State: RuleStateEnabled}}, "shared-secret", func() time.Time {
		return time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	})
	payload := []AlertIngestAlert{{
		Status: "firing", Fingerprint: "abc123",
		Labels:       map[string]string{"novaapm_rule_id": "rule-a", "service_id": "service-a", "severity": "critical"},
		Annotations:  map[string]string{"summary": "支付失败"},
		StartsAt:     time.Date(2026, 6, 22, 9, 59, 0, 0, time.UTC),
		GeneratorURL: "http://vmalert.local/vmalert/alert",
	}}

	count, err := ingestor.IngestAlerts(context.Background(), "shared-secret", payload)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, AlertStateFiring, repository.instance.State)
	require.Equal(t, "rule-a", repository.instance.RuleID)
	require.Equal(t, repository.event.ID, repository.instance.LastEventID)
	require.NotEmpty(t, repository.event.ID)
}

func TestEventIngestorAcceptsDirectVmalertAlerts(t *testing.T) {
	repository := &fakeEventRepository{}
	ingestor := NewEventIngestor(repository, &fakeEventRuleResolver{rule: Rule{ID: "rule-a", Spec: RuleSpec{Scope: RuleScope{ServiceID: "service-a"}}, State: RuleStateEnabled}}, "shared-secret", func() time.Time {
		return time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	})
	alerts := []AlertIngestAlert{{
		Fingerprint: "abc123",
		Labels: map[string]string{
			"novaapm_rule_id":    "rule-a",
			"novaapm_runtime_id": "vmalert-logs:vl-prod",
			"service_id":         "service-a",
			"severity":           "critical",
		},
		Annotations:  map[string]string{"summary": "支付失败"},
		StartsAt:     time.Date(2026, 6, 22, 9, 59, 0, 0, time.UTC),
		GeneratorURL: "http://vmalert.local/vmalert/alert",
	}}

	count, err := ingestor.IngestAlerts(context.Background(), "shared-secret", alerts)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, AlertStateFiring, repository.instance.State)
	require.Equal(t, "vmalert-logs:vl-prod", repository.instance.SourceRuntimeID)
}

func TestEventIngestorInfersResolvedDirectAlertsFromEndedAt(t *testing.T) {
	repository := &fakeEventRepository{}
	ingestor := NewEventIngestor(repository, &fakeEventRuleResolver{rule: Rule{ID: "rule-a", Spec: RuleSpec{Scope: RuleScope{ServiceID: "service-a"}}, State: RuleStateEnabled}}, "shared-secret", func() time.Time {
		return time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	})

	count, err := ingestor.IngestAlerts(context.Background(), "shared-secret", []AlertIngestAlert{{
		Fingerprint: "abc123",
		Labels:      map[string]string{"novaapm_rule_id": "rule-a", "service_id": "service-a"},
		StartsAt:    time.Date(2026, 6, 22, 9, 59, 0, 0, time.UTC),
		EndsAt:      time.Date(2026, 6, 22, 9, 59, 30, 0, time.UTC),
	}})

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, AlertStateResolved, repository.instance.State)
	require.Equal(t, repository.instance.EndsAt, repository.event.OccurredAt)
}

func TestEventIngestorRejectsInvalidTokenAndMissingRuleIdentity(t *testing.T) {
	repository := &fakeEventRepository{}
	ingestor := NewEventIngestor(repository, &fakeEventRuleResolver{rule: Rule{ID: "rule-a", Spec: RuleSpec{Scope: RuleScope{ServiceID: "service-a"}}, State: RuleStateEnabled}}, "shared-secret", time.Now)
	_, err := ingestor.IngestAlerts(context.Background(), "wrong", nil)
	require.ErrorIs(t, err, ErrPermissionDenied)

	_, err = ingestor.IngestAlerts(context.Background(), "shared-secret", []AlertIngestAlert{{Fingerprint: "abc", Status: "firing"}})
	require.ErrorIs(t, err, ErrInvalidSpec)
}

func TestEventIngestorRejectsUnknownDisabledOrMismatchedRule(t *testing.T) {
	payload := []AlertIngestAlert{{
		Status: "firing", Fingerprint: "abc123",
		Labels:   map[string]string{"novaapm_rule_id": "rule-a", "service_id": "service-a"},
		StartsAt: time.Date(2026, 6, 22, 9, 59, 0, 0, time.UTC),
	}}

	_, err := NewEventIngestor(&fakeEventRepository{}, &fakeEventRuleResolver{err: ErrNotFound}, "shared-secret", time.Now).IngestAlerts(context.Background(), "shared-secret", payload)
	require.ErrorIs(t, err, ErrInvalidSpec)

	disabled := Rule{ID: "rule-a", Spec: RuleSpec{Scope: RuleScope{ServiceID: "service-a"}}, State: RuleStateDisabled}
	_, err = NewEventIngestor(&fakeEventRepository{}, &fakeEventRuleResolver{rule: disabled}, "shared-secret", time.Now).IngestAlerts(context.Background(), "shared-secret", payload)
	require.ErrorIs(t, err, ErrInvalidSpec)

	mismatched := Rule{ID: "rule-a", Spec: RuleSpec{Scope: RuleScope{ServiceID: "service-b"}}, State: RuleStateEnabled}
	_, err = NewEventIngestor(&fakeEventRepository{}, &fakeEventRuleResolver{rule: mismatched}, "shared-secret", time.Now).IngestAlerts(context.Background(), "shared-secret", payload)
	require.ErrorIs(t, err, ErrInvalidSpec)
}

type fakeEventRepository struct {
	instance AlertInstance
	event    AlertEvent
}

func (r *fakeEventRepository) ApplyEvent(_ context.Context, instance AlertInstance, event AlertEvent) error {
	r.instance, r.event = instance, event
	return nil
}

func (r *fakeEventRepository) ListInstances(context.Context, AlertInstanceFilter) ([]AlertInstance, error) {
	return nil, nil
}

func (r *fakeEventRepository) ListEvents(context.Context, AlertEventFilter) ([]AlertEvent, error) {
	return nil, nil
}

type fakeEventRuleResolver struct {
	rule Rule
	err  error
}

func (r *fakeEventRuleResolver) GetRule(context.Context, string) (Rule, error) {
	if r.err != nil {
		return Rule{}, r.err
	}
	return r.rule, nil
}
