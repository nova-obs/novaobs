package rbac

import (
	"context"
	"sort"
	"strings"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type KubernetesRepository struct {
	clients    ClientsetProvider
	authorizer Authorizer
}

func NewKubernetesRepository(clients ClientsetProvider, dependencies ...any) KubernetesRepository {
	repo := KubernetesRepository{clients: clients, authorizer: denyAuthorizer{}}
	for _, dependency := range dependencies {
		if value, ok := dependency.(Authorizer); ok && value != nil {
			repo.authorizer = value
		}
	}
	return repo
}

func (r KubernetesRepository) ListRoles(ctx context.Context, filter ListFilter) ([]RoleResource, error) {
	filter = normalizeListFilter(filter)
	if filter.ClusterID == "" || filter.Namespace == "" {
		return nil, ErrInvalidRequest
	}
	if !r.allowed(ctx, filter.ClusterID, filter.Namespace) {
		return nil, ErrPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	result, err := client.RbacV1().Roles(filter.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]RoleResource, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapKubernetesRole(filter.ClusterID, item))
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) UpsertRole(context.Context, RoleResource) (RoleResource, error) {
	return RoleResource{}, ErrWriteUnavailable
}

func (r KubernetesRepository) DeleteRole(context.Context, DeleteRequest) (RoleResource, error) {
	return RoleResource{}, ErrWriteUnavailable
}

func (r KubernetesRepository) ListBindings(ctx context.Context, filter ListFilter) ([]BindingResource, error) {
	filter = normalizeListFilter(filter)
	if filter.ClusterID == "" || filter.Namespace == "" {
		return nil, ErrInvalidRequest
	}
	if !r.allowed(ctx, filter.ClusterID, filter.Namespace) {
		return nil, ErrPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	result, err := client.RbacV1().RoleBindings(filter.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]BindingResource, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapKubernetesBinding(filter.ClusterID, item))
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) UpsertBinding(context.Context, BindingResource) (BindingResource, error) {
	return BindingResource{}, ErrWriteUnavailable
}

func (r KubernetesRepository) DeleteBinding(context.Context, DeleteRequest) (BindingResource, error) {
	return BindingResource{}, ErrWriteUnavailable
}

func (r KubernetesRepository) allowed(ctx context.Context, clusterID string, namespace string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	decision := r.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.rbac",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}

func normalizeListFilter(filter ListFilter) ListFilter {
	filter.ClusterID = strings.TrimSpace(filter.ClusterID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	return filter
}

func mapKubernetesRole(clusterID string, item rbacv1.Role) RoleResource {
	rules := make([]Rule, 0, len(item.Rules))
	for _, rule := range item.Rules {
		rules = append(rules, Rule{
			APIGroups: append([]string(nil), rule.APIGroups...),
			Resources: append([]string(nil), rule.Resources...),
			Verbs:     append([]string(nil), rule.Verbs...),
		})
	}
	return RoleResource{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Namespace: item.Namespace,
		Kind:      "Role",
		Name:      item.Name,
		UID:       string(item.UID),
		Rules:     rules,
		Source:    "Kubernetes API",
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func mapKubernetesBinding(clusterID string, item rbacv1.RoleBinding) BindingResource {
	subjects := make([]Subject, 0, len(item.Subjects))
	for _, subject := range item.Subjects {
		subjects = append(subjects, Subject{
			Kind:      subject.Kind,
			Name:      subject.Name,
			Namespace: subject.Namespace,
		})
	}
	return BindingResource{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Namespace: item.Namespace,
		Kind:      "RoleBinding",
		Name:      item.Name,
		UID:       string(item.UID),
		RoleRef:   RoleRef{Kind: item.RoleRef.Kind, Name: item.RoleRef.Name},
		Subjects:  subjects,
		Source:    "Kubernetes API",
		UpdatedAt: item.CreationTimestamp.Time,
	}
}
