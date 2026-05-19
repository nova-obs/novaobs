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
	event.RequestSummary = sanitize(event.RequestSummary)
	if err := s.store.Insert(ctx, event); err != nil {
		return Event{}, err
	}
	return event, nil
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
		out[key] = value
	}
	return out
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
