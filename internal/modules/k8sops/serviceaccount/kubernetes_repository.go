package serviceaccount

import (
	"context"
	"sort"
	"strings"

	"novaobs/internal/platform/authctx"
	"novaobs/internal/platform/rbac"

	corev1 "k8s.io/api/core/v1"
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
		serviceAccount := mapKubernetesServiceAccount(filter.ClusterID, item)
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

func (r KubernetesRepository) Create(ctx context.Context, item ServiceAccount) (ServiceAccount, error) {
	item.ClusterID = strings.TrimSpace(item.ClusterID)
	item.Namespace = strings.TrimSpace(item.Namespace)
	item.Name = strings.TrimSpace(item.Name)
	if item.ClusterID == "" || item.Namespace == "" || item.Name == "" {
		return ServiceAccount{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, item.ClusterID)
	if err != nil {
		return ServiceAccount{}, err
	}
	created, err := client.CoreV1().ServiceAccounts(item.Namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: item.Name, Namespace: item.Namespace},
	}, metav1.CreateOptions{})
	if err != nil {
		return ServiceAccount{}, err
	}
	return mapKubernetesServiceAccount(item.ClusterID, *created), nil
}

func (r KubernetesRepository) Delete(ctx context.Context, req DeleteRequest) (ServiceAccount, error) {
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Name = strings.TrimSpace(req.Name)
	req.UID = strings.TrimSpace(req.UID)
	if req.ClusterID == "" || req.Namespace == "" || req.Name == "" || req.UID == "" {
		return ServiceAccount{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, req.ClusterID)
	if err != nil {
		return ServiceAccount{}, err
	}
	existing, err := client.CoreV1().ServiceAccounts(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		return ServiceAccount{}, err
	}
	if string(existing.UID) != req.UID {
		return ServiceAccount{}, ErrNotFound
	}
	deleted := mapKubernetesServiceAccount(req.ClusterID, *existing)
	if err := client.CoreV1().ServiceAccounts(req.Namespace).Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
		return ServiceAccount{}, err
	}
	return deleted, nil
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

func mapKubernetesServiceAccount(clusterID string, item corev1.ServiceAccount) ServiceAccount {
	return ServiceAccount{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Namespace: item.Namespace,
		Name:      item.Name,
		UID:       string(item.UID),
		Status:    "active",
		Source:    "Kubernetes API",
		CreatedAt: item.CreationTimestamp.Time,
	}
}
