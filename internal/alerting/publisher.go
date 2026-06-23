package alerting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FilePublisherConfig struct {
	RulesDirectory string
	ReloadURL      string
	RulesStatusURL string
	Client         HTTPDoer
}

// FileArtifactPublisher 通过同目录 rename 原子替换规则文件，再触发 vmalert reload。
// 每个 Runtime 使用稳定的摘要文件名，避免 Runtime ID 进入文件路径。
type FileArtifactPublisher struct {
	directory      string
	reloadURL      string
	rulesStatusURL string
	client         HTTPDoer
}

func NewFileArtifactPublisher(config FilePublisherConfig) (FileArtifactPublisher, error) {
	directory := strings.TrimSpace(config.RulesDirectory)
	if directory == "" {
		return FileArtifactPublisher{}, fmt.Errorf("规则目录不能为空")
	}
	reloadURL, err := validateReloadURL(config.ReloadURL)
	if err != nil {
		return FileArtifactPublisher{}, err
	}
	rulesStatusURL, err := validateReloadURL(config.RulesStatusURL)
	if err != nil {
		return FileArtifactPublisher{}, fmt.Errorf("vmalert rules URL 无效: %w", err)
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return FileArtifactPublisher{directory: directory, reloadURL: reloadURL, rulesStatusURL: rulesStatusURL, client: client}, nil
}

func (p FileArtifactPublisher) Publish(ctx context.Context, artifact Artifact) error {
	if artifact.RuntimeID == "" || artifact.Hash == "" || artifact.Content == "" {
		return fmt.Errorf("规则产物不完整")
	}
	if err := os.MkdirAll(p.directory, 0o750); err != nil {
		return fmt.Errorf("创建规则目录失败: %w", err)
	}
	filename := runtimeArtifactFilename(artifact.RuntimeID)
	target := filepath.Join(p.directory, filename)
	temporary, err := os.CreateTemp(p.directory, ".novaobs-vmalert-*.tmp")
	if err != nil {
		return fmt.Errorf("创建规则临时文件失败: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置规则文件权限失败: %w", err)
	}
	if _, err := temporary.WriteString(artifact.Content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入规则产物失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步规则产物失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭规则产物失败: %w", err)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return fmt.Errorf("替换规则产物失败: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.reloadURL, nil)
	if err != nil {
		return fmt.Errorf("创建 vmalert reload 请求失败: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("vmalert reload 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("vmalert reload 返回 HTTP %d", response.StatusCode)
	}
	return p.verifyApplied(ctx, artifact)
}

func (p FileArtifactPublisher) verifyApplied(ctx context.Context, artifact Artifact) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.rulesStatusURL, nil)
	if err != nil {
		return fmt.Errorf("创建 vmalert 规则回读请求失败: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("回读 vmalert 规则失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("回读 vmalert 规则返回 HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("读取 vmalert 规则状态失败: %w", err)
	}
	var status any
	if err := json.Unmarshal(payload, &status); err != nil {
		return fmt.Errorf("vmalert 规则回读响应不是有效 JSON")
	}
	appliedRuleIDs := map[string]bool{}
	collectAppliedRuleIDs(status, artifact.RuntimeID, appliedRuleIDs)
	expectedRuleIDs := map[string]bool{}
	for _, ruleID := range artifact.RuleIDs {
		expectedRuleIDs[ruleID] = true
		if !appliedRuleIDs[ruleID] {
			return fmt.Errorf("vmalert 规则回读缺少规则 %s", ruleID)
		}
	}
	for ruleID := range appliedRuleIDs {
		if !expectedRuleIDs[ruleID] {
			return fmt.Errorf("vmalert 规则回读存在未期望规则 %s", ruleID)
		}
	}
	return nil
}

func collectAppliedRuleIDs(value any, runtimeID string, target map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		if ruleID, ok := typed["novaobs_rule_id"].(string); ok {
			if currentRuntimeID, ok := typed["novaobs_runtime_id"].(string); ok && currentRuntimeID == runtimeID {
				target[ruleID] = true
			}
		}
		for _, child := range typed {
			collectAppliedRuleIDs(child, runtimeID, target)
		}
	case []any:
		for _, child := range typed {
			collectAppliedRuleIDs(child, runtimeID, target)
		}
	}
}

func validateReloadURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("vmalert reload URL 无效")
	}
	return parsed.String(), nil
}

func runtimeArtifactFilename(runtimeID string) string {
	sum := sha256.Sum256([]byte(runtimeID))
	return "runtime-" + hex.EncodeToString(sum[:8]) + ".yaml"
}
