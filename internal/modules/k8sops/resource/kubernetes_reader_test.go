package resource

import (
	"context"
	"strings"
	"testing"
	"time"

	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestKubernetesReaderListsDeploymentSummaries(t *testing.T) {
	createdAt := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC)
	replicas := int32(2)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "orders-api",
			Namespace:         "orders",
			UID:               "uid-orders-api",
			Labels:            map[string]string{"app": "orders-api"},
			CreationTimestamp: metav1.NewTime(createdAt),
		},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas:     2,
			AvailableReplicas: 2,
		},
	})

	reader := NewKubernetesReader(staticResourceClientsetProvider{client: client})
	items, err := reader.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: "Deployment"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, Identity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "orders-api",
		UID:        "uid-orders-api",
	}, items[0].Identity)
	require.Equal(t, "healthy", items[0].Status)
	require.Equal(t, "orders-api", items[0].Labels["app"])
	require.Equal(t, createdAt, items[0].UpdatedAt)
}

func TestKubernetesReaderListsAppsWorkloadSummaries(t *testing.T) {
	createdAt := time.Date(2026, 6, 25, 9, 30, 0, 0, time.UTC)
	replicas := int32(3)
	client := fake.NewSimpleClientset(
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: "orders", UID: "uid-sts", CreationTimestamp: metav1.NewTime(createdAt)},
			Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 3},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "orders", UID: "uid-ds", CreationTimestamp: metav1.NewTime(createdAt)},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 3,
				CurrentNumberScheduled: 3,
				NumberReady:            3,
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-api-7d9", Namespace: "orders", UID: "uid-rs", CreationTimestamp: metav1.NewTime(createdAt)},
			Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
			Status:     appsv1.ReplicaSetStatus{ReadyReplicas: 3, AvailableReplicas: 3},
		},
	)
	reader := NewKubernetesReader(staticResourceClientsetProvider{client: client})

	for _, tt := range []struct {
		kind string
		name string
		uid  string
	}{
		{kind: "StatefulSet", name: "orders-db", uid: "uid-sts"},
		{kind: "DaemonSet", name: "node-agent", uid: "uid-ds"},
		{kind: "ReplicaSet", name: "orders-api-7d9", uid: "uid-rs"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			items, err := reader.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: tt.kind})

			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, Identity{
				ClusterID:  "prod",
				Namespace:  "orders",
				APIVersion: "apps/v1",
				Kind:       tt.kind,
				Name:       tt.name,
				UID:        tt.uid,
			}, items[0].Identity)
			require.Equal(t, "healthy", items[0].Status)
			require.Equal(t, createdAt, items[0].UpdatedAt)
		})
	}
}

func TestKubernetesReaderReadsDetailAndYAML(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-7d9",
			Namespace: "orders",
			UID:       "uid-pod",
			Labels:    map[string]string{"app": "orders-api"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "orders:v1"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	})

	reader := NewKubernetesReader(staticResourceClientsetProvider{client: client})
	identity := Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "v1", Kind: "Pod", Name: "orders-api-7d9", UID: "uid-pod"}

	detail, err := reader.GetDetail(context.Background(), DetailQuery{Identity: identity})
	require.NoError(t, err)
	require.Equal(t, "healthy", detail.Status)
	require.Equal(t, "orders-api", detail.Labels["app"])
	require.Equal(t, "orders:v1", detail.Spec["containers"].([]any)[0].(map[string]any)["image"])

	rendered, err := reader.GetYAML(context.Background(), DetailQuery{Identity: identity})
	require.NoError(t, err)
	require.Contains(t, rendered.YAML, "kind: Pod")
	require.True(t, strings.Contains(rendered.YAML, "name: orders-api-7d9"))
}

func TestKubernetesReaderReadsAppsWorkloadYAML(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: "orders", UID: "uid-sts"},
			Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "orders", UID: "uid-ds"},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 1,
				CurrentNumberScheduled: 1,
				NumberReady:            1,
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-api-7d9", Namespace: "orders", UID: "uid-rs"},
			Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
			Status:     appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
	)
	reader := NewKubernetesReader(staticResourceClientsetProvider{client: client})

	for _, identity := range []Identity{
		{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "StatefulSet", Name: "orders-db", UID: "uid-sts"},
		{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-agent", UID: "uid-ds"},
		{ClusterID: "prod", Namespace: "orders", APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "orders-api-7d9", UID: "uid-rs"},
	} {
		t.Run(identity.Kind, func(t *testing.T) {
			detail, err := reader.GetDetail(context.Background(), DetailQuery{Identity: identity})
			require.NoError(t, err)
			require.Equal(t, "healthy", detail.Status)

			rendered, err := reader.GetYAML(context.Background(), DetailQuery{Identity: identity})
			require.NoError(t, err)
			require.Contains(t, rendered.YAML, "apiVersion: apps/v1")
			require.Contains(t, rendered.YAML, "kind: "+identity.Kind)
			require.Contains(t, rendered.YAML, "name: "+identity.Name)
		})
	}
}

func TestKubernetesReaderListsIstioResourceWithResolvedVersion(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1beta1", Resource: "virtualservices"}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "VirtualServiceList"},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "VirtualService",
			"metadata": map[string]any{
				"name":      "orders-vs",
				"namespace": "orders",
				"uid":       "uid-orders-vs",
				"labels":    map[string]any{"app": "orders"},
			},
			"spec": map[string]any{"hosts": []any{"orders.example.internal"}},
		}},
	)
	reader := NewKubernetesReader(staticResourceBundleProvider{bundle: kubeclient.Bundle{
		Clientset: fake.NewSimpleClientset(),
		Dynamic:   dynamicClient,
		Discovery: discoveryForResources("networking.istio.io/v1beta1", metav1.APIResource{Name: "virtualservices", Kind: "VirtualService", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}}),
	}})

	items, err := reader.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: "VirtualService"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, Identity{
		ClusterID:  "prod",
		Namespace:  "orders",
		APIVersion: "networking.istio.io/v1beta1",
		Kind:       "VirtualService",
		Name:       "orders-vs",
		UID:        "uid-orders-vs",
	}, items[0].Identity)
	require.Equal(t, "active", items[0].Status)
	require.Equal(t, "orders", items[0].Labels["app"])
}

func TestKubernetesReaderListsStartorchResourceKindsWithResolvedVersion(t *testing.T) {
	pvcGVR := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	pvGVR := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	envoyFilterGVR := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1alpha3", Resource: "envoyfilters"}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			pvcGVR:         "PersistentVolumeClaimList",
			pvGVR:          "PersistentVolumeList",
			envoyFilterGVR: "EnvoyFilterList",
		},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "PersistentVolumeClaim",
			"metadata": map[string]any{
				"name":      "orders-data",
				"namespace": "orders",
				"uid":       "uid-pvc",
			},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "PersistentVolume",
			"metadata": map[string]any{
				"name": "pv-orders",
				"uid":  "uid-pv",
			},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]any{
				"name":      "orders-filter",
				"namespace": "orders",
				"uid":       "uid-envoy",
			},
		}},
	)
	reader := NewKubernetesReader(staticResourceBundleProvider{bundle: kubeclient.Bundle{
		Clientset: fake.NewSimpleClientset(),
		Dynamic:   dynamicClient,
		Discovery: &discoveryfake.FakeDiscovery{
			Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{
				{GroupVersion: "v1", APIResources: []metav1.APIResource{
					{Name: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true},
					{Name: "persistentvolumes", Kind: "PersistentVolume", Namespaced: false},
				}},
				{GroupVersion: "networking.istio.io/v1alpha3", APIResources: []metav1.APIResource{{Name: "envoyfilters", Kind: "EnvoyFilter", Namespaced: true}}},
			}},
			FakedServerVersion: &version.Info{GitVersion: "v1.30.2"},
		},
	}})

	for _, tt := range []struct {
		kind       string
		wantName   string
		wantAPI    string
		wantNS     string
		wantStatus string
	}{
		{kind: "PersistentVolumeClaim", wantName: "orders-data", wantAPI: "v1", wantNS: "orders", wantStatus: "active"},
		{kind: "PersistentVolume", wantName: "pv-orders", wantAPI: "v1", wantNS: "", wantStatus: "active"},
		{kind: "EnvoyFilter", wantName: "orders-filter", wantAPI: "networking.istio.io/v1alpha3", wantNS: "orders", wantStatus: "active"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			items, err := reader.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: tt.kind})

			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, tt.wantName, items[0].Identity.Name)
			require.Equal(t, tt.wantAPI, items[0].Identity.APIVersion)
			require.Equal(t, tt.kind, items[0].Identity.Kind)
			require.Equal(t, tt.wantNS, items[0].Identity.Namespace)
			require.Equal(t, tt.wantStatus, items[0].Status)
		})
	}
}

func TestKubernetesReaderReturnsEmptyForKnownUnavailableKind(t *testing.T) {
	reader := NewKubernetesReader(staticResourceClientsetProvider{client: fake.NewSimpleClientset()})

	items, err := reader.List(context.Background(), ListFilter{ClusterID: "prod", Namespace: "orders", Kind: "VirtualService"})

	require.NoError(t, err)
	require.Empty(t, items)
}

func TestKubernetesReaderReadsClusterScopedPersistentVolumeYAML(t *testing.T) {
	pvGVR := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata": map[string]any{
			"name": "pv-orders",
			"uid":  "uid-pv",
		},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{pvGVR: "PersistentVolumeList"},
		object,
	)
	reader := NewKubernetesReader(staticResourceBundleProvider{bundle: kubeclient.Bundle{
		Clientset: fake.NewSimpleClientset(),
		Dynamic:   dynamicClient,
		Discovery: discoveryForResources("v1", metav1.APIResource{Name: "persistentvolumes", Kind: "PersistentVolume", Namespaced: false}),
	}})

	rendered, err := reader.GetYAML(context.Background(), DetailQuery{Identity: Identity{
		ClusterID:  "prod",
		Namespace:  "",
		APIVersion: "v1",
		Kind:       "PersistentVolume",
		Name:       "pv-orders",
		UID:        "uid-pv",
	}})

	require.NoError(t, err)
	require.Contains(t, rendered.YAML, "kind: PersistentVolume")
	require.Equal(t, "", rendered.Identity.Namespace)
}

func TestKubernetesReaderReadsIstioYAMLWithResolvedVersion(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1beta1", Resource: "virtualservices"}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.istio.io/v1beta1",
		"kind":       "VirtualService",
		"metadata": map[string]any{
			"name":          "orders-vs",
			"namespace":     "orders",
			"uid":           "uid-orders-vs",
			"managedFields": []any{map[string]any{"manager": "test"}},
		},
		"spec": map[string]any{"hosts": []any{"orders.example.internal"}},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "VirtualServiceList"},
		object,
	)
	reader := NewKubernetesReader(staticResourceBundleProvider{bundle: kubeclient.Bundle{
		Clientset: fake.NewSimpleClientset(),
		Dynamic:   dynamicClient,
		Discovery: discoveryForResources("networking.istio.io/v1beta1", metav1.APIResource{Name: "virtualservices", Kind: "VirtualService", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}}),
	}})
	identity := Identity{ClusterID: "prod", Namespace: "orders", APIVersion: "networking.istio.io/v1", Kind: "VirtualService", Name: "orders-vs", UID: "uid-orders-vs"}

	rendered, err := reader.GetYAML(context.Background(), DetailQuery{Identity: identity})

	require.NoError(t, err)
	require.Equal(t, "networking.istio.io/v1beta1", rendered.Identity.APIVersion)
	require.Contains(t, rendered.YAML, "apiVersion: networking.istio.io/v1beta1")
	require.Contains(t, rendered.YAML, "kind: VirtualService")
	require.NotContains(t, rendered.YAML, "managedFields")
}

func TestKubernetesReaderListsRuntimeGroupsFromTypedResources(t *testing.T) {
	replicas := int32(2)
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "orders", CreationTimestamp: metav1.NewTime(time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.0.0.12",
				Selector:  map[string]string{"app": "orders"},
				Ports:     []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-api", Namespace: "orders"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "orders"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "orders", "tier": "api"}},
				},
			},
			Status: appsv1.DeploymentStatus{ReadyReplicas: 2},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-api-1", Namespace: "orders", Labels: map[string]string{"app": "orders", "tier": "api"}},
			Spec: corev1.PodSpec{
				ServiceAccountName: "orders-sa",
				Containers: []corev1.Container{{
					Name:  "api",
					Image: "orders:v1",
					EnvFrom: []corev1.EnvFromSource{{
						ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "orders-config"}},
					}},
				}},
				Volumes: []corev1.Volume{{
					Name:         "data",
					VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "orders-data"}},
				}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "api",
					Ready:        true,
					RestartCount: 2,
				}},
			},
		},
	)
	reader := NewKubernetesReader(staticResourceClientsetProvider{client: client})

	result, err := reader.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{ClusterID: "prod", Namespace: "orders"})

	require.NoError(t, err)
	require.Equal(t, "prod", result.ClusterID)
	require.Equal(t, "orders", result.Namespace)
	require.Equal(t, uint64(1), result.Summary.GroupCount)
	require.Equal(t, uint64(1), result.Summary.ServiceCount)
	require.Equal(t, uint64(1), result.Summary.WorkloadCount)
	require.Equal(t, uint64(1), result.Summary.PodCount)
	require.Equal(t, uint64(1), result.Summary.PVCCount)
	require.Len(t, result.Groups, 1)
	group := result.Groups[0]
	require.Equal(t, "orders", group.DisplayName)
	require.Len(t, group.Services, 1)
	require.Len(t, group.Workloads, 1)
	require.Equal(t, "orders-api", group.Workloads[0].Name)
	require.Equal(t, uint64(1), group.Workloads[0].PodsSummary.Total)
	require.Equal(t, uint64(1), group.Workloads[0].PodsSummary.Running)
	require.Equal(t, uint64(1), group.Workloads[0].PodsSummary.ReadyContainers)
	require.Equal(t, int32(2), group.Workloads[0].PodsSummary.RestartCount)
	require.Equal(t, []string{"orders-sa"}, group.Workloads[0].ServiceAccounts)
	require.Equal(t, []string{"orders-config"}, group.Workloads[0].ConfigMaps)
	require.Equal(t, []string{"orders-data"}, group.Workloads[0].PersistentVolumeClaims)
}

func TestKubernetesReaderRuntimeGroupsAttachDynamicVersionedResources(t *testing.T) {
	typedClient := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "orders"},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.0.0.12",
				Selector:  map[string]string{"app": "orders"},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-api", Namespace: "orders"},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "orders"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "orders"}},
				},
			},
		},
	)
	hpaGVR := schema.GroupVersionResource{Group: "autoscaling", Version: "v1", Resource: "horizontalpodautoscalers"}
	ingressGVR := schema.GroupVersionResource{Group: "extensions", Version: "v1beta1", Resource: "ingresses"}
	gatewayGVR := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1beta1", Resource: "gateways"}
	virtualServiceGVR := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1beta1", Resource: "virtualservices"}
	destinationRuleGVR := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1alpha3", Resource: "destinationrules"}
	authorizationPolicyGVR := schema.GroupVersionResource{Group: "security.istio.io", Version: "v1beta1", Resource: "authorizationpolicies"}
	serviceRoleBindingGVR := schema.GroupVersionResource{Group: "rbac.istio.io", Version: "v1alpha1", Resource: "servicerolebindings"}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			hpaGVR:                 "HorizontalPodAutoscalerList",
			ingressGVR:             "IngressList",
			gatewayGVR:             "GatewayList",
			virtualServiceGVR:      "VirtualServiceList",
			destinationRuleGVR:     "DestinationRuleList",
			authorizationPolicyGVR: "AuthorizationPolicyList",
			serviceRoleBindingGVR:  "ServiceRoleBindingList",
		},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "autoscaling/v1",
			"kind":       "HorizontalPodAutoscaler",
			"metadata": map[string]any{
				"name":      "orders-hpa",
				"namespace": "orders",
			},
			"spec": map[string]any{
				"scaleTargetRef": map[string]any{"kind": "Deployment", "name": "orders-api"},
				"minReplicas":    int64(2),
				"maxReplicas":    int64(8),
			},
			"status": map[string]any{"currentReplicas": int64(3)},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "extensions/v1beta1",
			"kind":       "Ingress",
			"metadata": map[string]any{
				"name":      "orders-ing",
				"namespace": "orders",
			},
			"spec": map[string]any{"rules": []any{map[string]any{
				"host": "orders.example.internal",
				"http": map[string]any{"paths": []any{map[string]any{
					"backend": map[string]any{"serviceName": "orders"},
				}}},
			}}},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "Gateway",
			"metadata": map[string]any{
				"name":      "orders-gw",
				"namespace": "orders",
			},
			"spec": map[string]any{"servers": []any{map[string]any{
				"hosts": []any{"orders.example.internal"},
			}}},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "VirtualService",
			"metadata": map[string]any{
				"name":      "orders-vs",
				"namespace": "orders",
			},
			"spec": map[string]any{
				"hosts":    []any{"orders.example.internal"},
				"gateways": []any{"orders-gw"},
				"http": []any{map[string]any{
					"match":   []any{map[string]any{"uri": map[string]any{"prefix": "/api"}, "headers": map[string]any{"x-tenant": map[string]any{"exact": "gold"}}}},
					"rewrite": map[string]any{"uri": "/"},
					"route": []any{map[string]any{
						"destination": map[string]any{"host": "orders.orders.svc.cluster.local", "subset": "v1", "port": map[string]any{"number": int64(80)}},
						"weight":      int64(100),
					}},
				}},
			},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]any{
				"name":      "orders-dr",
				"namespace": "orders",
			},
			"spec": map[string]any{
				"host":          "orders",
				"trafficPolicy": map[string]any{"loadBalancer": map[string]any{"simple": "ROUND_ROBIN"}},
				"subsets":       []any{map[string]any{"name": "v1", "labels": map[string]any{"version": "v1"}}},
			},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "security.istio.io/v1beta1",
			"kind":       "AuthorizationPolicy",
			"metadata": map[string]any{
				"name":      "orders-authz",
				"namespace": "orders",
			},
			"spec": map[string]any{
				"selector": map[string]any{"matchLabels": map[string]any{"app": "orders"}},
				"action":   "ALLOW",
				"rules":    []any{map[string]any{}},
			},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "rbac.istio.io/v1alpha1",
			"kind":       "ServiceRoleBinding",
			"metadata": map[string]any{
				"name":      "orders-legacy-rbac",
				"namespace": "orders",
			},
			"spec": map[string]any{
				"roleRef": map[string]any{"name": "orders-reader"},
				"subjects": []any{map[string]any{
					"user": "cluster.local/ns/orders/sa/orders-sa",
				}},
			},
		}},
	)
	discovery := &discoveryfake.FakeDiscovery{
		Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{
			{GroupVersion: "autoscaling/v1", APIResources: []metav1.APIResource{{Name: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true}}},
			{GroupVersion: "extensions/v1beta1", APIResources: []metav1.APIResource{{Name: "ingresses", Kind: "Ingress", Namespaced: true}}},
			{GroupVersion: "networking.istio.io/v1beta1", APIResources: []metav1.APIResource{
				{Name: "gateways", Kind: "Gateway", Namespaced: true},
				{Name: "virtualservices", Kind: "VirtualService", Namespaced: true},
			}},
			{GroupVersion: "networking.istio.io/v1alpha3", APIResources: []metav1.APIResource{{Name: "destinationrules", Kind: "DestinationRule", Namespaced: true}}},
			{GroupVersion: "security.istio.io/v1beta1", APIResources: []metav1.APIResource{{Name: "authorizationpolicies", Kind: "AuthorizationPolicy", Namespaced: true}}},
			{GroupVersion: "rbac.istio.io/v1alpha1", APIResources: []metav1.APIResource{{Name: "servicerolebindings", Kind: "ServiceRoleBinding", Namespaced: true}}},
		}},
		FakedServerVersion: &version.Info{GitVersion: "v1.30.2"},
	}
	reader := NewKubernetesReader(staticResourceBundleProvider{bundle: kubeclient.Bundle{
		Clientset: typedClient,
		Dynamic:   dynamicClient,
		Discovery: discovery,
	}})

	result, err := reader.ListRuntimeGroups(context.Background(), RuntimeGroupsQuery{ClusterID: "prod", Namespace: "orders"})

	require.NoError(t, err)
	require.Equal(t, uint64(1), result.Summary.VirtualServiceCount)
	require.Equal(t, uint64(1), result.Summary.GatewayCount)
	require.Equal(t, uint64(1), result.Summary.DestinationRuleCount)
	require.Equal(t, uint64(1), result.Summary.SecurityPolicyCount)
	require.Len(t, result.Groups, 1)
	require.Len(t, result.Groups[0].Exposures, 2)
	service := result.Groups[0].Services[0]
	require.Equal(t, []string{"orders-vs"}, service.VirtualServices)
	require.Equal(t, []string{"orders-gw"}, service.Gateways)
	require.Equal(t, []string{"orders-dr"}, service.DestinationRules)
	require.Len(t, service.VirtualServiceDetails, 1)
	require.Len(t, service.VirtualServiceDetails[0].Routes, 1)
	require.Equal(t, "HTTP", service.VirtualServiceDetails[0].Routes[0].Protocol)
	require.Equal(t, "/", *service.VirtualServiceDetails[0].Routes[0].RewriteURI)
	require.Contains(t, service.VirtualServiceDetails[0].Routes[0].Matches[0].Summary, "uri prefix /api")
	require.Contains(t, service.VirtualServiceDetails[0].Routes[0].Matches[0].Summary, "headers x-tenant exact gold")
	require.Len(t, result.Groups[0].Workloads[0].HPAs, 1)
	require.Equal(t, "orders-hpa", result.Groups[0].Workloads[0].HPAs[0].Name)
	require.Len(t, result.Groups[0].Workloads[0].SecurityPolicies, 1)
	require.Equal(t, "orders-authz", result.Groups[0].Workloads[0].SecurityPolicies[0].Name)
}

func TestRuntimeGatewayExposuresExtractHosts(t *testing.T) {
	items := []unstructured.Unstructured{{
		Object: map[string]any{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "Gateway",
			"metadata": map[string]any{
				"name":      "orders-gw",
				"namespace": "orders",
			},
			"spec": map[string]any{"servers": []any{map[string]any{
				"hosts": []any{"orders.example.internal", "orders.mesh.internal"},
			}}},
		},
	}}

	exposures := runtimeGatewayExposures(items)

	require.Len(t, exposures, 1)
	require.Equal(t, "Gateway/orders-gw", exposures[0].Key)
	require.Equal(t, []string{"orders.example.internal", "orders.mesh.internal"}, exposures[0].Hosts)
}

func TestKubernetesReaderRequiresNamespaceScopedReadPermission(t *testing.T) {
	reader := NewKubernetesReader(staticResourceClientsetProvider{client: fake.NewSimpleClientset()}, denyResourceReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"})

	_, err := reader.List(ctx, ListFilter{ClusterID: "prod", Namespace: "orders", Kind: "Pod"})

	require.ErrorIs(t, err, ErrReadPermissionDenied)
}

func TestKubernetesReaderRequiresNamespaceForResourceList(t *testing.T) {
	reader := NewKubernetesReader(staticResourceClientsetProvider{client: fake.NewSimpleClientset()}, allowResourceReadAuthorizer{})

	_, err := reader.List(context.Background(), ListFilter{ClusterID: "prod", Kind: "Pod"})

	require.ErrorIs(t, err, ErrNamespaceRequired)
}

func TestPodLogOptionsAreBounded(t *testing.T) {
	options := podLogOptions("api")

	require.NotNil(t, options.TailLines)
	require.NotNil(t, options.LimitBytes)
	require.Equal(t, int64(200), *options.TailLines)
	require.Equal(t, int64(1_048_576), *options.LimitBytes)
	require.Equal(t, "api", options.Container)
}

type staticResourceClientsetProvider struct {
	client kubernetes.Interface
}

func (p staticResourceClientsetProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.client, nil
}

type staticResourceBundleProvider struct {
	bundle kubeclient.Bundle
}

func (p staticResourceBundleProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.bundle.Clientset, nil
}

func (p staticResourceBundleProvider) Bundle(_ context.Context, _ string) (kubeclient.Bundle, error) {
	return p.bundle, nil
}

func discoveryForResources(groupVersion string, resources ...metav1.APIResource) *discoveryfake.FakeDiscovery {
	return &discoveryfake.FakeDiscovery{
		Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{{
			GroupVersion: groupVersion,
			APIResources: resources,
		}}},
		FakedServerVersion: &version.Info{GitVersion: "v1.30.2"},
	}
}

type denyResourceReadAuthorizer struct{}

func (denyResourceReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type allowResourceReadAuthorizer struct{}

func (allowResourceReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}
