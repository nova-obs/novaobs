package dashboard

import (
	"context"
	"time"
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
		Signals: []Signal{
			{Key: "api-server", Label: "API Server", Status: HealthUnknown, Source: "startorch", CheckedAt: now},
			{Key: "collector", Label: "Collector", Status: HealthUnknown, Source: "NovaObs", CheckedAt: now},
		},
		Sync: SyncState{
			Status:       SyncUnknown,
			Source:       "startorch",
			TimeWindow:   "最近 15 分钟",
			LastSyncedAt: now,
		},
	}, nil
}
