package audit

import (
	"context"
	"testing"

	"novaapm/internal/database/memstore"

	"github.com/stretchr/testify/require"
)

func TestServiceRecordsSanitizedEvent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)

	event, err := svc.Record(context.Background(), Event{
		ActorID:      "user-1",
		ActorName:    "alice",
		ResourceType: "k8s.kubeconfig",
		ResourceName: "orders-admin",
		Action:       "export",
		Scope:        "cluster=prod namespace=orders",
		RequestSummary: map[string]any{
			"token":       "secret-token",
			"kubeconfig":  "secret-config",
			"description": "generate readonly kubeconfig",
		},
		Result: "success",
	})

	require.NoError(t, err)
	require.NotEmpty(t, event.ID)
	require.Equal(t, Actor{ID: "user-1", Name: "alice"}, event.Actor)
	require.Equal(t, Resource{Type: "k8s.kubeconfig", Name: "orders-admin"}, event.Resource)
	require.Equal(t, "[redacted]", event.RequestSummary["token"])
	require.Equal(t, "[redacted]", event.RequestSummary["kubeconfig"])
	require.Equal(t, "generate readonly kubeconfig", event.RequestSummary["description"])
}

func TestServiceSanitizesNestedSensitiveFields(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)

	event, err := svc.Record(context.Background(), Event{
		Actor:    Actor{ID: "user-1", Name: "alice"},
		Resource: Resource{Type: "k8s.deployment", Name: "orders-api"},
		Action:   "deploy",
		Scope:    "cluster=prod namespace=orders",
		Error:    "token leaked before sanitizer",
		Trace:    "trace-1",
		RequestSummary: map[string]any{
			"metadata": map[string]any{
				"token": "nested-token",
				"safe":  "value",
			},
			"containers": []any{
				map[string]any{"private_key": "nested-key"},
			},
		},
		Result: "failed",
	})

	require.NoError(t, err)
	require.Equal(t, "token leaked before sanitizer", event.Error)
	require.Equal(t, "trace-1", event.Trace)
	metadata := event.RequestSummary["metadata"].(map[string]any)
	require.Equal(t, "[redacted]", metadata["token"])
	require.Equal(t, "value", metadata["safe"])
	containers := event.RequestSummary["containers"].([]any)
	container := containers[0].(map[string]any)
	require.Equal(t, "[redacted]", container["private_key"])
}

func TestStoreRepositoryPersistsRecordedEvents(t *testing.T) {
	store := memstore.NewStore()
	svc := NewService(NewStoreRepository(store.AuditEvents()))

	recorded, err := svc.Record(context.Background(), Event{
		Actor:    Actor{ID: "user-1", Name: "alice"},
		Resource: Resource{Type: "k8s.terminal", Name: "orders"},
		Action:   "exec",
		Scope:    "cluster=prod namespace=orders",
		RequestSummary: map[string]any{
			"cluster_id": "prod",
			"namespace":  "orders",
			"command":    "get pods",
		},
		Result: "success",
	})
	require.NoError(t, err)

	events, err := svc.List(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, recorded.ID, events[0].ID)
	require.Equal(t, "k8s.terminal", events[0].ResourceType)
	require.Equal(t, "orders", events[0].RequestSummary["namespace"])
}
