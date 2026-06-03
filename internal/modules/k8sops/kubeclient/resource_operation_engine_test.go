package kubeclient

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestResourceOperationEngineTypedFirstDryRunApplyDeployment(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("patch", "deployments", successfulTypedPatch)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	engine := NewResourceOperationEngine(
		Bundle{Clientset: clientset, Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		}},
	)

	result, err := engine.Apply(context.Background(), ApplyRequest{Mode: OperationModeDryRun, YAMLContent: `apiVersion: apps/v1
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
	require.Len(t, result.Objects, 1)
	require.Equal(t, "typed", result.Objects[0].Executor)
	require.Empty(t, dynamicClient.Actions())
	actions := clientset.Actions()
	require.Len(t, actions, 1)
	action := actions[0].(k8stesting.PatchActionImpl)
	require.Equal(t, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, action.GetResource())
	require.Equal(t, "orders", action.GetNamespace())
	require.Equal(t, "orders-api", action.GetName())
	require.Equal(t, metav1.PatchOptions{FieldManager: DefaultFieldManager, DryRun: []string{metav1.DryRunAll}}, action.GetPatchOptions())
}

func TestResourceOperationEngineDryRunApplyCanForceConflicts(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("patch", "configmaps", successfulConfigMapPatch)
	engine := NewResourceOperationEngine(
		Bundle{Clientset: clientset},
		CapabilitySnapshot{Resources: []APIResource{
			{Version: "v1", GroupVersion: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
		}},
	)

	_, err := engine.DryRunApply(context.Background(), DryRunApplyRequest{ForceConflicts: true, YAMLContent: `apiVersion: v1
kind: ConfigMap
metadata:
  name: novaobs-logs-agent-config
  namespace: novaobs-system
data:
  collector.yaml: |
    receivers: {}`})

	require.NoError(t, err)
	actions := clientset.Actions()
	require.Len(t, actions, 1)
	action := actions[0].(k8stesting.PatchActionImpl)
	require.NotNil(t, action.GetPatchOptions().Force)
	require.True(t, *action.GetPatchOptions().Force)
	require.Equal(t, []string{metav1.DryRunAll}, action.GetPatchOptions().DryRun)
}

func TestResourceOperationEngineDynamicFallbackApplyVirtualService(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("patch", "*", successfulDryRunPatch)
	engine := NewResourceOperationEngine(
		Bundle{Clientset: clientset, Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "networking.istio.io", Version: "v1beta1", GroupVersion: "networking.istio.io/v1beta1", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
		}},
	)

	result, err := engine.Apply(context.Background(), ApplyRequest{Mode: OperationModeApply, YAMLContent: `apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: orders-vs
  namespace: orders
spec:
  hosts:
  - orders.example.internal`})

	require.NoError(t, err)
	require.Len(t, result.Objects, 1)
	require.Equal(t, "dynamic", result.Objects[0].Executor)
	require.Empty(t, clientset.Actions())
	actions := dynamicClient.Actions()
	require.Len(t, actions, 1)
	action := actions[0].(k8stesting.PatchActionImpl)
	require.Equal(t, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1beta1", Resource: "virtualservices"}, action.GetResource())
	require.Empty(t, action.GetPatchOptions().DryRun)
}

func TestResourceOperationEngineTypedFirstDryRunDeleteDeployment(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("delete", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	engine := NewResourceOperationEngine(
		Bundle{Clientset: clientset, Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		}},
	)

	result, err := engine.Delete(context.Background(), DeleteRequest{
		Mode: OperationModeDryRun,
		Identity: OperationObject{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Namespace:  "orders",
			Name:       "orders-api",
		},
	})

	require.NoError(t, err)
	require.Len(t, result.Objects, 1)
	require.Equal(t, "typed", result.Objects[0].Executor)
	require.Empty(t, dynamicClient.Actions())
	actions := clientset.Actions()
	require.Len(t, actions, 1)
	action := actions[0].(k8stesting.DeleteActionImpl)
	require.Equal(t, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, action.GetResource())
	require.Equal(t, "orders-api", action.GetName())
	require.Equal(t, metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}}, action.GetDeleteOptions())
}

func TestResourceOperationEngineDynamicFallbackDryRunDeleteVirtualService(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})
	engine := NewResourceOperationEngine(
		Bundle{Clientset: clientset, Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "networking.istio.io", Version: "v1beta1", GroupVersion: "networking.istio.io/v1beta1", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
		}},
	)

	result, err := engine.Delete(context.Background(), DeleteRequest{
		Mode: OperationModeDryRun,
		Identity: OperationObject{
			APIVersion: "networking.istio.io/v1",
			Kind:       "VirtualService",
			Namespace:  "orders",
			Name:       "orders-vs",
		},
	})

	require.NoError(t, err)
	require.Len(t, result.Objects, 1)
	require.Equal(t, "dynamic", result.Objects[0].Executor)
	require.Empty(t, clientset.Actions())
	actions := dynamicClient.Actions()
	require.Len(t, actions, 1)
	action := actions[0].(k8stesting.DeleteActionImpl)
	require.Equal(t, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1beta1", Resource: "virtualservices"}, action.GetResource())
	require.Equal(t, "orders-vs", action.GetName())
	require.Equal(t, metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}}, action.GetDeleteOptions())
}

func TestResourceOperationEngineRejectsImplicitMutatingMode(t *testing.T) {
	engine := NewResourceOperationEngine(
		Bundle{Clientset: fake.NewSimpleClientset(), Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		}},
	)

	_, applyErr := engine.Apply(context.Background(), ApplyRequest{YAMLContent: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: orders-api
  namespace: orders`})
	_, deleteErr := engine.Delete(context.Background(), DeleteRequest{Identity: OperationObject{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "orders",
		Name:       "orders-api",
	}})

	require.ErrorIs(t, applyErr, ErrResourceOperationInvalid)
	require.ErrorIs(t, deleteErr, ErrResourceOperationInvalid)
}

func TestResourceOperationEnginePreviewApplyDiffInputs(t *testing.T) {
	existing := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "orders-api",
			"namespace": "orders",
		},
		"spec": map[string]any{
			"replicas": int64(1),
		},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), existing)
	dynamicClient.PrependReactor("patch", "*", successfulDryRunPatch)
	engine := NewResourceOperationEngine(
		Bundle{Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Group: "apps", Version: "v1", GroupVersion: "apps/v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
			{Version: "v1", GroupVersion: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
		}},
	)

	result, err := engine.PreviewApply(context.Background(), DryRunApplyRequest{YAMLContent: `apiVersion: v1
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
  replicas: 2`})

	require.NoError(t, err)
	require.Len(t, result.Objects, 2)
	require.Equal(t, "create", result.Objects[0].Operation)
	require.Empty(t, result.Objects[0].BeforeHash)
	require.NotEmpty(t, result.Objects[0].AfterHash)
	require.Equal(t, "update", result.Objects[1].Operation)
	require.NotEmpty(t, result.Objects[1].BeforeHash)
	require.NotEmpty(t, result.Objects[1].AfterHash)
	require.NotEqual(t, result.Objects[1].BeforeHash, result.Objects[1].AfterHash)

	actions := dynamicClient.Actions()
	require.Len(t, actions, 4)
	require.Equal(t, "get", actions[0].GetVerb())
	require.Equal(t, "patch", actions[1].GetVerb())
	require.Equal(t, "get", actions[2].GetVerb())
	require.Equal(t, "patch", actions[3].GetVerb())
}

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
	require.Equal(t, "extensions/v1beta1", result.Objects[0].APIVersion)
	require.Equal(t, "Ingress", result.Objects[0].Kind)
	require.Equal(t, "orders", result.Objects[0].Namespace)
	require.Equal(t, "orders-ingress", result.Objects[0].Name)
	require.Equal(t, OperationExecutorDynamic, result.Objects[0].Executor)
	require.Equal(t, "create", result.Objects[0].Operation)
	require.Empty(t, result.Objects[0].BeforeHash)
	require.NotEmpty(t, result.Objects[0].AfterHash)

	actions := dynamicClient.Actions()
	require.Len(t, actions, 2)
	require.Equal(t, "get", actions[0].GetVerb())
	action := actions[1].(k8stesting.PatchActionImpl)
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
	actions := dynamicClient.Actions()
	require.Len(t, actions, 4)
	require.Equal(t, "configmaps", actions[0].GetResource().Resource)
	require.Equal(t, "configmaps", actions[1].GetResource().Resource)
	require.Equal(t, "deployments", actions[2].GetResource().Resource)
	require.Equal(t, "deployments", actions[3].GetResource().Resource)
}

func TestResourceOperationEngineDryRunApplyDefersObjectsInNamespaceCreatedBySameBundle(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == "novaobs-system" {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "novaobs-system")
		}
		return successfulDryRunPatch(action)
	})
	engine := NewResourceOperationEngine(
		Bundle{Dynamic: dynamicClient},
		CapabilitySnapshot{Resources: []APIResource{
			{Version: "v1", GroupVersion: "v1", Resource: "namespaces", Kind: "Namespace", Namespaced: false},
			{Version: "v1", GroupVersion: "v1", Resource: "serviceaccounts", Kind: "ServiceAccount", Namespaced: true},
			{Version: "v1", GroupVersion: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
		}},
	)

	result, err := engine.DryRunApply(context.Background(), DryRunApplyRequest{YAMLContent: `apiVersion: v1
kind: Namespace
metadata:
  name: novaobs-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: novaobs-logs-agent
  namespace: novaobs-system
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: novaobs-logs-agent-config
  namespace: novaobs-system`})

	require.NoError(t, err)
	require.Len(t, result.Objects, 3)
	require.Equal(t, OperationExecutorDynamic, result.Objects[0].Executor)
	require.Equal(t, OperationExecutorDeferredDryRun, result.Objects[1].Executor)
	require.Equal(t, OperationExecutorDeferredDryRun, result.Objects[2].Executor)
	require.Len(t, result.Warnings, 2)
	require.Contains(t, result.Warnings[0], `namespace "novaobs-system"`)
	actions := dynamicClient.Actions()
	require.Len(t, actions, 6)
	require.Equal(t, "namespaces", actions[1].GetResource().Resource)
	require.Equal(t, "serviceaccounts", actions[3].GetResource().Resource)
	require.Equal(t, "configmaps", actions[5].GetResource().Resource)
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
	require.Len(t, dynamicClient.Actions(), 2)
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

func successfulTypedPatch(action k8stesting.Action) (bool, runtime.Object, error) {
	patch := action.(k8stesting.PatchAction)
	return true, &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: patch.GetResource().GroupVersion().String(), Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      patch.GetName(),
			Namespace: action.GetNamespace(),
		},
	}, nil
}

func successfulConfigMapPatch(action k8stesting.Action) (bool, runtime.Object, error) {
	patch := action.(k8stesting.PatchAction)
	return true, &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: patch.GetResource().GroupVersion().String(), Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      patch.GetName(),
			Namespace: action.GetNamespace(),
		},
	}, nil
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
