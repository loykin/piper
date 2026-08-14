package project

import "context"

type Repository interface {
	Create(ctx context.Context, project *Project) error
	Get(ctx context.Context, id string) (*Project, error)
	List(ctx context.Context) ([]*Project, error)
	SetOwner(ctx context.Context, id, memberID string) error
	Delete(ctx context.Context, id string) error
}
