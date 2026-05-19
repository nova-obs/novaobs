package deployment

import (
	"context"
	"sort"
	"strings"
)

type Reader interface {
	ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error)
	ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error)
}

type Service struct {
	reader Reader
}

func NewService(reader Reader) Service {
	return Service{reader: reader}
}

func (s Service) ListHistory(ctx context.Context, filter ListFilter) ([]HistoryRecord, error) {
	return s.reader.ListHistory(ctx, filter)
}

func (s Service) ListAuditEvents(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	return s.reader.ListAuditEvents(ctx, filter)
}

type MemoryReader struct {
	history []HistoryRecord
	audits  []AuditEvent
}

func NewMemoryReader(history []HistoryRecord, audits ...[]AuditEvent) MemoryReader {
	copiedHistory := make([]HistoryRecord, len(history))
	copy(copiedHistory, history)
	copiedAudits := []AuditEvent{}
	if len(audits) > 0 {
		copiedAudits = make([]AuditEvent, len(audits[0]))
		copy(copiedAudits, audits[0])
	}
	return MemoryReader{history: copiedHistory, audits: copiedAudits}
}

func (r MemoryReader) ListHistory(_ context.Context, filter ListFilter) ([]HistoryRecord, error) {
	out := make([]HistoryRecord, 0, len(r.history))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.history {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Workload), query) && !strings.Contains(strings.ToLower(item.Action), query) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		less := out[left].StartedAt.Before(out[right].StartedAt)
		if strings.EqualFold(filter.Order, "asc") {
			return less
		}
		return !less
	})
	return paginateHistory(out, filter.Page, filter.PageSize), nil
}

func (r MemoryReader) ListAuditEvents(_ context.Context, filter ListFilter) ([]AuditEvent, error) {
	out := make([]AuditEvent, 0, len(r.audits))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, item := range r.audits {
		if filter.ClusterID != "" && item.ClusterID != filter.ClusterID {
			continue
		}
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.ResourceName), query) && !strings.Contains(strings.ToLower(item.Action), query) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(left, right int) bool {
		less := out[left].CreatedAt.Before(out[right].CreatedAt)
		if strings.EqualFold(filter.Order, "asc") {
			return less
		}
		return !less
	})
	return paginateAudit(out, filter.Page, filter.PageSize), nil
}

func paginateHistory(items []HistoryRecord, page int, pageSize int) []HistoryRecord {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []HistoryRecord{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func paginateAudit(items []AuditEvent, page int, pageSize int) []AuditEvent {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		return items
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []AuditEvent{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
