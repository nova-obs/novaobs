package certificate

import (
	"context"
	"sort"
	"strings"
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Certificate, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return Service{repo: repo}
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Certificate, error) {
	return s.repo.List(ctx, filter)
}

type MemoryRepository struct {
	items []Certificate
}

func NewMemoryRepository(items []Certificate) MemoryRepository {
	copied := make([]Certificate, len(items))
	copy(copied, items)
	return MemoryRepository{items: copied}
}

func (r MemoryRepository) List(_ context.Context, filter ListFilter) ([]Certificate, error) {
	out := make([]Certificate, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), query) && !strings.Contains(strings.ToLower(item.CommonName), query) {
			continue
		}
		item.PrivateKey = ""
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		less := out[left].NotAfter.Before(out[right].NotAfter)
		if strings.EqualFold(filter.Order, "desc") {
			return !less
		}
		return less
	})
	return paginate(out, filter.Page, filter.PageSize), nil
}

func paginate(items []Certificate, page int, pageSize int) []Certificate {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []Certificate{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
