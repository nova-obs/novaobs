package metrics

import (
	"strconv"
	"strings"
)

func buildHandoffArtifacts(sourceKind string, environmentID string, remoteWriteURL string) []HandoffArtifact {
	label := EnvironmentIdentityLabel + "=" + environmentID
	vmagentArgs := strings.Join([]string{
		"-remoteWrite.url=" + remoteWriteURL,
		"-remoteWrite.label=" + label,
	}, "\n")
	prometheusPatch := "global:\n  external_labels:\n    " + EnvironmentIdentityLabel + ": " + environmentID +
		"\nremote_write:\n  - url: " + strconv.Quote(remoteWriteURL) + "\n"
	artifacts := []HandoffArtifact{
		{Kind: "vmagent_args", Content: vmagentArgs, Note: "合并到现有 vmagent 参数；保留现有抓取与鉴权配置。"},
		{Kind: "prometheus_patch", Content: prometheusPatch, Note: "合并到现有 Prometheus 配置，不要覆盖已有 global 或 remote_write 项。"},
	}
	if sourceKind == SourceKindKubernetesInfra {
		operatorPatch := "spec:\n  externalLabels:\n    " + EnvironmentIdentityLabel + ": " + environmentID +
			"\n  remoteWrite:\n    - url: " + strconv.Quote(remoteWriteURL) + "\n"
		artifacts = append([]HandoffArtifact{{Kind: "vmoperator_patch", Content: operatorPatch, Note: "合并到该集群现有 VMAgent；采集对象仍由 VMServiceScrape、VMPodScrape 等原生资源管理。"}}, artifacts...)
	}
	if sourceKind == SourceKindLogDerived {
		return []HandoffArtifact{{Kind: "vmalert_args", Content: "-remoteWrite.url=" + remoteWriteURL + "\n-external.label=" + label, Note: "合并到负责 Logs-to-Metrics recording rules 的 vmalert；日志查询与规则仍由告警域管理。"}}
	}
	return artifacts
}
