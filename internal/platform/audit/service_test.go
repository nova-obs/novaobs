package audit

import (
	"context"
	"testing"

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
