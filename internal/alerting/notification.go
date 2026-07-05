package alerting

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"novaobs/internal/platform/audit"
	platformrbac "novaobs/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var notificationReceiverPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type NotificationPolicy struct {
	ID          string    `json:"id" bson:"_id"`
	Name        string    `json:"name" bson:"name"`
	Description string    `json:"description,omitempty" bson:"description,omitempty"`
	ServiceID   string    `json:"service_id,omitempty" bson:"service_id,omitempty"`
	Receiver    string    `json:"receiver" bson:"receiver"`
	Enabled     bool      `json:"enabled" bson:"enabled"`
	CreatedBy   Actor     `json:"created_by" bson:"created_by"`
	UpdatedBy   Actor     `json:"updated_by" bson:"updated_by"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

func (p NotificationPolicy) Normalize() NotificationPolicy {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.ServiceID = strings.TrimSpace(p.ServiceID)
	p.Receiver = strings.TrimSpace(p.Receiver)
	return p
}

func (p NotificationPolicy) Validate() error {
	p = p.Normalize()
	if p.Name == "" || len(p.Name) > 120 {
		return invalidSpec("notification_policy.name", "通知策略名称不能为空且不能超过 120 个字符")
	}
	if !notificationReceiverPattern.MatchString(p.Receiver) {
		return invalidSpec("notification_policy.receiver", "通知 receiver 必须是稳定标识，不能填写 URL 或凭据")
	}
	return nil
}

type CreateNotificationPolicyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ServiceID   string `json:"service_id"`
	Receiver    string `json:"receiver"`
	Enabled     bool   `json:"enabled"`
}

type UpdateNotificationPolicyRequest = CreateNotificationPolicyRequest

type PolicyRepository interface {
	SavePolicy(ctx context.Context, expectedUpdatedAt time.Time, policy NotificationPolicy, auditEvent audit.Event) error
	GetPolicy(ctx context.Context, id string) (NotificationPolicy, error)
	ListPolicies(ctx context.Context, serviceID string, enabledOnly bool) ([]NotificationPolicy, error)
}

type PolicyResolver interface {
	ValidatePolicy(ctx context.Context, policyID string, serviceID string) error
}

type StorePolicyResolver struct{ repository PolicyRepository }

func NewStorePolicyResolver(repository PolicyRepository) StorePolicyResolver {
	return StorePolicyResolver{repository: repository}
}

func (r StorePolicyResolver) ValidatePolicy(ctx context.Context, policyID string, serviceID string) error {
	if r.repository == nil {
		return ErrUnavailable
	}
	policy, err := r.repository.GetPolicy(ctx, policyID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return invalidSpec("notification.policy_id", "通知策略不存在")
		}
		return err
	}
	if !policy.Enabled || (policy.ServiceID != "" && policy.ServiceID != serviceID) {
		return invalidSpec("notification.policy_id", "通知策略已停用或不属于当前服务")
	}
	return nil
}

type PolicyDependencies struct {
	Repository PolicyRepository
	Authorizer Authorizer
	Clock      func() time.Time
	NewID      func() string
}

type PolicyService struct {
	repository PolicyRepository
	authorizer Authorizer
	clock      func() time.Time
	newID      func() string
}

func NewPolicyService(deps PolicyDependencies) PolicyService {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	newID := deps.NewID
	if newID == nil {
		newID = func() string { return primitive.NewObjectID().Hex() }
	}
	authorizer := deps.Authorizer
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	return PolicyService{repository: deps.Repository, authorizer: authorizer, clock: clock, newID: newID}
}

func (s PolicyService) Create(ctx context.Context, subject platformrbac.Subject, request CreateNotificationPolicyRequest) (NotificationPolicy, error) {
	now := s.clock().UTC()
	actor := actorFromSubject(subject)
	policy := policyFromRequest(request).Normalize()
	policy.ID, policy.CreatedBy, policy.UpdatedBy, policy.CreatedAt, policy.UpdatedAt = s.newID(), actor, actor, now, now
	if err := policy.Validate(); err != nil {
		return NotificationPolicy{}, err
	}
	if !s.allowed(subject, policy.ServiceID, "manage") {
		return NotificationPolicy{}, ErrPermissionDenied
	}
	if s.repository == nil {
		return NotificationPolicy{}, ErrUnavailable
	}
	if err := s.repository.SavePolicy(ctx, time.Time{}, policy, policyAudit(s.newID(), actor, policy, "create", now)); err != nil {
		return NotificationPolicy{}, err
	}
	return policy, nil
}

func (s PolicyService) Update(ctx context.Context, subject platformrbac.Subject, id string, request UpdateNotificationPolicyRequest) (NotificationPolicy, error) {
	if s.repository == nil {
		return NotificationPolicy{}, ErrUnavailable
	}
	current, err := s.repository.GetPolicy(ctx, strings.TrimSpace(id))
	if err != nil {
		return NotificationPolicy{}, err
	}
	updated := policyFromRequest(request).Normalize()
	updated.ID, updated.CreatedBy, updated.CreatedAt = current.ID, current.CreatedBy, current.CreatedAt
	updated.UpdatedBy, updated.UpdatedAt = actorFromSubject(subject), s.clock().UTC()
	if err := updated.Validate(); err != nil {
		return NotificationPolicy{}, err
	}
	if updated.Receiver != current.Receiver {
		return NotificationPolicy{}, invalidSpec("notification_policy.receiver", "Receiver 标识创建后不可修改；请新建策略后更新规则关联")
	}
	if !s.allowed(subject, current.ServiceID, "manage") || !s.allowed(subject, updated.ServiceID, "manage") {
		return NotificationPolicy{}, ErrPermissionDenied
	}
	if err := s.repository.SavePolicy(ctx, current.UpdatedAt, updated, policyAudit(s.newID(), updated.UpdatedBy, updated, "update", updated.UpdatedAt)); err != nil {
		return NotificationPolicy{}, err
	}
	return updated, nil
}

func (s PolicyService) List(ctx context.Context, subject platformrbac.Subject, serviceID string, enabledOnly bool) ([]NotificationPolicy, error) {
	if s.repository == nil {
		return nil, ErrUnavailable
	}
	items, err := s.repository.ListPolicies(ctx, strings.TrimSpace(serviceID), enabledOnly)
	if err != nil {
		return nil, err
	}
	visible := make([]NotificationPolicy, 0, len(items))
	for _, item := range items {
		authorizationScope := item.ServiceID
		if authorizationScope == "" && strings.TrimSpace(serviceID) != "" {
			authorizationScope = strings.TrimSpace(serviceID)
		}
		if s.allowed(subject, authorizationScope, "read") {
			visible = append(visible, item)
		}
	}
	slices.SortFunc(visible, func(a, b NotificationPolicy) int { return strings.Compare(a.Name, b.Name) })
	return visible, nil
}

func (s PolicyService) allowed(subject platformrbac.Subject, serviceID string, action string) bool {
	if subject.ID == "" || subject.Type == "" {
		return false
	}
	scope := platformrbac.Scope{ServiceID: serviceID}
	if serviceID == "" {
		scope = platformrbac.Scope{Global: true}
	}
	return s.authorizer.Authorize(subject, platformrbac.Request{Resource: "alerts.notification-policy", Action: action, Scope: scope}).Allowed
}

func policyFromRequest(request CreateNotificationPolicyRequest) NotificationPolicy {
	return NotificationPolicy{
		Name: request.Name, Description: request.Description, ServiceID: request.ServiceID,
		Receiver: request.Receiver, Enabled: request.Enabled,
	}
}

func policyAudit(id string, actor Actor, policy NotificationPolicy, action string, now time.Time) audit.Event {
	return audit.Event{
		ID: id, Actor: audit.Actor{ID: actor.ID, Name: actor.Name},
		Resource: audit.Resource{Type: "alerts.notification-policy", Name: policy.ID},
		Action:   action, Scope: policy.ServiceID,
		RequestSummary: map[string]any{"name": policy.Name, "service_id": policy.ServiceID, "receiver": policy.Receiver, "enabled": policy.Enabled},
		Result:         "accepted", CreatedAt: now,
	}
}
