package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultKubectlBinary = "kubectl"
	defaultKubectlMode   = "kubectl"
)

type KubeconfigProvider interface {
	Kubeconfig(ctx context.Context, clusterID string) ([]byte, error)
}

type KubectlExecutorConfig struct {
	BinaryPath string
	Timeout    time.Duration
	TempDir    string
}

type KubectlExecutor struct {
	provider KubeconfigProvider
	config   KubectlExecutorConfig
}

func NewKubectlExecutor(provider KubeconfigProvider, config KubectlExecutorConfig) KubectlExecutor {
	if config.BinaryPath == "" {
		config.BinaryPath = defaultKubectlBinary
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.TempDir == "" {
		config.TempDir = os.TempDir()
	}
	return KubectlExecutor{provider: provider, config: config}
}

func (e KubectlExecutor) Exec(ctx context.Context, req ExecRequest, parsed ParsedCommand) (ExecResult, error) {
	if e.provider == nil {
		return ExecResult{}, errors.New("kubeconfig provider is required")
	}
	kubeconfig, err := e.provider.Kubeconfig(ctx, req.ClusterID)
	if err != nil {
		return ExecResult{}, fmt.Errorf("load kubeconfig for cluster %s: %w", req.ClusterID, err)
	}
	if len(strings.TrimSpace(string(kubeconfig))) == 0 {
		return ExecResult{}, fmt.Errorf("load kubeconfig for cluster %s: empty kubeconfig", req.ClusterID)
	}
	if err := os.MkdirAll(e.config.TempDir, 0o700); err != nil {
		return ExecResult{}, fmt.Errorf("create kubectl temp dir: %w", err)
	}
	kubeconfigPath, cleanup, err := writeTemporaryKubeconfig(e.config.TempDir, req.ClusterID, kubeconfig)
	if err != nil {
		return ExecResult{}, err
	}
	defer cleanup()

	commandCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()

	args := append([]string{parsed.Verb}, parsed.Args...)
	cmd := exec.CommandContext(commandCtx, e.config.BinaryPath, args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	output, err := cmd.CombinedOutput()
	if commandCtx.Err() == context.DeadlineExceeded {
		return ExecResult{Output: fmt.Sprintf("kubectl command timed out after %s", e.config.Timeout), ExitCode: 124, Mode: defaultKubectlMode}, nil
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ExecResult{Output: string(output), ExitCode: exitErr.ExitCode(), Mode: defaultKubectlMode}, nil
		}
		return ExecResult{}, fmt.Errorf("execute kubectl: %w", err)
	}
	return ExecResult{Output: string(output), ExitCode: 0, Mode: defaultKubectlMode}, nil
}

func writeTemporaryKubeconfig(tempDir string, clusterID string, kubeconfig []byte) (string, func(), error) {
	file, err := os.CreateTemp(tempDir, "novaobs-kubeconfig-"+safeFilename(clusterID)+"-*.yaml")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary kubeconfig: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = os.Remove(path)
	}
	if _, err := file.Write(kubeconfig); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write temporary kubeconfig: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temporary kubeconfig: %w", err)
	}
	return path, cleanup, nil
}

func safeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "cluster"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "..", "-")
	return filepath.Base(replacer.Replace(value))
}
