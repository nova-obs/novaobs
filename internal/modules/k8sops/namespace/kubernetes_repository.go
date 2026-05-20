package namespace

import (
	"context"
	"errors"
	"strings"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	corev1 "k8s.io/api/core/v1"
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
