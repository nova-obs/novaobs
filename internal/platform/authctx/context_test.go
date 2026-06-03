package authctx

import (
	"context"
	"testing"

	"novaobs/internal/platform/rbac"

	"github.com/stretchr/testify/require"
)

func TestSubjectRoundTrip(t *testing.T) {
	subject := rbac.Subject{ID: "user-1", Type: "user", DisplayName: "alice"}

	got, ok := SubjectFrom(WithSubject(context.Background(), subject))

	require.True(t, ok)
	require.Equal(t, subject, got)
}

func TestSubjectFromEmptyContextReturnsAnonymous(t *testing.T) {
	got, ok := SubjectFrom(context.Background())

	require.False(t, ok)
	require.Equal(t, rbac.Subject{ID: "anonymous", Type: "anonymous", DisplayName: "anonymous"}, got)
}
