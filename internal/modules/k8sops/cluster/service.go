package cluster

import (
	"context"
	"sort"
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
	sortClusters(out, filter.Sort, filter.Order)
	return paginate(out, filter.Page, filter.PageSize), nil
}

func sortClusters(items []Cluster, field string, order string) {
	desc := strings.EqualFold(order, "desc")
	sort.SliceStable(items, func(left, right int) bool {
		var less bool
		switch field {
		case "region":
			less = items[left].Region < items[right].Region
		case "status":
			less = items[left].Status < items[right].Status
		default:
			less = items[left].Name < items[right].Name
		}
		if desc {
			return !less
		}
		return less
	})
}

func paginate(items []Cluster, page int, pageSize int) []Cluster {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []Cluster{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
