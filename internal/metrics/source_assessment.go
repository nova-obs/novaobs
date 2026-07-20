package metrics

import (
	"context"
	"sort"
	"strings"

	k8sresource "novaapm/internal/modules/k8sops/resource"
	platformrbac "novaapm/internal/platform/rbac"
)

func (s IntegrationService) AssessSource(ctx context.Context, subject platformrbac.Subject, sourceID string) (SourceAssessment, error) {
	source, err := s.repository.GetSourceAccess(ctx, strings.TrimSpace(sourceID))
	if err != nil {
		return SourceAssessment{}, err
	}
	integration, err := s.repository.GetIntegration(ctx, source.IntegrationID)
	if err != nil {
		return SourceAssessment{}, err
	}
	if !s.allowed(subject, integration.EnvironmentID, "metrics.integration", "read") {
		return SourceAssessment{}, ErrPermissionDenied
	}
	bindings, err := s.environments.ListResourceBindings(ctx, integration.EnvironmentID)
	if err != nil {
		return SourceAssessment{}, err
	}
	resourceRef := ""
	for _, binding := range bindings {
		if binding.ID == source.ResourceBindingID {
			resourceRef = binding.ResourceRef
			break
		}
	}
	now := s.now()
	if source.SourceKind != SourceKindKubernetesInfra {
		return SourceAssessment{SourceAccessID: source.ID, ResourceRef: resourceRef, Status: HealthUnknown, RecommendedMode: CollectionModeExternal, Message: "主机环境没有平台执行通道，请复用现有 Prometheus、vmagent 或 node_exporter 交付链路", AssessedAt: now}, nil
	}
	if s.k8sResources == nil {
		return SourceAssessment{}, ErrManagedCollectorUnsupported
	}
	resources, err := s.k8sResources.List(ctx, k8sresource.ListFilter{ClusterID: resourceRef, Page: 1, PageSize: 2000})
	if err != nil {
		return SourceAssessment{}, err
	}
	collector, signals := detectKubernetesMetricsStack(resources)
	assessment := SourceAssessment{SourceAccessID: source.ID, ResourceRef: resourceRef, DetectedCollector: collector, DetectedSignals: signals, AssessedAt: now}
	if collector != "" {
		assessment.Status, assessment.RecommendedMode, assessment.Message = HealthHealthy, CollectionModeExternal, "已发现现有采集器，推荐生成 remoteWrite 片段并由运维合并"
	} else {
		assessment.Status, assessment.RecommendedMode, assessment.Message = HealthDegraded, CollectionModeManaged, "未发现 Prometheus 或 vmagent，可预览并部署平台受管 vmagent"
	}
	return assessment, nil
}

func detectKubernetesMetricsStack(resources []k8sresource.ResourceSummary) (string, []string) {
	collector := ""
	signalSet := map[string]struct{}{}
	for _, resource := range resources {
		name := strings.ToLower(resource.Identity.Name)
		kind := strings.ToLower(resource.Identity.Kind)
		if kind == "deployment" || kind == "statefulset" || kind == "vmagent" || kind == "prometheus" {
			switch {
			case kind == "vmagent" || strings.Contains(name, "vmagent"):
				collector = "vmagent"
			case collector == "" && (kind == "prometheus" || strings.Contains(name, "prometheus")):
				collector = "prometheus"
			}
		}
		switch {
		case strings.Contains(name, "node-exporter"):
			signalSet["node-exporter"] = struct{}{}
		case strings.Contains(name, "kube-state-metrics"):
			signalSet["kube-state-metrics"] = struct{}{}
		case strings.Contains(name, "coredns") || strings.Contains(name, "kube-dns"):
			signalSet["coredns"] = struct{}{}
		}
	}
	signals := make([]string, 0, len(signalSet))
	for signal := range signalSet {
		signals = append(signals, signal)
	}
	sort.Strings(signals)
	return collector, signals
}
