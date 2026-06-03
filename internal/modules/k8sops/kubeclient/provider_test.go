package kubeclient

import (
	"context"
	"testing"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestProviderUsesAnonymousSubjectWhenContextHasNoSubject(t *testing.T) {
	reader := staticCredentialReader{kubeconfig: minimalKubeconfig()}
	provider := NewProvider(&reader)

	_, err := provider.Clientset(context.Background(), "prod")

	require.NoError(t, err)
	require.Equal(t, "anonymous", reader.subject.ID)
	require.Equal(t, "anonymous", reader.subject.Type)
}

func TestProviderReadsKubeconfigWithContextSubject(t *testing.T) {
	reader := staticCredentialReader{kubeconfig: minimalKubeconfig()}
	provider := NewProvider(&reader)
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"})

	_, err := provider.Clientset(ctx, "prod")

	require.NoError(t, err)
	require.Equal(t, "prod", reader.clusterID)
	require.Equal(t, "user-1", reader.subject.ID)
}

func TestProviderBuildsBundleAndCapabilitySnapshotFromDiscovery(t *testing.T) {
	reader := staticCredentialReader{kubeconfig: minimalKubeconfig()}
	discovery := &discoveryfake.FakeDiscovery{
		Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{
			{
				GroupVersion: "networking.istio.io/v1",
				APIResources: []metav1.APIResource{
					{Name: "virtualservices", Kind: "VirtualService", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "watch"}},
				},
			},
			{
				GroupVersion: "autoscaling/v2",
				APIResources: []metav1.APIResource{
					{Name: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
				},
			},
		}},
		FakedServerVersion: &version.Info{GitVersion: "v1.30.2"},
	}
	provider := newProviderWithFactories(
		&reader,
		func(*rest.Config) (kubernetes.Interface, error) {
			return fake.NewSimpleClientset(), nil
		},
		func(*rest.Config) (DynamicInterface, error) {
			return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil
		},
		func(*rest.Config) (DiscoveryInterface, error) {
			return discovery, nil
		},
	)

	bundle, err := provider.Bundle(context.Background(), "prod")
	require.NoError(t, err)
	require.NotNil(t, bundle.Clientset)
	require.NotNil(t, bundle.Dynamic)
	require.NotNil(t, bundle.Discovery)

	snapshot, err := provider.Capabilities(context.Background(), "prod")
	require.NoError(t, err)
	require.Equal(t, "prod", snapshot.ClusterID)
	require.Equal(t, "v1.30.2", snapshot.ServerVersion)
	require.True(t, snapshot.Supports("networking.istio.io", "v1", "virtualservices"))
	require.True(t, snapshot.Supports("autoscaling", "v2", "horizontalpodautoscalers"))
	require.False(t, snapshot.Supports("networking.istio.io", "v1alpha3", "virtualservices"))
}

func TestProviderCapabilitiesKeepPartialDiscoveryResources(t *testing.T) {
	reader := staticCredentialReader{kubeconfig: minimalKubeconfig()}
	discoveryClient := &discoveryfake.FakeDiscovery{
		Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
				},
			},
		}},
		FakedServerVersion: &version.Info{GitVersion: "v1.29.7"},
	}
	discoveryClient.Fake.PrependReactor("get", "resource", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{
			{Group: "broken.example.io", Version: "v1"}: context.Canceled,
		}}
	})
	provider := newProviderWithFactories(
		&reader,
		func(*rest.Config) (kubernetes.Interface, error) {
			return fake.NewSimpleClientset(), nil
		},
		func(*rest.Config) (DynamicInterface, error) {
			return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil
		},
		func(*rest.Config) (DiscoveryInterface, error) {
			return discoveryClient, nil
		},
	)

	snapshot, err := provider.Capabilities(context.Background(), "prod")

	require.NoError(t, err)
	require.True(t, snapshot.Supports("apps", "v1", "deployments"))
	require.NotEmpty(t, snapshot.Warnings)
}

type staticCredentialReader struct {
	kubeconfig []byte
	subject    platformrbac.Subject
	clusterID  string
}

func (r *staticCredentialReader) Kubeconfig(_ context.Context, subject platformrbac.Subject, clusterID string) ([]byte, error) {
	r.subject = subject
	r.clusterID = clusterID
	return r.kubeconfig, nil
}

func minimalKubeconfig() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- name: prod
  cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
users:
- name: novaobs
  user:
    token: test-token
contexts:
- name: prod
  context:
    cluster: prod
    user: novaobs
current-context: prod
`)
}
