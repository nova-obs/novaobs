package serviceaccount

import (
	"context"
	"sort"
	"strings"

	"novaobs/internal/platform/authctx"
	"novaobs/internal/platform/rbac"

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

func (r KubernetesRepository) List(ctx context.Context, filter ListFilter) ([]ServiceAccount, error) {
	filter.ClusterID = strings.TrimSpace(filter.ClusterID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
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
	result, err := client.CoreV1().ServiceAccounts(filter.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	items := make([]ServiceAccount, 0, len(result.Items))
	for _, item := range result.Items {
		serviceAccount := ServiceAccount{
			ID:        string(item.UID),
			ClusterID: filter.ClusterID,
			Namespace: item.Namespace,
			Name:      item.Name,
			UID:       string(item.UID),
			Status:    "active",
			Source:    "Kubernetes API",
			CreatedAt: item.CreationTimestamp.Time,
		}
		if query != "" && !strings.Contains(strings.ToLower(serviceAccount.Name), query) && !strings.Contains(strings.ToLower(serviceAccount.UID), query) {
			continue
		}
		items = append(items, serviceAccount)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].Name < items[right].Name
	})
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) Create(context.Context, ServiceAccount) (ServiceAccount, error) {
	return ServiceAccount{}, ErrWriteUnavailable
}

func (r KubernetesRepository) Delete(context.Context, DeleteRequest) (ServiceAccount, error) {
	return ServiceAccount{}, ErrWriteUnavailable
}

func (r KubernetesRepository) allowed(ctx context.Context, clusterID string, namespace string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	decision := r.authorizer.Authorize(subject, rbac.Request{
		Resource: "k8s.service-account",
		Action:   "read",
		Scope:    rbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}
