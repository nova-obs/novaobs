package terminal

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestKubectlExecutorRunsStructuredArgsWithTemporaryKubeconfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake kubectl is unix-only")
	}
	kubectlPath := writeFakeKubectl(t, `#!/bin/sh
echo "KUBECONFIG=$KUBECONFIG"
printf 'ARGS=%s\n' "$*"
cat "$KUBECONFIG"
`)
	executor := NewKubectlExecutor(staticKubeconfigProvider{"prod": []byte("apiVersion: v1\nkind: Config\n")}, KubectlExecutorConfig{
		BinaryPath: kubectlPath,
		Timeout:    5 * time.Second,
		TempDir:    t.TempDir(),
	})

	result, err := executor.Exec(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, ExecRequest{ClusterID: "prod", Namespace: "orders"}, ParsedCommand{Verb: "get", Args: []string{"pods", "-n", "orders"}})

	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, "kubectl", result.Mode)
	require.Contains(t, result.Output, "ARGS=get pods -n orders")
	require.Contains(t, result.Output, "apiVersion: v1")
	require.Contains(t, result.Output, "KUBECONFIG=")
}

func TestKubectlExecutorMapsNonZeroExitCodeAndMergesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake kubectl is unix-only")
	}
	kubectlPath := writeFakeKubectl(t, `#!/bin/sh
echo "not found" >&2
exit 7
`)
	executor := NewKubectlExecutor(staticKubeconfigProvider{"prod": []byte("apiVersion: v1\n")}, KubectlExecutorConfig{
		BinaryPath: kubectlPath,
		Timeout:    5 * time.Second,
		TempDir:    t.TempDir(),
	})

	result, err := executor.Exec(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, ExecRequest{ClusterID: "prod", Namespace: "orders"}, ParsedCommand{Verb: "get", Args: []string{"pods"}})

	require.NoError(t, err)
	require.Equal(t, 7, result.ExitCode)
	require.Contains(t, result.Output, "not found")
}

func TestKubectlExecutorTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake kubectl is unix-only")
	}
	kubectlPath := writeFakeKubectl(t, `#!/bin/sh
sleep 1
echo "late"
`)
	executor := NewKubectlExecutor(staticKubeconfigProvider{"prod": []byte("apiVersion: v1\n")}, KubectlExecutorConfig{
		BinaryPath: kubectlPath,
		Timeout:    10 * time.Millisecond,
		TempDir:    t.TempDir(),
	})

	result, err := executor.Exec(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, ExecRequest{ClusterID: "prod", Namespace: "orders"}, ParsedCommand{Verb: "get", Args: []string{"pods"}})

	require.NoError(t, err)
	require.Equal(t, 124, result.ExitCode)
	require.Contains(t, result.Output, "timed out")
}

func TestKubectlExecutorRejectsMissingKubeconfig(t *testing.T) {
	executor := NewKubectlExecutor(staticKubeconfigProvider{}, KubectlExecutorConfig{BinaryPath: "kubectl", Timeout: time.Second, TempDir: t.TempDir()})

	_, err := executor.Exec(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"}, ExecRequest{ClusterID: "missing", Namespace: "orders"}, ParsedCommand{Verb: "get", Args: []string{"pods"}})

	require.Error(t, err)
	require.Contains(t, err.Error(), "kubeconfig")
}

func writeFakeKubectl(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}

type staticKubeconfigProvider map[string][]byte

func (p staticKubeconfigProvider) Kubeconfig(ctx context.Context, subject platformrbac.Subject, clusterID string) ([]byte, error) {
	value, ok := p[clusterID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(strings.TrimSpace(string(value)) + "\n"), nil
}
