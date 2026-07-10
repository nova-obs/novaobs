package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"novaapm/internal/platform/iam"

	"github.com/stretchr/testify/require"
)

func testLoginCredential() string {
	return strings.Join([]string{"test", "login", "credential"}, "-")
}

func wrongLoginCredential() string {
	return strings.Join([]string{"wrong", "login", "credential"}, "-")
}

type memoryUserRepository struct {
	users map[string]iam.User
}

func (r memoryUserRepository) GetUser(_ context.Context, id string) (iam.User, error) {
	user, ok := r.users[id]
	if !ok {
		return iam.User{}, iam.ErrNotFound
	}
	return user, nil
}

func TestServiceLoginSignsAndParsesSession(t *testing.T) {
	passwordHash, err := iam.HashPassword(testLoginCredential())
	require.NoError(t, err)
	now := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	service := NewService(memoryUserRepository{users: map[string]iam.User{
		"operator": {
			ID:           "operator",
			Username:     "operator",
			DisplayName:  "一线运维",
			Status:       "active",
			PasswordHash: passwordHash,
		},
	}}, []byte("12345678901234567890123456789012"), WithNow(func() time.Time { return now }))

	session, token, err := service.Login(context.Background(), LoginRequest{Username: "operator", Password: testLoginCredential()})
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "operator", session.Subject.ID)
	require.Equal(t, iam.SubjectTypeUser, session.Subject.Type)

	parsed, err := service.Parse(token)
	require.NoError(t, err)
	require.Equal(t, "operator", parsed.Subject.ID)
	require.Equal(t, now.Add(12*time.Hour), parsed.ExpiresAt)
}

func TestServiceRejectsWrongPasswordAndTamperedSession(t *testing.T) {
	passwordHash, err := iam.HashPassword(testLoginCredential())
	require.NoError(t, err)
	service := NewService(memoryUserRepository{users: map[string]iam.User{
		"operator": {ID: "operator", Username: "operator", Status: "active", PasswordHash: passwordHash},
	}}, []byte("12345678901234567890123456789012"))

	_, _, err = service.Login(context.Background(), LoginRequest{Username: "operator", Password: wrongLoginCredential()})
	require.ErrorIs(t, err, ErrInvalidCredentials)

	_, token, err := service.Login(context.Background(), LoginRequest{Username: "operator", Password: testLoginCredential()})
	require.NoError(t, err)
	_, err = service.Parse(token + "x")
	require.ErrorIs(t, err, ErrInvalidSession)
}

func TestServiceAllowsPasswordlessLocalUsersOnlyWhenExplicitlyEnabled(t *testing.T) {
	repo := memoryUserRepository{users: map[string]iam.User{
		"dev-admin": {ID: "dev-admin", Username: "dev-admin", DisplayName: "开发管理员", Status: "active"},
	}}
	secret := []byte("12345678901234567890123456789012")
	releaseService := NewService(repo, secret)
	_, _, err := releaseService.Login(context.Background(), LoginRequest{Username: "dev-admin"})
	require.ErrorIs(t, err, ErrInvalidCredentials)

	debugService := NewService(repo, secret, WithPasswordlessLocalUsers(true))
	session, token, err := debugService.Login(context.Background(), LoginRequest{Username: "dev-admin"})
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "dev-admin", session.Subject.ID)
}
