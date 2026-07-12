package metrics

import (
	"context"
	"errors"

	"novaapm/internal/database"

	"go.mongodb.org/mongo-driver/mongo"
)

type StoreIntegrationRepository struct {
	integrations database.MetricsIntegrationStore
	sources      database.MetricsSourceAccessStore
	snapshots    database.MetricsHealthSnapshotStore
	releases     database.MetricsCollectorReleaseStore
}

func NewStoreIntegrationRepository(integrations database.MetricsIntegrationStore, sources database.MetricsSourceAccessStore, snapshots database.MetricsHealthSnapshotStore, releases ...database.MetricsCollectorReleaseStore) StoreIntegrationRepository {
	repository := StoreIntegrationRepository{integrations: integrations, sources: sources}
	repository.snapshots = snapshots
	if len(releases) > 0 {
		repository.releases = releases[0]
	}
	return repository
}

func (r StoreIntegrationRepository) SaveCollectorRelease(ctx context.Context, item CollectorRelease) error {
	if r.releases == nil {
		return ErrIntegrationNotFound
	}
	return r.releases.Insert(ctx, item)
}

func (r StoreIntegrationRepository) UpdateCollectorRelease(ctx context.Context, item CollectorRelease) error {
	if r.releases == nil {
		return ErrIntegrationNotFound
	}
	return r.releases.Update(ctx, item.ID, item)
}

func (r StoreIntegrationRepository) GetLatestCollectorRelease(ctx context.Context, sourceAccessID string) (CollectorRelease, error) {
	if r.releases == nil {
		return CollectorRelease{}, ErrIntegrationNotFound
	}
	var item CollectorRelease
	if err := r.releases.FindLatestBySourceAccess(ctx, sourceAccessID, &item); err != nil {
		return CollectorRelease{}, ErrIntegrationNotFound
	}
	return item, nil
}

func (r StoreIntegrationRepository) SaveHealthSnapshot(ctx context.Context, item HealthSnapshot) error {
	if r.snapshots == nil {
		return ErrIntegrationNotFound
	}
	return r.snapshots.Insert(ctx, item)
}

func (r StoreIntegrationRepository) GetLatestHealthSnapshot(ctx context.Context, integrationID string) (HealthSnapshot, error) {
	if r.snapshots == nil {
		return HealthSnapshot{}, ErrIntegrationNotFound
	}
	var item HealthSnapshot
	if err := r.snapshots.FindLatestByIntegration(ctx, integrationID, &item); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, database.ErrNotFound) {
			return HealthSnapshot{}, ErrIntegrationNotFound
		}
		return HealthSnapshot{}, err
	}
	return item, nil
}

func (r StoreIntegrationRepository) CreateIntegration(ctx context.Context, item Integration) error {
	err := r.integrations.Insert(ctx, item)
	if errors.Is(err, database.ErrConflict) {
		return ErrIntegrationAlreadyExists
	}
	return err
}
func (r StoreIntegrationRepository) UpdateIntegration(ctx context.Context, item Integration) error {
	err := r.integrations.Update(ctx, item.ID, item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrIntegrationNotFound
	}
	return err
}
func (r StoreIntegrationRepository) DeleteIntegration(ctx context.Context, id string) error {
	sources, err := r.ListSourceAccesses(ctx, id)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if err := r.sources.Delete(ctx, source.ID); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
	}
	err = r.integrations.Delete(ctx, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrIntegrationNotFound
	}
	return err
}
func (r StoreIntegrationRepository) ListIntegrations(ctx context.Context) ([]Integration, error) {
	var items []Integration
	if err := r.integrations.FindAll(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}
func (r StoreIntegrationRepository) GetIntegration(ctx context.Context, id string) (Integration, error) {
	var item Integration
	err := r.integrations.FindByID(ctx, id, &item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Integration{}, ErrIntegrationNotFound
	}
	return item, err
}
func (r StoreIntegrationRepository) FindIntegrationByEnvironment(ctx context.Context, environmentID string) (Integration, error) {
	var item Integration
	err := r.integrations.FindByEnvironment(ctx, environmentID, &item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Integration{}, ErrIntegrationNotFound
	}
	return item, err
}
func (r StoreIntegrationRepository) CreateSourceAccess(ctx context.Context, item SourceAccess) error {
	err := r.sources.Insert(ctx, item)
	if errors.Is(err, database.ErrConflict) {
		return ErrIntegrationAlreadyExists
	}
	return err
}
func (r StoreIntegrationRepository) UpdateSourceAccess(ctx context.Context, item SourceAccess) error {
	err := r.sources.Update(ctx, item.ID, item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrSourceAccessNotFound
	}
	return err
}
func (r StoreIntegrationRepository) DeleteSourceAccess(ctx context.Context, id string) error {
	err := r.sources.Delete(ctx, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrSourceAccessNotFound
	}
	return err
}
func (r StoreIntegrationRepository) ListSourceAccesses(ctx context.Context, integrationID string) ([]SourceAccess, error) {
	var items []SourceAccess
	if err := r.sources.FindByIntegration(ctx, integrationID, &items); err != nil {
		return nil, err
	}
	sortSourceAccesses(items)
	return items, nil
}
func (r StoreIntegrationRepository) GetSourceAccess(ctx context.Context, id string) (SourceAccess, error) {
	var item SourceAccess
	err := r.sources.FindByID(ctx, id, &item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return SourceAccess{}, ErrSourceAccessNotFound
	}
	return item, err
}
