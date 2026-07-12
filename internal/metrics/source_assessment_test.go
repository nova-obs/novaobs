package metrics

import (
	"testing"

	k8sresource "novaapm/internal/modules/k8sops/resource"

	"github.com/stretchr/testify/require"
)

func TestDetectKubernetesMetricsStackPrefersExistingVMAgent(t *testing.T) {
	collector, signals := detectKubernetesMetricsStack([]k8sresource.ResourceSummary{
		{Identity: k8sresource.Identity{Kind: "Deployment", Name: "vmagent-prod"}},
		{Identity: k8sresource.Identity{Kind: "DaemonSet", Name: "node-exporter"}},
		{Identity: k8sresource.Identity{Kind: "Deployment", Name: "kube-state-metrics"}},
	})

	require.Equal(t, "vmagent", collector)
	require.Equal(t, []string{"kube-state-metrics", "node-exporter"}, signals)
}

func TestDetectKubernetesMetricsStackDoesNotMistakeExporterForCollector(t *testing.T) {
	collector, signals := detectKubernetesMetricsStack([]k8sresource.ResourceSummary{{Identity: k8sresource.Identity{Kind: "DaemonSet", Name: "node-exporter"}}})

	require.Empty(t, collector)
	require.Equal(t, []string{"node-exporter"}, signals)
}

func TestDetectKubernetesMetricsStackRecognizesOperatorCustomResources(t *testing.T) {
	collector, _ := detectKubernetesMetricsStack([]k8sresource.ResourceSummary{{Identity: k8sresource.Identity{Kind: "VMAgent", Name: "cluster-agent"}}})

	require.Equal(t, "vmagent", collector)
}
