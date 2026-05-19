package resource

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]ResourceSummary, error)
	GetDetail(ctx context.Context, query DetailQuery) (ResourceDetail, error)
	GetYAML(ctx context.Context, query DetailQuery) (ResourceYAML, error)
	GetPodLogs(ctx context.Context, query PodLogQuery) (PodLogResult, error)
}

type Service struct {
	reader Reader
}

func NewService(reader Reader) Service {
	return Service{reader: reader}
}

func (s Service) List(ctx context.Context, filter ListFilter) ([]ResourceSummary, error) {
	return s.reader.List(ctx, filter)
}

func (s Service) GetDetail(ctx context.Context, query DetailQuery) (ResourceDetail, error) {
	return s.reader.GetDetail(ctx, query)
}

func (s Service) GetYAML(ctx context.Context, query DetailQuery) (ResourceYAML, error) {
	return s.reader.GetYAML(ctx, query)
}

func (s Service) GetPodLogs(ctx context.Context, query PodLogQuery) (PodLogResult, error) {
	return s.reader.GetPodLogs(ctx, query)
}

type MemoryReader struct {
	items []ResourceSummary
}

func NewMemoryReader(items []ResourceSummary) MemoryReader {
	copied := make([]ResourceSummary, len(items))
	copy(copied, items)
	return MemoryReader{items: copied}
}

func (r MemoryReader) List(_ context.Context, filter ListFilter) ([]ResourceSummary, error) {
	out := make([]ResourceSummary, 0, len(r.items))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.items {
		if !identityMatches(item.Identity, filter) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Identity.Name), query) && !strings.Contains(strings.ToLower(item.Status), query) {
			continue
		}
		out = append(out, item)
	}
	sortResources(out, filter.Sort, filter.Order)
	return paginate(out, filter.Page, filter.PageSize), nil
}

func (r MemoryReader) GetDetail(_ context.Context, query DetailQuery) (ResourceDetail, error) {
	item, ok := r.find(query.Identity)
	if !ok {
		return ResourceDetail{}, errors.New("resource not found")
	}
	return ResourceDetail{Identity: item.Identity, Status: item.Status, Labels: item.Labels, Spec: map[string]any{"managed_by": "novaobs"}, UpdatedAt: item.UpdatedAt}, nil
}

func (r MemoryReader) GetYAML(_ context.Context, query DetailQuery) (ResourceYAML, error) {
	item, ok := r.find(query.Identity)
	if !ok {
		return ResourceYAML{}, errors.New("resource not found")
	}
	yaml := fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: %s\n  namespace: %s\n  uid: %s\n", item.Identity.APIVersion, item.Identity.Kind, item.Identity.Name, item.Identity.Namespace, item.Identity.UID)
	return ResourceYAML{Identity: item.Identity, YAML: yaml}, nil
}

func (r MemoryReader) GetPodLogs(_ context.Context, query PodLogQuery) (PodLogResult, error) {
	identity := Identity{ClusterID: query.ClusterID, Namespace: query.Namespace, APIVersion: "v1", Kind: "Pod", Name: query.Pod}
	return PodLogResult{Identity: identity, Container: query.Container, Lines: []string{}}, nil
}

func (r MemoryReader) find(identity Identity) (ResourceSummary, bool) {
	for _, item := range r.items {
		if item.Identity.ClusterID == identity.ClusterID &&
			item.Identity.Namespace == identity.Namespace &&
			item.Identity.APIVersion == identity.APIVersion &&
			item.Identity.Kind == identity.Kind &&
			item.Identity.Name == identity.Name &&
			(identity.UID == "" || item.Identity.UID == identity.UID) {
			return item, true
		}
	}
	return ResourceSummary{}, false
}

func identityMatches(identity Identity, filter ListFilter) bool {
	if filter.ClusterID != "" && identity.ClusterID != filter.ClusterID {
		return false
	}
	if filter.Namespace != "" && identity.Namespace != filter.Namespace {
		return false
	}
	if filter.APIVersion != "" && identity.APIVersion != filter.APIVersion {
		return false
	}
	if filter.Kind != "" && identity.Kind != filter.Kind {
		return false
	}
	return true
}

func sortResources(items []ResourceSummary, field string, order string) {
	desc := strings.EqualFold(order, "desc")
	sort.SliceStable(items, func(left, right int) bool {
		var less bool
		switch field {
		case "kind":
			less = items[left].Identity.Kind < items[right].Identity.Kind
		case "updated_at":
			less = items[left].UpdatedAt.Before(items[right].UpdatedAt)
		default:
			less = items[left].Identity.Name < items[right].Identity.Name
		}
		if desc {
			return !less
		}
		return less
	})
}

func paginate(items []ResourceSummary, page int, pageSize int) []ResourceSummary {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []ResourceSummary{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
