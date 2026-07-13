package alerting

import (
	"context"
	"strconv"
	"testing"
	"time"

	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestEnableCreatesProductionRuleAndUpdateWithoutDraft(t *testing.T) {
	repo := newFakeRepository()
	service := newTestService(repo)

	result, err := service.Enable(context.Background(), testSubject(), EnableRequest{
		Spec:          validRuleSpec(),
		ChangeSummary: "创建支付失败日志告警",
	})

	require.NoError(t, err)
	require.Equal(t, RuleStateEnabled, result.Rule.State)
	require.Equal(t, ApplyStatusPending, result.Rule.ApplyStatus)
	require.NotEmpty(t, result.Rule.CurrentUpdateID)
	require.Equal(t, UpdateActionCreate, result.Update.Action)
	require.Equal(t, testSubject().ID, result.Update.Actor.ID)
	require.Len(t, repo.changes, 1)
}

func TestUpdateKeepsRuleIdentityAndAppendsHistory(t *testing.T) {
	repo := newFakeRepository()
	service := newTestService(repo)
	created, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})
	require.NoError(t, err)

	updatedSpec := validRuleSpec()
	updatedSpec.Trigger.Threshold = 5
	updated, err := service.Update(context.Background(), testSubject(), created.Rule.ID, UpdateRequest{
		Spec:          updatedSpec,
		ChangeSummary: "阈值从 3 调整为 5",
	})

	require.NoError(t, err)
	require.Equal(t, created.Rule.ID, updated.Rule.ID)
	require.Equal(t, float64(5), updated.Rule.Spec.Trigger.Threshold)
	require.Equal(t, UpdateActionUpdate, updated.Update.Action)
	require.Equal(t, created.Update.ID, updated.Update.ParentUpdateID)
	require.Len(t, repo.changes, 2)
}

func TestRollbackCopiesHistoricalSnapshotIntoNewUpdate(t *testing.T) {
	repo := newFakeRepository()
	service := newTestService(repo)
	created, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})
	require.NoError(t, err)
	changedSpec := validRuleSpec()
	changedSpec.Trigger.Threshold = 9
	_, err = service.Update(context.Background(), testSubject(), created.Rule.ID, UpdateRequest{Spec: changedSpec})
	require.NoError(t, err)

	rolledBack, err := service.Rollback(context.Background(), testSubject(), created.Rule.ID, RollbackRequest{
		UpdateID: created.Update.ID,
	})

	require.NoError(t, err)
	require.Equal(t, float64(3), rolledBack.Rule.Spec.Trigger.Threshold)
	require.Equal(t, UpdateActionRollback, rolledBack.Update.Action)
	require.Equal(t, created.Update.ID, rolledBack.Update.SourceUpdateID)
	require.Len(t, repo.changes, 3)
}

func TestRollbackToDisableRecordRestoresDisabledState(t *testing.T) {
	repo := newFakeRepository()
	service := newTestService(repo)
	created, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})
	require.NoError(t, err)
	disabled, err := service.Disable(context.Background(), testSubject(), created.Rule.ID, DisableRequest{ChangeSummary: "维护期停用"})
	require.NoError(t, err)
	_, err = service.Update(context.Background(), testSubject(), created.Rule.ID, UpdateRequest{Spec: validRuleSpec()})
	require.NoError(t, err)

	rolledBack, err := service.Rollback(context.Background(), testSubject(), created.Rule.ID, RollbackRequest{UpdateID: disabled.Update.ID})
	require.NoError(t, err)
	require.Equal(t, RuleStateDisabled, rolledBack.Rule.State)
}

func TestDisableRejectsUnexpectedSignalType(t *testing.T) {
	repo := newFakeRepository()
	service := newTestService(repo)
	created, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})
	require.NoError(t, err)

	_, err = service.Disable(context.Background(), testSubject(), created.Rule.ID, DisableRequest{
		ExpectedSignalType: SignalTypeMetrics,
		ChangeSummary:      "停用指标告警",
	})
	require.ErrorIs(t, err, ErrConflict)

	current, err := repo.GetRule(context.Background(), created.Rule.ID)
	require.NoError(t, err)
	require.Equal(t, RuleStateEnabled, current.State)
}

func TestTestRuleDoesNotPersistAnything(t *testing.T) {
	repo := newFakeRepository()
	tester := &fakeTester{result: TestResult{MatchedLogCount: 184, EstimatedInstanceCount: 6}}
	service := NewService(Dependencies{
		Repository: repo,
		Authorizer: allowAuthorizer{},
		Tester:     tester,
		Clock:      fixedClock,
		NewID:      sequentialIDs(),
	})

	result, err := service.Test(context.Background(), testSubject(), TestRequest{
		Spec:       validRuleSpec(),
		RangeStart: fixedClock().Add(-5 * time.Minute),
		RangeEnd:   fixedClock(),
	})

	require.NoError(t, err)
	require.Equal(t, int64(184), result.MatchedLogCount)
	require.NotEmpty(t, result.InputHash)
	require.Empty(t, repo.changes)
}

func TestManageOperationsFailClosedWithoutPermission(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(Dependencies{Repository: repo, Clock: fixedClock, NewID: sequentialIDs()})

	_, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})

	require.ErrorIs(t, err, ErrPermissionDenied)
	require.Empty(t, repo.changes)
}

func TestEnableRequiresFreshTestReceiptWhenSignerConfigured(t *testing.T) {
	repo := newFakeRepository()
	signer := NewHMACTestReceiptSigner([]byte("12345678901234567890123456789012"))
	service := NewService(Dependencies{
		Repository: repo, Authorizer: allowAuthorizer{}, Tester: &fakeTester{},
		ReceiptSigner: signer, Clock: fixedClock, NewID: sequentialIDs(),
	})

	_, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})
	require.ErrorIs(t, err, ErrTestRequired)

	tested, err := service.Test(context.Background(), testSubject(), TestRequest{
		Spec: validRuleSpec(), RangeStart: fixedClock().Add(-5 * time.Minute), RangeEnd: fixedClock(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, tested.TestToken)

	_, err = service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec(), TestToken: tested.TestToken})
	require.NoError(t, err)
}

func TestTestReceiptCannotBeReusedAfterRuleInputChanges(t *testing.T) {
	signer := NewHMACTestReceiptSigner([]byte("12345678901234567890123456789012"))
	service := NewService(Dependencies{
		Repository: newFakeRepository(), Authorizer: allowAuthorizer{}, Tester: &fakeTester{},
		ReceiptSigner: signer, Clock: fixedClock, NewID: sequentialIDs(),
	})
	tested, err := service.Test(context.Background(), testSubject(), TestRequest{
		Spec: validRuleSpec(), RangeStart: fixedClock().Add(-time.Minute), RangeEnd: fixedClock(),
	})
	require.NoError(t, err)
	changed := validRuleSpec()
	changed.Trigger.Threshold = 4

	_, err = service.Enable(context.Background(), testSubject(), EnableRequest{Spec: changed, TestToken: tested.TestToken})
	require.ErrorIs(t, err, ErrTestRequired)
}

func TestEnableRequiresManagedNotificationPolicyWhenResolverConfigured(t *testing.T) {
	service := NewService(Dependencies{
		Repository: newFakeRepository(), Authorizer: allowAuthorizer{},
		PolicyResolver: rejectingPolicyResolver{}, Clock: fixedClock, NewID: sequentialIDs(),
	})
	_, err := service.Enable(context.Background(), testSubject(), EnableRequest{Spec: validRuleSpec()})
	require.ErrorIs(t, err, ErrInvalidSpec)
}

type fakeRepository struct {
	rules   map[string]Rule
	updates map[string]UpdateRecord
	changes []ChangeSet
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{rules: map[string]Rule{}, updates: map[string]UpdateRecord{}}
}

func (r *fakeRepository) SaveChange(_ context.Context, change ChangeSet) error {
	r.rules[change.Rule.ID] = change.Rule
	r.updates[change.Update.ID] = change.Update
	r.changes = append(r.changes, change)
	return nil
}

func (r *fakeRepository) ListRules(_ context.Context, _ RuleFilter) ([]Rule, error) {
	out := make([]Rule, 0, len(r.rules))
	for _, rule := range r.rules {
		out = append(out, rule)
	}
	return out, nil
}

func (r *fakeRepository) GetRule(_ context.Context, id string) (Rule, error) {
	rule, ok := r.rules[id]
	if !ok {
		return Rule{}, ErrNotFound
	}
	return rule, nil
}

func (r *fakeRepository) GetUpdate(_ context.Context, ruleID string, updateID string) (UpdateRecord, error) {
	update, ok := r.updates[updateID]
	if !ok || update.RuleID != ruleID {
		return UpdateRecord{}, ErrNotFound
	}
	return update, nil
}

func (r *fakeRepository) ListUpdates(_ context.Context, ruleID string, _ int) ([]UpdateRecord, error) {
	out := []UpdateRecord{}
	for _, update := range r.updates {
		if update.RuleID == ruleID {
			out = append(out, update)
		}
	}
	return out, nil
}

type allowAuthorizer struct{}

func (allowAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type fakeTester struct{ result TestResult }

func (t *fakeTester) Test(context.Context, TestRequest) (TestResult, error) { return t.result, nil }

type rejectingPolicyResolver struct{}

func (rejectingPolicyResolver) ValidatePolicy(context.Context, string, string) error {
	return invalidSpec("notification.policy_id", "通知策略不存在")
}

func testSubject() platformrbac.Subject {
	return platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "Alice"}
}

func fixedClock() time.Time {
	return time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
}

func sequentialIDs() func() string {
	index := 0
	return func() string {
		index++
		return "id-" + strconv.Itoa(index)
	}
}

func newTestService(repo Repository) Service {
	return NewService(Dependencies{
		Repository: repo,
		Authorizer: allowAuthorizer{},
		Clock:      fixedClock,
		NewID:      sequentialIDs(),
	})
}
