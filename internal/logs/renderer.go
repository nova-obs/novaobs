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
}

type k8sDaemonSetBundleTemplateData struct {
	AgentNamespace       string
	AgentName            string
	CollectorConfigBlock string
	ConfigHash           string
}

func renderAgentConfig(input renderInput) (string, string) {
	if input.Source.SourceType == SourceTypeVMFile {
		return renderVMFileConfig(input)
	}
	return renderK8sDaemonSetBundle([]renderInput{input})
}

func renderK8sDaemonSetBundle(inputs []renderInput) (string, string) {
	yaml := renderK8sDaemonSetBundleYAML(inputs, "")
	hash := hashYAML(yaml)
	return renderK8sDaemonSetBundleYAML(inputs, hash), hash
}

func renderK8sDaemonSetBundleYAML(inputs []renderInput, configHash string) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	source := first.Source
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaobs-system")
	agentName := "novaobs-logs-agent"
	collectorConfig := renderK8sCollectorConfig(inputs)
	data := k8sDaemonSetBundleTemplateData{
		AgentNamespace:       agentNamespace,
		AgentName:            agentName,
		CollectorConfigBlock: indentYAMLBlock(collectorConfig, "    "),
		ConfigHash:           yamlQuote(configHash),
	}
	var buffer bytes.Buffer
	if err := k8sDaemonSetBundleTemplate.Execute(&buffer, data); err != nil {
		panic(fmt.Sprintf("render k8s daemonset bundle template: %v", err))
	}
	return buffer.String()
}

func renderK8sCollectorConfig(inputs []renderInput) string {
	for _, input := range inputs {
		if strings.TrimSpace(input.Source.CollectorYAML) != "" {
			return strings.TrimSpace(input.Source.CollectorYAML)
		}
	}
	source := inputs[0].Source
	exporterName, exporterYAML := renderDownstreamExporter(inputs[0].Endpoint)
	return fmt.Sprintf(`extensions:
  file_storage/filelog_offsets:
    directory: /var/lib/otelcol/filelog_offsets
    create_directory: true
receivers:
  file_log/k8s:
    include:
%s
    poll_interval: 5s
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
      - type: container
processors:
  k8sattributes:
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
  filter/workload:
    logs:
      include:
        match_type: expr
        expressions:
%s
  transform/novaobs_routes:
    log_statements:
      - context: log
        statements:
%s
  resource/novaobs:
    attributes:
      - key: novaobs.source.type
        value: %s
        action: upsert
  batch:
exporters:
%s
service:
  extensions: [file_storage/filelog_offsets]
  pipelines:
    logs:
      receivers: [file_log/k8s]
      processors: [k8sattributes, filter/workload, transform/novaobs_routes, resource/novaobs, batch]
      exporters: [%s]`,
		renderK8sIncludes(inputs),
		renderFilterExpressions(inputs),
		renderTransformStatements(inputs),
		yamlQuote(source.SourceType),
		exporterYAML,
		exporterName,
	)
}

func renderK8sIncludes(inputs []renderInput) string {
	seen := map[string]bool{}
	lines := []string{}
	for _, input := range inputs {
		include := k8sStdoutInclude(input.Source)
		if input.Source.SourceType == SourceTypeK8sHostPath && strings.TrimSpace(input.Source.PathPattern) != "" {
			include = input.Source.PathPattern
		}
		if seen[include] {
			continue
		}
		seen[include] = true
		lines = append(lines, "          - "+yamlQuote(include))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
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

func renderFilterExpressions(inputs []renderInput) string {
	seen := map[string]bool{}
	lines := []string{}
	for _, input := range inputs {
		expr := workloadFilterExpression(input.Source)
		if seen[expr] {
			continue
		}
		seen[expr] = true
		lines = append(lines, "              - "+yamlQuote(expr))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func renderTransformStatements(inputs []renderInput) string {
	lines := []string{}
	for _, input := range inputs {
		expr := workloadFilterExpression(input.Source)
		for _, statement := range []string{
			fmt.Sprintf(`set(resource.attributes["service.name"], %q) where %s`, input.ServiceName, expr),
			fmt.Sprintf(`set(resource.attributes["deployment.environment"], %q) where %s`, input.Environment, expr),
			fmt.Sprintf(`set(resource.attributes["novaobs.route.id"], %q) where %s`, input.Route.ID, expr),
			fmt.Sprintf(`set(resource.attributes["novaobs.source.type"], %q) where %s`, input.Source.SourceType, expr),
		} {
			lines = append(lines, "              - "+yamlQuote(statement))
		}
		for _, rule := range input.Source.ParseRules {
			if !rule.Enabled {
				continue
			}
			switch rule.RuleType {
			case ParseRuleRegex:
				lines = append(lines, fmt.Sprintf("              # parse rule: %s", rule.Name))
				lines = append(lines, "              - "+yamlQuote(fmt.Sprintf(`merge_maps(attributes, ExtractPatterns(body, %q), "upsert") where %s`, rule.Pattern, expr)))
			case ParseRuleJSON:
				lines = append(lines, fmt.Sprintf("              # parse rule: %s", rule.Name))
				lines = append(lines, "              - "+yamlQuote(fmt.Sprintf(`merge_maps(attributes, ParseJSON(body), "upsert") where %s`, expr)))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func renderVMFileConfig(input renderInput) (string, string) {
	source := input.Source
	if strings.TrimSpace(source.CollectorYAML) != "" {
		yaml := strings.TrimSpace(source.CollectorYAML) + "\n"
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
      - key: novaobs.route.id
        value: %s
        action: upsert
      - key: novaobs.host.group
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
		yamlQuote(input.Route.ID),
		yamlQuote(source.HostGroup),
		exporterYAML,
		pipelineProcessors,
		exporterName,
	)
	return yaml, hashYAML(yaml)
}

func renderDownstreamExporter(endpoint LogEndpoint) (string, string) {
	endpoint = normalizeEndpoint(endpoint)
	switch endpoint.SinkType {
	case EndpointSinkES:
		lines := []string{
			"  elasticsearch/logs_downstream:",
			"    endpoints:",
			"      - " + yamlQuote(endpoint.WriteURL),
		}
		if endpoint.StreamName != "" {
			lines = append(lines, "    logs_index: "+yamlQuote(endpoint.StreamName))
		}
		return "elasticsearch/logs_downstream", strings.Join(lines, "\n")
	case EndpointSinkKafka:
		lines := []string{
			"  kafka/logs_downstream:",
			"    brokers:",
		}
		for _, broker := range splitEndpointList(endpoint.WriteURL) {
			lines = append(lines, "      - "+yamlQuote(broker))
		}
		topic := firstNonEmpty(endpoint.StreamName, "novaobs-logs")
		lines = append(lines, "    topic: "+yamlQuote(topic))
		return "kafka/logs_downstream", strings.Join(lines, "\n")
	default:
		return "otlp_http/logs_downstream", strings.Join([]string{
			"  otlp_http/logs_downstream:",
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
