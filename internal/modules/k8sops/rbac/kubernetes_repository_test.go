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

	_, missingNamespaceErr := repo.ListRoles(ctx, ListFilter{})
	_, deniedErr := repo.ListBindings(ctx, ListFilter{ClusterID: "test03-02", Namespace: "logplatform"})

	require.ErrorIs(t, missingNamespaceErr, ErrInvalidRequest)
	require.ErrorIs(t, deniedErr, ErrPermissionDenied)
}

func TestKubernetesRepositoryUpsertsRole(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	created, err := repo.UpsertRole(context.Background(), RoleResource{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "Role",
		Name:      "reader",
		Rules:     []Rule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}},
	})
	require.NoError(t, err)
	require.Equal(t, "reader", created.Name)

	updated, err := repo.UpsertRole(context.Background(), RoleResource{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "Role",
		Name:      "reader",
		Rules:     []Rule{{APIGroups: []string{""}, Resources: []string{"pods", "services"}, Verbs: []string{"get", "list", "watch"}}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pods", "services"}, updated.Rules[0].Resources)
}

func TestKubernetesRepositoryUpsertsClusterRole(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	created, err := repo.UpsertRole(context.Background(), RoleResource{
		ClusterID: "prod",
		Kind:      "ClusterRole",
		Name:      "novaobs-reader",
		Rules:     []Rule{{Resources: []string{"nodes"}, Verbs: []string{"get", "list"}}},
	})

	require.NoError(t, err)
	require.Equal(t, "ClusterRole", created.Kind)
	stored, getErr := client.RbacV1().ClusterRoles().Get(context.Background(), "novaobs-reader", metav1.GetOptions{})
	require.NoError(t, getErr)
	require.Equal(t, []string{"nodes"}, stored.Rules[0].Resources)
}

func TestKubernetesRepositoryDeletesRoleByUID(t *testing.T) {
	client := fake.NewSimpleClientset(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "logplatform", UID: "uid-role"},
	})
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	deleted, err := repo.DeleteRole(context.Background(), DeleteRequest{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "Role",
		Name:      "reader",
		UID:       "uid-role",
	})
	require.NoError(t, err)
	require.Equal(t, "reader", deleted.Name)
	_, err = client.RbacV1().Roles("logplatform").Get(context.Background(), "reader", metav1.GetOptions{})
	require.Error(t, err)
}

func TestKubernetesRepositoryUpsertsRoleBinding(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	created, err := repo.UpsertBinding(context.Background(), BindingResource{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "RoleBinding",
		Name:      "reader-binding",
		RoleRef:   RoleRef{Kind: "Role", Name: "reader"},
		Subjects:  []Subject{{Kind: "ServiceAccount", Name: "reader", Namespace: "logplatform"}},
	})
	require.NoError(t, err)
	require.Equal(t, "reader-binding", created.Name)

	updated, err := repo.UpsertBinding(context.Background(), BindingResource{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "RoleBinding",
		Name:      "reader-binding",
		RoleRef:   RoleRef{Kind: "Role", Name: "reader"},
		Subjects:  []Subject{{Kind: "ServiceAccount", Name: "reader"}, {Kind: "ServiceAccount", Name: "writer"}},
	})
	require.NoError(t, err)
	require.Len(t, updated.Subjects, 2)
}

func TestKubernetesRepositoryUpsertsClusterRoleBinding(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	created, err := repo.UpsertBinding(context.Background(), BindingResource{
		ClusterID: "prod",
		Kind:      "ClusterRoleBinding",
		Name:      "novaobs-reader-binding",
		RoleRef:   RoleRef{Kind: "ClusterRole", Name: "novaobs-reader"},
		Subjects:  []Subject{{Kind: "User", Name: "alice"}},
	})

	require.NoError(t, err)
	require.Equal(t, "ClusterRoleBinding", created.Kind)
	stored, getErr := client.RbacV1().ClusterRoleBindings().Get(context.Background(), "novaobs-reader-binding", metav1.GetOptions{})
	require.NoError(t, getErr)
	require.Equal(t, "novaobs-reader", stored.RoleRef.Name)
}

func TestKubernetesRepositoryDeletesRoleBindingByUID(t *testing.T) {
	client := fake.NewSimpleClientset(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "reader-binding", Namespace: "logplatform", UID: "uid-binding"},
		RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: "reader"},
	})
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	deleted, err := repo.DeleteBinding(context.Background(), DeleteRequest{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "RoleBinding",
		Name:      "reader-binding",
		UID:       "uid-binding",
	})
	require.NoError(t, err)
	require.Equal(t, "reader-binding", deleted.Name)
	_, err = client.RbacV1().RoleBindings("logplatform").Get(context.Background(), "reader-binding", metav1.GetOptions{})
	require.Error(t, err)
}

func TestKubernetesRepositoryRejectsDeleteWhenUIDMismatches(t *testing.T) {
	client := fake.NewSimpleClientset(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "logplatform", UID: "uid-role"},
	})
	repo := NewKubernetesRepository(staticRBACClientsetProvider{client: client}, allowRBACReadAuthorizer{})

	_, err := repo.DeleteRole(context.Background(), DeleteRequest{
		ClusterID: "test03-02",
		Namespace: "logplatform",
		Kind:      "Role",
		Name:      "reader",
		UID:       "uid-other",
	})

	require.ErrorIs(t, err, ErrNotFound)
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
