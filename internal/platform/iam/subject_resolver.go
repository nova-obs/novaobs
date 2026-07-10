package iam

import (
	"context"
	"strings"

	platformrbac "novaapm/internal/platform/rbac"
)

type SubjectResolver struct {
	repo Repository
}

func NewSubjectResolver(repo Repository) SubjectResolver {
	return SubjectResolver{repo: repo}
}

func (r SubjectResolver) ResolveSubjects(subject platformrbac.Subject) ([]platformrbac.Subject, error) {
	if r.repo == nil || strings.TrimSpace(subject.ID) == "" || strings.TrimSpace(subject.Type) == "" {
		return nil, nil
	}
	memberships, err := r.repo.ListMembershipsBySubject(context.Background(), subject.ID, subject.Type)
	if err != nil {
		return nil, err
	}
	out := make([]platformrbac.Subject, 0, len(memberships))
	for _, membership := range memberships {
		if strings.TrimSpace(membership.GroupID) == "" {
			continue
		}
		out = append(out, platformrbac.Subject{
			ID:   strings.TrimSpace(membership.GroupID),
			Type: SubjectTypeGroup,
		})
	}
	return out, nil
}
