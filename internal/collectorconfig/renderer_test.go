package collectorconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSourcesMergesServiceRulesWithoutPlatformTemplate(t *testing.T) {
	sources := ConfigSources{
		ServiceEnrichmentPatches: []ServiceEnrichmentPatch{{
			ID:        "enrich-1",
			ServiceID: "svc-1",
			PatchYAML: "processors:\n  transform/enrich_svc_1:\n    log_statements:\n      - context: log\n        statements:\n          - set(attributes[\"service.name\"], \"orders-api\")\nservice:\n  pipelines:\n    logs:\n      processors: [transform/enrich_svc_1]\n",
		}},
		ServicePipelinePatches: []ServicePipelinePatch{{
			ID:        "patch-1",
			ServiceID: "svc-1",
			PatchYAML: "processors:\n  transform/parser_svc_1:\n    log_statements:\n      - context: log\n        statements:\n          - set(attributes[\"parser\"], \"regex\")\nservice:\n  pipelines:\n    logs:\n      processors: [transform/parser_svc_1]\n",
			Enabled:   true,
		}},
	}

	result := ValidateSources(sources)

	require.True(t, result.Valid)
	require.Empty(t, result.Errors)
	require.Contains(t, result.RenderedYAML, "transform/enrich_svc_1")
	require.Contains(t, result.RenderedYAML, "transform/parser_svc_1")
	require.Contains(t, result.RenderedYAML, "processors: [transform/enrich_svc_1, transform/parser_svc_1]")
	require.NotEmpty(t, result.ConfigHash)
}

func TestRenderSourcesIgnoresDeprecatedGroupOverride(t *testing.T) {
	result := ValidateSources(ConfigSources{
		GroupOverride: &CollectorGroupOverride{
			ID:           "override-1",
			OverrideYAML: "processors:\n  batch:\n    timeout: 5s\n",
		},
		ServiceEnrichmentPatches: []ServiceEnrichmentPatch{{
			ID:        "enrich-1",
			ServiceID: "svc-1",
			PatchYAML: "processors:\n  transform/enrich_svc_1:\n    log_statements:\n      - context: log\n        statements:\n          - set(attributes[\"service.name\"], \"orders-api\")\nservice:\n  pipelines:\n    logs:\n      processors: [transform/enrich_svc_1]\n",
		}},
	})

	require.True(t, result.Valid)
	require.NotContains(t, result.RenderedYAML, "timeout: 5s")
	require.NotContains(t, result.RenderedYAML, "batch:")
	require.Contains(t, result.RenderedYAML, "transform/enrich_svc_1")
}

func TestValidateSourcesRejectsEmptyRuleSet(t *testing.T) {
	result := ValidateSources(ConfigSources{})

	require.False(t, result.Valid)
	require.Contains(t, result.Errors, "没有可发布的服务规则")
	require.Empty(t, result.RenderedYAML)
	require.Empty(t, result.ConfigHash)
}

func TestValidateSourcesWarnsWhenPlatformTemplateContainsBootstrapOpAMP(t *testing.T) {
	result := ValidateSources(ConfigSources{
		PlatformTemplate: &CollectorPlatformTemplate{
			ID:       "tmpl-1",
			Name:     "platform-base",
			BaseYAML: "extensions:\n  opamp:\n    server:\n      ws:\n        endpoint: ws://127.0.0.1:3001/api/v1/opamp/ws\nservice:\n  extensions: [opamp]\n  pipelines:\n    logs:\n      receivers: [otlp]\n      exporters: [debug]\n",
		},
	})

	require.True(t, result.Valid)
	require.Contains(t, result.Warnings, "平台模板包含 opamp extension，生产建议只放在本地 bootstrap 配置中")
}

func TestPreviewParserRegexExtractsNamedGroups(t *testing.T) {
	result := PreviewParser(ParserPreviewRequest{
		ParseMode:         "regex",
		RegexPattern:      `order_id=(?P<order_id>[\w-]+) user_id=(?P<user_id>[\w-]+)`,
		AttributeMappings: map[string]string{"order_id": "order.id"},
		ResourceMappings:  map[string]string{"user_id": "enduser.id"},
		SampleLog:         "INFO order_id=o-1 user_id=u-2 created",
	})

	require.True(t, result.Valid)
	require.Empty(t, result.Errors)
	require.Equal(t, "o-1", result.ParsedFields["order_id"])
	require.Equal(t, "u-2", result.ParsedFields["user_id"])
	require.Equal(t, "o-1", result.MappedAttributes["order.id"])
	require.Equal(t, "u-2", result.MappedResources["enduser.id"])
}

func TestPreviewParserRejectsInvalidRegex(t *testing.T) {
	result := PreviewParser(ParserPreviewRequest{
		ParseMode:    "regex",
		RegexPattern: "[",
		SampleLog:    "INFO broken",
	})

	require.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
}

func TestBuildEnrichmentPatchUsesOnlyRealServiceAttributes(t *testing.T) {
	patch := BuildEnrichmentPatch(ServiceAttributes{
		ID:          "svc-1",
		Name:        "orders-api",
		Environment: "production",
		Cluster:     "prod-1",
		Namespace:   "orders",
		OwnerTeam:   "",
		AlertRoute:  "",
	}, "group-1")

	require.Contains(t, patch.PatchYAML, `set(attributes["service.name"], "orders-api")`)
	require.Contains(t, patch.PatchYAML, `set(attributes["deployment.environment"], "production")`)
	require.NotContains(t, patch.PatchYAML, `owner_team`)
	require.NotContains(t, patch.PatchYAML, `alert_route`)
	require.Contains(t, patch.Warnings, "owner_team 为空，未生成 owner_team enrichment")
	require.Contains(t, patch.Warnings, "alert_route 为空，未生成 alert_route enrichment")
}

func TestBuildPipelinePatchRegexGeneratesTransformParserProcessor(t *testing.T) {
	patch, err := BuildPipelinePatch(ServiceParserRule{
		ID:               "rule-1",
		ServiceID:        "svc-1",
		CollectorGroupID: "group-1",
		ParseMode:        "regex",
		RegexPattern:     `^(?P<level>\w+) order_id=(?P<order_id>[\w-]+)$`,
		AttributeMappings: map[string]string{
			"level":    "log.level",
			"order_id": "order.id",
		},
		SampleLog: "INFO order_id=o-1",
		Enabled:   true,
		Version:   2,
	})

	require.NoError(t, err)
	require.True(t, patch.Enabled)
	require.Contains(t, patch.PatchYAML, "processors:")
	require.Contains(t, patch.PatchYAML, "transform/parser_svc_1")
	require.Contains(t, patch.PatchYAML, "ExtractPatterns(body")
	require.Contains(t, patch.PatchYAML, `set(attributes[\"log.level\"], attributes[\"level\"])`)
	require.Contains(t, patch.PatchYAML, `set(attributes[\"order.id\"], attributes[\"order_id\"])`)
	require.NotContains(t, patch.PatchYAML, "receivers:")
	require.NotContains(t, patch.PatchYAML, "filelog/")
	require.NotContains(t, patch.PatchYAML, "pipeline.parser.pattern")
}

func TestBuildPipelinePatchRegexComposesMappingsAndOTTLStatements(t *testing.T) {
	patch, err := BuildPipelinePatch(ServiceParserRule{
		ID:               "rule-1",
		ServiceID:        "svc-1",
		CollectorGroupID: "group-1",
		ParseMode:        "regex",
		RegexPattern:     `^(?P<level>\w+) host=(?P<host>[\w-]+)$`,
		AttributeMappings: map[string]string{
			"level": "log.level",
		},
		ResourceMappings: map[string]string{
			"host": "host.name",
		},
		OTTLStatements: []string{
			`set(attributes["log.category"], "network")`,
		},
		SampleLog: "INFO host=edge-1",
		Enabled:   true,
		Version:   2,
	})

	require.NoError(t, err)
	require.Contains(t, patch.PatchYAML, "ExtractPatterns(body")
	require.Contains(t, patch.PatchYAML, `set(attributes[\"log.level\"], attributes[\"level\"])`)
	require.Contains(t, patch.PatchYAML, `set(resource.attributes[\"host.name\"], attributes[\"host\"])`)
	require.Contains(t, patch.PatchYAML, `set(attributes[\"log.category\"], \"network\")`)
}

func TestBuildPipelinePatchJSONGeneratesTransformParserProcessor(t *testing.T) {
	patch, err := BuildPipelinePatch(ServiceParserRule{
		ID:               "rule-1",
		ServiceID:        "svc-1",
		CollectorGroupID: "group-1",
		ParseMode:        "json",
		AttributeMappings: map[string]string{
			"level":    "log.level",
			"order_id": "order.id",
		},
		SampleLog: `{"level":"info","order_id":"o-1"}`,
		Enabled:   true,
		Version:   1,
	})

	require.NoError(t, err)
	require.Contains(t, patch.PatchYAML, "ParseJSON(body)")
	require.Contains(t, patch.PatchYAML, `set(attributes[\"log.level\"], attributes[\"level\"])`)
	require.Contains(t, patch.PatchYAML, `set(attributes[\"order.id\"], attributes[\"order_id\"])`)
	require.NotContains(t, patch.PatchYAML, "receivers:")
	require.NotContains(t, patch.PatchYAML, "filelog/")
}
