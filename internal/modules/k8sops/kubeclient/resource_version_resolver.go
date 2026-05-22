package kubeclient

import (
	"errors"
	"strings"
)

var (
	ErrResourceVersionRequestInvalid = errors.New("k8s_resource_version_request_invalid")
	ErrResourceVersionUnsupported    = errors.New("k8s_resource_version_unsupported")
)

type ResourceVersionRequest struct {
	APIVersion string
	Kind       string
}

type ResolvedResourceVersion struct {
	Group        string `json:"group"`
	Version      string `json:"version"`
	GroupVersion string `json:"group_version"`
	APIVersion   string `json:"api_version"`
	Resource     string `json:"resource"`
	Kind         string `json:"kind"`
	Namespaced   bool   `json:"namespaced"`
}

type ResourceVersionResolver struct {
	snapshot CapabilitySnapshot
}

type resourceCandidate struct {
	group    string
	version  string
	resource string
	kind     string
}

func NewResourceVersionResolver(snapshot CapabilitySnapshot) ResourceVersionResolver {
	return ResourceVersionResolver{snapshot: snapshot}
}

func (r ResourceVersionResolver) Resolve(req ResourceVersionRequest) (ResolvedResourceVersion, error) {
	req.APIVersion = strings.TrimSpace(req.APIVersion)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.APIVersion == "" || req.Kind == "" {
		return ResolvedResourceVersion{}, ErrResourceVersionRequestInvalid
	}
	if candidates := candidatesForKind(req.Kind); len(candidates) > 0 {
		for _, candidate := range candidates {
			if resource, ok := r.find(candidate.group, candidate.version, candidate.resource, candidate.kind); ok {
				return resolvedFromAPIResource(resource), nil
			}
		}
		return ResolvedResourceVersion{}, ErrResourceVersionUnsupported
	}
	group, version := parseAPIVersion(req.APIVersion)
	if resource, ok := r.find(group, version, "", req.Kind); ok {
		return resolvedFromAPIResource(resource), nil
	}
	return ResolvedResourceVersion{}, ErrResourceVersionUnsupported
}

func (r ResourceVersionResolver) find(group string, version string, resourceName string, kind string) (APIResource, bool) {
	for _, resource := range r.snapshot.Resources {
		if resource.Group != group || resource.Version != version {
			continue
		}
		if resourceName != "" && resource.Resource != resourceName {
			continue
		}
		if !strings.EqualFold(resource.Kind, kind) {
			continue
		}
		return resource, true
	}
	return APIResource{}, false
}

func candidatesForKind(kind string) []resourceCandidate {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "pod":
		return []resourceCandidate{{version: "v1", resource: "pods", kind: "Pod"}}
	case "service":
		return []resourceCandidate{{version: "v1", resource: "services", kind: "Service"}}
	case "configmap":
		return []resourceCandidate{{version: "v1", resource: "configmaps", kind: "ConfigMap"}}
	case "persistentvolumeclaim":
		return []resourceCandidate{{version: "v1", resource: "persistentvolumeclaims", kind: "PersistentVolumeClaim"}}
	case "persistentvolume":
		return []resourceCandidate{{version: "v1", resource: "persistentvolumes", kind: "PersistentVolume"}}
	case "deployment":
		return []resourceCandidate{{group: "apps", version: "v1", resource: "deployments", kind: "Deployment"}}
	case "statefulset":
		return []resourceCandidate{{group: "apps", version: "v1", resource: "statefulsets", kind: "StatefulSet"}}
	case "daemonset":
		return []resourceCandidate{{group: "apps", version: "v1", resource: "daemonsets", kind: "DaemonSet"}}
	case "replicaset":
		return []resourceCandidate{{group: "apps", version: "v1", resource: "replicasets", kind: "ReplicaSet"}}
	case "horizontalpodautoscaler":
		return []resourceCandidate{
			{group: "autoscaling", version: "v2", resource: "horizontalpodautoscalers", kind: "HorizontalPodAutoscaler"},
			{group: "autoscaling", version: "v2beta2", resource: "horizontalpodautoscalers", kind: "HorizontalPodAutoscaler"},
			{group: "autoscaling", version: "v1", resource: "horizontalpodautoscalers", kind: "HorizontalPodAutoscaler"},
		}
	case "ingress":
		return []resourceCandidate{
			{group: "networking.k8s.io", version: "v1", resource: "ingresses", kind: "Ingress"},
			{group: "networking.k8s.io", version: "v1beta1", resource: "ingresses", kind: "Ingress"},
			{group: "extensions", version: "v1beta1", resource: "ingresses", kind: "Ingress"},
		}
	case "gateway":
		return istioNetworkingCandidates("gateways", "Gateway")
	case "virtualservice":
		return istioNetworkingCandidates("virtualservices", "VirtualService")
	case "destinationrule":
		return istioNetworkingCandidates("destinationrules", "DestinationRule")
	case "envoyfilter":
		return istioNetworkingCandidates("envoyfilters", "EnvoyFilter")
	case "peerauthentication":
		return istioSecurityCandidates("peerauthentications", "PeerAuthentication")
	case "authorizationpolicy":
		return istioSecurityCandidates("authorizationpolicies", "AuthorizationPolicy")
	case "requestauthentication":
		return istioSecurityCandidates("requestauthentications", "RequestAuthentication")
	case "policy":
		return []resourceCandidate{{group: "authentication.istio.io", version: "v1alpha1", resource: "policies", kind: "Policy"}}
	case "meshpolicy":
		return []resourceCandidate{{group: "authentication.istio.io", version: "v1alpha1", resource: "meshpolicies", kind: "MeshPolicy"}}
	case "servicerolebinding":
		return []resourceCandidate{{group: "rbac.istio.io", version: "v1alpha1", resource: "servicerolebindings", kind: "ServiceRoleBinding"}}
	case "clusterrbacconfig":
		return []resourceCandidate{{group: "rbac.istio.io", version: "v1alpha1", resource: "clusterrbacconfigs", kind: "ClusterRbacConfig"}}
	default:
		return nil
	}
}

func istioNetworkingCandidates(resource string, kind string) []resourceCandidate {
	return []resourceCandidate{
		{group: "networking.istio.io", version: "v1", resource: resource, kind: kind},
		{group: "networking.istio.io", version: "v1beta1", resource: resource, kind: kind},
		{group: "networking.istio.io", version: "v1alpha3", resource: resource, kind: kind},
	}
}

func istioSecurityCandidates(resource string, kind string) []resourceCandidate {
	return []resourceCandidate{
		{group: "security.istio.io", version: "v1", resource: resource, kind: kind},
		{group: "security.istio.io", version: "v1beta1", resource: resource, kind: kind},
		{group: "security.istio.io", version: "v1alpha1", resource: resource, kind: kind},
	}
}

func parseAPIVersion(apiVersion string) (string, string) {
	parts := strings.Split(strings.TrimSpace(apiVersion), "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return strings.Join(parts[:len(parts)-1], "/"), parts[len(parts)-1]
}

func resolvedFromAPIResource(resource APIResource) ResolvedResourceVersion {
	groupVersion := resource.GroupVersion
	if groupVersion == "" {
		groupVersion = joinGroupVersion(resource.Group, resource.Version)
	}
	return ResolvedResourceVersion{
		Group:        resource.Group,
		Version:      resource.Version,
		GroupVersion: groupVersion,
		APIVersion:   groupVersion,
		Resource:     resource.Resource,
		Kind:         resource.Kind,
		Namespaced:   resource.Namespaced,
	}
}

func joinGroupVersion(group string, version string) string {
	if strings.TrimSpace(group) == "" {
		return strings.TrimSpace(version)
	}
	return strings.TrimSpace(group) + "/" + strings.TrimSpace(version)
}
