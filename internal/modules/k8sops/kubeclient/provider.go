package kubeclient

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	ErrClusterRequired = errors.New("k8s_cluster_required")
)

type CredentialReader interface {
	Kubeconfig(ctx context.Context, subject platformrbac.Subject, clusterID string) ([]byte, error)
}

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type DynamicInterface = dynamic.Interface
type DiscoveryInterface = discovery.DiscoveryInterface

type typedClientFactory func(*rest.Config) (kubernetes.Interface, error)
type dynamicClientFactory func(*rest.Config) (dynamic.Interface, error)
type discoveryClientFactory func(*rest.Config) (discovery.DiscoveryInterface, error)

type Bundle struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
}

type APIResource struct {
	Group        string   `json:"group"`
	Version      string   `json:"version"`
	GroupVersion string   `json:"group_version"`
	Resource     string   `json:"resource"`
	Kind         string   `json:"kind"`
	Namespaced   bool     `json:"namespaced"`
	Verbs        []string `json:"verbs"`
}

type CapabilitySnapshot struct {
	ClusterID     string        `json:"cluster_id"`
	ServerVersion string        `json:"server_version"`
	Resources     []APIResource `json:"resources"`
	Warnings      []string      `json:"warnings"`
}

func (s CapabilitySnapshot) Supports(group string, version string, resource string) bool {
	for _, item := range s.Resources {
		if item.Group == group && item.Version == version && item.Resource == resource {
			return true
		}
	}
	return false
}

type Provider struct {
	credentials      CredentialReader
	timeout          time.Duration
	typedFactory     typedClientFactory
	dynamicFactory   dynamicClientFactory
	discoveryFactory discoveryClientFactory
}

func NewProvider(credentials CredentialReader) Provider {
	return newProviderWithFactories(credentials, nil, nil, nil)
}

func newProviderWithFactories(
	credentials CredentialReader,
	typedFactory typedClientFactory,
	dynamicFactory dynamicClientFactory,
	discoveryFactory discoveryClientFactory,
) Provider {
	if typedFactory == nil {
		typedFactory = func(config *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(config)
		}
	}
	if dynamicFactory == nil {
		dynamicFactory = func(config *rest.Config) (dynamic.Interface, error) {
			return dynamic.NewForConfig(config)
		}
	}
	if discoveryFactory == nil {
		discoveryFactory = func(config *rest.Config) (discovery.DiscoveryInterface, error) {
			return discovery.NewDiscoveryClientForConfig(config)
		}
	}
	return Provider{
		credentials:      credentials,
		timeout:          15 * time.Second,
		typedFactory:     typedFactory,
		dynamicFactory:   dynamicFactory,
		discoveryFactory: discoveryFactory,
	}
}

func (p Provider) Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error) {
	bundle, err := p.Bundle(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	return bundle.Clientset, nil
}

func (p Provider) Bundle(ctx context.Context, clusterID string) (Bundle, error) {
	config, err := p.restConfig(ctx, clusterID)
	if err != nil {
		return Bundle{}, err
	}
	clientset, err := p.typedFactory(rest.CopyConfig(config))
	if err != nil {
		return Bundle{}, err
	}
	dynamicClient, err := p.dynamicFactory(rest.CopyConfig(config))
	if err != nil {
		return Bundle{}, err
	}
	discoveryClient, err := p.discoveryFactory(rest.CopyConfig(config))
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Clientset: clientset, Dynamic: dynamicClient, Discovery: discoveryClient}, nil
}

func (p Provider) Capabilities(ctx context.Context, clusterID string) (CapabilitySnapshot, error) {
	clusterID = strings.TrimSpace(clusterID)
	bundle, err := p.Bundle(ctx, clusterID)
	if err != nil {
		return CapabilitySnapshot{}, err
	}
	return DiscoverCapabilities(clusterID, bundle.Discovery)
}

func DiscoverCapabilities(clusterID string, discoveryClient discovery.DiscoveryInterface) (CapabilitySnapshot, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return CapabilitySnapshot{}, ErrClusterRequired
	}
	if discoveryClient == nil {
		return CapabilitySnapshot{}, errors.New("k8s_discovery_client_required")
	}
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		return CapabilitySnapshot{}, err
	}
	_, resourceLists, discoveryErr := discoveryClient.ServerGroupsAndResources()
	warnings := discoveryWarnings(discoveryErr)
	if discoveryErr != nil && len(warnings) == 0 {
		return CapabilitySnapshot{}, discoveryErr
	}
	return CapabilitySnapshot{
		ClusterID:     clusterID,
		ServerVersion: gitVersion(serverVersion),
		Resources:     flattenAPIResources(resourceLists),
		Warnings:      warnings,
	}, nil
}

func (p Provider) restConfig(ctx context.Context, clusterID string) (*rest.Config, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return nil, ErrClusterRequired
	}
	subject, _ := authctx.SubjectFrom(ctx)
	kubeconfig, err := p.credentials.Kubeconfig(ctx, subject, clusterID)
	if err != nil {
		return nil, err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	if config.Timeout == 0 {
		config.Timeout = p.timeout
	}
	return config, nil
}

func gitVersion(versionInfo *version.Info) string {
	if versionInfo == nil {
		return ""
	}
	return versionInfo.GitVersion
}

func discoveryWarnings(err error) []string {
	if err == nil {
		return []string{}
	}
	if !discovery.IsGroupDiscoveryFailedError(err) {
		return []string{}
	}
	return []string{err.Error()}
}

func flattenAPIResources(resourceLists []*metav1.APIResourceList) []APIResource {
	items := make([]APIResource, 0)
	for _, list := range resourceLists {
		if list == nil {
			continue
		}
		groupVersion, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, resource := range list.APIResources {
			items = append(items, APIResource{
				Group:        groupVersion.Group,
				Version:      groupVersion.Version,
				GroupVersion: list.GroupVersion,
				Resource:     resource.Name,
				Kind:         resource.Kind,
				Namespaced:   resource.Namespaced,
				Verbs:        append([]string{}, resource.Verbs...),
			})
		}
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Group != items[right].Group {
			return items[left].Group < items[right].Group
		}
		if items[left].Version != items[right].Version {
			return items[left].Version < items[right].Version
		}
		return items[left].Resource < items[right].Resource
	})
	return items
}
