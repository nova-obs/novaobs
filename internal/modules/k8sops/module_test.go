package k8sops

import (
	"context"
	"testing"

	"novaobs/internal/modules/k8sops/terminal"
	"novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestModuleUsesDryRunTerminalByDefault(t *testing.T) {
	module := NewModuleWithSecurity(allowAllAuthorizer{}, nil, nil)

	result, err := module.Terminal.Exec(context.Background(), rbac.Subject{ID: "user-1", Type: "user"}, terminal.ExecRequest{
		ClusterID: "prod",
		Namespace: "orders",
		Command:   "get pods",
	})

	require.NoError(t, err)
	require.Equal(t, "dry_run", result.Mode)
}

func TestModuleUsesExplicitTerminalExecutor(t *testing.T) {
	module := NewModuleWithSecurity(allowAllAuthorizer{}, nil, nil, moduleTerminalExecutor{})

	result, err := module.Terminal.Exec(context.Background(), rbac.Subject{ID: "user-1", Type: "user"}, terminal.ExecRequest{
		ClusterID: "prod",
		Namespace: "orders",
		Command:   "get pods",
	})

	require.NoError(t, err)
	require.Equal(t, "custom", result.Mode)
	require.Equal(t, "custom executor", result.Output)
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(subject rbac.Subject, req rbac.Request) rbac.Decision {
	return rbac.Decision{Allowed: true}
}

type moduleTerminalExecutor struct{}

func (moduleTerminalExecutor) Exec(ctx context.Context, subject rbac.Subject, req terminal.ExecRequest, parsed terminal.ParsedCommand) (terminal.ExecResult, error) {
	return terminal.ExecResult{Output: "custom executor", Mode: "custom"}, nil
}
