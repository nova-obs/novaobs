package environment

import (
	"context"
	"errors"

	"novaapm/internal/database"

	"go.mongodb.org/mongo-driver/mongo"
)

type StoreRepository struct {
	environments database.EnvironmentStore
	bindings     database.EnvironmentResourceBindingStore
}

func NewStoreRepository(environments database.EnvironmentStore, bindings database.EnvironmentResourceBindingStore) StoreRepository {
	return StoreRepository{environments: environments, bindings: bindings}
}

func (r StoreRepository) CreateEnvironment(ctx context.Context, item Environment) error {
	return r.environments.Insert(ctx, item)
}

func (r StoreRepository) UpdateEnvironment(ctx context.Context, item Environment) error {
	err := r.environments.Update(ctx, item.ID, item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrEnvironmentNotFound
	}
	return err
}

func (r StoreRepository) ListEnvironments(ctx context.Context) ([]Environment, error) {
	var items []Environment
	if err := r.environments.FindAll(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r StoreRepository) GetEnvironment(ctx context.Context, id string) (Environment, error) {
	var item Environment
	err := r.environments.FindByID(ctx, id, &item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Environment{}, ErrEnvironmentNotFound
	}
	return item, err
}

func (r StoreRepository) CreateResourceBinding(ctx context.Context, item ResourceBinding) error {
	err := r.bindings.Insert(ctx, item)
	if errors.Is(err, database.ErrConflict) || mongo.IsDuplicateKeyError(err) {
		return ErrResourceAlreadyBound
	}
	return err
}

func (r StoreRepository) ListResourceBindings(ctx context.Context, environmentID string) ([]ResourceBinding, error) {
	var items []ResourceBinding
	if err := r.bindings.FindByEnvironment(ctx, environmentID, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r StoreRepository) FindResourceBinding(ctx context.Context, resourceKind string, resourceRef string) (ResourceBinding, error) {
	var item ResourceBinding
	err := r.bindings.FindByResource(ctx, resourceKind, resourceRef, &item)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ResourceBinding{}, ErrBindingNotFound
	}
	return item, err
}

func (r StoreRepository) DeleteResourceBinding(ctx context.Context, id string) error {
	err := r.bindings.Delete(ctx, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrBindingNotFound
	}
	return err
}
