package logs

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

//go:embed templates/collector_daemonset_manifest.yaml
var k8sDaemonSetBundleTemplateSource string

var k8sDaemonSetBundleTemplate = template.Must(template.New("k8s-daemonset-bundle").Parse(k8sDaemonSetBundleTemplateSource))

type renderInput struct {
	ServiceName string
	Environment string
	Source      LogSource
	Endpoint    LogEndpoint
	Route       LogRoute
	Deployment  agentDeploymentOptions
}

type renderedRouteConfig struct {
	ManifestYAML           string
	CollectorYAML          string
	CollectorConfigHash    string
	DeploymentManifestYAML string
	DeploymentManifestHash string
	RouteIDs               []string
}

type k8sDaemonSetBundleTemplateData struct {
	AgentNamespace       string
	AgentName            string
	ClusterID            string
	CollectorGroupID     string
	CollectorConfigBlock string
	ConfigHash           string
	RuntimeLogMounts     []k8sRuntimeLogMount
	OpAMPEnabled         bool
	OpAMPEndpoint        string
}

type k8sRuntimeLogMount struct {
	Name string
	Path string
}

func renderAgentConfig(input renderInput) (string, string) {
	if input.Source.SourceType == SourceTypeVMFile {
		return renderVMFileConfig(input)
	}
	return renderK8sDaemonSetBundle([]renderInput{input})
}

func renderK8sCollectorConfigWithHash(inputs []renderInput) (string, string) {
	yaml := renderK8sCollectorConfig(inputs, "")
	return yaml, hashYAML(yaml)
}

func renderK8sCollectorConfigWithPatchHash(inputs []renderInput, processorPatch string) (string, string) {
	yaml := renderK8sCollectorConfig(inputs, processorPatch)
	return yaml, hashYAML(yaml)
}

func renderK8sDaemonSetBundle(inputs []renderInput) (string, string) {
	rendered := renderK8sDaemonSetBundleWithHashes(inputs, "")
	return rendered.ManifestYAML, rendered.CollectorConfigHash
}

func renderK8sDaemonSetBundleWithHashes(inputs []renderInput, processorPatch string) renderedRouteConfig {
	collectorYAML, collectorHash := renderK8sCollectorConfigWithPatchHash(inputs, processorPatch)
	deploymentManifest := renderK8sDeploymentManifestYAML(inputs)
	yaml := renderK8sDaemonSetBundleYAML(inputs, collectorHash, processorPatch)
	return renderedRouteConfig{
		ManifestYAML:           yaml,
		CollectorYAML:          collectorYAML,
		CollectorConfigHash:    collectorHash,
		DeploymentManifestYAML: deploymentManifest,
		DeploymentManifestHash: hashYAML(deploymentManifest),
		RouteIDs:               renderRouteIDs(inputs),
	}
}

func renderK8sDeploymentManifestYAML(inputs []renderInput) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	source := first.Source
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaobs-system")
	agentName := "novaobs-logs-agent"
	data := k8sDaemonSetBundleTemplateData{
		AgentNamespace:       agentNamespace,
		AgentName:            agentName,
		ClusterID:            firstNonEmpty(source.ClusterID, "<cluster-id>"),
		CollectorGroupID:     firstNonEmpty(first.Route.AgentGroupID, "<collector-group-id>"),
		CollectorConfigBlock: "    <collector-yaml-managed-by-config-version>",
		ConfigHash:           yamlQuote("<collector-config-hash>"),
		RuntimeLogMounts:     k8sRuntimeLogMounts(),
		OpAMPEnabled:         opAMPEnabled(first.Deployment),
		OpAMPEndpoint:        strings.TrimSpace(first.Deployment.OpAMPEndpoint),
	}
	var buffer bytes.Buffer
	if err := k8sDaemonSetBundleTemplate.Execute(&buffer, data); err != nil {
		panic(fmt.Sprintf("render k8s deployment manifest template: %v", err))
	}
	return buffer.String()
}

func renderRouteIDs(inputs []renderInput) []string {
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		ids = append(ids, input.Route.ID)
	}
	sort.Strings(ids)
	return ids
}

func renderK8sDaemonSetBundleYAML(inputs []renderInput, configHash string, processorPatch string) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	source := first.Source
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaobs-system")
	agentName := "novaobs-logs-agent"
	collectorConfig := renderK8sCollectorConfig(inputs, processorPatch)
	data := k8sDaemonSetBundleTemplateData{
		AgentNamespace:       agentNamespace,
		AgentName:            agentName,
		ClusterID:            firstNonEmpty(source.ClusterID, "<cluster-id>"),
		CollectorGroupID:     firstNonEmpty(first.Route.AgentGroupID, "<collector-group-id>"),
		CollectorConfigBlock: indentYAMLBlock(collectorConfig, "    "),
		ConfigHash:           yamlQuote(configHash),
		RuntimeLogMounts:     k8sRuntimeLogMounts(),
		OpAMPEnabled:         opAMPEnabled(first.Deployment),
		OpAMPEndpoint:        strings.TrimSpace(first.Deployment.OpAMPEndpoint),
	}
	var buffer bytes.Buffer
	if err := k8sDaemonSetBundleTemplate.Execute(&buffer, data); err != nil {
		panic(fmt.Sprintf("render k8s daemonset bundle template: %v", err))
	}
	return buffer.String()
}

func k8sRuntimeLogMounts() []k8sRuntimeLogMount {
	return []k8sRuntimeLogMount{
		{Name: "docker-containers", Path: "/data/docker/containers"},
	}
}

func renderK8sCollectorConfig(inputs []renderInput, processorPatch string) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	suffixes := routeComponentSuffixes(inputs)
	opAMPBlock := ""
	if opAMPEnabled(first.Deployment) {
		opAMPBlock = `  opamp:
    server:
      ws:
        endpoint: ${env:NOVAOBS_OPAMP_ENDPOINT}
`
	}
	patchBlock := ""
	if trimmed := strings.TrimSpace(processorPatch); trimmed != "" {
		patchBlock = indentYAMLBlock(trimmed, "  ") + "\n"
	}
	return fmt.Sprintf(`extensions:
  file_storage/filelog_offsets:
    directory: /var/lib/otelcol/filelog_offsets
    create_directory: true
  health_check:
    endpoint: 0.0.0.0:13133
%s
receivers:
%s
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  k8s_attributes:
    auth_type: serviceAccount
    passthrough: false
    filter:
      node_from_env_var: KUBE_NODE_NAME
    extract:
      metadata:
        - k8s.namespace.name
        - k8s.pod.name
        - k8s.container.name
        - k8s.deployment.name
        - k8s.statefulset.name
        - k8s.daemonset.name
        - k8s.cronjob.name
        - k8s.job.name
%s
%s  batch:
exporters:
%s
service:
  extensions: [%s]
  telemetry:
    resource:
      service.name: novaobs-logs-agent
      k8s.pod.uid: ${env:KUBE_POD_UID}
      k8s.pod.name: ${env:KUBE_POD_NAME}
      k8s.node.name: ${env:KUBE_NODE_NAME}
      k8s.pod.ip: ${env:KUBE_POD_IP}
    metrics:
      level: normal
      readers:
        - pull:
            exporter:
              prometheus:
                host: 0.0.0.0
                port: 8888
  pipelines:
%s`,
		opAMPBlock,
		renderK8sReceivers(inputs, suffixes),
		renderK8sRouteProcessors(inputs, suffixes),
		patchBlock,
		renderK8sExporters(inputs),
		renderServiceExtensions(first.Deployment),
		renderK8sPipelines(inputs, suffixes),
	)
}

func renderServiceExtensions(options agentDeploymentOptions) string {
	extensions := []string{"file_storage/filelog_offsets", "health_check"}
	if opAMPEnabled(options) {
		extensions = append(extensions, "opamp")
	}
	return strings.Join(extensions, ", ")
}

func opAMPEnabled(options agentDeploymentOptions) bool {
	return strings.TrimSpace(options.OpAMPEndpoint) != ""
}

func renderK8sReceivers(inputs []renderInput, suffixes map[string]string) string {
	lines := []string{}
	for _, input := range inputs {
		lines = append(lines, renderK8sReceiver(input, suffixForInput(input, suffixes)))
	}
	return strings.Join(lines, "\n")
}

func renderK8sReceiver(input renderInput, suffix string) string {
	include := k8sStdoutInclude(input.Source)
	extraOperators := ""
	if trimmed := strings.TrimSpace(input.Source.OperatorsYAML); trimmed != "" {
		extraOperators = "\n" + indentYAMLBlock(trimmed, "      ")
	}
	return fmt.Sprintf(`  file_log/%s:
    include:
      - %s
    exclude:
      - "/var/log/pods/*_novaobs-logs-agent-*_*/*/*.log"
      - "/var/log/pods/*/*/*.gz"
      - "/var/log/pods/*/*/*.tmp"
      - "/var/log/pods/*/*/*.log.*"
    poll_interval: 10s
    max_concurrent_files: 64
    max_batches: 2
    max_log_size: 1MiB
    file_cache_advise: true
    include_file_path: true
    include_file_name: false
    start_at: end
    storage: file_storage/filelog_offsets
    retry_on_failure:
      enabled: true
      initial_interval: 1s
      max_interval: 30s
      max_elapsed_time: 0
    operators:
      - type: container%s`, suffix, yamlQuote(include), extraOperators)
}

func renderK8sRouteProcessors(inputs []renderInput, suffixes map[string]string) string {
	lines := []string{}
	for _, input := range inputs {
		suffix := suffixForInput(input, suffixes)
		lines = append(lines, fmt.Sprintf(`  resource/%s:
    attributes:
      - key: service.name
        value: %s
        action: upsert
      - key: deployment.environment
        value: %s
        action: upsert`, suffix, yamlQuote(input.ServiceName), yamlQuote(input.Environment)))
		if hasEnabledParseRules(input.Source.ParseRules) {
			lines = append(lines, renderK8sParseProcessor(suffix, input.Source.ParseRules))
		}
	}
	return strings.Join(lines, "\n")
}

func hasEnabledParseRules(rules []LogParseRule) bool {
	for _, rule := range rules {
		if rule.Enabled {
			return true
		}
	}
	return false
}

func renderK8sParseProcessor(suffix string, rules []LogParseRule) string {
	lines := []string{
		"  transform/" + suffix + ":",
		"    log_statements:",
		"      - context: log",
		"        statements:",
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		switch rule.RuleType {
		case ParseRuleRegex:
			lines = append(lines, fmt.Sprintf("          # parse rule: %s", rule.Name))
			lines = append(lines, "          - "+yamlQuote(fmt.Sprintf(`merge_maps(attributes, ExtractPatterns(body, %q), "upsert")`, rule.Pattern)))
		case ParseRuleJSON:
			lines = append(lines, fmt.Sprintf("          # parse rule: %s", rule.Name))
			lines = append(lines, "          - "+yamlQuote(`merge_maps(attributes, ParseJSON(body), "upsert")`))
		}
	}
	return strings.Join(lines, "\n")
}

func renderK8sExporters(inputs []renderInput) string {
	seen := map[string]bool{}
	lines := []string{}
	for _, input := range inputs {
		name := downstreamExporterName(input.Endpoint)
		if seen[name] {
			continue
		}
		seen[name] = true
		lines = append(lines, renderDownstreamExporterYAML(input.Endpoint, name))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func renderK8sPipelines(inputs []renderInput, suffixes map[string]string) string {
	lines := []string{}
	for _, input := range inputs {
		suffix := suffixForInput(input, suffixes)
		processors := []string{"memory_limiter", "k8s_attributes", "resource/" + suffix}
		if hasEnabledParseRules(input.Source.ParseRules) {
			processors = append(processors, "transform/"+suffix)
		}
		processors = append(processors, "batch")
		lines = append(lines, fmt.Sprintf(`    logs/%s:
      receivers: [file_log/%s]
      processors: [%s]
      exporters: [%s]`, suffix, suffix, strings.Join(processors, ", "), downstreamExporterName(input.Endpoint)))
	}
	return strings.Join(lines, "\n")
}

func routeComponentSuffix(input renderInput) string {
	return safeSegment(firstNonEmpty(input.Source.Namespace+"-"+input.Source.WorkloadName, input.ServiceName, input.Route.ID))
}

func routeComponentSuffixes(inputs []renderInput) map[string]string {
	baseCounts := map[string]int{}
	for _, input := range inputs {
		baseCounts[routeComponentSuffix(input)]++
	}
	used := map[string]int{}
	out := map[string]string{}
	for _, input := range inputs {
		base := routeComponentSuffix(input)
		suffix := base
		if baseCounts[base] > 1 {
			suffix = base + "-" + shortComponentID(input.Route.ID)
		}
		used[suffix]++
		if used[suffix] > 1 {
			suffix = suffix + "-" + fmt.Sprintf("%d", used[suffix])
		}
		out[input.Route.ID] = suffix
	}
	return out
}

func suffixForInput(input renderInput, suffixes map[string]string) string {
	if suffix := suffixes[input.Route.ID]; suffix != "" {
		return suffix
	}
	return routeComponentSuffix(input)
}

func shortComponentID(value string) string {
	value = safeSegment(value)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func k8sStdoutInclude(source LogSource) string {
	namespace := strings.TrimSpace(source.Namespace)
	workloadName := strings.TrimSpace(source.WorkloadName)
	if namespace != "" && workloadName != "" {
		return fmt.Sprintf("/var/log/pods/%s_%s*_*/*/*.log", namespace, workloadName)
	}
	if namespace != "" {
		return fmt.Sprintf("/var/log/pods/%s_*_*/*/*.log", namespace)
	}
	return "/var/log/pods/*_*_*/*/*.log"
}

func renderVMFileConfig(input renderInput) (string, string) {
	source := input.Source
	if strings.TrimSpace(source.CustomCollectorYAML) != "" {
		yaml := strings.TrimSpace(source.CustomCollectorYAML) + "\n"
		return yaml, hashYAML(yaml)
	}
	exporterName, exporterYAML := renderDownstreamExporter(input.Endpoint)
	parseProcessor := ""
	pipelineProcessors := "resource/novaobs, batch"
	if len(source.ParseRules) > 0 {
		parseProcessor = renderVMParseProcessor(source.ParseRules)
		pipelineProcessors = "transform/novaobs_parse, resource/novaobs, batch"
	}
	yaml := fmt.Sprintf(`receivers:
  file_log/vm:
    include:
      - %s
    include_file_path: true
    include_file_name: false
    start_at: end
processors:
%s
  resource/novaobs:
    attributes:
      - key: service.name
        value: %s
        action: upsert
      - key: deployment.environment
        value: %s
        action: upsert
  batch:
exporters:
%s
service:
  pipelines:
    logs:
      receivers: [file_log/vm]
      processors: [%s]
      exporters: [%s]
`,
		yamlQuote(source.PathPattern),
		parseProcessor,
		yamlQuote(input.ServiceName),
		yamlQuote(input.Environment),
		exporterYAML,
		pipelineProcessors,
		exporterName,
	)
	return yaml, hashYAML(yaml)
}

func renderDownstreamExporter(endpoint LogEndpoint) (string, string) {
	name := downstreamExporterNameWithSuffix(endpoint, "logs_downstream")
	return name, renderDownstreamExporterYAML(endpoint, name)
}

func downstreamExporterName(endpoint LogEndpoint) string {
	return downstreamExporterNameWithSuffix(endpoint, "endpoint_"+safeSegment(firstNonEmpty(endpoint.Name, endpoint.ID, hashYAML(endpoint.WriteURL))))
}

func downstreamExporterNameWithSuffix(endpoint LogEndpoint, suffix string) string {
	endpoint = normalizeEndpoint(endpoint)
	switch endpoint.SinkType {
	case EndpointSinkES:
		return "elasticsearch/" + suffix
	case EndpointSinkKafka:
		return "kafka/" + suffix
	default:
		return "otlp_http/" + suffix
	}
}

func renderDownstreamExporterYAML(endpoint LogEndpoint, name string) string {
	endpoint = normalizeEndpoint(endpoint)
	switch endpoint.SinkType {
	case EndpointSinkES:
		lines := []string{
			"  " + name + ":",
			"    endpoints:",
			"      - " + yamlQuote(endpoint.WriteURL),
		}
		if endpoint.StreamName != "" {
			lines = append(lines, "    logs_index: "+yamlQuote(endpoint.StreamName))
		}
		return strings.Join(lines, "\n")
	case EndpointSinkKafka:
		lines := []string{
			"  " + name + ":",
			"    brokers:",
		}
		for _, broker := range splitEndpointList(endpoint.WriteURL) {
			lines = append(lines, "      - "+yamlQuote(broker))
		}
		topic := firstNonEmpty(endpoint.StreamName, "novaobs-logs")
		lines = append(lines, "    topic: "+yamlQuote(topic))
		return strings.Join(lines, "\n")
	default:
		return strings.Join([]string{
			"  " + name + ":",
			"    logs_endpoint: " + yamlQuote(endpoint.WriteURL),
		}, "\n")
	}
}

func renderVMParseProcessor(rules []LogParseRule) string {
	lines := []string{
		"  transform/novaobs_parse:",
		"    log_statements:",
		"      - context: log",
		"        statements:",
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		switch rule.RuleType {
		case ParseRuleRegex:
			lines = append(lines, fmt.Sprintf("          # parse rule: %s", rule.Name))
			lines = append(lines, "          - "+yamlQuote(fmt.Sprintf(`merge_maps(attributes, ExtractPatterns(body, %q), "upsert")`, rule.Pattern)))
		case ParseRuleJSON:
			lines = append(lines, fmt.Sprintf("          # parse rule: %s", rule.Name))
			lines = append(lines, "          - "+yamlQuote(`merge_maps(attributes, ParseJSON(body), "upsert")`))
		}
	}
	return strings.Join(lines, "\n")
}

func workloadFilterExpression(source LogSource) string {
	parts := []string{fmt.Sprintf(`resource.attributes["k8s.namespace.name"] == %q`, source.Namespace)}
	if source.WorkloadName == "" {
		return strings.Join(parts, " and ")
	}
	kinds := map[string][]string{
		"Deployment":  {"k8s.deployment.name"},
		"StatefulSet": {"k8s.statefulset.name"},
		"DaemonSet":   {"k8s.daemonset.name"},
		"Job":         {"k8s.job.name"},
		"CronJob":     {"k8s.cronjob.name"},
	}
	attrs := kinds[source.WorkloadKind]
	if len(attrs) == 0 {
		attrs = []string{"k8s.deployment.name", "k8s.statefulset.name", "k8s.daemonset.name", "k8s.job.name", "k8s.cronjob.name"}
	}
	matches := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		matches = append(matches, fmt.Sprintf(`resource.attributes["%s"] == %q`, attr, source.WorkloadName))
	}
	sort.Strings(matches)
	parts = append(parts, "("+strings.Join(matches, " or ")+")")
	return strings.Join(parts, " and ")
}

func yamlQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func hashYAML(yaml string) string {
	sum := sha256.Sum256([]byte(yaml))
	return hex.EncodeToString(sum[:])[:16]
}

func indentYAMLBlock(value string, prefix string) string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return prefix
	}
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
