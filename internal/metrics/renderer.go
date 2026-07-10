package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	platformimages "novaapm/internal/platform/images"
	"novaapm/internal/servicecatalog"

	"gopkg.in/yaml.v3"
)

type metricRuntimeRoute struct {
	Route   MetricRoute
	Service servicecatalog.Service
}

type renderedMetricsRuntime struct {
	Name         string
	ConfigYAML   string
	ConfigHash   string
	ManifestYAML string
	ManifestHash string
}

func renderMetricsCollectorRuntime(clusterID string, runtimeNamespace string, remoteWriteURL string, routes []metricRuntimeRoute) (renderedMetricsRuntime, error) {
	clusterID = strings.TrimSpace(clusterID)
	runtimeNamespace = normalizeMetricsCollectorNamespace(runtimeNamespace)
	remoteWriteURL = strings.TrimSpace(remoteWriteURL)
	if clusterID == "" || remoteWriteURL == "" {
		return renderedMetricsRuntime{}, fmt.Errorf("指标采集运行时缺少集群或 Remote Write 地址")
	}
	if len(routes) == 0 {
		return renderedMetricsRuntime{}, fmt.Errorf("指标采集运行时没有可渲染路由")
	}
	sort.SliceStable(routes, func(left, right int) bool { return routes[left].Route.ID < routes[right].Route.ID })
	scrapeConfigs := make([]map[string]any, 0, len(routes))
	for _, item := range routes {
		if item.Route.Status == MetricRouteStatusDisabled {
			continue
		}
		scrapeConfigs = append(scrapeConfigs, metricScrapeConfig(item))
	}
	config, err := yaml.Marshal(map[string]any{
		"global":         map[string]any{"scrape_interval": "30s", "scrape_timeout": "10s"},
		"scrape_configs": scrapeConfigs,
	})
	if err != nil {
		return renderedMetricsRuntime{}, err
	}
	if len(config) > 900*1024 {
		return renderedMetricsRuntime{}, fmt.Errorf("指标采集配置超过 900KiB，请拆分 VictoriaMetrics 端点或产品运行时")
	}
	configYAML := string(config)
	configHash := metricDigest(configYAML + "\x00remote_write_url=" + remoteWriteURL)
	groupSeed := clusterID + "\x00" + runtimeNamespace + "\x00" + routes[0].Service.ProductID + "\x00" + routes[0].Route.EndpointID
	name := "novaapm-vmagent-" + metricDigest(groupSeed)[:12]
	manifest, err := renderMetricsRuntimeManifest(name, runtimeNamespace, remoteWriteURL, configYAML, configHash, metricRuntimeNamespaces(routes))
	if err != nil {
		return renderedMetricsRuntime{}, err
	}
	return renderedMetricsRuntime{
		Name: name, ConfigYAML: configYAML, ConfigHash: configHash,
		ManifestYAML: manifest, ManifestHash: metricDigest(manifest),
	}, nil
}

func metricScrapeConfig(item metricRuntimeRoute) map[string]any {
	portLabel := "__meta_kubernetes_endpointslice_port_name"
	if _, err := strconv.Atoi(item.Route.Port); err == nil {
		portLabel = "__meta_kubernetes_endpointslice_port"
	}
	relabels := []map[string]any{
		{"action": "keep", "source_labels": []string{"__meta_kubernetes_service_name"}, "regex": regexp.QuoteMeta(item.Route.K8sServiceName)},
		{"action": "keep", "source_labels": []string{portLabel}, "regex": regexp.QuoteMeta(item.Route.Port)},
		{"action": "keep", "source_labels": []string{"__meta_kubernetes_endpointslice_endpoint_conditions_ready"}, "regex": "true"},
		{"action": "replace", "target_label": "service_name", "replacement": item.Service.Name},
		{"action": "replace", "target_label": "novaapm_service_id", "replacement": item.Service.ID},
		{"action": "replace", "target_label": "novaapm_product_id", "replacement": item.Service.ProductID},
		{"action": "replace", "target_label": "novaapm_route_id", "replacement": item.Route.ID},
		{"action": "replace", "target_label": "k8s_cluster_id", "replacement": item.Route.ClusterID},
		{"action": "replace", "target_label": "k8s_namespace_name", "replacement": item.Route.Namespace},
	}
	return map[string]any{
		"job_name":        "novaapm_" + item.Route.ID,
		"scrape_interval": item.Route.ScrapeInterval,
		"scrape_timeout":  item.Route.ScrapeTimeout,
		"scheme":          item.Route.Scheme,
		"metrics_path":    item.Route.MetricsPath,
		"honor_labels":    false,
		"sample_limit":    50000,
		"kubernetes_sd_configs": []map[string]any{{
			"role":       "endpointslice",
			"namespaces": map[string]any{"names": []string{item.Route.Namespace}},
		}},
		"relabel_configs": relabels,
	}
}

func metricRuntimeNamespaces(routes []metricRuntimeRoute) []string {
	seen := map[string]struct{}{}
	for _, item := range routes {
		seen[item.Route.Namespace] = struct{}{}
	}
	namespaces := make([]string, 0, len(seen))
	for namespace := range seen {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

func renderMetricsRuntimeManifest(name string, namespace string, remoteWriteURL string, configYAML string, configHash string, targetNamespaces []string) (string, error) {
	labels := map[string]any{
		"app.kubernetes.io/name":       "novaapm-vmagent",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "novaapm",
		"novaapm.io/config-hash":       configHash[:16],
	}
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "novaapm"}}},
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": name, "labels": labels}, "rules": []map[string]any{
			{"apiGroups": []string{""}, "resources": []string{"pods", "services", "endpoints"}, "verbs": []string{"get", "list", "watch"}},
			{"apiGroups": []string{"discovery.k8s.io"}, "resources": []string{"endpointslices"}, "verbs": []string{"get", "list", "watch"}},
		}},
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels}, "data": map[string]any{"promscrape.yml": configYAML}},
		{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels},
			"spec": map[string]any{"selector": map[string]any{"app.kubernetes.io/instance": name}, "ports": []map[string]any{{"name": "http", "port": 8429, "targetPort": "http"}}},
		},
		{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels},
			"spec": map[string]any{
				"replicas": 1,
				"strategy": map[string]any{"type": "Recreate"},
				"selector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/instance": name}},
				"template": map[string]any{
					"metadata": map[string]any{"labels": labels},
					"spec": map[string]any{
						"serviceAccountName": name,
						"securityContext":    map[string]any{"runAsNonRoot": true, "runAsUser": 65534, "runAsGroup": 65534, "fsGroup": 65534},
						"containers": []map[string]any{{
							"name": "vmagent", "image": platformimages.VMAgentImagePlaceholder, "imagePullPolicy": "IfNotPresent",
							"args": []string{
								"-promscrape.config=/etc/vmagent/promscrape.yml",
								"-promscrape.config.strictParse=true",
								"-promscrape.configCheckInterval=30s",
								"-remoteWrite.url=" + remoteWriteURL,
								"-remoteWrite.tmpDataPath=/var/lib/vmagent",
								"-remoteWrite.maxDiskUsagePerURL=4GiB",
								"-httpListenAddr=:8429",
							},
							"ports":           []map[string]any{{"name": "http", "containerPort": 8429}},
							"readinessProbe":  map[string]any{"httpGet": map[string]any{"path": "/-/healthy", "port": "http"}, "initialDelaySeconds": 5, "periodSeconds": 10},
							"livenessProbe":   map[string]any{"httpGet": map[string]any{"path": "/-/healthy", "port": "http"}, "initialDelaySeconds": 15, "periodSeconds": 20},
							"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}},
							"resources":       map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
							"volumeMounts":    []map[string]any{{"name": "config", "mountPath": "/etc/vmagent", "readOnly": true}, {"name": "queue", "mountPath": "/var/lib/vmagent"}, {"name": "tmp", "mountPath": "/tmp"}},
						}},
						"volumes": []map[string]any{{"name": "config", "configMap": map[string]any{"name": name}}, {"name": "queue", "emptyDir": map[string]any{"sizeLimit": "5Gi"}}, {"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "128Mi"}}},
					},
				},
			},
		},
	}
	roleBindings := make([]map[string]any, 0, len(targetNamespaces))
	for _, targetNamespace := range targetNamespaces {
		roleBindings = append(roleBindings, map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding",
			"metadata": map[string]any{"name": name, "namespace": targetNamespace, "labels": labels},
			"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": name},
			"subjects": []map[string]any{{"kind": "ServiceAccount", "name": name, "namespace": namespace}},
		})
	}
	ordered := make([]map[string]any, 0, len(objects)+len(roleBindings))
	ordered = append(ordered, objects[:3]...)
	ordered = append(ordered, roleBindings...)
	ordered = append(ordered, objects[3:]...)
	objects = ordered
	parts := make([]string, 0, len(objects))
	for _, object := range objects {
		content, err := yaml.Marshal(object)
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.TrimSpace(string(content)))
	}
	return strings.Join(parts, "\n---\n") + "\n", nil
}

func metricDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
