package serviceaccount

import (
	"context"
	"testing"
	"time"

	"novaobs/internal/platform/authctx"
	"novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesRepositoryListsServiceAccounts(t *testing.T) {
	createdAt := time.Date(2026, 5, 20, 15, 40, 0, 0, time.UTC)
	client := fake.NewSimpleClientset(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prometheus",
			Namespace:         "logplatform",
			UID:               "uid-prometheus",
			CreationTimestamp: metav1.NewTime(createdAt),
		},
	})
	repo := NewKubernetesRepository(staticServiceAccountClientsetProvider{client: client}, allowServiceAccountReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), rbac.Subject{ID: "dev-admin", Type: "user"})

	items, err := repo.List(ctx, ListFilter{ClusterID: "test03-02", Namespace: "logplatform"})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, ServiceAccount{
		ID:        "uid-prometheus",
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Name:      "prometheus",
		UID:       "uid-prometheus",
		Status:    "active",
		Source:    "Kubernetes API",
		CreatedAt: createdAt,
	}, items[0])
}

func TestKubernetesRepositoryRequiresNamespaceAndReadPermission(t *testing.T) {
	repo := NewKubernetesRepository(staticServiceAccountClientsetProvider{client: fake.NewSimpleClientset()}, denyServiceAccountReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), rbac.Subject{ID: "user-1", Type: "user"})

	_, missingNamespaceErr := repo.List(ctx, ListFilter{ClusterID: "test03-02"})
	_, deniedErr := repo.List(ctx, ListFilter{ClusterID: "test03-02", Namespace: "logplatform"})

	require.ErrorIs(t, missingNamespaceErr, ErrInvalidRequest)
	require.ErrorIs(t, deniedErr, ErrPermissionDenied)
}

type staticServiceAccountClientsetProvider struct {
	client kubernetes.Interface
}

func (p staticServiceAccountClientsetProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.client, nil
}

type allowServiceAccountReadAuthorizer struct{}

func (allowServiceAccountReadAuthorizer) Authorize(_ rbac.Subject, _ rbac.Request) rbac.Decision {
	return rbac.Decision{Allowed: true}
}

type denyServiceAccountReadAuthorizer struct{}

func (denyServiceAccountReadAuthorizer) Authorize(_ rbac.Subject, _ rbac.Request) rbac.Decision {
	return rbac.Decision{Allowed: false, Reason: "permission_denied"}
}
