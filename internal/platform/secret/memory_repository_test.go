package secret

import (
	"context"
	"errors"
	"sync"
)

type MemoryRepository struct {
	mu    sync.Mutex
	items map[string]Secret
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{items: map[string]Secret{}}
}

func (r *MemoryRepository) Save(ctx context.Context, item Secret) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[item.ID] = item
	return nil
}

func (r *MemoryRepository) Get(ctx context.Context, id string) (Secret, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return Secret{}, errors.New("secret not found")
	}
	return item, nil
}
