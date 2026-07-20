package collectorconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceAttributes struct {
	ID            string
	Name          string
	CMDBServiceID string
	BusinessID    string
	ApplicationID string
	IdentityType  string
	EnvironmentID string
	Cluster       string
	Namespace     string
	OwnerTeam     string
	AlertRoute    string
}

func ValidateSources(sources ConfigSources) ValidationResult {
	rendered, warnings, breakdown, err := RenderSources(sources)
	result := ValidationResult{
		Valid:           err == nil,
		RenderedYAML:    rendered,
		Warnings:        warnings,
		SourceBreakdown: breakdown,
		Errors:          []string{},
	}
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.ConfigHash = HashYAML(rendered)
	return result
}

func RenderSources(sources ConfigSources) (string, []string, []SourceBreakdown, error) {
	warnings := append([]string{}, sources.Warnings...)
	breakdown := []SourceBreakdown{}
	rendered := ""
	if sources.PlatformTemplate != nil && strings.TrimSpace(sources.PlatformTemplate.BaseYAML) != "" {
		warnings = append(warnings, platformTemplateWarnings(sources.PlatformTemplate.BaseYAML)...)
		breakdown = append(breakdown, SourceBreakdown{
			Type:   "platform_template",
			ID:     sources.PlatformTemplate.ID,
			Name:   sources.PlatformTemplate.Name,
			Status: sources.PlatformTemplate.Status,
			Hash:   firstNonEmpty(sources.PlatformTemplate.ConfigHash, HashYAML(sources.PlatformTemplate.BaseYAML)),
		})
		rendered = strings.TrimSpace(sources.PlatformTemplate.BaseYAML)
	}
	parts := []string{}
	for _, patch := range sources.ServiceEnrichmentPatches {
		if strings.TrimSpace(patch.PatchYAML) == "" {
			continue
		}
		parts = append(parts, patch.PatchYAML)
		breakdown = append(breakdown, SourceBreakdown{
			Type:     "service_enrichment",
			ID:       patch.ID,
			Name:     patch.ServiceID,
			Status:   patch.Status,
			Hash:     firstNonEmpty(patch.ConfigHash, HashYAML(patch.PatchYAML)),
			Warnings: append([]string{}, patch.Warnings...),
		})
		warnings = append(warnings, patch.Warnings...)
	}
	for _, patch := range sources.ServicePipelinePatches {
		if !patch.Enabled || strings.TrimSpace(patch.PatchYAML) == "" {
			continue
		}
		parts = append(parts, patch.PatchYAML)
		breakdown = append(breakdown, SourceBreakdown{
			Type:   "service_pipeline_patch",
			ID:     patch.ID,
			Name:   patch.ServiceID,
			Status: patch.Status,
			Hash:   firstNonEmpty(patch.ConfigHash, HashYAML(patch.PatchYAML)),
		})
	}
	var err error
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if rendered == "" {
			rendered = part
			continue
		}
		rendered, err = MergeYAMLDocuments(rendered, part)
		if err != nil {
			return "", warnings, breakdown, err
		}
	}
	if strings.TrimSpace(rendered) == "" {
		return "", warnings, breakdown, fmt.Errorf("没有可发布的服务规则")
	}
	rendered, err = normalizeProcessorChain(rendered)
	if err != nil {
		return "", warnings, breakdown, err
	}
	return rendered, warnings, breakdown, nil
}

func platformTemplateWarnings(baseYAML string) []string {
	warnings := []string{}
	doc, err := parseYAMLDocument(baseYAML)
	if err != nil || len(doc.Content) == 0 {
		return warnings
	}
	root := doc.Content[0]
	extensions := yamlMappingValue(root, "extensions")
	if extensions == nil || extensions.Kind != yaml.MappingNode {
		return warnings
	}
	for i := 0; i+1 < len(extensions.Content); i += 2 {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(extensions.Content[i].Value)), "opamp") {
			warnings = append(warnings, "平台模板包含 opamp extension，生产建议只放在本地 bootstrap 配置中")
			return warnings
		}
	}
	return warnings
}

func PreviewParser(request ParserPreviewRequest) ParserPreviewResult {
	mode := firstNonEmpty(strings.TrimSpace(request.ParseMode), "none")
	result := ParserPreviewResult{
		Valid:            true,
		ParseMode:        mode,
		SampleLog:        request.SampleLog,
		ParsedFields:     map[string]any{},
		MappedAttributes: map[string]any{},
		MappedResources:  map[string]any{},
		UnmappedFields:   []string{},
		Warnings:         []string{},
		Errors:           []string{},
	}
	switch mode {
	case "none":
		return result
	case "regex":
		regex, err := regexp.Compile(request.RegexPattern)
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("正则表达式无效: %v", err))
			return result
		}
		matches := regex.FindStringSubmatch(request.SampleLog)
		if matches == nil {
			result.Valid = false
			result.Errors = append(result.Errors, "sample log 未匹配正则")
			return result
		}
		for i, name := range regex.SubexpNames() {
			if i == 0 || name == "" {
				continue
			}
			result.ParsedFields[name] = matches[i]
		}
		applyParserMappings(&result, request.AttributeMappings, request.ResourceMappings, request.JSONMappings)
		return result
	case "json":
		if strings.TrimSpace(request.SampleLog) == "" {
			return result
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(request.SampleLog), &parsed); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("sample log JSON 无效: %v", err))
			return result
		}
		result.ParsedFields = parsed
		applyParserMappings(&result, request.AttributeMappings, request.ResourceMappings, request.JSONMappings)
		return result
	case "ottl":
		if len(request.OTTLStatements) == 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "ottl 模式至少需要一条 statement")
		}
		return result
	default:
		result.Valid = false
		result.Errors = append(result.Errors, "parse_mode 只支持 none、json、regex 或 ottl")
		return result
	}
}

func applyParserMappings(result *ParserPreviewResult, attributeMappings map[string]string, resourceMappings map[string]string, legacyJSONMappings map[string]string) {
	effectiveAttributes := mergeMappings(legacyJSONMappings, attributeMappings)
	used := map[string]struct{}{}
	for source, target := range effectiveAttributes {
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source == "" || target == "" {
			continue
		}
		value, ok := lookupParsedField(result.ParsedFields, source)
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("字段 %s 未在样例日志中提取到，未映射到 %s", source, target))
			continue
		}
		result.MappedAttributes[target] = value
		used[source] = struct{}{}
	}
	for source, target := range resourceMappings {
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source == "" || target == "" {
			continue
		}
		value, ok := lookupParsedField(result.ParsedFields, source)
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("字段 %s 未在样例日志中提取到，未映射到 resource.%s", source, target))
			continue
		}
		result.MappedResources[target] = value
		used[source] = struct{}{}
	}
	for field := range result.ParsedFields {
		if _, ok := used[field]; !ok {
			result.UnmappedFields = append(result.UnmappedFields, field)
		}
	}
	sort.Strings(result.UnmappedFields)
	sort.Strings(result.Warnings)
}

func mergeMappings(legacy map[string]string, current map[string]string) map[string]string {
	merged := map[string]string{}
	for source, target := range legacy {
		merged[source] = target
	}
	for source, target := range current {
		merged[source] = target
	}
	return merged
}

func lookupParsedField(fields map[string]any, source string) (any, bool) {
	if value, ok := fields[source]; ok {
		return value, true
	}
	parts := strings.Split(source, ".")
	var current any = fields
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func BuildEnrichmentPatch(attrs ServiceAttributes, collectorGroupID string) ServiceEnrichmentPatch {
	warnings := []string{}
	statements := []string{}
	add := func(field string, value string, warning string) {
		value = strings.TrimSpace(value)
		if value == "" {
			if warning != "" {
				warnings = append(warnings, warning)
			}
			return
		}
		statements = append(statements, fmt.Sprintf("          - set(attributes[%q], %q)", field, value))
	}
	add("service.name", attrs.Name, "service.name 为空，未生成 service.name enrichment")
	add("deployment.environment", attrs.EnvironmentID, "deployment.environment 为空，未生成 deployment.environment enrichment")
	add("cmdb.service_id", attrs.CMDBServiceID, "")
	add("business_id", attrs.BusinessID, "")
	add("application_id", attrs.ApplicationID, "")
	add("service.runtime.type", attrs.IdentityType, "service.runtime.type 为空，未生成 service.runtime.type enrichment")
	add("k8s.cluster.name", attrs.Cluster, "k8s.cluster.name 为空，未生成 k8s.cluster.name enrichment")
	add("k8s.namespace.name", attrs.Namespace, "k8s.namespace.name 为空，未生成 k8s.namespace.name enrichment")
	add("owner_team", attrs.OwnerTeam, "owner_team 为空，未生成 owner_team enrichment")
	add("alert_route", attrs.AlertRoute, "alert_route 为空，未生成 alert_route enrichment")

	processor := processorName("transform/enrich", attrs.ID)
	var builder strings.Builder
	if len(statements) > 0 {
		builder.WriteString("processors:\n")
		builder.WriteString(fmt.Sprintf("  %s:\n", processor))
		builder.WriteString("    log_statements:\n")
		builder.WriteString("      - context: log\n")
		builder.WriteString("        statements:\n")
		for _, statement := range statements {
			builder.WriteString(statement)
			builder.WriteByte('\n')
		}
		builder.WriteString("service:\n")
		builder.WriteString("  pipelines:\n")
		builder.WriteString("    logs:\n")
		builder.WriteString(fmt.Sprintf("      processors: [%s]\n", processor))
	}
	body := strings.TrimSpace(builder.String())
	return ServiceEnrichmentPatch{
		ServiceID:        attrs.ID,
		CollectorGroupID: collectorGroupID,
		PatchYAML:        body,
		ConfigHash:       HashYAML(body),
		Warnings:         warnings,
		Status:           "generated",
	}
}

func BuildPipelinePatch(rule ServiceParserRule) (ServicePipelinePatch, error) {
	mode := firstNonEmpty(strings.TrimSpace(rule.ParseMode), "none")
	if mode == "none" || !rule.Enabled {
		return ServicePipelinePatch{
			ServiceID:        rule.ServiceID,
			CollectorGroupID: rule.CollectorGroupID,
			ParserRuleID:     rule.ID,
			Status:           "skipped",
			Enabled:          false,
			Version:          rule.Version,
		}, nil
	}
	preview := PreviewParser(ParserPreviewRequest{
		ParseMode:         mode,
		ParseFrom:         rule.ParseFrom,
		RegexPattern:      rule.RegexPattern,
		JSONMappings:      rule.JSONMappings,
		AttributeMappings: rule.AttributeMappings,
		ResourceMappings:  rule.ResourceMappings,
		OTTLStatements:    rule.OTTLStatements,
		SampleLog:         rule.SampleLog,
	})
	if !preview.Valid && mode == "regex" {
		return ServicePipelinePatch{}, fmt.Errorf("%s", strings.Join(preview.Errors, "; "))
	}
	switch mode {
	case "regex":
		body := buildParserTransformPatch(rule.ServiceID, []string{
			fmt.Sprintf(`merge_maps(attributes, ExtractPatterns(%s, %q), "upsert")`, firstNonEmpty(strings.TrimSpace(rule.ParseFrom), "body"), rule.RegexPattern),
		}, ruleMappings(rule), rule.OTTLStatements)
		return ServicePipelinePatch{
			ServiceID:        rule.ServiceID,
			CollectorGroupID: rule.CollectorGroupID,
			ParserRuleID:     rule.ID,
			PatchYAML:        body,
			ConfigHash:       HashYAML(body),
			Status:           "generated",
			Enabled:          true,
			Version:          rule.Version,
		}, nil
	case "json":
		body := buildParserTransformPatch(rule.ServiceID, []string{
			fmt.Sprintf(`merge_maps(attributes, ParseJSON(%s), "upsert")`, firstNonEmpty(strings.TrimSpace(rule.ParseFrom), "body")),
		}, ruleMappings(rule), rule.OTTLStatements)
		return ServicePipelinePatch{
			ServiceID:        rule.ServiceID,
			CollectorGroupID: rule.CollectorGroupID,
			ParserRuleID:     rule.ID,
			PatchYAML:        body,
			ConfigHash:       HashYAML(body),
			Status:           "generated",
			Enabled:          true,
			Version:          rule.Version,
		}, nil
	case "ottl":
		processor := processorName("transform/parser", rule.ServiceID)
		var builder strings.Builder
		builder.WriteString("processors:\n")
		builder.WriteString(fmt.Sprintf("  %s:\n", processor))
		builder.WriteString("    log_statements:\n")
		builder.WriteString("      - context: log\n")
		builder.WriteString("        statements:\n")
		for _, statement := range rule.OTTLStatements {
			builder.WriteString(fmt.Sprintf("          - %s\n", quoteYAMLScalar(statement)))
		}
		builder.WriteString("service:\n")
		builder.WriteString("  pipelines:\n")
		builder.WriteString("    logs:\n")
		builder.WriteString(fmt.Sprintf("      processors: [%s]\n", processor))
		body := strings.TrimSpace(builder.String())
		return ServicePipelinePatch{
			ServiceID:        rule.ServiceID,
			CollectorGroupID: rule.CollectorGroupID,
			ParserRuleID:     rule.ID,
			PatchYAML:        body,
			ConfigHash:       HashYAML(body),
			Status:           "generated",
			Enabled:          true,
			Version:          rule.Version,
		}, nil
	default:
		return ServicePipelinePatch{}, fmt.Errorf("parse_mode 只支持 none、json、regex 或 ottl")
	}
}

func buildParserTransformPatch(serviceID string, parserStatements []string, mappings parserMappings, postStatements []string) string {
	processor := processorName("transform/parser", serviceID)
	statements := append([]string{}, parserStatements...)
	statements = append(statements, mappingTransformStatements(mappings)...)
	statements = appendOTTLStatements(statements, postStatements)
	var builder strings.Builder
	builder.WriteString("processors:\n")
	builder.WriteString(fmt.Sprintf("  %s:\n", processor))
	builder.WriteString("    log_statements:\n")
	builder.WriteString("      - context: log\n")
	builder.WriteString("        statements:\n")
	for _, statement := range statements {
		builder.WriteString(fmt.Sprintf("          - %s\n", quoteYAMLScalar(statement)))
	}
	builder.WriteString("service:\n")
	builder.WriteString("  pipelines:\n")
	builder.WriteString("    logs:\n")
	builder.WriteString(fmt.Sprintf("      processors: [%s]\n", processor))
	return strings.TrimSpace(builder.String())
}

func appendOTTLStatements(statements []string, ottlStatements []string) []string {
	result := append([]string{}, statements...)
	for _, statement := range ottlStatements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		result = append(result, statement)
	}
	return result
}

func mappingTransformStatements(mappings parserMappings) []string {
	statements := []string{}
	keys := make([]string, 0, len(mappings.Attributes))
	for source := range mappings.Attributes {
		keys = append(keys, source)
	}
	sort.Strings(keys)
	for _, source := range keys {
		target := strings.TrimSpace(mappings.Attributes[source])
		source = strings.TrimSpace(source)
		if source == "" || target == "" {
			continue
		}
		statements = append(statements, fmt.Sprintf("set(attributes[%q], attributes[%q]) where attributes[%q] != nil", target, source, source))
	}
	keys = make([]string, 0, len(mappings.Resources))
	for source := range mappings.Resources {
		keys = append(keys, source)
	}
	sort.Strings(keys)
	for _, source := range keys {
		target := strings.TrimSpace(mappings.Resources[source])
		source = strings.TrimSpace(source)
		if source == "" || target == "" {
			continue
		}
		statements = append(statements, fmt.Sprintf("set(resource.attributes[%q], attributes[%q]) where attributes[%q] != nil", target, source, source))
	}
	return statements
}

type parserMappings struct {
	Attributes map[string]string
	Resources  map[string]string
}

func ruleMappings(rule ServiceParserRule) parserMappings {
	return parserMappings{
		Attributes: mergeMappings(rule.JSONMappings, rule.AttributeMappings),
		Resources:  rule.ResourceMappings,
	}
}

func buildFilelogOperatorPatch(receiver string, fields map[string]string, mappings parserMappings) string {
	var builder strings.Builder
	builder.WriteString("receivers:\n")
	builder.WriteString(fmt.Sprintf("  %s:\n", receiver))
	builder.WriteString("    operators:\n")
	builder.WriteString("      - ")
	ordered := []string{"id", "type", "regex", "parse_from"}
	first := true
	for _, key := range ordered {
		value := strings.TrimSpace(fields[key])
		if value == "" {
			continue
		}
		if first {
			builder.WriteString(fmt.Sprintf("%s: %s\n", key, operatorScalar(key, value)))
			first = false
			continue
		}
		builder.WriteString(fmt.Sprintf("        %s: %s\n", key, operatorScalar(key, value)))
	}
	writeMoveOperators(&builder, mappings)
	return strings.TrimSpace(builder.String())
}

func writeMoveOperators(builder *strings.Builder, mappings parserMappings) {
	writeMappingMoveOperators(builder, mappings.Attributes, "attributes", stanzaAttributePath, stanzaAttributeTargetPath)
	writeMappingMoveOperators(builder, mappings.Resources, "resource", stanzaAttributePath, stanzaResourceTargetPath)
}

func writeMappingMoveOperators(builder *strings.Builder, mappings map[string]string, prefix string, sourcePath func(string) string, targetPath func(string) string) {
	keys := make([]string, 0, len(mappings))
	for source := range mappings {
		keys = append(keys, source)
	}
	sort.Strings(keys)
	for _, source := range keys {
		target := strings.TrimSpace(mappings[source])
		source = strings.TrimSpace(source)
		if source == "" || target == "" {
			continue
		}
		builder.WriteString("      - ")
		builder.WriteString(fmt.Sprintf("id: %s\n", quoteYAMLScalar(safeOperatorID(fmt.Sprintf("map_%s_%s_to_%s", prefix, source, target)))))
		builder.WriteString("        type: move\n")
		builder.WriteString(fmt.Sprintf("        from: %s\n", sourcePath(source)))
		builder.WriteString(fmt.Sprintf("        to: %s\n", targetPath(target)))
	}
}

func stanzaAttributePath(name string) string {
	name = strings.TrimSpace(name)
	if regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
		return "attributes." + name
	}
	return fmt.Sprintf("attributes[%q]", name)
}

func stanzaAttributeTargetPath(name string) string {
	return fmt.Sprintf("attributes[%q]", strings.TrimSpace(name))
}

func stanzaResourceTargetPath(name string) string {
	return fmt.Sprintf("resource[%q]", strings.TrimSpace(name))
}

func safeOperatorID(value string) string {
	replacer := strings.NewReplacer(".", "_", "/", "_", "[", "_", "]", "_", "\"", "", " ", "_", "-", "_")
	return replacer.Replace(value)
}

func operatorScalar(key string, value string) string {
	switch key {
	case "type", "parse_from":
		return value
	default:
		return quoteYAMLScalar(value)
	}
}

func MergeYAMLDocuments(baseYAML string, patchYAML string) (string, error) {
	baseDoc, err := parseYAMLDocument(baseYAML)
	if err != nil {
		return "", err
	}
	patchDoc, err := parseYAMLDocument(patchYAML)
	if err != nil {
		return "", err
	}
	if len(baseDoc.Content) == 0 {
		return strings.TrimSpace(patchYAML), nil
	}
	if len(patchDoc.Content) == 0 {
		return strings.TrimSpace(baseYAML), nil
	}
	merged := cloneYAMLNode(baseDoc.Content[0])
	mergeYAMLNode(merged, patchDoc.Content[0])
	outDoc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{merged}}
	data, err := yaml.Marshal(&outDoc)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func normalizeProcessorChain(body string) (string, error) {
	doc, err := parseYAMLDocument(body)
	if err != nil || len(doc.Content) == 0 {
		return body, err
	}
	root := doc.Content[0]
	pipelines := mappingValue(root, "service", "pipelines")
	if pipelines == nil || pipelines.Kind != yaml.MappingNode {
		return strings.TrimSpace(body), nil
	}
	for i := 0; i+1 < len(pipelines.Content); i += 2 {
		pipeline := pipelines.Content[i+1]
		if pipeline.Kind != yaml.MappingNode {
			continue
		}
		receivers := yamlMappingValue(pipeline, "receivers")
		if receivers != nil && receivers.Kind == yaml.SequenceNode {
			receivers.Content = scalarNodes(uniqueScalarNames(receivers))
		}
		processors := yamlMappingValue(pipeline, "processors")
		if processors != nil && processors.Kind == yaml.SequenceNode {
			processors.Content = scalarNodes(sortProcessors(uniqueScalarNames(processors)))
		}
		exporters := yamlMappingValue(pipeline, "exporters")
		if exporters != nil && exporters.Kind == yaml.SequenceNode {
			exporters.Content = scalarNodes(uniqueScalarNames(exporters))
		}
	}
	outDoc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	data, err := yaml.Marshal(&outDoc)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func uniqueScalarNames(node *yaml.Node) []string {
	seen := map[string]struct{}{}
	names := []string{}
	for _, item := range node.Content {
		name := strings.TrimSpace(item.Value)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func sortProcessors(names []string) []string {
	sort.SliceStable(names, func(i, j int) bool {
		return processorRank(names[i]) < processorRank(names[j])
	})
	return names
}

func processorRank(name string) int {
	switch {
	case name == "memory_limiter":
		return 10
	case name == "k8sattributes":
		return 20
	case strings.HasPrefix(name, "filter/"):
		return 30
	case strings.HasPrefix(name, "transform/enrich"):
		return 40
	case strings.HasPrefix(name, "transform/parser"):
		return 50
	case strings.HasPrefix(name, "transform/"):
		return 60
	case name == "batch":
		return 90
	default:
		return 70
	}
}

func scalarNodes(names []string) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name})
	}
	return nodes
}

func writeStringList(builder *strings.Builder, key string, values []string) {
	cleaned := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return
	}
	builder.WriteString(key)
	builder.WriteString(":\n")
	for _, value := range cleaned {
		builder.WriteString("      - ")
		builder.WriteString(quoteYAMLScalar(value))
		builder.WriteByte('\n')
	}
}

func writeReceiverResource(builder *strings.Builder, key string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	builder.WriteString(fmt.Sprintf("      %s: %s\n", quoteYAMLScalar(key), quoteYAMLScalar(value)))
}

func parseYAMLDocument(body string) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(body), &node); err != nil {
		return nil, err
	}
	return &node, nil
}

func mergeYAMLNode(base *yaml.Node, patch *yaml.Node) {
	if base.Kind == yaml.SequenceNode && patch.Kind == yaml.SequenceNode {
		base.Content = append(base.Content, cloneYAMLChildren(patch.Content)...)
		return
	}
	if base.Kind != yaml.MappingNode || patch.Kind != yaml.MappingNode {
		*base = *cloneYAMLNode(patch)
		return
	}
	for i := 0; i+1 < len(patch.Content); i += 2 {
		patchKey := patch.Content[i]
		patchValue := patch.Content[i+1]
		baseValue := yamlMappingValue(base, patchKey.Value)
		if baseValue == nil {
			base.Content = append(base.Content, cloneYAMLNode(patchKey), cloneYAMLNode(patchValue))
			continue
		}
		mergeYAMLNode(baseValue, patchValue)
	}
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mappingValue(node *yaml.Node, path ...string) *yaml.Node {
	current := node
	for _, key := range path {
		current = yamlMappingValue(current, key)
		if current == nil {
			return nil
		}
	}
	return current
}

func cloneYAMLChildren(nodes []*yaml.Node) []*yaml.Node {
	out := make([]*yaml.Node, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, cloneYAMLNode(node))
	}
	return out
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	if len(node.Content) > 0 {
		cloned.Content = cloneYAMLChildren(node.Content)
	}
	return &cloned
}

func processorName(prefix string, raw string) string {
	return prefix + "_" + safeComponentSuffix(raw)
}

func safeComponentSuffix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "unknown"
	}
	replacer := strings.NewReplacer("-", "_", ".", "_", "/", "_", ":", "_")
	return replacer.Replace(raw)
}

func quoteYAMLScalar(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func HashYAML(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
