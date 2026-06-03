package dashboard

import (
	"context"
	"errors"
	"strings"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Reader interface {
	Read(ctx context.Context, query Query) (Snapshot, error)
}

type Service struct {
	reader Reader
}

func NewService(reader Reader) Service {
	return Service{reader: reader}
}

func (s Service) Get(ctx context.Context, query Query) (Snapshot, error) {
	return s.reader.Read(ctx, query)
}

type StaticReader struct {
	now func() time.Time
}

func NewStaticReader() StaticReader {
	return StaticReader{now: time.Now}
}

func (r StaticReader) Read(_ context.Context, query Query) (Snapshot, error) {
	clusterID := query.ClusterID
	if clusterID == "" {
		clusterID = "default"
	}
	now := r.now().UTC()
	return Snapshot{
		Stats: Stats{
			ClusterID:  clusterID,
			Health:     HealthUnknown,
			Namespaces: 0,
			Workloads:  0,
			Pods:       PodStats{},
		},
		Signals: []Signal{},
		Sync: SyncState{
			Status:       SyncUnknown,
			Source:       "NovaObs",
			TimeWindow:   "等待真实集群接入",
			LastSyncedAt: now,
		},
	}, nil
}

var ErrReadPermissionDenied = errors.New("permission_denied")

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type ReadAuthorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type KubernetesReader struct {
	clients    ClientsetProvider
	authorizer ReadAuthorizer
	now        func() time.Time
}

func NewKubernetesReader(clients ClientsetProvider, dependencies ...any) KubernetesReader {
	reader := KubernetesReader{clients: clients, authorizer: allowReadAuthorizer{}, now: time.Now}
	for _, dependency := range dependencies {
		if value, ok := dependency.(ReadAuthorizer); ok && value != nil {
			reader.authorizer = value
		}
	}
	return reader
}

func (r KubernetesReader) Read(ctx context.Context, query Query) (Snapshot, error) {
	clusterID := strings.TrimSpace(query.ClusterID)
	if clusterID == "" {
		clusterID = "default"
	}
	if !r.allowed(ctx, clusterID, "") {
		return Snapshot{}, ErrReadPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, clusterID)
	if err != nil {
		return Snapshot{}, err
	}
	now := r.now().UTC()
	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Snapshot{}, err
	}
	pods := PodStats{}
	workloads := 0
	for _, namespace := range namespaces.Items {
		name := namespace.Name
		if !r.allowed(ctx, clusterID, name) {
			continue
		}
		deploymentList, err := client.AppsV1().Deployments(name).List(ctx, metav1.ListOptions{})
		if err != nil {
			return Snapshot{}, err
		}
		podList, err := client.CoreV1().Pods(name).List(ctx, metav1.ListOptions{})
		if err != nil {
			return Snapshot{}, err
		}
		workloads += len(deploymentList.Items)
		pods.Total += len(podList.Items)
		for _, pod := range podList.Items {
			if podIsReady(pod) {
				pods.Ready++
			}
		}
	}
	pods.Warning = pods.Total - pods.Ready
	health := dashboardHealth(pods, workloads)
	return Snapshot{
		Stats: Stats{
			ClusterID:  clusterID,
			Health:     health,
			Namespaces: len(namespaces.Items),
			Workloads:  workloads,
			Pods:       pods,
		},
		Signals: []Signal{
			{Key: "api-server", Label: "API Server", Status: HealthHealthy, Source: "Kubernetes API", CheckedAt: now},
			{Key: "namespaces", Label: "Namespaces", Status: namespaceHealth(len(namespaces.Items)), Source: "Kubernetes API", CheckedAt: now},
			{Key: "workloads", Label: "Deployments", Status: workloadHealth(workloads), Source: "Kubernetes API", CheckedAt: now},
			{Key: "pods", Label: "Pods", Status: health, Source: "Kubernetes API", CheckedAt: now},
		},
		Sync: SyncState{
			Status:       SyncApplied,
			Source:       "Kubernetes API",
			TimeWindow:   "实时只读快照",
			LastSyncedAt: now,
		},
	}, nil
}

func (r KubernetesReader) allowed(ctx context.Context, clusterID string, namespace string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	resource := "k8s.resource"
	scope := platformrbac.Scope{ClusterID: clusterID, Namespace: strings.TrimSpace(namespace)}
	if strings.TrimSpace(namespace) == "" {
		resource = "k8s.namespace"
	}
	decision := r.authorizer.Authorize(subject, platformrbac.Request{
		Resource: resource,
		Action:   "read",
		Scope:    scope,
	})
	return decision.Allowed
}

func podIsReady(pod corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded {
		return true
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func dashboardHealth(pods PodStats, workloads int) HealthStatus {
	if pods.Total == 0 && workloads == 0 {
		return HealthUnknown
	}
	if pods.Warning > 0 {
		return HealthWarning
	}
	return HealthHealthy
}

func namespaceHealth(namespaces int) HealthStatus {
	if namespaces == 0 {
		return HealthUnknown
	}
	return HealthHealthy
}

func workloadHealth(workloads int) HealthStatus {
	if workloads == 0 {
		return HealthUnknown
	}
	return HealthHealthy
}

type allowReadAuthorizer struct{}

func (allowReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}
