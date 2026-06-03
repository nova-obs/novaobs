package dashboard

import (
	"context"
	"testing"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type staticReader struct {
	snapshot Snapshot
}

func (r staticReader) Read(context.Context, Query) (Snapshot, error) {
	return r.snapshot, nil
}

func TestServiceReturnsDashboardSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 19, 10, 30, 0, 0, time.UTC)
	service := NewService(staticReader{snapshot: Snapshot{
		Stats: Stats{
			ClusterID:  "prod",
			Health:     HealthUnknown,
			Namespaces: 12,
			Workloads:  47,
			Pods:       PodStats{Total: 128, Ready: 109, Warning: 7},
		},
		Signals: []Signal{
			{Key: "api", Label: "API Server", Status: HealthUnknown, Source: "startorch", CheckedAt: now},
		},
		Sync: SyncState{
			Status:       SyncUnknown,
			Source:       "startorch",
			TimeWindow:   "最近 15 分钟",
			LastSyncedAt: now,
		},
	}})

	snapshot, err := service.Get(context.Background(), Query{ClusterID: "prod"})

	require.NoError(t, err)
	require.Equal(t, "prod", snapshot.Stats.ClusterID)
	require.Equal(t, HealthUnknown, snapshot.Stats.Health)
	require.Equal(t, 109, snapshot.Stats.Pods.Ready)
	require.Equal(t, "startorch", snapshot.Sync.Source)
	require.Len(t, snapshot.Signals, 1)
}

func TestStaticReaderDefaultsUnknownClusterHealth(t *testing.T) {
	reader := NewStaticReader()

	snapshot, err := reader.Read(context.Background(), Query{ClusterID: "prod"})

	require.NoError(t, err)
	require.Equal(t, "prod", snapshot.Stats.ClusterID)
	require.Equal(t, HealthUnknown, snapshot.Stats.Health)
	require.Equal(t, SyncUnknown, snapshot.Sync.Status)
	require.Equal(t, "NovaObs", snapshot.Sync.Source)
	require.Equal(t, "等待真实集群接入", snapshot.Sync.TimeWindow)
	require.Empty(t, snapshot.Signals)
}

func TestKubernetesReaderBuildsRealClusterSnapshot(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "logplatform", UID: "uid-logplatform"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "monitoring", UID: "uid-monitoring"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "logplatform"},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "logplatform"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "agent-0", Namespace: "monitoring"},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
	)
	reader := NewKubernetesReader(staticDashboardClientsetProvider{client: client}, allowDashboardReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "dev-admin", Type: "user"})

	snapshot, err := reader.Read(ctx, Query{ClusterID: "test03-02"})

	require.NoError(t, err)
	require.Equal(t, "test03-02", snapshot.Stats.ClusterID)
	require.Equal(t, 2, snapshot.Stats.Namespaces)
	require.Equal(t, 1, snapshot.Stats.Workloads)
	require.Equal(t, 2, snapshot.Stats.Pods.Total)
	require.Equal(t, 1, snapshot.Stats.Pods.Ready)
	require.Equal(t, 1, snapshot.Stats.Pods.Warning)
	require.Equal(t, HealthWarning, snapshot.Stats.Health)
	require.Equal(t, SyncApplied, snapshot.Sync.Status)
	require.Equal(t, "Kubernetes API", snapshot.Sync.Source)
	require.NotEmpty(t, snapshot.Signals)
}

func TestKubernetesReaderRequiresNamespaceReadPermission(t *testing.T) {
	reader := NewKubernetesReader(staticDashboardClientsetProvider{client: fake.NewSimpleClientset()}, denyDashboardReadAuthorizer{})
	ctx := authctx.WithSubject(context.Background(), platformrbac.Subject{ID: "user-1", Type: "user"})

	_, err := reader.Read(ctx, Query{ClusterID: "prod"})

	require.ErrorIs(t, err, ErrReadPermissionDenied)
}

type staticDashboardClientsetProvider struct {
	client kubernetes.Interface
}

func (p staticDashboardClientsetProvider) Clientset(_ context.Context, _ string) (kubernetes.Interface, error) {
	return p.client, nil
}

type allowDashboardReadAuthorizer struct{}

func (allowDashboardReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}

type denyDashboardReadAuthorizer struct{}

func (denyDashboardReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: false, Reason: "permission_denied"}
}
