package cluster

import (
	"context"
	"errors"
	"sort"
	"strings"

	"novaobs/internal/modules/k8sops/kubeclient"
)

var (
	ErrInvalidClusterRequest        = errors.New("invalid_cluster_request")
	ErrClusterRepositoryWrite       = errors.New("cluster_repository_write_unavailable")
	ErrClusterCapabilityUnavailable = errors.New("cluster_capability_unavailable")
)

type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Cluster, error)
}

type UpsertRepository interface {
	Upsert(ctx context.Context, item Cluster) (Cluster, error)
}

type DeleteRepository interface {
	Delete(ctx context.Context, id string) error
}

type Service struct {
	repo Repository
}

type CapabilityProvider interface {
	Capabilities(ctx context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error)
}

type CapabilityService struct {
	provider CapabilityProvider
}

func NewService(repo Repository) Service {
	return Service{repo: repo}
}

func NewCapabilityService(provider CapabilityProvider) CapabilityService {
	return CapabilityService{provider: provider}
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]Cluster, error) {
	return s.repo.List(ctx, filter)
}

func (s CapabilityService) Get(ctx context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return kubeclient.CapabilitySnapshot{}, ErrInvalidClusterRequest
	}
	if s.provider == nil {
		return kubeclient.CapabilitySnapshot{}, ErrClusterCapabilityUnavailable
	}
	return s.provider.Capabilities(ctx, clusterID)
}

func (s Service) Create(ctx context.Context, req UpsertRequest) (Cluster, error) {
	item := Cluster{
		ID:          strings.TrimSpace(req.ID),
		Name:        strings.TrimSpace(req.Name),
		Version:     strings.TrimSpace(req.Version),
		Region:      strings.TrimSpace(req.Region),
		Description: strings.TrimSpace(req.Description),
		Status:      strings.TrimSpace(req.Status),
	}
	if item.ID == "" || item.Name == "" {
		return Cluster{}, ErrInvalidClusterRequest
	}
	if item.Status == "" {
		item.Status = "active"
	}
	repo, ok := s.repo.(UpsertRepository)
	if !ok {
		return Cluster{}, ErrClusterRepositoryWrite
	}
	return repo.Upsert(ctx, item)
}

func (s Service) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidClusterRequest
	}
	repo, ok := s.repo.(DeleteRepository)
	if !ok {
		return ErrClusterRepositoryWrite
	}
	return repo.Delete(ctx, id)
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

func (r *MemoryRepository) Upsert(_ context.Context, item Cluster) (Cluster, error) {
	if item.Status == "" {
		item.Status = "active"
	}
	for index, current := range r.items {
		if current.ID == item.ID {
			r.items[index] = item
			return item, nil
		}
	}
	r.items = append(r.items, item)
	return item, nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	id = strings.TrimSpace(id)
	next := make([]Cluster, 0, len(r.items))
	for _, item := range r.items {
		if item.ID != id {
			next = append(next, item)
		}
	}
	r.items = next
	return nil
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
