package metrics

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	ErrIntegrationNotFound      = errors.New("metrics_integration_not_found")
	ErrIntegrationAlreadyExists = errors.New("metrics_integration_already_exists")
	ErrSourceAccessNotFound     = errors.New("metrics_source_access_not_found")
)

type IntegrationRepository interface {
	CreateIntegration(ctx context.Context, item Integration) error
	UpdateIntegration(ctx context.Context, item Integration) error
	DeleteIntegration(ctx context.Context, id string) error
	ListIntegrations(ctx context.Context) ([]Integration, error)
	GetIntegration(ctx context.Context, id string) (Integration, error)
	FindIntegrationByEnvironment(ctx context.Context, environmentID string) (Integration, error)
	CreateSourceAccess(ctx context.Context, item SourceAccess) error
	UpdateSourceAccess(ctx context.Context, item SourceAccess) error
	DeleteSourceAccess(ctx context.Context, id string) error
	ListSourceAccesses(ctx context.Context, integrationID string) ([]SourceAccess, error)
	GetSourceAccess(ctx context.Context, id string) (SourceAccess, error)
	SaveHealthSnapshot(ctx context.Context, item HealthSnapshot) error
	GetLatestHealthSnapshot(ctx context.Context, integrationID string) (HealthSnapshot, error)
	SaveCollectorRelease(ctx context.Context, item CollectorRelease) error
	UpdateCollectorRelease(ctx context.Context, item CollectorRelease) error
	GetLatestCollectorRelease(ctx context.Context, sourceAccessID string) (CollectorRelease, error)
}

type MemoryIntegrationRepository struct {
	mu           sync.RWMutex
	integrations map[string]Integration
	sources      map[string]SourceAccess
	snapshots    map[string][]HealthSnapshot
	releases     map[string][]CollectorRelease
}

func NewMemoryIntegrationRepository() *MemoryIntegrationRepository {
	return &MemoryIntegrationRepository{integrations: map[string]Integration{}, sources: map[string]SourceAccess{}, snapshots: map[string][]HealthSnapshot{}, releases: map[string][]CollectorRelease{}}
}

func (r *MemoryIntegrationRepository) SaveCollectorRelease(_ context.Context, item CollectorRelease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releases[item.SourceAccessID] = append(r.releases[item.SourceAccessID], item)
	return nil
}

func (r *MemoryIntegrationRepository) UpdateCollectorRelease(_ context.Context, item CollectorRelease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.releases[item.SourceAccessID]
	for index := range items {
		if items[index].ID == item.ID {
			items[index] = item
			r.releases[item.SourceAccessID] = items
			return nil
		}
	}
	return ErrIntegrationNotFound
}

func (r *MemoryIntegrationRepository) GetLatestCollectorRelease(_ context.Context, sourceAccessID string) (CollectorRelease, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.releases[sourceAccessID]
	if len(items) == 0 {
		return CollectorRelease{}, ErrIntegrationNotFound
	}
	return items[len(items)-1], nil
}

func (r *MemoryIntegrationRepository) SaveHealthSnapshot(_ context.Context, item HealthSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots[item.IntegrationID] = append(r.snapshots[item.IntegrationID], item)
	return nil
}

func (r *MemoryIntegrationRepository) GetLatestHealthSnapshot(_ context.Context, integrationID string) (HealthSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.snapshots[integrationID]
	if len(items) == 0 {
		return HealthSnapshot{}, ErrIntegrationNotFound
	}
	return items[len(items)-1], nil
}

func (r *MemoryIntegrationRepository) CreateIntegration(_ context.Context, item Integration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, current := range r.integrations {
		if current.EnvironmentID == item.EnvironmentID {
			return ErrIntegrationAlreadyExists
		}
	}
	r.integrations[item.ID] = item
	return nil
}

func (r *MemoryIntegrationRepository) UpdateIntegration(_ context.Context, item Integration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.integrations[item.ID]; !ok {
		return ErrIntegrationNotFound
	}
	r.integrations[item.ID] = item
	return nil
}

func (r *MemoryIntegrationRepository) DeleteIntegration(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.integrations[id]; !ok {
		return ErrIntegrationNotFound
	}
	delete(r.integrations, id)
	for sourceID, source := range r.sources {
		if source.IntegrationID == id {
			delete(r.sources, sourceID)
		}
	}
	return nil
}

func (r *MemoryIntegrationRepository) ListIntegrations(_ context.Context) ([]Integration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Integration, 0, len(r.integrations))
	for _, item := range r.integrations {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].EnvironmentID < items[j].EnvironmentID })
	return items, nil
}

func (r *MemoryIntegrationRepository) GetIntegration(_ context.Context, id string) (Integration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.integrations[id]
	if !ok {
		return Integration{}, ErrIntegrationNotFound
	}
	return item, nil
}

func (r *MemoryIntegrationRepository) FindIntegrationByEnvironment(_ context.Context, environmentID string) (Integration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, item := range r.integrations {
		if item.EnvironmentID == environmentID {
			return item, nil
		}
	}
	return Integration{}, ErrIntegrationNotFound
}

func (r *MemoryIntegrationRepository) CreateSourceAccess(_ context.Context, item SourceAccess) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, current := range r.sources {
		if current.IntegrationID == item.IntegrationID && current.ResourceBindingID == item.ResourceBindingID && current.SourceKind == item.SourceKind {
			return ErrIntegrationAlreadyExists
		}
	}
	r.sources[item.ID] = item
	return nil
}

func (r *MemoryIntegrationRepository) UpdateSourceAccess(_ context.Context, item SourceAccess) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sources[item.ID]; !ok {
		return ErrSourceAccessNotFound
	}
	r.sources[item.ID] = item
	return nil
}

func (r *MemoryIntegrationRepository) DeleteSourceAccess(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sources[id]; !ok {
		return ErrSourceAccessNotFound
	}
	delete(r.sources, id)
	return nil
}

func (r *MemoryIntegrationRepository) ListSourceAccesses(_ context.Context, integrationID string) ([]SourceAccess, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]SourceAccess, 0)
	for _, item := range r.sources {
		if item.IntegrationID == integrationID {
			items = append(items, item)
		}
	}
	sortSourceAccesses(items)
	return items, nil
}

func (r *MemoryIntegrationRepository) GetSourceAccess(_ context.Context, id string) (SourceAccess, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.sources[id]
	if !ok {
		return SourceAccess{}, ErrSourceAccessNotFound
	}
	return item, nil
}

func sortSourceAccesses(items []SourceAccess) {
	rank := func(kind string) int {
		if kind == SourceKindKubernetesInfra {
			return 0
		}
		if kind == SourceKindHostInfra {
			return 1
		}
		return 2
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := rank(items[i].SourceKind), rank(items[j].SourceKind)
		if left == right {
			return items[i].ID < items[j].ID
		}
		return left < right
	})
}
