package secret

import (
	"context"
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
		return Secret{}, ErrNotFound
	}
	return item, nil
}

func (r *MemoryRepository) FindByTypeAndScope(ctx context.Context, typ string, scope Scope) (Secret, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.items {
		if item.Type == typ && scopeMatches(item.Scope, scope) {
			return item, nil
		}
	}
	return Secret{}, ErrNotFound
}

func (r *MemoryRepository) ListByType(ctx context.Context, typ string) ([]Secret, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Secret, 0, len(r.items))
	for _, item := range r.items {
		if item.Type == typ {
			out = append(out, item)
		}
	}
	return out, nil
}

func scopeMatches(left Scope, right Scope) bool {
	return left.ClusterID == right.ClusterID && left.Namespace == right.Namespace && left.ServiceID == right.ServiceID
}
