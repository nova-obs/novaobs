package namespace

import (
	"context"
	"errors"
	"strings"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var ErrReadPermissionDenied = errors.New("permission_denied")

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type ReadAuthorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type KubernetesRepository struct {
	clients    ClientsetProvider
	authorizer ReadAuthorizer
}

func NewKubernetesRepository(clients ClientsetProvider, dependencies ...any) KubernetesRepository {
	repo := KubernetesRepository{clients: clients, authorizer: allowReadAuthorizer{}}
	for _, dependency := range dependencies {
		if value, ok := dependency.(ReadAuthorizer); ok && value != nil {
			repo.authorizer = value
		}
	}
	return repo
}

func (r KubernetesRepository) List(ctx context.Context, filter ListFilter) ([]Namespace, error) {
	if !r.allowed(ctx, filter.ClusterID) {
		return nil, ErrReadPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	result, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]Namespace, 0, len(result.Items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range result.Items {
		namespace := namespaceFromKubernetes(filter.ClusterID, item)
		if query != "" && !strings.Contains(strings.ToLower(namespace.Name), query) && !strings.Contains(strings.ToLower(namespace.Owner), query) {
			continue
		}
		items = append(items, namespace)
	}
	sortNamespaces(items, filter.Sort, filter.Order)
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) Create(ctx context.Context, item Namespace) (Namespace, error) {
	item = normalizeNamespace(item)
	if item.ClusterID == "" || item.Name == "" {
		return Namespace{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, item.ClusterID)
	if err != nil {
		return Namespace{}, err
	}
	labels := map[string]string{}
	if item.Owner != "" {
		labels["novaobs.io/owner"] = item.Owner
	}
	created, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: item.Name, Labels: labels},
	}, metav1.CreateOptions{})
	if err != nil {
		return Namespace{}, err
	}
	return namespaceFromKubernetes(item.ClusterID, *created), nil
}

func (r KubernetesRepository) Delete(ctx context.Context, req DeleteRequest) (Namespace, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Name == "" || req.UID == "" {
		return Namespace{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, req.ClusterID)
	if err != nil {
		return Namespace{}, err
	}
	existing, err := client.CoreV1().Namespaces().Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return Namespace{}, ErrNotFound
		}
		return Namespace{}, err
	}
	if string(existing.UID) != req.UID {
		return Namespace{}, ErrNotFound
	}
	deleted := namespaceFromKubernetes(req.ClusterID, *existing)
	if err := client.CoreV1().Namespaces().Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return Namespace{}, ErrNotFound
		}
		return Namespace{}, err
	}
	return deleted, nil
}

func (r KubernetesRepository) allowed(ctx context.Context, clusterID string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	decision := r.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.namespace",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: strings.TrimSpace(clusterID)},
	})
	return decision.Allowed
}

func namespaceFromKubernetes(clusterID string, item corev1.Namespace) Namespace {
	owner := item.Labels["novaobs.io/owner"]
	if owner == "" {
		owner = item.Labels["owner"]
	}
	return Namespace{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Name:      item.Name,
		Status:    namespaceStatus(item.Status.Phase),
		Owner:     owner,
		Phase:     string(item.Status.Phase),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

type allowReadAuthorizer struct{}

func (allowReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

func namespaceStatus(phase corev1.NamespacePhase) string {
	switch phase {
	case corev1.NamespaceActive:
		return "active"
	case corev1.NamespaceTerminating:
		return "terminating"
	default:
		return "unknown"
	}
}
