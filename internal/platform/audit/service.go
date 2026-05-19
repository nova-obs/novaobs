package audit

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Store interface {
	Insert(ctx context.Context, event Event) error
	List(ctx context.Context) ([]Event, error)
}

type Service struct {
	store Store
}

func NewService(store Store) Service {
	return Service{store: store}
}

func (s Service) Record(ctx context.Context, event Event) (Event, error) {
	if event.ID == "" {
		event.ID = primitive.NewObjectID().Hex()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event = normalizeEvent(event)
	event.RequestSummary = sanitize(event.RequestSummary)
	if err := s.store.Insert(ctx, event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func normalizeEvent(event Event) Event {
	if event.Actor.ID == "" {
		event.Actor.ID = event.ActorID
	}
	if event.Actor.Name == "" {
		event.Actor.Name = event.ActorName
	}
	if event.ActorID == "" {
		event.ActorID = event.Actor.ID
	}
	if event.ActorName == "" {
		event.ActorName = event.Actor.Name
	}
	if event.Resource.Type == "" {
		event.Resource.Type = event.ResourceType
	}
	if event.Resource.Name == "" {
		event.Resource.Name = event.ResourceName
	}
	if event.ResourceType == "" {
		event.ResourceType = event.Resource.Type
	}
	if event.ResourceName == "" {
		event.ResourceName = event.Resource.Name
	}
	if event.Error == "" {
		event.Error = event.ErrorMessage
	}
	if event.ErrorMessage == "" {
		event.ErrorMessage = event.Error
	}
	if event.Trace == "" {
		event.Trace = event.TraceID
	}
	if event.TraceID == "" {
		event.TraceID = event.Trace
	}
	return event
}

func sanitize(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") || strings.Contains(lower, "kubeconfig") ||
			strings.Contains(lower, "private_key") {
			out[key] = "[redacted]"
			continue
		}
		out[key] = sanitizeValue(value)
	}
	return out
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitize(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeValue(item)
		}
		return out
	default:
		return value
	}
}

type MemoryStore struct {
	mu     sync.Mutex
	events []Event
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) Insert(ctx context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *MemoryStore) List(ctx context.Context) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out, nil
}
