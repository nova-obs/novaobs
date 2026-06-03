package authctx

import (
	"context"

	"novaobs/internal/platform/rbac"
)

type subjectKey struct{}

var anonymousSubject = rbac.Subject{ID: "anonymous", Type: "anonymous", DisplayName: "anonymous"}

func WithSubject(ctx context.Context, subject rbac.Subject) context.Context {
	if subject.ID == "" || subject.Type == "" {
		return context.WithValue(ctx, subjectKey{}, anonymousSubject)
	}
	if subject.DisplayName == "" {
		subject.DisplayName = subject.ID
	}
	return context.WithValue(ctx, subjectKey{}, subject)
}

func SubjectFrom(ctx context.Context) (rbac.Subject, bool) {
	subject, ok := ctx.Value(subjectKey{}).(rbac.Subject)
	if !ok || subject.ID == "" || subject.Type == "" {
		return anonymousSubject, false
	}
	return subject, true
}
