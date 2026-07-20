package environment

import (
	"context"
	"sort"
	"sync"
)

type Repository interface {
	CreateEnvironment(ctx context.Context, item Environment) error
	UpdateEnvironment(ctx context.Context, item Environment) error
	ListEnvironments(ctx context.Context) ([]Environment, error)
	GetEnvironment(ctx context.Context, id string) (Environment, error)
	CreateResourceBinding(ctx context.Context, item ResourceBinding) error
	ListResourceBindings(ctx context.Context, environmentID string) ([]ResourceBinding, error)
	FindResourceBinding(ctx context.Context, resourceKind string, resourceRef string) (ResourceBinding, error)
	DeleteResourceBinding(ctx context.Context, id string) error
}

type MemoryRepository struct {
	mu           sync.RWMutex
	environments map[string]Environment
	bindings     map[string]ResourceBinding
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		environments: map[string]Environment{},
		bindings:     map[string]ResourceBinding{},
	}
}

func (r *MemoryRepository) CreateEnvironment(_ context.Context, item Environment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.environments[item.ID] = item
	return nil
}

func (r *MemoryRepository) UpdateEnvironment(_ context.Context, item Environment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.environments[item.ID]; !ok {
		return ErrEnvironmentNotFound
	}
	r.environments[item.ID] = item
	return nil
}

func (r *MemoryRepository) ListEnvironments(_ context.Context) ([]Environment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Environment, 0, len(r.environments))
	for _, item := range r.environments {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (r *MemoryRepository) GetEnvironment(_ context.Context, id string) (Environment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.environments[id]
	if !ok {
		return Environment{}, ErrEnvironmentNotFound
	}
	return item, nil
}

func (r *MemoryRepository) CreateResourceBinding(_ context.Context, item ResourceBinding) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, current := range r.bindings {
		if current.ResourceKind == item.ResourceKind && current.ResourceRef == item.ResourceRef {
			return ErrResourceAlreadyBound
		}
	}
	r.bindings[item.ID] = item
	return nil
}

func (r *MemoryRepository) ListResourceBindings(_ context.Context, environmentID string) ([]ResourceBinding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]ResourceBinding, 0)
	for _, item := range r.bindings {
		if item.EnvironmentID == environmentID {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (r *MemoryRepository) FindResourceBinding(_ context.Context, resourceKind string, resourceRef string) (ResourceBinding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, item := range r.bindings {
		if item.ResourceKind == resourceKind && item.ResourceRef == resourceRef {
			return item, nil
		}
	}
	return ResourceBinding{}, ErrBindingNotFound
}

func (r *MemoryRepository) DeleteResourceBinding(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bindings[id]; !ok {
		return ErrBindingNotFound
	}
	delete(r.bindings, id)
	return nil
}
