package kubeclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResourceVersionResolverPrefersSupportedIstioCandidate(t *testing.T) {
	resolver := NewResourceVersionResolver(CapabilitySnapshot{
		Resources: []APIResource{
			{Group: "networking.istio.io", Version: "v1beta1", GroupVersion: "networking.istio.io/v1beta1", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
			{Group: "networking.istio.io", Version: "v1alpha3", GroupVersion: "networking.istio.io/v1alpha3", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
		},
	})

	resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: "networking.istio.io/v1alpha3", Kind: "VirtualService"})

	require.NoError(t, err)
	require.Equal(t, "networking.istio.io/v1beta1", resolved.APIVersion)
	require.Equal(t, "virtualservices", resolved.Resource)
	require.True(t, resolved.Namespaced)
}

func TestResourceVersionResolverFallsBackAcrossHPAAndIngressVersions(t *testing.T) {
	resolver := NewResourceVersionResolver(CapabilitySnapshot{
		Resources: []APIResource{
			{Group: "autoscaling", Version: "v1", GroupVersion: "autoscaling/v1", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
			{Group: "extensions", Version: "v1beta1", GroupVersion: "extensions/v1beta1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
		},
	})

	hpa, err := resolver.Resolve(ResourceVersionRequest{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"})
	require.NoError(t, err)
	require.Equal(t, "autoscaling/v1", hpa.APIVersion)
	require.Equal(t, "horizontalpodautoscalers", hpa.Resource)

	ingress, err := resolver.Resolve(ResourceVersionRequest{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"})
	require.NoError(t, err)
	require.Equal(t, "extensions/v1beta1", ingress.APIVersion)
	require.Equal(t, "ingresses", ingress.Resource)
}

func TestResourceVersionResolverMatchesExactDiscoveredResource(t *testing.T) {
	resolver := NewResourceVersionResolver(CapabilitySnapshot{
		Resources: []APIResource{
			{Group: "example.io", Version: "v1", GroupVersion: "example.io/v1", Resource: "widgets", Kind: "Widget", Namespaced: true},
		},
	})

	resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: "example.io/v1", Kind: "Widget"})

	require.NoError(t, err)
	require.Equal(t, "example.io", resolved.Group)
	require.Equal(t, "v1", resolved.Version)
	require.Equal(t, "widgets", resolved.Resource)
}

func TestResourceVersionResolverRejectsMissingOrUnsupportedResource(t *testing.T) {
	resolver := NewResourceVersionResolver(CapabilitySnapshot{})

	_, err := resolver.Resolve(ResourceVersionRequest{Kind: "VirtualService"})
	require.ErrorIs(t, err, ErrResourceVersionRequestInvalid)

	_, err = resolver.Resolve(ResourceVersionRequest{APIVersion: "networking.istio.io/v1", Kind: "VirtualService"})
	require.ErrorIs(t, err, ErrResourceVersionUnsupported)
}
