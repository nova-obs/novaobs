package logs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type renderInput struct {
	ServiceName string
	Environment string
	Source      LogSource
	Endpoint    LogEndpoint
	Route       LogRoute
}

func renderAgentConfig(input renderInput) (string, string) {
	if input.Source.SourceType == SourceTypeVMFile {
		return renderVMFileConfig(input)
	}
	return renderK8sDaemonSetBundle([]renderInput{input})
}

func renderK8sDaemonSetBundle(inputs []renderInput) (string, string) {
	if len(inputs) == 0 {
		return "", hashYAML("")
	}
	first := inputs[0]
	source := first.Source
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaobs-system")
	agentName := "novaobs-logs-agent"
	includes := renderK8sIncludes(inputs)
	filterExpressions := renderFilterExpressions(inputs)
	transformStatements := renderTransformStatements(inputs)
	yaml := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: %s
rules:
  - apiGroups: [""]
    resources: ["pods", "namespaces", "nodes"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: %s
subjects:
  - kind: ServiceAccount
    name: %s
    namespace: %s
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-config
  namespace: %s
data:
  collector.yaml: |
    receivers:
      filelog/k8s:
        include:
%s
        include_file_path: true
        include_file_name: false
        start_at: end
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
      otlphttp/victorialogs:
        logs_endpoint: %s
    service:
      pipelines:
        logs:
          receivers: [filelog/k8s]
          processors: [k8sattributes, filter/workload, transform/novaobs_routes, resource/novaobs, batch]
          exporters: [otlphttp/victorialogs]
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/part-of: novaobs
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/part-of: novaobs
    spec:
      serviceAccountName: %s
      containers:
        - name: otelcol
          image: otel/opentelemetry-collector-contrib:0.102.1
          args: ["--config=/conf/collector.yaml"]
          env:
            - name: KUBE_NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
          volumeMounts:
            - name: config
              mountPath: /conf
              readOnly: true
            - name: pod-logs
              mountPath: /var/log/pods
              readOnly: true
      volumes:
        - name: config
          configMap:
            name: %s-config
        - name: pod-logs
          hostPath:
            path: /var/log/pods
            type: DirectoryOrCreate
`,
		agentNamespace,
		agentName,
		agentNamespace,
		agentName,
		agentName,
		agentName,
		agentName,
		agentNamespace,
		agentName,
		agentNamespace,
		includes,
		filterExpressions,
		transformStatements,
		yamlQuote(source.SourceType),
		yamlQuote(first.Endpoint.WriteURL),
		agentName,
		agentNamespace,
		agentName,
		agentName,
		agentName,
		agentName,
		agentName,
	)
	return yaml, hashYAML(yaml)
}

func renderK8sIncludes(inputs []renderInput) string {
	seen := map[string]bool{}
	lines := []string{}
	for _, input := range inputs {
		include := fmt.Sprintf("/var/log/pods/%s_*_*/*/*.log", input.Source.Namespace)
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
	parseProcessor := ""
	pipelineProcessors := "resource/novaobs, batch"
	if len(source.ParseRules) > 0 {
		parseProcessor = renderVMParseProcessor(source.ParseRules)
		pipelineProcessors = "transform/novaobs_parse, resource/novaobs, batch"
	}
	yaml := fmt.Sprintf(`receivers:
  filelog/vm:
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
  otlphttp/victorialogs:
    logs_endpoint: %s
service:
  pipelines:
    logs:
      receivers: [filelog/vm]
      processors: [%s]
      exporters: [otlphttp/victorialogs]
`,
		yamlQuote(source.PathPattern),
		parseProcessor,
		yamlQuote(input.ServiceName),
		yamlQuote(input.Environment),
		yamlQuote(input.Route.ID),
		yamlQuote(source.HostGroup),
		yamlQuote(input.Endpoint.WriteURL),
		pipelineProcessors,
	)
	return yaml, hashYAML(yaml)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
