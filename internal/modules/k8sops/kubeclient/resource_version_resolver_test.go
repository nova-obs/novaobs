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

func TestResourceVersionResolverCoversStartorchVersionCandidates(t *testing.T) {
	resolver := NewResourceVersionResolver(CapabilitySnapshot{
		Resources: []APIResource{
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "namespaces", Kind: "Namespace", Namespaced: false},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "serviceaccounts", Kind: "ServiceAccount", Namespaced: true},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "persistentvolumes", Kind: "PersistentVolume", Namespaced: false},
			{Group: "rbac.authorization.k8s.io", Version: "v1", GroupVersion: "rbac.authorization.k8s.io/v1", Resource: "clusterroles", Kind: "ClusterRole", Namespaced: false},
			{Group: "rbac.authorization.k8s.io", Version: "v1", GroupVersion: "rbac.authorization.k8s.io/v1", Resource: "clusterrolebindings", Kind: "ClusterRoleBinding", Namespaced: false},
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "replicasets", Kind: "ReplicaSet", Namespaced: true},
			{Group: "autoscaling", Version: "v1", GroupVersion: "autoscaling/v1", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
			{Group: "extensions", Version: "v1beta1", GroupVersion: "extensions/v1beta1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
			{Group: "networking.istio.io", Version: "v1alpha3", GroupVersion: "networking.istio.io/v1alpha3", Resource: "gateways", Kind: "Gateway", Namespaced: true},
			{Group: "networking.istio.io", Version: "v1alpha3", GroupVersion: "networking.istio.io/v1alpha3", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
			{Group: "networking.istio.io", Version: "v1alpha3", GroupVersion: "networking.istio.io/v1alpha3", Resource: "destinationrules", Kind: "DestinationRule", Namespaced: true},
			{Group: "networking.istio.io", Version: "v1alpha3", GroupVersion: "networking.istio.io/v1alpha3", Resource: "envoyfilters", Kind: "EnvoyFilter", Namespaced: true},
			{Group: "security.istio.io", Version: "v1beta1", GroupVersion: "security.istio.io/v1beta1", Resource: "peerauthentications", Kind: "PeerAuthentication", Namespaced: true},
			{Group: "security.istio.io", Version: "v1alpha1", GroupVersion: "security.istio.io/v1alpha1", Resource: "authorizationpolicies", Kind: "AuthorizationPolicy", Namespaced: true},
			{Group: "security.istio.io", Version: "v1", GroupVersion: "security.istio.io/v1", Resource: "requestauthentications", Kind: "RequestAuthentication", Namespaced: true},
			{Group: "authentication.istio.io", Version: "v1alpha1", GroupVersion: "authentication.istio.io/v1alpha1", Resource: "policies", Kind: "Policy", Namespaced: true},
			{Group: "authentication.istio.io", Version: "v1alpha1", GroupVersion: "authentication.istio.io/v1alpha1", Resource: "meshpolicies", Kind: "MeshPolicy", Namespaced: false},
			{Group: "rbac.istio.io", Version: "v1alpha1", GroupVersion: "rbac.istio.io/v1alpha1", Resource: "servicerolebindings", Kind: "ServiceRoleBinding", Namespaced: true},
			{Group: "rbac.istio.io", Version: "v1alpha1", GroupVersion: "rbac.istio.io/v1alpha1", Resource: "clusterrbacconfigs", Kind: "ClusterRbacConfig", Namespaced: false},
		},
	})
	cases := []struct {
		name       string
		apiVersion string
		kind       string
		want       string
	}{
		{name: "namespace", apiVersion: "v1", kind: "Namespace", want: "v1"},
		{name: "serviceaccount", apiVersion: "v1", kind: "ServiceAccount", want: "v1"},
		{name: "pvc", apiVersion: "v1", kind: "PersistentVolumeClaim", want: "v1"},
		{name: "pv", apiVersion: "v1", kind: "PersistentVolume", want: "v1"},
		{name: "clusterrole", apiVersion: "rbac.authorization.k8s.io/v1beta1", kind: "ClusterRole", want: "rbac.authorization.k8s.io/v1"},
		{name: "clusterrolebinding", apiVersion: "rbac.authorization.k8s.io/v1beta1", kind: "ClusterRoleBinding", want: "rbac.authorization.k8s.io/v1"},
		{name: "replicaset", apiVersion: "apps/v1", kind: "ReplicaSet", want: "apps/v1"},
		{name: "hpa", apiVersion: "autoscaling/v2", kind: "HorizontalPodAutoscaler", want: "autoscaling/v1"},
		{name: "ingress", apiVersion: "networking.k8s.io/v1", kind: "Ingress", want: "extensions/v1beta1"},
		{name: "gateway", apiVersion: "networking.istio.io/v1", kind: "Gateway", want: "networking.istio.io/v1alpha3"},
		{name: "virtualservice", apiVersion: "networking.istio.io/v1", kind: "VirtualService", want: "networking.istio.io/v1alpha3"},
		{name: "destinationrule", apiVersion: "networking.istio.io/v1", kind: "DestinationRule", want: "networking.istio.io/v1alpha3"},
		{name: "envoyfilter", apiVersion: "networking.istio.io/v1", kind: "EnvoyFilter", want: "networking.istio.io/v1alpha3"},
		{name: "peerauthentication", apiVersion: "security.istio.io/v1", kind: "PeerAuthentication", want: "security.istio.io/v1beta1"},
		{name: "authorizationpolicy", apiVersion: "security.istio.io/v1", kind: "AuthorizationPolicy", want: "security.istio.io/v1alpha1"},
		{name: "requestauthentication", apiVersion: "security.istio.io/v1", kind: "RequestAuthentication", want: "security.istio.io/v1"},
		{name: "legacy policy", apiVersion: "authentication.istio.io/v1alpha1", kind: "Policy", want: "authentication.istio.io/v1alpha1"},
		{name: "legacy meshpolicy", apiVersion: "authentication.istio.io/v1alpha1", kind: "MeshPolicy", want: "authentication.istio.io/v1alpha1"},
		{name: "legacy servicerolebinding", apiVersion: "rbac.istio.io/v1alpha1", kind: "ServiceRoleBinding", want: "rbac.istio.io/v1alpha1"},
		{name: "legacy clusterrbacconfig", apiVersion: "rbac.istio.io/v1alpha1", kind: "ClusterRbacConfig", want: "rbac.istio.io/v1alpha1"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: tt.apiVersion, Kind: tt.kind})

			require.NoError(t, err)
			require.Equal(t, tt.want, resolved.APIVersion)
		})
	}
}

func TestResourceVersionResolverUsesKubernetes126ServedAPIs(t *testing.T) {
	resolver := NewResourceVersionResolver(CapabilitySnapshot{
		ServerVersion: "v1.26.15",
		Resources: []APIResource{
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "services", Kind: "Service", Namespaced: true},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true},
			{Group: "", Version: "v1", GroupVersion: "v1", Resource: "persistentvolumes", Kind: "PersistentVolume", Namespaced: false},
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "statefulsets", Kind: "StatefulSet", Namespaced: true},
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "daemonsets", Kind: "DaemonSet", Namespaced: true},
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "replicasets", Kind: "ReplicaSet", Namespaced: true},
			{Group: "autoscaling", Version: "v2", GroupVersion: "autoscaling/v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
			{Group: "networking.k8s.io", Version: "v1", GroupVersion: "networking.k8s.io/v1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
			{Group: "rbac.authorization.k8s.io", Version: "v1", GroupVersion: "rbac.authorization.k8s.io/v1", Resource: "roles", Kind: "Role", Namespaced: true},
			{Group: "rbac.authorization.k8s.io", Version: "v1", GroupVersion: "rbac.authorization.k8s.io/v1", Resource: "rolebindings", Kind: "RoleBinding", Namespaced: true},
			{Group: "rbac.authorization.k8s.io", Version: "v1", GroupVersion: "rbac.authorization.k8s.io/v1", Resource: "clusterroles", Kind: "ClusterRole", Namespaced: false},
			{Group: "rbac.authorization.k8s.io", Version: "v1", GroupVersion: "rbac.authorization.k8s.io/v1", Resource: "clusterrolebindings", Kind: "ClusterRoleBinding", Namespaced: false},
		},
	})

	cases := []struct {
		name       string
		apiVersion string
		kind       string
		want       string
	}{
		{name: "deployment", apiVersion: "apps/v1", kind: "Deployment", want: "apps/v1"},
		{name: "statefulset", apiVersion: "apps/v1", kind: "StatefulSet", want: "apps/v1"},
		{name: "daemonset", apiVersion: "apps/v1", kind: "DaemonSet", want: "apps/v1"},
		{name: "replicaset", apiVersion: "apps/v1", kind: "ReplicaSet", want: "apps/v1"},
		{name: "hpa v2", apiVersion: "autoscaling/v2", kind: "HorizontalPodAutoscaler", want: "autoscaling/v2"},
		{name: "hpa v2beta2 migrated", apiVersion: "autoscaling/v2beta2", kind: "HorizontalPodAutoscaler", want: "autoscaling/v2"},
		{name: "ingress v1", apiVersion: "networking.k8s.io/v1", kind: "Ingress", want: "networking.k8s.io/v1"},
		{name: "ingress extensions migrated", apiVersion: "extensions/v1beta1", kind: "Ingress", want: "networking.k8s.io/v1"},
		{name: "clusterrole v1beta1 migrated", apiVersion: "rbac.authorization.k8s.io/v1beta1", kind: "ClusterRole", want: "rbac.authorization.k8s.io/v1"},
		{name: "clusterrolebinding v1beta1 migrated", apiVersion: "rbac.authorization.k8s.io/v1beta1", kind: "ClusterRoleBinding", want: "rbac.authorization.k8s.io/v1"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: tt.apiVersion, Kind: tt.kind})

			require.NoError(t, err)
			require.Equal(t, tt.want, resolved.APIVersion)
		})
	}
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
