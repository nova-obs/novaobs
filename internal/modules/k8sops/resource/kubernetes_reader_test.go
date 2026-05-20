package resource

import (
	"context"
	"strings"
	"testing"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
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

type denyResourceReadAuthorizer struct{}

func (denyResourceReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}

type allowResourceReadAuthorizer struct{}

func (allowResourceReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}
