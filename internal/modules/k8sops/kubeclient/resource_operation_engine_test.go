package kubeclient

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestResourceOperationEngineDryRunApplyUsesServerSideApplyAndResolvedGVR(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("patch", "*", successfulDryRunPatch)
	engine := NewResourceOperationEngine(
		Bundle{Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "extensions", Version: "v1beta1", GroupVersion: "extensions/v1beta1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
		}},
	)

	result, err := engine.DryRunApply(context.Background(), DryRunApplyRequest{YAMLContent: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: orders-ingress
  namespace: orders
spec:
  rules: []`})

	require.NoError(t, err)
	require.Len(t, result.Objects, 1)
	require.Equal(t, OperationObject{
		APIVersion: "extensions/v1beta1",
		Kind:       "Ingress",
		Namespace:  "orders",
		Name:       "orders-ingress",
		Resolved: ResolvedResourceVersion{
			Group:        "extensions",
			Version:      "v1beta1",
			GroupVersion: "extensions/v1beta1",
			APIVersion:   "extensions/v1beta1",
			Resource:     "ingresses",
			Kind:         "Ingress",
			Namespaced:   true,
		},
	}, result.Objects[0])

	actions := dynamicClient.Actions()
	require.Len(t, actions, 1)
	action := actions[0].(k8stesting.PatchActionImpl)
	require.Equal(t, schema.GroupVersionResource{Group: "extensions", Version: "v1beta1", Resource: "ingresses"}, action.GetResource())
	require.Equal(t, "orders", action.GetNamespace())
	require.Equal(t, "orders-ingress", action.GetName())
	require.Equal(t, types.ApplyPatchType, action.GetPatchType())
	require.Equal(t, metav1.PatchOptions{FieldManager: DefaultFieldManager, DryRun: []string{metav1.DryRunAll}}, action.GetPatchOptions())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(action.GetPatch(), &payload))
	require.Equal(t, "extensions/v1beta1", payload["apiVersion"])
	require.Equal(t, "Ingress", payload["kind"])
}

func TestResourceOperationEngineDryRunApplyHandlesMultiDocumentYAML(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("patch", "*", successfulDryRunPatch)
	engine := NewResourceOperationEngine(
		Bundle{Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Version: "v1", GroupVersion: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		}},
	)

	result, err := engine.DryRunApply(context.Background(), DryRunApplyRequest{YAMLContent: `apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: orders
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders
spec:
  selector:
    matchLabels:
      app: orders-api
  template:
    metadata:
      labels:
        app: orders-api
    spec:
      containers:
      - name: api
        image: orders:v1`})

	require.NoError(t, err)
	require.Len(t, result.Objects, 2)
	require.Len(t, dynamicClient.Actions(), 2)
	require.Equal(t, "configmaps", dynamicClient.Actions()[0].GetResource().Resource)
	require.Equal(t, "deployments", dynamicClient.Actions()[1].GetResource().Resource)
}

func TestResourceOperationEngineDryRunApplyRejectsUnsupportedResource(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	engine := NewResourceOperationEngine(Bundle{Dynamic: dynamicClient}, CapabilitySnapshot{})

	_, err := engine.DryRunApply(context.Background(), DryRunApplyRequest{YAMLContent: `apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: orders-vs
  namespace: orders`})

	require.ErrorIs(t, err, ErrResourceVersionUnsupported)
	require.Empty(t, dynamicClient.Actions())
}

func TestResourceOperationEngineDryRunApplyMapsAPIServerValidationErrors(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "apps", Kind: "Deployment"},
			"orders-api",
			field.ErrorList{field.Required(field.NewPath("spec"), "required")},
		)
	})
	engine := NewResourceOperationEngine(
		Bundle{Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		}},
	)

	_, err := engine.DryRunApply(context.Background(), DryRunApplyRequest{YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`})

	require.ErrorIs(t, err, ErrResourceOperationInvalid)
}

func TestProviderBackedResourceOperationEngineDiscoversCapabilitiesAndDryRuns(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("patch", "*", successfulDryRunPatch)
	provider := &staticBundleProvider{bundle: Bundle{
		Dynamic: dynamicClient,
		Discovery: &discoveryfake.FakeDiscovery{
			Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: metav1.Verbs{"patch"}},
				},
			}}},
			FakedServerVersion: &version.Info{GitVersion: "v1.30.2"},
		},
	}}
	engine := NewProviderBackedResourceOperationEngine(provider)

	result, err := engine.DryRunApply(context.Background(), ClusterDryRunApplyRequest{
		ClusterID: "prod",
		YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`,
	})

	require.NoError(t, err)
	require.Equal(t, 1, provider.calls)
	require.Equal(t, "prod", provider.clusterID)
	require.Len(t, result.Objects, 1)
	require.Equal(t, "apps/v1", result.Objects[0].APIVersion)
	require.Len(t, dynamicClient.Actions(), 1)
}

func successfulDryRunPatch(action k8stesting.Action) (bool, runtime.Object, error) {
	patch := action.(k8stesting.PatchAction)
	return true, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": patch.GetResource().GroupVersion().String(),
		"kind":       "DryRunObject",
		"metadata": map[string]any{
			"name":      patch.GetName(),
			"namespace": action.GetNamespace(),
		},
	}}, nil
}

type staticBundleProvider struct {
	bundle    Bundle
	clusterID string
	calls     int
}

func (p *staticBundleProvider) Bundle(_ context.Context, clusterID string) (Bundle, error) {
	p.calls++
	p.clusterID = clusterID
	return p.bundle, nil
}
