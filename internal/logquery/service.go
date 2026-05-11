package logquery

import "strings"

type Service struct {
	entries []LogEntry
}

func NewService() Service {
	return Service{entries: nil}
}

func (s Service) Search(query Query) Result {
	if s.entries == nil {
		return Result{Items: []LogEntry{}, Total: 0}
	}
	items := make([]LogEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		if query.Service != "" && entry.Service != query.Service {
			continue
		}
		if query.Environment != "" && entry.Environment != query.Environment {
			continue
		}
		if query.Level != "" && !strings.EqualFold(entry.Level, query.Level) {
			continue
		}
		if query.TraceID != "" && entry.TraceID != query.TraceID {
			continue
		}
		if query.RequestID != "" && entry.RequestID != query.RequestID {
			continue
		}
		if query.Keyword != "" && !strings.Contains(entry.Message, query.Keyword) {
			continue
		}
		if query.Start != nil && entry.Timestamp.Before(*query.Start) {
			continue
		}
		if query.End != nil && entry.Timestamp.After(*query.End) {
			continue
		}
		items = append(items, entry)
	}
	return Result{Items: items, Total: len(items)}
}
