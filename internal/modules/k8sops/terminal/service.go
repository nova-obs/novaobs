package terminal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"novaapm/internal/platform/audit"
	platformrbac "novaapm/internal/platform/rbac"
)

var (
	ErrPermissionDenied = errors.New("permission_denied")
	ErrInvalidRequest   = errors.New("invalid_k8s_terminal_request")
	ErrCommandBlocked   = errors.New("k8s_terminal_command_blocked")
)

type Authorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type Auditor interface {
	Record(ctx context.Context, event audit.Event) (audit.Event, error)
}

type Executor interface {
	Exec(ctx context.Context, subject platformrbac.Subject, req ExecRequest, parsed ParsedCommand) (ExecResult, error)
}

type Service struct {
	authorizer Authorizer
	auditor    Auditor
	executor   Executor
	policy     CommandPolicy
}

func NewService(security ...any) Service {
	service := Service{authorizer: denyAuthorizer{}, auditor: noopAuditor{}, executor: dryRunExecutor{}, policy: DefaultCommandPolicy()}
	for _, item := range security {
		switch value := item.(type) {
		case Authorizer:
			if value != nil {
				service.authorizer = value
			}
		case Auditor:
			if value != nil {
				service.auditor = value
			}
		case Executor:
			if value != nil {
				service.executor = value
			}
		case CommandPolicy:
			service.policy = value
		}
	}
	return service
}

func (s Service) Exec(ctx context.Context, subject platformrbac.Subject, req ExecRequest) (ExecResult, error) {
	req = normalizeRequest(req)
	if req.ClusterID == "" || req.Namespace == "" || req.Command == "" {
		return ExecResult{}, ErrInvalidRequest
	}
	decision := s.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.terminal",
		Action:   "exec",
		Scope:    platformrbac.Scope{ClusterID: req.ClusterID, Namespace: req.Namespace},
	})
	if !decision.Allowed {
		return ExecResult{}, ErrPermissionDenied
	}
	parsed, blockReason, err := s.policy.Parse(req.Command)
	if err != nil {
		return ExecResult{}, err
	}
	if blockReason != "" {
		event, auditErr := s.record(ctx, subject, req, ParsedCommand{Verb: firstField(req.Command)}, "blocked", blockReason)
		if auditErr != nil {
			return ExecResult{}, auditErr
		}
		return ExecResult{
			Status:        "blocked",
			ClusterID:     req.ClusterID,
			Namespace:     req.Namespace,
			Command:       req.Command,
			Verb:          firstField(req.Command),
			Output:        blockReason,
			ExitCode:      126,
			AuditID:       event.ID,
			BlockedReason: blockReason,
			Mode:          "policy_guard",
		}, ErrCommandBlocked
	}
	result, err := s.executor.Exec(ctx, subject, req, parsed)
	if err != nil {
		return ExecResult{}, err
	}
	result.Output, result.OutputTruncated = s.policy.TrimOutput(result.Output)
	event, err := s.record(ctx, subject, req, parsed, "accepted", "")
	if err != nil {
		return ExecResult{}, err
	}
	result.Status = "accepted"
	result.ClusterID = req.ClusterID
	result.Namespace = req.Namespace
	result.Command = req.Command
	result.Verb = parsed.Verb
	result.Args = append([]string{}, parsed.Args...)
	result.AuditID = event.ID
	return result, nil
}

func normalizeRequest(req ExecRequest) ExecRequest {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Command = strings.TrimSpace(req.Command)
	return req
}

func firstField(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	if fields[0] == "kubectl" && len(fields) > 1 {
		return strings.ToLower(fields[1])
	}
	return strings.ToLower(fields[0])
}

func (s Service) record(ctx context.Context, subject platformrbac.Subject, req ExecRequest, parsed ParsedCommand, result string, reason string) (audit.Event, error) {
	return s.auditor.Record(ctx, audit.Event{
		Actor:        audit.Actor{ID: subject.ID, Name: subject.DisplayName},
		Resource:     audit.Resource{Type: "k8s.terminal", Name: req.Namespace},
		ResourceType: "k8s.terminal",
		ResourceName: req.Namespace,
		Action:       "exec",
		Scope:        fmt.Sprintf("cluster=%s namespace=%s", req.ClusterID, req.Namespace),
		Result:       result,
		RequestSummary: map[string]any{
			"cluster_id": req.ClusterID,
			"namespace":  req.Namespace,
			"command":    req.Command,
			"verb":       parsed.Verb,
			"args":       parsed.Args,
			"reason":     reason,
		},
		CreatedAt: time.Now().UTC(),
	})
}

type dryRunExecutor struct{}

func (dryRunExecutor) Exec(_ context.Context, subject platformrbac.Subject, req ExecRequest, parsed ParsedCommand) (ExecResult, error) {
	return ExecResult{
		Status:   "accepted",
		Verb:     parsed.Verb,
		Args:     append([]string{}, parsed.Args...),
		Output:   fmt.Sprintf("NovaAPM 已校验只读命令，等待接入受控 Kubernetes executor：kubectl %s", strings.Join(append([]string{parsed.Verb}, parsed.Args...), " ")),
		ExitCode: 0,
		Mode:     "dry_run",
	}, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type noopAuditor struct{}

func (noopAuditor) Record(ctx context.Context, event audit.Event) (audit.Event, error) {
	return event, nil
}
