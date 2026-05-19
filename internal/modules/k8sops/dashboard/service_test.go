package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	require.Equal(t, "最近 15 分钟", snapshot.Sync.TimeWindow)
}
