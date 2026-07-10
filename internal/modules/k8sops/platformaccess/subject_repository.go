package platformaccess

import (
	"context"

	"novaapm/internal/database"
	"novaapm/internal/platform/iam"
)

type MemorySubjectRepository struct {
	items map[string]SubjectRecord
}

func NewMemorySubjectRepository() *MemorySubjectRepository {
	return &MemorySubjectRepository{items: map[string]SubjectRecord{}}
}

func (r *MemorySubjectRepository) SaveSubject(_ context.Context, subject SubjectRecord) error {
	r.items[subject.ID] = subject
	return nil
}

func (r *MemorySubjectRepository) ListSubjects(_ context.Context) ([]SubjectRecord, error) {
	out := make([]SubjectRecord, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, item)
	}
	return out, nil
}

func (r *MemorySubjectRepository) DeleteSubject(_ context.Context, id string) error {
	delete(r.items, id)
	return nil
}

type StoreSubjectRepository struct {
	store database.PlatformSubjectStore
}

func NewStoreSubjectRepository(store database.PlatformSubjectStore) StoreSubjectRepository {
	return StoreSubjectRepository{store: store}
}

func (r StoreSubjectRepository) SaveSubject(ctx context.Context, subject SubjectRecord) error {
	return r.store.Upsert(ctx, subject.ID, subject)
}

func (r StoreSubjectRepository) ListSubjects(ctx context.Context) ([]SubjectRecord, error) {
	var subjects []SubjectRecord
	err := r.store.FindAll(ctx, &subjects)
	return subjects, err
}

func (r StoreSubjectRepository) DeleteSubject(ctx context.Context, id string) error {
	return r.store.Delete(ctx, id)
}

type IAMSubjectRepository struct {
	service iam.Service
}

func NewIAMSubjectRepository(service iam.Service) IAMSubjectRepository {
	return IAMSubjectRepository{service: service}
}

func (r IAMSubjectRepository) ListSubjects(ctx context.Context) ([]SubjectRecord, error) {
	subjects, err := r.service.SubjectDirectory(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SubjectRecord, 0, len(subjects))
	for _, subject := range subjects {
		out = append(out, SubjectRecord{
			ID:          subject.ID,
			SubjectID:   subject.SubjectID,
			SubjectType: subject.SubjectType,
			DisplayName: subject.DisplayName,
			Email:       subject.Email,
			Source:      "iam",
			BindingRefs: subject.BindingRefs,
			CreatedAt:   subject.CreatedAt,
			UpdatedAt:   subject.UpdatedAt,
		})
	}
	return out, nil
}
