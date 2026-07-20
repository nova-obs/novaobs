package namespace

import (
	"context"
	"testing"
	"time"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesRepositoryListsNamespacesFromCluster(t *testing.T) {
	createdAt := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "orders",
				UID:               "uid-orders",
				Labels:            map[string]string{"novaapm.io/owner": "orders-team"},
				CreationTimestamp: metav1.NewTime(createdAt),
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "payment",
				UID:               "uid-payment",
				CreationTimestamp: metav1.NewTime(createdAt.Add(time.Minute)),
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating},
		},
	)

	repo := NewKubernetesRepository(staticClientsetProvider{client: client})
	items, err := repo.List(context.Background(), ListFilter{ClusterID: "prod", Sort: "name", Order: "asc"})

	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, Namespace{
		ID:        "uid-orders",
		ClusterID: "prod",
		Name:      "orders",
		Status:    "active",
		Owner:     "orders-team",
		Phase:     "Active",
		UpdatedAt: createdAt,
	}, items[0])
	require.Equal(t, "payment", items[1].Name)
	require.Equal(t, "terminating", items[1].Status)
}

func TestKubernetesRepositoryRequiresNamespaceReadPermission(t *testing.T) {
	repo := NewKubernetesRepository(staticClientsetProvider{client: fake.NewSimpleClientset()}, denyReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"})

	_, err := repo.List(ctx, ListFilter{ClusterID: "prod"})

	require.ErrorIs(t, err, ErrReadPermissionDenied)
}

func TestKubernetesRepositoryCreatesNamespaceInCluster(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := NewKubernetesRepository(staticClientsetProvider{client: client})

	created, err := repo.Create(context.Background(), Namespace{
		ClusterID: "prod",
		Name:      "orders",
		Owner:     "orders-team",
	})

	require.NoError(t, err)
	require.Equal(t, "orders", created.Name)
	require.Equal(t, "prod", created.ClusterID)
	require.Equal(t, "orders-team", created.Owner)
	stored, getErr := client.CoreV1().Namespaces().Get(context.Background(), "orders", metav1.GetOptions{})
	require.NoError(t, getErr)
	require.Equal(t, "orders-team", stored.Labels["novaapm.io/owner"])
}

func TestKubernetesRepositoryDeletesNamespaceByUID(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", UID: "uid-orders"},
	})
	repo := NewKubernetesRepository(staticClientsetProvider{client: client})

	deleted, err := repo.Delete(context.Background(), DeleteRequest{ClusterID: "prod", Name: "orders", UID: "uid-orders"})

	require.NoError(t, err)
	require.Equal(t, "orders", deleted.Name)
	_, getErr := client.CoreV1().Namespaces().Get(context.Background(), "orders", metav1.GetOptions{})
	require.Error(t, getErr)
}

func TestKubernetesRepositoryDeleteRejectsUIDMismatch(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", UID: "uid-orders"},
	})
	repo := NewKubernetesRepository(staticClientsetProvider{client: client})

	_, err := repo.Delete(context.Background(), DeleteRequest{ClusterID: "prod", Name: "orders", UID: "other-uid"})

	require.ErrorIs(t, err, ErrNotFound)
	_, getErr := client.CoreV1().Namespaces().Get(context.Background(), "orders", metav1.GetOptions{})
	require.NoError(t, getErr)
}

type staticClientsetProvider struct {
	client kubernetes.Interface
}

func (p staticClientsetProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.client, nil
}

type denyReadAuthorizer struct{}

func (denyReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}
