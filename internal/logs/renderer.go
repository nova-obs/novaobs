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

	platformimages "novaapm/internal/platform/images"

	"gopkg.in/yaml.v3"
)

//go:embed templates/collector_daemonset_manifest.yaml
var k8sDaemonSetBundleTemplateSource string

var k8sDaemonSetBundleTemplate = template.Must(template.New("k8s-daemonset-bundle").Parse(k8sDaemonSetBundleTemplateSource))

const k8sLogsAgentName = "novaapm-logs-agent"

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
	CollectorConfigFiles   map[string]string
	ConfigFileRefs         []LogCollectorConfigFile
	CollectorConfigHash    string
	DeploymentManifestYAML string
	DeploymentManifestHash string
	RouteIDs               []string
}

type k8sDaemonSetBundleTemplateData struct {
	AgentNamespace   string
	AgentName        string
	ClusterID        string
	CollectorGroupID string
	ConfigHash       string
	ConfigMaps       []k8sConfigMapTemplateData
	ConfigArgs       []string
	RuntimeLogMounts []k8sRuntimeLogMount
	OpAMPEnabled     bool
	OpAMPEndpoint    string
}

type k8sConfigMapTemplateData struct {
	Name         string
	Role         string
	DataKey      string
	MountPath    string
	ContentBlock string
	ConfigHash   string
	RouteID      string
	ServiceID    string
}

type k8sCollectorConfigBundle struct {
	CollectorYAML        string
	CollectorConfigFiles map[string]string
	ConfigFileRefs       []LogCollectorConfigFile
	ConfigHash           string
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
	deployment := agentDeploymentOptions{}
	if len(inputs) > 0 {
		deployment = inputs[0].Deployment
	}
	bundle, err := renderK8sCollectorConfigBundle(inputs, "", deployment)
	if err != nil {
		return "", ""
	}
	return bundle.CollectorYAML, bundle.ConfigHash
}

func renderK8sCollectorConfigWithPatchHash(inputs []renderInput, processorPatch string) (string, string, error) {
	deployment := agentDeploymentOptions{}
	if len(inputs) > 0 {
		deployment = inputs[0].Deployment
	}
	bundle, err := renderK8sCollectorConfigBundle(inputs, processorPatch, deployment)
	if err != nil {
		return "", "", err
	}
	return bundle.CollectorYAML, bundle.ConfigHash, nil
}

func renderK8sDaemonSetBundle(inputs []renderInput) (string, string) {
	rendered, err := renderK8sDaemonSetBundleWithHashes(inputs, "")
	if err != nil {
		return "", ""
	}
	return rendered.ManifestYAML, rendered.CollectorConfigHash
}

func renderK8sDaemonSetBundleWithHashes(inputs []renderInput, processorPatch string) (renderedRouteConfig, error) {
	return renderK8sDaemonSetBundleWithTemplateValues(inputs, processorPatch, platformimages.DefaultTemplateValues)
}

func renderK8sDaemonSetBundleWithTemplateValues(inputs []renderInput, processorPatch string, templateValues map[string]string) (renderedRouteConfig, error) {
	deployment := agentDeploymentOptions{}
	if len(inputs) > 0 {
		deployment = inputs[0].Deployment
	}
	bundle, err := renderK8sCollectorConfigBundle(inputs, processorPatch, deployment)
	if err != nil {
		return renderedRouteConfig{}, err
	}
	deploymentManifest := renderK8sDeploymentManifestYAMLWithTemplateValues(inputs, placeholderConfigFiles(bundle.CollectorConfigFiles), bundle.ConfigFileRefs, templateValues)
	yaml := renderK8sDaemonSetBundleYAMLWithTemplateValues(inputs, bundle.CollectorConfigFiles, bundle.ConfigFileRefs, bundle.ConfigHash, templateValues)
	return renderedRouteConfig{
		ManifestYAML:           yaml,
		CollectorYAML:          bundle.CollectorYAML,
		CollectorConfigFiles:   bundle.CollectorConfigFiles,
		ConfigFileRefs:         bundle.ConfigFileRefs,
		CollectorConfigHash:    bundle.ConfigHash,
		DeploymentManifestYAML: deploymentManifest,
		DeploymentManifestHash: hashYAML(deploymentManifest),
		RouteIDs:               renderRouteIDs(inputs),
	}, nil
}

func renderK8sCollectorRuntimeBundleWithTemplateValues(clusterID string, agentNamespace string, collectorGroupID string, inputs []renderInput, processorPatch string, deployment agentDeploymentOptions, templateValues map[string]string) (renderedRouteConfig, error) {
	bundle, err := renderK8sCollectorConfigBundle(inputs, processorPatch, deployment)
	if err != nil {
		return renderedRouteConfig{}, err
	}
	deploymentManifest := renderK8sRuntimeDeploymentManifestYAMLWithTemplateValues(clusterID, agentNamespace, collectorGroupID, placeholderConfigFiles(bundle.CollectorConfigFiles), bundle.ConfigFileRefs, "<collector-config-hash>", deployment, templateValues)
	manifest := renderK8sRuntimeDeploymentManifestYAMLWithTemplateValues(clusterID, agentNamespace, collectorGroupID, bundle.CollectorConfigFiles, bundle.ConfigFileRefs, bundle.ConfigHash, deployment, templateValues)
	return renderedRouteConfig{
		ManifestYAML:           manifest,
		CollectorYAML:          bundle.CollectorYAML,
		CollectorConfigFiles:   bundle.CollectorConfigFiles,
		ConfigFileRefs:         bundle.ConfigFileRefs,
		CollectorConfigHash:    bundle.ConfigHash,
		DeploymentManifestYAML: deploymentManifest,
		DeploymentManifestHash: hashYAML(deploymentManifest),
		RouteIDs:               renderRouteIDs(inputs),
	}, nil
}

func renderK8sDeploymentManifestYAML(inputs []renderInput) string {
	deployment := agentDeploymentOptions{}
	if len(inputs) > 0 {
		deployment = inputs[0].Deployment
	}
	bundle, err := renderK8sCollectorConfigBundle(inputs, "", deployment)
	if err != nil {
		return ""
	}
	return renderK8sDeploymentManifestYAMLWithTemplateValues(inputs, placeholderConfigFiles(bundle.CollectorConfigFiles), bundle.ConfigFileRefs, platformimages.DefaultTemplateValues)
}

func renderK8sDeploymentManifestYAMLWithTemplateValues(inputs []renderInput, configFiles map[string]string, refs []LogCollectorConfigFile, templateValues map[string]string) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	source := first.Source
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaapm-system")
	return renderK8sManifestYAMLWithTemplateValues(
		firstNonEmpty(source.ClusterID, "<cluster-id>"),
		agentNamespace,
		firstNonEmpty(first.Route.AgentGroupID, "<collector-group-id>"),
		configFiles,
		refs,
		"<collector-config-hash>",
		first.Deployment,
		templateValues,
	)
}

func renderRouteIDs(inputs []renderInput) []string {
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		ids = append(ids, input.Route.ID)
	}
	sort.Strings(ids)
	return ids
}

func renderK8sDaemonSetBundleYAML(inputs []renderInput, configFiles map[string]string, refs []LogCollectorConfigFile, configHash string) string {
	return renderK8sDaemonSetBundleYAMLWithTemplateValues(inputs, configFiles, refs, configHash, platformimages.DefaultTemplateValues)
}

func renderK8sDaemonSetBundleYAMLWithTemplateValues(inputs []renderInput, configFiles map[string]string, refs []LogCollectorConfigFile, configHash string, templateValues map[string]string) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	source := first.Source
	agentNamespace := firstNonEmpty(source.AgentNamespace, "novaapm-system")
	return renderK8sManifestYAMLWithTemplateValues(
		firstNonEmpty(source.ClusterID, "<cluster-id>"),
		agentNamespace,
		firstNonEmpty(first.Route.AgentGroupID, "<collector-group-id>"),
		configFiles,
		refs,
		configHash,
		first.Deployment,
		templateValues,
	)
}

func renderK8sRuntimeDeploymentManifestYAMLWithTemplateValues(clusterID string, agentNamespace string, collectorGroupID string, configFiles map[string]string, refs []LogCollectorConfigFile, configHash string, deployment agentDeploymentOptions, templateValues map[string]string) string {
	return renderK8sManifestYAMLWithTemplateValues(clusterID, agentNamespace, collectorGroupID, configFiles, refs, configHash, deployment, templateValues)
}

func renderK8sRuntimeServiceConfigManifest(manifest string, configMapNames map[string]struct{}) string {
	filtered := filterYAMLDocuments(manifest, func(identity manifestDocumentIdentity) bool {
		if identity.Kind == "DaemonSet" {
			return true
		}
		if identity.Kind != "ConfigMap" {
			return false
		}
		if configMapNames == nil {
			return true
		}
		_, ok := configMapNames[identity.Name]
		return ok
	})
	if strings.TrimSpace(filtered) == "" {
		return manifest
	}
	return filtered
}

func renderK8sManifestYAMLWithTemplateValues(clusterID string, agentNamespace string, collectorGroupID string, configFiles map[string]string, refs []LogCollectorConfigFile, configHash string, deployment agentDeploymentOptions, templateValues map[string]string) string {
	data := k8sDaemonSetBundleTemplateData{
		AgentNamespace:   firstNonEmpty(agentNamespace, "novaapm-system"),
		AgentName:        k8sLogsAgentName,
		ClusterID:        firstNonEmpty(clusterID, "<cluster-id>"),
		CollectorGroupID: firstNonEmpty(collectorGroupID, "<collector-group-id>"),
		ConfigHash:       yamlQuote(configHash),
		ConfigMaps:       k8sConfigMapTemplateDataList(configFiles, refs),
		ConfigArgs:       k8sConfigArgs(refs),
		RuntimeLogMounts: k8sRuntimeLogMounts(),
		OpAMPEnabled:     opAMPEnabled(deployment),
		OpAMPEndpoint:    strings.TrimSpace(deployment.OpAMPEndpoint),
	}
	var buffer bytes.Buffer
	if err := k8sDaemonSetBundleTemplate.Execute(&buffer, data); err != nil {
		panic(fmt.Sprintf("render k8s daemonset bundle template: %v", err))
	}
	return platformimages.ApplyTemplateValues(buffer.String(), templateValues)
}

type manifestDocumentIdentity struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

func filterYAMLDocuments(raw string, keep func(manifestDocumentIdentity) bool) string {
	documents := splitYAMLDocuments(raw)
	selected := make([]string, 0, len(documents))
	for _, document := range documents {
		identity, ok := parseManifestDocumentIdentity(document)
		if !ok || !keep(identity) {
			continue
		}
		selected = append(selected, strings.TrimSpace(document))
	}
	if len(selected) == 0 {
		return ""
	}
	return strings.Join(selected, "\n---\n") + "\n"
}

func splitYAMLDocuments(raw string) []string {
	lines := strings.Split(raw, "\n")
	documents := []string{}
	current := []string{}
	appendCurrent := func() {
		document := strings.TrimSpace(strings.Join(current, "\n"))
		if document != "" {
			documents = append(documents, document)
		}
		current = []string{}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			appendCurrent()
			continue
		}
		current = append(current, line)
	}
	appendCurrent()
	return documents
}

func parseManifestDocumentIdentity(raw string) (manifestDocumentIdentity, bool) {
	var document struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Namespace string `yaml:"namespace"`
			Name      string `yaml:"name"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return manifestDocumentIdentity{}, false
	}
	identity := manifestDocumentIdentity{
		APIVersion: strings.TrimSpace(document.APIVersion),
		Kind:       strings.TrimSpace(document.Kind),
		Namespace:  strings.TrimSpace(document.Metadata.Namespace),
		Name:       strings.TrimSpace(document.Metadata.Name),
	}
	return identity, identity.APIVersion != "" && identity.Kind != "" && identity.Name != ""
}

func renderIdleK8sCollectorConfig(deployment agentDeploymentOptions) string {
	opAMPBlock := ""
	if opAMPEnabled(deployment) {
		opAMPBlock = `  opamp:
    server:
      ws:
        endpoint: ${env:NOVAAPM_OPAMP_ENDPOINT}
`
	}
	return fmt.Sprintf(`extensions:
  file_storage/filelog_offsets:
    directory: /var/lib/otelcol/filelog_offsets
    create_directory: true
  health_check:
    endpoint: 0.0.0.0:13133
%s
receivers:
  file_log/novaapm_idle:
    include:
      - "/var/log/pods/__novaapm_idle__/*/*.log"
    exclude:
      - "/var/log/pods/*_novaapm-logs-agent-*_*/*/*.log"
      - "/var/log/pods/*/*/*.gz"
      - "/var/log/pods/*/*/*.tmp"
      - "/var/log/pods/*/*/*.log.*"
    poll_interval: 10s
    max_concurrent_files: 64
    max_batches: 2
    start_at: end
    storage: file_storage/filelog_offsets
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
  batch:
exporters:
  debug:
    verbosity: basic
service:
  extensions: [%s]
  telemetry:
    resource:
      service.name: novaapm-logs-agent
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
    logs/idle:
      receivers: [file_log/novaapm_idle]
      processors: [memory_limiter, k8s_attributes, batch]
      exporters: [debug]`, opAMPBlock, renderServiceExtensions(deployment))
}

func k8sRuntimeLogMounts() []k8sRuntimeLogMount {
	return []k8sRuntimeLogMount{
		{Name: "docker-containers", Path: "/data/docker/containers"},
	}
}

func renderK8sCollectorConfigBundle(inputs []renderInput, processorPatch string, deployment agentDeploymentOptions) (k8sCollectorConfigBundle, error) {
	files, refs, err := renderK8sCollectorConfigFiles(inputs, processorPatch, deployment)
	if err != nil {
		return k8sCollectorConfigBundle{}, err
	}
	collectorYAML, err := renderK8sCollectorConfig(inputs, processorPatch)
	if err != nil {
		return k8sCollectorConfigBundle{}, err
	}
	if strings.TrimSpace(collectorYAML) == "" {
		collectorYAML = files["base.yaml"]
	}
	return k8sCollectorConfigBundle{
		CollectorYAML:        collectorYAML,
		CollectorConfigFiles: files,
		ConfigFileRefs:       refs,
		ConfigHash:           hashConfigFiles(files),
	}, nil
}

func renderK8sCollectorConfigFiles(inputs []renderInput, processorPatch string, deployment agentDeploymentOptions) (map[string]string, []LogCollectorConfigFile, error) {
	files := map[string]string{}
	refs := []LogCollectorConfigFile{{
		Path:          "base.yaml",
		ConfigMapName: k8sBaseConfigMapName(k8sLogsAgentName),
		Role:          "base",
	}}
	if len(inputs) == 0 {
		files["base.yaml"] = renderIdleK8sCollectorConfig(deployment)
		return files, refs, nil
	}
	first := inputs[0]
	files["base.yaml"] = renderK8sBaseCollectorConfig(first.Deployment, processorPatch)
	suffixes := routeComponentSuffixes(inputs)
	for _, input := range inputs {
		suffix := suffixForInput(input, suffixes)
		fragment, err := renderK8sRouteFragment(input, suffix)
		if err != nil {
			return nil, nil, err
		}
		fileID := serviceConfigFileID(input, suffix)
		path := "services/" + fileID + ".yaml"
		files[path] = fragment
		refs = append(refs, LogCollectorConfigFile{
			Path:          path,
			ConfigMapName: k8sServiceConfigMapName(k8sLogsAgentName, fileID),
			Role:          "service",
			RouteID:       input.Route.ID,
			ServiceID:     input.Route.ServiceID,
		})
	}
	return files, refs, nil
}

func renderK8sBaseCollectorConfig(deployment agentDeploymentOptions, processorPatch string) string {
	opAMPBlock := ""
	if opAMPEnabled(deployment) {
		opAMPBlock = `  opamp:
    server:
      ws:
        endpoint: ${env:NOVAAPM_OPAMP_ENDPOINT}
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
receivers: {}
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
%s  batch:
exporters: {}
service:
  extensions: [%s]
  telemetry:
    resource:
      service.name: novaapm-logs-agent
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
  pipelines: {}`, opAMPBlock, patchBlock, renderServiceExtensions(deployment))
}

func renderK8sCollectorConfig(inputs []renderInput, processorPatch string) (string, error) {
	if len(inputs) == 0 {
		return "", nil
	}
	first := inputs[0]
	suffixes := routeComponentSuffixes(inputs)
	fragmentBundle, err := collectK8sRouteFragments(inputs, suffixes)
	if err != nil {
		return "", err
	}
	opAMPBlock := ""
	if opAMPEnabled(first.Deployment) {
		opAMPBlock = `  opamp:
    server:
      ws:
        endpoint: ${env:NOVAAPM_OPAMP_ENDPOINT}
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
      service.name: novaapm-logs-agent
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
		indentYAMLBlock(fragmentBundle.receivers, "  "),
		indentYAMLBlock(fragmentBundle.processors, "  "),
		patchBlock,
		indentYAMLBlock(fragmentBundle.exporters, "  "),
		renderServiceExtensions(first.Deployment),
		indentYAMLBlock(fragmentBundle.pipelines, "    "),
	), nil
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
      - "/var/log/pods/*_novaapm-logs-agent-*_*/*/*.log"
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

type k8sRouteFragmentBundle struct {
	receivers  string
	processors string
	exporters  string
	pipelines  string
}

type k8sRouteFragmentSections struct {
	receivers  map[string]string
	processors map[string]string
	exporters  map[string]string
	pipelines  map[string]string
}

func collectK8sRouteFragments(inputs []renderInput, suffixes map[string]string) (k8sRouteFragmentBundle, error) {
	merged := k8sRouteFragmentSections{
		receivers:  map[string]string{},
		processors: map[string]string{},
		exporters:  map[string]string{},
		pipelines:  map[string]string{},
	}
	for _, input := range inputs {
		suffix := suffixForInput(input, suffixes)
		fragment, err := renderK8sRouteFragment(input, suffix)
		if err != nil {
			return k8sRouteFragmentBundle{}, err
		}
		sections, err := parseK8sRouteFragment(fragment)
		if err != nil {
			return k8sRouteFragmentBundle{}, err
		}
		mergeFragmentMap(merged.receivers, sections.receivers)
		mergeFragmentMap(merged.processors, sections.processors)
		mergeFragmentMap(merged.exporters, sections.exporters)
		mergeFragmentMap(merged.pipelines, sections.pipelines)
	}
	return k8sRouteFragmentBundle{
		receivers:  renderFragmentMap(merged.receivers),
		processors: renderFragmentMap(merged.processors),
		exporters:  renderFragmentMap(merged.exporters),
		pipelines:  renderFragmentMap(merged.pipelines),
	}, nil
}

func renderK8sRouteFragment(input renderInput, suffix string) (string, error) {
	fragment := strings.TrimSpace(input.Source.CollectorFragmentYAML)
	if fragment == "" {
		fragment = renderDefaultK8sRouteFragment(input, suffix)
	} else if input.Endpoint.SinkType == EndpointSinkVL && input.Endpoint.AccountID != "" {
		root, err := parseCollectorYAML(fragment)
		if err != nil {
			return "", fmt.Errorf("collector fragment 必须是合法 YAML: %w", err)
		}
		if err := validateVictoriaLogsCollectorTenant(yamlMappingValue(root, "exporters"), input.Endpoint); err != nil {
			return "", err
		}
	}
	if _, err := parseK8sRouteFragment(fragment); err != nil {
		return "", err
	}
	return strings.TrimSpace(fragment), nil
}

func renderDefaultK8sRouteFragment(input renderInput, suffix string) string {
	return strings.Join([]string{
		"receivers:",
		indentYAMLBlock(renderK8sReceiver(input, suffix), "  "),
		"processors:",
		indentYAMLBlock(renderK8sRouteProcessor(input, suffix), "  "),
		"exporters:",
		indentYAMLBlock(renderDownstreamExporterYAML(input.Endpoint, downstreamExporterName(input.Endpoint)), "  "),
		"service:",
		"  pipelines:",
		indentYAMLBlock(renderK8sPipeline(input, suffix), "    "),
	}, "\n")
}

func parseK8sRouteFragment(raw string) (k8sRouteFragmentSections, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return k8sRouteFragmentSections{}, fmt.Errorf("collector fragment 必须是合法 YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return k8sRouteFragmentSections{}, fmt.Errorf("collector fragment root 必须是 YAML mapping")
	}
	root := doc.Content[0]
	sections := k8sRouteFragmentSections{
		receivers:  yamlSectionEntries(root, "receivers"),
		processors: yamlSectionEntries(root, "processors"),
		exporters:  yamlSectionEntries(root, "exporters"),
		pipelines:  yamlSectionEntries(root, "service", "pipelines"),
	}
	for _, name := range []string{"memory_limiter", "k8s_attributes", "batch"} {
		if _, ok := sections.processors[name]; ok {
			return k8sRouteFragmentSections{}, fmt.Errorf("collector fragment 不能定义公共 processor %q，请只编辑业务 route 片段", name)
		}
	}
	if len(sections.receivers) == 0 || len(sections.pipelines) == 0 {
		return k8sRouteFragmentSections{}, fmt.Errorf("collector fragment 必须包含 receivers 和 service.pipelines")
	}
	return sections, nil
}

func yamlSectionEntries(root *yaml.Node, path ...string) map[string]string {
	node := yamlNestedMapping(root, path...)
	if node == nil || node.Kind != yaml.MappingNode {
		return map[string]string{}
	}
	out := map[string]string{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		pair := yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{key, value}}
		rendered, err := yaml.Marshal(&pair)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(key.Value)] = strings.TrimSpace(string(rendered))
	}
	return out
}

func yamlNestedMapping(root *yaml.Node, path ...string) *yaml.Node {
	current := root
	for _, key := range path {
		if current == nil || current.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(current.Content); i += 2 {
			if current.Content[i].Value == key {
				next = current.Content[i+1]
				break
			}
		}
		current = next
	}
	if current == nil || current.Kind != yaml.MappingNode {
		return nil
	}
	return current
}

func mergeFragmentMap(dst map[string]string, src map[string]string) {
	for key, value := range src {
		if key == "" || strings.TrimSpace(value) == "" {
			continue
		}
		dst[key] = value
	}
}

func renderFragmentMap(items map[string]string) string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, items[key])
	}
	return strings.Join(lines, "\n")
}

func renderK8sRouteProcessors(inputs []renderInput, suffixes map[string]string) string {
	lines := []string{}
	for _, input := range inputs {
		suffix := suffixForInput(input, suffixes)
		lines = append(lines, renderK8sRouteProcessor(input, suffix))
	}
	return strings.Join(lines, "\n")
}

func renderK8sRouteProcessor(input renderInput, suffix string) string {
	lines := []string{fmt.Sprintf(`  resource/%s:
    attributes:
      - key: service.name
        value: %s
        action: upsert
      - key: novaapm.service_id
        value: %s
        action: upsert
      - key: deployment.environment
        value: %s
        action: upsert`, suffix, yamlQuote(input.ServiceName), yamlQuote(input.Route.ServiceID), yamlQuote(input.Environment))}
	if hasEnabledParseRules(input.Source.ParseRules) {
		lines = append(lines, renderK8sParseProcessor(suffix, input.Source.ParseRules))
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
		lines = append(lines, renderK8sPipeline(input, suffix))
	}
	return strings.Join(lines, "\n")
}

func renderK8sPipeline(input renderInput, suffix string) string {
	processors := []string{"memory_limiter", "k8s_attributes", "resource/" + suffix}
	if hasEnabledParseRules(input.Source.ParseRules) {
		processors = append(processors, "transform/"+suffix)
	}
	processors = append(processors, "batch")
	return fmt.Sprintf(`    logs/%s:
      receivers: [file_log/%s]
      processors: [%s]
      exporters: [%s]`, suffix, suffix, strings.Join(processors, ", "), downstreamExporterName(input.Endpoint))
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
	pipelineProcessors := "resource/novaapm, batch"
	if len(source.ParseRules) > 0 {
		parseProcessor = renderVMParseProcessor(source.ParseRules)
		pipelineProcessors = "transform/novaapm_parse, resource/novaapm, batch"
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
  resource/novaapm:
    attributes:
      - key: service.name
        value: %s
        action: upsert
      - key: novaapm.service_id
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
		yamlQuote(input.Route.ServiceID),
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
	case EndpointSinkOTel:
		return "otlp_http/" + suffix
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
		topic := firstNonEmpty(endpoint.StreamName, "novaapm-logs")
		lines = append(lines, "    topic: "+yamlQuote(topic))
		return strings.Join(lines, "\n")
	case EndpointSinkOTel:
		return strings.Join([]string{
			"  " + name + ":",
			"    logs_endpoint: " + yamlQuote(endpoint.WriteURL),
		}, "\n")
	default:
		lines := []string{
			"  " + name + ":",
			"    logs_endpoint: " + yamlQuote(endpoint.WriteURL),
		}
		if endpoint.AccountID != "" && endpoint.ProjectID != "" {
			lines = append(lines,
				"    headers:",
				"      AccountID: "+yamlQuote(endpoint.AccountID),
				"      ProjectID: "+yamlQuote(endpoint.ProjectID),
			)
		}
		return strings.Join(lines, "\n")
	}
}

func renderVMParseProcessor(rules []LogParseRule) string {
	lines := []string{
		"  transform/novaapm_parse:",
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

func hashConfigFiles(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write([]byte(files[key]))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

func placeholderConfigFiles(files map[string]string) map[string]string {
	out := map[string]string{}
	for path := range files {
		if path == "base.yaml" {
			out[path] = "<collector-base-config-managed-by-version>"
			continue
		}
		out[path] = "<collector-service-config-managed-by-version>"
	}
	return out
}

func k8sConfigMapTemplateDataList(files map[string]string, refs []LogCollectorConfigFile) []k8sConfigMapTemplateData {
	ordered := sortedConfigFileRefs(refs)
	out := make([]k8sConfigMapTemplateData, 0, len(ordered))
	for _, ref := range ordered {
		content := strings.TrimRight(files[ref.Path], "\n")
		dataKey := "fragment.yaml"
		if ref.Role == "base" {
			dataKey = "base.yaml"
		}
		out = append(out, k8sConfigMapTemplateData{
			Name:         ref.ConfigMapName,
			Role:         ref.Role,
			DataKey:      dataKey,
			MountPath:    ref.Path,
			ContentBlock: indentYAMLBlock(content, "    "),
			ConfigHash:   hashYAML(content),
			RouteID:      ref.RouteID,
			ServiceID:    ref.ServiceID,
		})
	}
	return out
}

func k8sConfigArgs(refs []LogCollectorConfigFile) []string {
	ordered := sortedConfigFileRefs(refs)
	args := make([]string, 0, len(ordered))
	for _, ref := range ordered {
		args = append(args, "--config=/conf/"+ref.Path)
	}
	return args
}

func sortedConfigFileRefs(refs []LogCollectorConfigFile) []LogCollectorConfigFile {
	ordered := append([]LogCollectorConfigFile{}, refs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Role == "base" && ordered[j].Role != "base" {
			return true
		}
		if ordered[i].Role != "base" && ordered[j].Role == "base" {
			return false
		}
		return ordered[i].Path < ordered[j].Path
	})
	return ordered
}

func serviceConfigFileID(input renderInput, suffix string) string {
	parts := []string{"svc", safeSegment(suffix)}
	if routeID := shortComponentID(input.Route.ID); routeID != "" {
		parts = append(parts, routeID)
	}
	return k8sResourceName(strings.Join(parts, "-"))
}

func k8sBaseConfigMapName(agentName string) string {
	return k8sResourceName(agentName + "-base-config")
}

func k8sServiceConfigMapName(agentName string, fileID string) string {
	return k8sResourceName(agentName + "-" + fileID)
}

func k8sResourceName(value string) string {
	value = safeSegment(value)
	if len(value) <= 253 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := "-" + hex.EncodeToString(sum[:])[:10]
	prefix := strings.Trim(value[:253-len(suffix)], "-")
	if prefix == "" {
		prefix = "novaapm"
	}
	return prefix + suffix
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
