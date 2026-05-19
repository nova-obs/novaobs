package secret

import "context"

type Repository interface {
	Save(ctx context.Context, item Secret) error
	Get(ctx context.Context, id string) (Secret, error)
}
