package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeTemplateTypeCoversStartorchResourceTypes(t *testing.T) {
	cases := map[string]string{
		"hpa":              "HorizontalPodAutoscaler",
		"gateways":         "Gateway",
		"virtualservices":  "VirtualService",
		"destinationrules": "DestinationRule",
		"envoyfilters":     "EnvoyFilter",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, want, normalizeTemplateType(input))
		})
	}
}

func TestBaseTemplateCoversStartorchTemplateTypes(t *testing.T) {
	cases := []string{
		"Deployment",
		"StatefulSet",
		"Service",
		"ConfigMap",
		"Ingress",
		"HorizontalPodAutoscaler",
		"Gateway",
		"VirtualService",
		"DestinationRule",
		"EnvoyFilter",
	}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			result, err := BaseTemplate(kind)
			require.NoError(t, err)
			require.Equal(t, normalizeTemplateType(kind), result.Type)
			require.Contains(t, result.YAMLContent, "kind: "+result.Type)
			require.NotEmpty(t, result.Variables)
			require.Equal(t, "novaapm-base", result.Source)
		})
	}
}
