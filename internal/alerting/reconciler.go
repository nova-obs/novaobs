package alerting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultDeploymentLease = 30 * time.Second

// ReconcileRepository 只暴露运行面调和所需能力，避免 controller 依赖用户侧 CRUD。
type ReconcileRepository interface {
	ClaimNextDeployment(ctx context.Context, workerID string, runtimeID string, now time.Time, lease time.Duration) (Deployment, error)
	ListRuntimeRules(ctx context.Context, runtimeID string) ([]Rule, error)
	CompleteDeployment(ctx context.Context, deployment Deployment, artifact Artifact) error
}

type ArtifactPublisher interface {
	Publish(ctx context.Context, artifact Artifact) error
}

type ReconcilerDependencies struct {
	Repository ReconcileRepository
	Publisher  ArtifactPublisher
	WorkerID   string
	RuntimeID  string
	Clock      func() time.Time
	Lease      time.Duration
}

type Reconciler struct {
	repository ReconcileRepository
	publisher  ArtifactPublisher
	workerID   string
	runtimeID  string
	clock      func() time.Time
	lease      time.Duration
}

func NewReconciler(deps ReconcilerDependencies) Reconciler {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	lease := deps.Lease
	if lease <= 0 {
		lease = defaultDeploymentLease
	}
	return Reconciler{repository: deps.Repository, publisher: deps.Publisher, workerID: strings.TrimSpace(deps.WorkerID), runtimeID: strings.TrimSpace(deps.RuntimeID), clock: clock, lease: lease}
}

// ReconcileOnce 最多处理一个 Deployment；返回 false 表示当前没有待处理任务。
func (r Reconciler) ReconcileOnce(ctx context.Context) (bool, error) {
	if r.repository == nil || r.publisher == nil || r.workerID == "" || r.runtimeID == "" {
		return false, ErrUnavailable
	}
	now := r.clock().UTC()
	deployment, err := r.repository.ClaimNextDeployment(ctx, r.workerID, r.runtimeID, now, r.lease)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	deployment.Attempt++
	deployment.UpdatedAt = now
	rules, err := r.repository.ListRuntimeRules(ctx, deployment.RuntimeID)
	if err != nil {
		return true, r.fail(ctx, deployment, Artifact{}, fmt.Errorf("读取 Runtime 规则失败: %w", err))
	}
	artifact, err := CompileVmalertArtifact(deployment.RuntimeID, rules, now)
	if err != nil {
		return true, r.fail(ctx, deployment, Artifact{}, err)
	}
	deployment.Status = DeploymentStatusPublishing
	deployment.DesiredArtifactHash = artifact.Hash
	if err := r.publisher.Publish(ctx, artifact); err != nil {
		return true, r.fail(ctx, deployment, artifact, err)
	}
	deployment.Status = DeploymentStatusApplied
	deployment.AppliedArtifactHash = artifact.Hash
	deployment.LastError = ""
	deployment.LeaseOwner = ""
	deployment.LeaseExpiresAt = time.Time{}
	deployment.NextAttemptAt = time.Time{}
	deployment.UpdatedAt = now
	if err := r.repository.CompleteDeployment(ctx, deployment, artifact); err != nil {
		return true, err
	}
	return true, nil
}

func (r Reconciler) fail(ctx context.Context, deployment Deployment, artifact Artifact, cause error) error {
	deployment.Status = DeploymentStatusFailed
	deployment.LastError = boundedError(cause)
	deployment.LeaseOwner = ""
	deployment.LeaseExpiresAt = time.Time{}
	deployment.UpdatedAt = r.clock().UTC()
	deployment.NextAttemptAt = deployment.UpdatedAt.Add(retryDelay(deployment.Attempt))
	if err := r.repository.CompleteDeployment(ctx, deployment, artifact); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(1<<(attempt-1)) * 5 * time.Second
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
