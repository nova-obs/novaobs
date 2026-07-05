package alerting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"novaobs/internal/database"
	"novaobs/internal/platform/audit"
)

// Repository 保存告警控制面的生产真值。SaveChange 必须原子校验
// ExpectedCurrentUpdateID，并同时写入 Rule、UpdateRecord 和 Audit。
type Repository interface {
	SaveChange(ctx context.Context, change ChangeSet) error
	ListRules(ctx context.Context, filter RuleFilter) ([]Rule, error)
	GetRule(ctx context.Context, id string) (Rule, error)
	GetUpdate(ctx context.Context, ruleID string, updateID string) (UpdateRecord, error)
	ListUpdates(ctx context.Context, ruleID string, limit int) ([]UpdateRecord, error)
}

type ChangeSet struct {
	Rule                    Rule
	Update                  UpdateRecord
	Audit                   audit.Event
	ExpectedCurrentUpdateID string
}

type StoreRepository struct{ store database.AlertingStore }

func NewStoreRepository(store database.AlertingStore) StoreRepository {
	return StoreRepository{store: store}
}

func (r StoreRepository) SaveChange(ctx context.Context, change ChangeSet) error {
	err := r.store.SaveChange(ctx, change.ExpectedCurrentUpdateID, change.Rule, change.Update, change.Audit)
	return mapStoreError(err)
}

func (r StoreRepository) ListRules(ctx context.Context, filter RuleFilter) ([]Rule, error) {
	var items []Rule
	if err := r.store.FindRules(ctx, filter.ServiceID, filter.State, &items); err != nil {
		return nil, mapStoreError(err)
	}
	slices.SortFunc(items, func(a, b Rule) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return items, nil
}

func (r StoreRepository) GetRule(ctx context.Context, id string) (Rule, error) {
	var item Rule
	err := r.store.FindRuleByID(ctx, id, &item)
	return item, mapStoreError(err)
}

func (r StoreRepository) GetUpdate(ctx context.Context, ruleID string, updateID string) (UpdateRecord, error) {
	var item UpdateRecord
	err := r.store.FindUpdate(ctx, ruleID, updateID, &item)
	return item, mapStoreError(err)
}

func (r StoreRepository) ListUpdates(ctx context.Context, ruleID string, limit int) ([]UpdateRecord, error) {
	var items []UpdateRecord
	if err := r.store.FindUpdates(ctx, ruleID, limit, &items); err != nil {
		return nil, mapStoreError(err)
	}
	slices.SortFunc(items, func(a, b UpdateRecord) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r StoreRepository) ListRuntimeRules(ctx context.Context, runtimeID string) ([]Rule, error) {
	var rules []Rule
	endpointID := strings.TrimPrefix(runtimeID, "vmalert-logs:")
	if endpointID == "" || endpointID == runtimeID && strings.Contains(runtimeID, ":") {
		return nil, ErrInvalidSpec
	}
	if err := r.store.FindRuntimeRules(ctx, endpointID, &rules); err != nil {
		return nil, mapStoreError(err)
	}
	for index := range rules {
		if rules[index].State != RuleStateEnabled {
			continue
		}
		policy, err := r.GetPolicy(ctx, rules[index].Spec.Notification.PolicyID)
		if err != nil {
			return nil, err
		}
		rules[index].Spec.Notification.Receiver = policy.Receiver
	}
	slices.SortFunc(rules, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	return rules, nil
}

func (r StoreRepository) MarkRuntimeRulesApplied(ctx context.Context, runtimeID string, appliedAt time.Time) (int, error) {
	endpointID := strings.TrimPrefix(runtimeID, "vmalert-logs:")
	if endpointID == "" || endpointID == runtimeID && strings.Contains(runtimeID, ":") {
		return 0, ErrInvalidSpec
	}
	applied, err := r.store.MarkRuntimeRulesApplied(ctx, endpointID, appliedAt.UTC())
	return int(applied), mapStoreError(err)
}

func (r StoreRepository) ApplyEvent(ctx context.Context, instance AlertInstance, event AlertEvent) error {
	return mapStoreError(r.store.ApplyAlertEvent(ctx, instance, event))
}

func (r StoreRepository) ListInstances(ctx context.Context, filter AlertInstanceFilter) ([]AlertInstance, error) {
	var instances []AlertInstance
	if err := r.store.FindAlertInstances(ctx, filter.RuleID, filter.ServiceID, filter.State, filter.Limit, &instances); err != nil {
		return nil, mapStoreError(err)
	}
	return instances, nil
}

func (r StoreRepository) ListEvents(ctx context.Context, filter AlertEventFilter) ([]AlertEvent, error) {
	var events []AlertEvent
	if err := r.store.FindAlertEvents(ctx, filter.RuleID, filter.Fingerprint, filter.Limit, &events); err != nil {
		return nil, mapStoreError(err)
	}
	return events, nil
}

func (r StoreRepository) SavePolicy(ctx context.Context, expectedUpdatedAt time.Time, policy NotificationPolicy, auditEvent audit.Event) error {
	return mapStoreError(r.store.SaveNotificationPolicy(ctx, expectedUpdatedAt, policy, auditEvent))
}

func (r StoreRepository) GetPolicy(ctx context.Context, id string) (NotificationPolicy, error) {
	var policy NotificationPolicy
	err := r.store.FindNotificationPolicyByID(ctx, id, &policy)
	return policy, mapStoreError(err)
}

func (r StoreRepository) ListPolicies(ctx context.Context, serviceID string, enabledOnly bool) ([]NotificationPolicy, error) {
	var policies []NotificationPolicy
	if err := r.store.FindNotificationPolicies(ctx, serviceID, enabledOnly, &policies); err != nil {
		return nil, mapStoreError(err)
	}
	slices.SortFunc(policies, func(a, b NotificationPolicy) int { return strings.Compare(a.Name, b.Name) })
	return policies, nil
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, database.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, database.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}
