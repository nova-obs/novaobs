package alerting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconcilerPublishesCurrentRuntimeArtifactAndMarksDeploymentApplied(t *testing.T) {
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	rule := Rule{ID: "rule-a", Spec: validRuleSpec(), State: RuleStateEnabled, CurrentUpdateID: "update-a"}
	deployment := Deployment{ID: "deployment-a", RuleID: rule.ID, UpdateID: rule.CurrentUpdateID, RuntimeID: rule.Spec.Scope.EndpointID, Status: DeploymentStatusPending}
	repository := &fakeReconcileRepository{claimed: deployment, rules: []Rule{rule}}
	publisher := &fakeArtifactPublisher{}
	reconciler := NewReconciler(ReconcilerDependencies{
		Repository: repository,
		Publisher:  publisher,
		WorkerID:   "worker-a",
		RuntimeID:  deployment.RuntimeID,
		Clock:      func() time.Time { return now },
	})

	worked, err := reconciler.ReconcileOnce(context.Background())

	require.NoError(t, err)
	require.True(t, worked)
	require.Equal(t, deployment.RuntimeID, publisher.artifact.RuntimeID)
	require.Contains(t, publisher.artifact.Content, "rule-a")
	require.Equal(t, DeploymentStatusApplied, repository.completed.Status)
	require.Equal(t, publisher.artifact.Hash, repository.completed.AppliedArtifactHash)
	require.Equal(t, publisher.artifact.Hash, repository.savedArtifact.Hash)
}

func TestReconcilerRecordsPublishingFailureWithoutLosingDeployment(t *testing.T) {
	rule := Rule{ID: "rule-a", Spec: validRuleSpec(), State: RuleStateEnabled, CurrentUpdateID: "update-a"}
	deployment := Deployment{ID: "deployment-a", RuleID: rule.ID, UpdateID: rule.CurrentUpdateID, RuntimeID: rule.Spec.Scope.EndpointID, Status: DeploymentStatusPending}
	repository := &fakeReconcileRepository{claimed: deployment, rules: []Rule{rule}}
	publisher := &fakeArtifactPublisher{err: errors.New("reload failed")}
	reconciler := NewReconciler(ReconcilerDependencies{Repository: repository, Publisher: publisher, WorkerID: "worker-a", RuntimeID: deployment.RuntimeID})

	worked, err := reconciler.ReconcileOnce(context.Background())

	require.Error(t, err)
	require.True(t, worked)
	require.Equal(t, DeploymentStatusFailed, repository.completed.Status)
	require.Equal(t, 1, repository.completed.Attempt)
	require.NotEmpty(t, repository.completed.LastError)
}

func TestReconcilerIncrementsAttemptWhenCompilationFails(t *testing.T) {
	spec := validRuleSpec()
	spec.Notification.AlertmanagerReceiver = ""
	rule := Rule{ID: "rule-a", Spec: spec, State: RuleStateEnabled, CurrentUpdateID: "update-a"}
	deployment := Deployment{ID: "deployment-a", RuleID: rule.ID, UpdateID: rule.CurrentUpdateID, RuntimeID: rule.Spec.Scope.EndpointID, Status: DeploymentStatusFailed, Attempt: 2}
	repository := &fakeReconcileRepository{claimed: deployment, rules: []Rule{rule}}
	reconciler := NewReconciler(ReconcilerDependencies{Repository: repository, Publisher: &fakeArtifactPublisher{}, WorkerID: "worker-a", RuntimeID: deployment.RuntimeID})

	worked, err := reconciler.ReconcileOnce(context.Background())

	require.Error(t, err)
	require.True(t, worked)
	require.Equal(t, DeploymentStatusFailed, repository.completed.Status)
	require.Equal(t, 3, repository.completed.Attempt)
}

type fakeReconcileRepository struct {
	claimed       Deployment
	rules         []Rule
	completed     Deployment
	savedArtifact Artifact
}

func (r *fakeReconcileRepository) ClaimNextDeployment(context.Context, string, string, time.Time, time.Duration) (Deployment, error) {
	if r.claimed.ID == "" {
		return Deployment{}, ErrNotFound
	}
	claimed := r.claimed
	r.claimed = Deployment{}
	return claimed, nil
}

func (r *fakeReconcileRepository) ListRuntimeRules(context.Context, string) ([]Rule, error) {
	return append([]Rule(nil), r.rules...), nil
}

func (r *fakeReconcileRepository) SaveArtifact(context.Context, Artifact) error {
	return nil
}

func (r *fakeReconcileRepository) CompleteDeployment(_ context.Context, deployment Deployment, artifact Artifact) error {
	r.completed = deployment
	r.savedArtifact = artifact
	return nil
}

type fakeArtifactPublisher struct {
	artifact Artifact
	err      error
}

func (p *fakeArtifactPublisher) Publish(_ context.Context, artifact Artifact) error {
	p.artifact = artifact
	return p.err
}
