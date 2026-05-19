package cluster

import (
	"context"
	"strings"
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Cluster, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return Service{repo: repo}
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Cluster, error) {
	return s.repo.List(ctx, filter)
}

type MemoryRepository struct {
	items []Cluster
}

func NewMemoryRepository(items []Cluster) MemoryRepository {
	copied := make([]Cluster, len(items))
	copy(copied, items)
	return MemoryRepository{items: copied}
}

func (r MemoryRepository) List(_ context.Context, filter ListFilter) ([]Cluster, error) {
	out := make([]Cluster, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if query == "" || strings.Contains(strings.ToLower(item.Name), query) || strings.Contains(strings.ToLower(item.ID), query) {
			out = append(out, item)
		}
	}
	return out, nil
}
