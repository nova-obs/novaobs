package rbac

import (
	"context"
	"testing"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesRepositoryListsRolesAndBindings(t *testing.T) {
	createdAt := time.Date(2026, 5, 20, 18, 30, 0, 0, time.UTC)
	client := fake.NewSimpleClientset(
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "logplatform", UID: "uid-role", CreationTimestamp: metav1.NewTime(createdAt)},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list"},
			}},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "reader-binding", Namespace: "logplatform", UID: "uid-binding", CreationTimestamp: metav1.NewTime(createdAt)},
			RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: "reader"},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "reader", Namespace: "logplatform"}},
		},
	)
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "dev-admin", Type: "user"})

	roles, roleErr := repo.ListRoles(ctx, ListFilter{ClusterID: "test03-02", Namespace: "logplatform"})
	bindings, bindingErr := repo.ListBindings(ctx, ListFilter{ClusterID: "test03-02", Namespace: "logplatform"})

	require.NoError(t, roleErr)
	require.NoError(t, bindingErr)
	require.Equal(t, []RoleResource{{
		ID:        "uid-role",
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "Role",
		Name:      "reader",
		UID:       "uid-role",
		Rules:     []Rule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}},
		Source:    "Kubernetes API",
		UpdatedAt: createdAt,
	}}, roles)
	require.Equal(t, []BindingResource{{
		ID:        "uid-binding",
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "RoleBinding",
		Name:      "reader-binding",
		UID:       "uid-binding",
		RoleRef:   RoleRef{Kind: "Role", Name: "reader"},
		Subjects:  []Subject{{Kind: "ServiceAccount", Name: "reader", Namespace: "logplatform"}},
		Source:    "Kubernetes API",
		UpdatedAt: createdAt,
	}}, bindings)
}

func TestKubernetesRepositoryRequiresNamespaceAndReadPermission(t *testing.T) {
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: fake.NewSimpleClientset()}, denyRBACReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"})

	_, missingNamespaceErr := repo.ListRoles(ctx, ListFilter{ClusterID: "test03-02"})
	_, deniedErr := repo.ListBindings(ctx, ListFilter{ClusterID: "test03-02", Namespace: "logplatform"})

	require.ErrorIs(t, missingNamespaceErr, ErrInvalidRequest)
	require.ErrorIs(t, deniedErr, ErrPermissionDenied)
}

func TestKubernetesRepositoryDisablesWrites(t *testing.T) {
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: fake.NewSimpleClientset()}, allowRBACReadAuthorizer{})

	_, roleErr := repo.UpsertRole(context.Background(), RoleResource{})
	_, bindingErr := repo.DeleteBinding(context.Background(), DeleteRequest{})

	require.ErrorIs(t, roleErr, ErrWriteUnavailable)
	require.ErrorIs(t, bindingErr, ErrWriteUnavailable)
}

type staticRBACClientsetProvider struct {
	client kubernetes.Interface
}

func (p staticRBACClientsetProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.client, nil
}

type allowRBACReadAuthorizer struct{}

func (allowRBACReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type denyRBACReadAuthorizer struct{}

func (denyRBACReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}
