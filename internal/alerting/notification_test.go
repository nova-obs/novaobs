package alerting

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestNotificationPolicyValidateRequiresStableReceiver(t *testing.T) {
	policy := validNotificationPolicy()
	require.NoError(t, policy.Validate())
	policy.Receiver = "https://secret.example/hook"
	require.ErrorIs(t, policy.Validate(), ErrInvalidSpec)
}

func TestPolicyServiceCreateAndUpdateAreAudited(t *testing.T) {
	repository := &fakePolicyRepository{}
	service := NewPolicyService(PolicyDependencies{
		Repository: repository, Authorizer: allowAuthorizer{}, Clock: fixedClock, NewID: sequentialIDs(),
	})

	created, err := service.Create(context.Background(), testSubject(), CreateNotificationPolicyRequest{
		Name: "支付值班", Receiver: "pay-oncall", Enabled: true,
	})
	require.NoError(t, err)
	require.Equal(t, "pay-oncall", created.Receiver)
	require.Len(t, repository.audits, 1)

	updated, err := service.Update(context.Background(), testSubject(), created.ID, UpdateNotificationPolicyRequest{
		Name: "支付值班（主）", Receiver: "pay-oncall", Enabled: true,
	})
	require.NoError(t, err)
	require.Equal(t, "支付值班（主）", updated.Name)
	require.Len(t, repository.audits, 2)

	_, err = service.Update(context.Background(), testSubject(), created.ID, UpdateNotificationPolicyRequest{
		Name: "支付值班", Receiver: "another-receiver", Enabled: true,
	})
	require.ErrorIs(t, err, ErrInvalidSpec)
}

func TestStorePolicyResolverRejectsDisabledOrCrossServicePolicy(t *testing.T) {
	repository := &fakePolicyRepository{policy: validNotificationPolicy()}
	resolver := StorePolicyResolver{repository: repository}
	require.NoError(t, resolver.ValidatePolicy(context.Background(), repository.policy.ID, "service-a"))

	repository.policy.ServiceID = "service-b"
	require.ErrorIs(t, resolver.ValidatePolicy(context.Background(), repository.policy.ID, "service-a"), ErrInvalidSpec)
	repository.policy.ServiceID = ""
	repository.policy.Enabled = false
	require.ErrorIs(t, resolver.ValidatePolicy(context.Background(), repository.policy.ID, "service-a"), ErrInvalidSpec)
}

func TestPolicyServiceFailsClosedWithoutPermission(t *testing.T) {
	repository := &fakePolicyRepository{}
	service := NewPolicyService(PolicyDependencies{Repository: repository, Authorizer: policyDenyAuthorizer{}})
	_, err := service.Create(context.Background(), testSubject(), CreateNotificationPolicyRequest{
		Name: "支付值班", Receiver: "pay-oncall", Enabled: true,
	})
	require.ErrorIs(t, err, ErrPermissionDenied)
	require.Empty(t, repository.audits)
}

func validNotificationPolicy() NotificationPolicy {
	return NotificationPolicy{
		ID: "policy-a", Name: "支付值班", Receiver: "pay-oncall", Enabled: true,
	}
}

type fakePolicyRepository struct {
	policy NotificationPolicy
	audits []audit.Event
}

func (r *fakePolicyRepository) SavePolicy(_ context.Context, _ time.Time, policy NotificationPolicy, auditEvent audit.Event) error {
	r.policy = policy
	r.audits = append(r.audits, auditEvent)
	return nil
}

func (r *fakePolicyRepository) GetPolicy(context.Context, string) (NotificationPolicy, error) {
	if r.policy.ID == "" {
		return NotificationPolicy{}, ErrNotFound
	}
	return r.policy, nil
}

func (r *fakePolicyRepository) ListPolicies(context.Context, string, bool) ([]NotificationPolicy, error) {
	if r.policy.ID == "" {
		return nil, nil
	}
	return []NotificationPolicy{r.policy}, nil
}

type policyDenyAuthorizer struct{}

func (policyDenyAuthorizer) Authorize(platformrbac.Subject, platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false}
}
