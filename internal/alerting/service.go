package alerting

import (
	"context"
	"time"

	"novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type QueryTester interface {
	Test(ctx context.Context, req TestRequest) (TestResult, error)
}

type Dependencies struct {
	Repository      Repository
	Authorizer      Authorizer
	Tester          QueryTester
	ScopeResolver   ScopeResolver
	ReceiptSigner   TestReceiptSigner
	EventRepository EventRepository
	PolicyResolver  PolicyResolver
	Clock           func() time.Time
	NewID           func() string
}

type Service struct {
	repository      Repository
	authorizer      Authorizer
	tester          QueryTester
	scopeResolver   ScopeResolver
	receiptSigner   TestReceiptSigner
	eventRepository EventRepository
	policyResolver  PolicyResolver
	clock           func() time.Time
	newID           func() string
}

type EnableRequest struct {
	Spec          RuleSpec `json:"spec"`
	ChangeSummary string   `json:"change_summary"`
	TestToken     string   `json:"test_token"`
}

type UpdateRequest struct {
	Spec          RuleSpec `json:"spec"`
	ChangeSummary string   `json:"change_summary"`
	TestToken     string   `json:"test_token"`
}

type RollbackRequest struct {
	UpdateID      string `json:"update_id"`
	ChangeSummary string `json:"change_summary"`
}

type ChangeResult struct {
	Rule   Rule         `json:"rule"`
	Update UpdateRecord `json:"update"`
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

func NewService(deps Dependencies) Service {
	service := Service{
		repository:      deps.Repository,
		authorizer:      denyAuthorizer{},
		tester:          deps.Tester,
		scopeResolver:   deps.ScopeResolver,
		receiptSigner:   deps.ReceiptSigner,
		eventRepository: deps.EventRepository,
		policyResolver:  deps.PolicyResolver,
		clock:           time.Now,
		newID:           func() string { return primitive.NewObjectID().Hex() },
	}
	if deps.Authorizer != nil {
		service.authorizer = deps.Authorizer
	}
	if deps.Clock != nil {
		service.clock = deps.Clock
	}
	if deps.NewID != nil {
		service.newID = deps.NewID
	}
	return service
}

func (s Service) ListInstances(ctx context.Context, subject platformrbac.Subject, filter AlertInstanceFilter) ([]AlertInstance, error) {
	if s.eventRepository == nil {
		return nil, ErrUnavailable
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 50
	}
	if filter.RuleID != "" {
		rule, err := s.Get(ctx, subject, filter.RuleID)
		if err != nil {
			return nil, err
		}
		filter.ServiceID = rule.Spec.Scope.ServiceID
	} else if filter.ServiceID != "" && !s.allowed(subject, RuleScope{ServiceID: filter.ServiceID}, "read") {
		return nil, ErrPermissionDenied
	}
	instances, err := s.eventRepository.ListInstances(ctx, filter)
	if err != nil || filter.RuleID != "" || filter.ServiceID != "" {
		return instances, err
	}
	rules, err := s.repository.ListRules(ctx, RuleFilter{})
	if err != nil {
		return nil, err
	}
	allowedRules := make(map[string]bool, len(rules))
	for _, rule := range rules {
		allowedRules[rule.ID] = s.allowed(subject, rule.Spec.Scope, "read")
	}
	visible := make([]AlertInstance, 0, len(instances))
	for _, instance := range instances {
		if allowedRules[instance.RuleID] {
			visible = append(visible, instance)
		}
	}
	return visible, nil
}

func (s Service) ListEvents(ctx context.Context, subject platformrbac.Subject, filter AlertEventFilter) ([]AlertEvent, error) {
	if s.eventRepository == nil {
		return nil, ErrUnavailable
	}
	if filter.RuleID == "" {
		return nil, invalidSpec("rule_id", "查询告警事件必须指定规则")
	}
	if _, err := s.Get(ctx, subject, filter.RuleID); err != nil {
		return nil, err
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 50
	}
	return s.eventRepository.ListEvents(ctx, filter)
}

func (s Service) Enable(ctx context.Context, subject platformrbac.Subject, req EnableRequest) (ChangeResult, error) {
	spec, err := s.resolveSpec(ctx, req.Spec)
	if err != nil {
		return ChangeResult{}, err
	}
	if err := spec.Validate(); err != nil {
		return ChangeResult{}, err
	}
	if !s.allowed(subject, spec.Scope, "manage") {
		return ChangeResult{}, ErrPermissionDenied
	}
	if err := s.validatePolicy(ctx, spec); err != nil {
		return ChangeResult{}, err
	}
	if err := s.verifyTestReceipt(subject, spec, req.TestToken); err != nil {
		return ChangeResult{}, err
	}
	if s.repository == nil {
		return ChangeResult{}, ErrUnavailable
	}
	now := s.clock().UTC()
	actor := actorFromSubject(subject)
	ruleID := s.newID()
	updateID := s.newID()
	auditID := s.newID()
	inputHash, err := spec.InputHash()
	if err != nil {
		return ChangeResult{}, err
	}
	change := ChangeSet{
		Rule: Rule{
			ID:              ruleID,
			Spec:            spec,
			State:           RuleStateEnabled,
			ApplyStatus:     ApplyStatusPending,
			Health:          EvaluationHealth{Status: EvaluationHealthUnknown},
			CurrentUpdateID: updateID,
			CreatedBy:       actor,
			UpdatedBy:       actor,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		Update: UpdateRecord{
			ID:             updateID,
			RuleID:         ruleID,
			Action:         UpdateActionCreate,
			ResultingState: RuleStateEnabled,
			ChangeSummary:  defaultSummary(req.ChangeSummary, "创建并启用告警"),
			Spec:           spec,
			InputHash:      inputHash,
			Actor:          actor,
			AuditID:        auditID,
			CreatedAt:      now,
		},
		Audit: newAuditEvent(auditID, actor, ruleID, spec, UpdateActionCreate, now),
	}
	if err := s.repository.SaveChange(ctx, change); err != nil {
		return ChangeResult{}, err
	}
	return resultFromChange(change), nil
}

func (s Service) Update(ctx context.Context, subject platformrbac.Subject, ruleID string, req UpdateRequest) (ChangeResult, error) {
	if s.repository == nil {
		return ChangeResult{}, ErrUnavailable
	}
	current, err := s.repository.GetRule(ctx, ruleID)
	if err != nil {
		return ChangeResult{}, err
	}
	spec, err := s.resolveSpec(ctx, req.Spec)
	if err != nil {
		return ChangeResult{}, err
	}
	if err := spec.Validate(); err != nil {
		return ChangeResult{}, err
	}
	if !s.allowed(subject, current.Spec.Scope, "manage") || !s.allowed(subject, spec.Scope, "manage") {
		return ChangeResult{}, ErrPermissionDenied
	}
	if err := s.validatePolicy(ctx, spec); err != nil {
		return ChangeResult{}, err
	}
	if err := s.verifyTestReceipt(subject, spec, req.TestToken); err != nil {
		return ChangeResult{}, err
	}
	return s.saveExistingChange(ctx, subject, current, spec, RuleStateEnabled, UpdateActionUpdate, "", defaultSummary(req.ChangeSummary, "更新告警配置"))
}

func (s Service) Rollback(ctx context.Context, subject platformrbac.Subject, ruleID string, req RollbackRequest) (ChangeResult, error) {
	if s.repository == nil {
		return ChangeResult{}, ErrUnavailable
	}
	current, err := s.repository.GetRule(ctx, ruleID)
	if err != nil {
		return ChangeResult{}, err
	}
	if !s.allowed(subject, current.Spec.Scope, "manage") {
		return ChangeResult{}, ErrPermissionDenied
	}
	source, err := s.repository.GetUpdate(ctx, ruleID, req.UpdateID)
	if err != nil {
		return ChangeResult{}, err
	}
	if !s.allowed(subject, source.Spec.Scope, "manage") {
		return ChangeResult{}, ErrPermissionDenied
	}
	if err := s.validatePolicy(ctx, source.Spec); err != nil {
		return ChangeResult{}, err
	}
	targetState := source.ResultingState
	if targetState == "" {
		targetState = RuleStateEnabled
	}
	return s.saveExistingChange(ctx, subject, current, source.Spec.Normalize(), targetState, UpdateActionRollback, source.ID, defaultSummary(req.ChangeSummary, "回退到历史配置"))
}

func (s Service) Disable(ctx context.Context, subject platformrbac.Subject, ruleID string, summary string) (ChangeResult, error) {
	if s.repository == nil {
		return ChangeResult{}, ErrUnavailable
	}
	current, err := s.repository.GetRule(ctx, ruleID)
	if err != nil {
		return ChangeResult{}, err
	}
	if !s.allowed(subject, current.Spec.Scope, "manage") {
		return ChangeResult{}, ErrPermissionDenied
	}
	if current.State == RuleStateDisabled {
		return ChangeResult{}, ErrConflict
	}
	return s.saveExistingChange(ctx, subject, current, current.Spec, RuleStateDisabled, UpdateActionDisable, "", defaultSummary(summary, "停用告警"))
}

func (s Service) Test(ctx context.Context, subject platformrbac.Subject, req TestRequest) (TestResult, error) {
	resolved, err := s.resolveSpec(ctx, req.Spec)
	if err != nil {
		return TestResult{}, err
	}
	req.Spec = resolved
	if err := req.Spec.Validate(); err != nil {
		return TestResult{}, err
	}
	if !s.allowed(subject, req.Spec.Scope, "manage") {
		return TestResult{}, ErrPermissionDenied
	}
	if err := s.validatePolicy(ctx, req.Spec); err != nil {
		return TestResult{}, err
	}
	if req.RangeStart.IsZero() || req.RangeEnd.IsZero() || !req.RangeStart.Before(req.RangeEnd) || req.RangeEnd.Sub(req.RangeStart) > 24*time.Hour {
		return TestResult{}, invalidSpec("range", "测试时间范围必须有效且不能超过 24 小时")
	}
	if s.tester == nil {
		return TestResult{}, ErrUnavailable
	}
	result, err := s.tester.Test(ctx, req)
	if err != nil {
		return TestResult{}, err
	}
	result.InputHash, err = req.Spec.InputHash()
	if err != nil {
		return TestResult{}, err
	}
	result.TestedAt = s.clock().UTC()
	if s.receiptSigner != nil {
		result.TestToken, err = s.receiptSigner.Issue(subject, result.InputHash, result.TestedAt)
		if err != nil {
			return TestResult{}, err
		}
	}
	return result, nil
}

func (s Service) verifyTestReceipt(subject platformrbac.Subject, spec RuleSpec, token string) error {
	if s.receiptSigner == nil {
		return nil
	}
	hash, err := spec.InputHash()
	if err != nil {
		return err
	}
	if err := s.receiptSigner.Verify(subject, hash, token, s.clock().UTC()); err != nil {
		return ErrTestRequired
	}
	return nil
}

func (s Service) validatePolicy(ctx context.Context, spec RuleSpec) error {
	if s.policyResolver == nil {
		return nil
	}
	return s.policyResolver.ValidatePolicy(ctx, spec.Notification.PolicyID, spec.Scope.ServiceID)
}

func (s Service) resolveSpec(ctx context.Context, spec RuleSpec) (RuleSpec, error) {
	spec = spec.Normalize()
	if s.scopeResolver == nil {
		return spec, nil
	}
	scope, err := s.scopeResolver.ResolveScope(ctx, spec)
	if err != nil {
		return RuleSpec{}, err
	}
	spec.Scope = scope
	return spec.Normalize(), nil
}

func (s Service) List(ctx context.Context, subject platformrbac.Subject, filter RuleFilter) ([]Rule, error) {
	if s.repository == nil {
		return nil, ErrUnavailable
	}
	rules, err := s.repository.ListRules(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if s.allowed(subject, rule.Spec.Scope, "read") {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (s Service) Get(ctx context.Context, subject platformrbac.Subject, id string) (Rule, error) {
	if s.repository == nil {
		return Rule{}, ErrUnavailable
	}
	rule, err := s.repository.GetRule(ctx, id)
	if err != nil {
		return Rule{}, err
	}
	if !s.allowed(subject, rule.Spec.Scope, "read") {
		return Rule{}, ErrPermissionDenied
	}
	return rule, nil
}

func (s Service) ListUpdates(ctx context.Context, subject platformrbac.Subject, ruleID string, limit int) ([]UpdateRecord, error) {
	if _, err := s.Get(ctx, subject, ruleID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return s.repository.ListUpdates(ctx, ruleID, limit)
}

func (s Service) saveExistingChange(ctx context.Context, subject platformrbac.Subject, current Rule, spec RuleSpec, state string, action string, sourceUpdateID string, summary string) (ChangeResult, error) {
	inputHash, err := spec.InputHash()
	if err != nil {
		return ChangeResult{}, err
	}
	now := s.clock().UTC()
	actor := actorFromSubject(subject)
	updateID := s.newID()
	auditID := s.newID()
	updated := current
	updated.Spec = spec
	updated.State = state
	updated.ApplyStatus = ApplyStatusPending
	updated.CurrentUpdateID = updateID
	updated.UpdatedBy = actor
	updated.UpdatedAt = now
	change := ChangeSet{
		Rule: updated,
		Update: UpdateRecord{
			ID:             updateID,
			RuleID:         current.ID,
			ParentUpdateID: current.CurrentUpdateID,
			SourceUpdateID: sourceUpdateID,
			Action:         action,
			ResultingState: state,
			ChangeSummary:  summary,
			Spec:           spec,
			InputHash:      inputHash,
			Actor:          actor,
			AuditID:        auditID,
			CreatedAt:      now,
		},
		Audit:                   newAuditEvent(auditID, actor, current.ID, spec, action, now),
		ExpectedCurrentUpdateID: current.CurrentUpdateID,
	}
	if err := s.repository.SaveChange(ctx, change); err != nil {
		return ChangeResult{}, err
	}
	return resultFromChange(change), nil
}

func (s Service) allowed(subject platformrbac.Subject, scope RuleScope, action string) bool {
	if subject.ID == "" || subject.Type == "" {
		return false
	}
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "alerts.rule",
		Action:   action,
		Scope:    platformrbac.Scope{ServiceID: scope.ServiceID},
	})
	return decision.Allowed
}

func actorFromSubject(subject platformrbac.Subject) Actor {
	return Actor{ID: subject.ID, Type: subject.Type, Name: subject.DisplayName}
}

func newAuditEvent(id string, actor Actor, ruleID string, spec RuleSpec, action string, now time.Time) audit.Event {
	return audit.Event{
		ID:       id,
		Actor:    audit.Actor{ID: actor.ID, Name: actor.Name},
		Resource: audit.Resource{Type: "alerts.rule", Name: ruleID},
		Action:   action,
		Scope:    spec.Scope.ServiceID,
		RequestSummary: map[string]any{
			"signal_type":        spec.SignalType,
			"service_id":         spec.Scope.ServiceID,
			"log_route_id":       spec.Scope.LogRouteID,
			"log_target_id":      spec.Scope.LogTargetID,
			"metrics_binding_id": spec.Scope.MetricsBindingID,
			"endpoint_id":        spec.Scope.EndpointID,
			"rule_name":          spec.Name,
		},
		Result:    "accepted",
		CreatedAt: now,
	}
}

func defaultSummary(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	if len(value) > 500 {
		return value[:500]
	}
	return value
}

func resultFromChange(change ChangeSet) ChangeResult {
	return ChangeResult{Rule: change.Rule, Update: change.Update}
}
