package namespace

import (
	"context"
	"sort"
	"strings"
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Namespace, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return Service{repo: repo}
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Namespace, error) {
	return s.repo.List(ctx, filter)
}

type MemoryRepository struct {
	items []Namespace
}

func NewMemoryRepository(items []Namespace) MemoryRepository {
	copied := make([]Namespace, len(items))
	copy(copied, items)
	return MemoryRepository{items: copied}
}

func (r MemoryRepository) List(_ context.Context, filter ListFilter) ([]Namespace, error) {
	out := make([]Namespace, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), query) && !strings.Contains(strings.ToLower(item.Owner), query) {
			continue
		}
		out = append(out, item)
	}
	sortNamespaces(out, filter.Sort, filter.Order)
	return paginate(out, filter.Page, filter.PageSize), nil
}

func sortNamespaces(items []Namespace, field string, order string) {
	desc := strings.EqualFold(order, "desc")
	sort.SliceStable(items, func(left, right int) bool {
		var less bool
		switch field {
		case "status":
			less = items[left].Status < items[right].Status
		case "updated_at":
			less = items[left].UpdatedAt.Before(items[right].UpdatedAt)
		default:
			less = items[left].Name < items[right].Name
		}
		if desc {
			return !less
		}
		return less
	})
}

func paginate(items []Namespace, page int, pageSize int) []Namespace {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []Namespace{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
