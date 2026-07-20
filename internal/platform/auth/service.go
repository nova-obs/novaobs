package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"novaapm/internal/platform/iam"
	platformrbac "novaapm/internal/platform/rbac"
)

var (
	ErrInvalidCredentials = errors.New("invalid_credentials")
	ErrInvalidSession     = errors.New("invalid_session")
)

type UserRepository interface {
	GetUser(ctx context.Context, id string) (iam.User, error)
}

type Service struct {
	users                  UserRepository
	sessionSecret          []byte
	allowPasswordlessLocal bool
	now                    func() time.Time
	sessionTTL             time.Duration
}

type Option func(*Service)

func WithPasswordlessLocalUsers(allowed bool) Option {
	return func(s *Service) {
		s.allowPasswordlessLocal = allowed
	}
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func NewService(users UserRepository, sessionSecret []byte, options ...Option) *Service {
	service := &Service{
		users:         users,
		sessionSecret: append([]byte(nil), sessionSecret...),
		now:           func() time.Time { return time.Now().UTC() },
		sessionTTL:    12 * time.Hour,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Session struct {
	Subject   platformrbac.Subject `json:"subject"`
	ExpiresAt time.Time            `json:"expires_at"`
}

type tokenPayload struct {
	SubjectID   string `json:"sub"`
	SubjectType string `json:"typ"`
	DisplayName string `json:"name"`
	ExpiresAt   int64  `json:"exp"`
}

func (s *Service) Login(ctx context.Context, req LoginRequest) (Session, string, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return Session{}, "", ErrInvalidCredentials
	}
	user, err := s.users.GetUser(ctx, username)
	if err != nil || user.Status != "active" {
		return Session{}, "", ErrInvalidCredentials
	}
	if user.PasswordHash == "" {
		if !s.allowPasswordlessLocal || strings.TrimSpace(req.Password) != "" {
			return Session{}, "", ErrInvalidCredentials
		}
	} else if !iam.VerifyPassword(user.PasswordHash, req.Password) {
		return Session{}, "", ErrInvalidCredentials
	}
	session := Session{
		Subject: platformrbac.Subject{
			ID:          user.ID,
			Type:        iam.SubjectTypeUser,
			DisplayName: firstNonEmpty(user.DisplayName, user.Username, user.ID),
		},
		ExpiresAt: s.now().Add(s.sessionTTL),
	}
	token, err := s.Sign(session)
	return session, token, err
}

func (s *Service) Sign(session Session) (string, error) {
	if len(s.sessionSecret) == 0 || session.Subject.ID == "" || session.Subject.Type == "" {
		return "", ErrInvalidSession
	}
	payload := tokenPayload{
		SubjectID:   session.Subject.ID,
		SubjectType: session.Subject.Type,
		DisplayName: session.Subject.DisplayName,
		ExpiresAt:   session.ExpiresAt.Unix(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signature := s.sign(encodedPayload)
	return encodedPayload + "." + signature, nil
}

func (s *Service) Parse(token string) (Session, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Session{}, ErrInvalidSession
	}
	if !hmac.Equal([]byte(parts[1]), []byte(s.sign(parts[0]))) {
		return Session{}, ErrInvalidSession
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	var payload tokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return Session{}, ErrInvalidSession
	}
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	if !expiresAt.After(s.now()) || payload.SubjectID == "" || payload.SubjectType == "" {
		return Session{}, ErrInvalidSession
	}
	return Session{
		Subject: platformrbac.Subject{
			ID:          payload.SubjectID,
			Type:        payload.SubjectType,
			DisplayName: payload.DisplayName,
		},
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) sign(payload string) string {
	mac := hmac.New(sha256.New, s.sessionSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
