package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/project"
)

type projectRepo struct{ dbstore.BaseRepo }

func NewProjectRepo(exec *dbstore.Executor, source string) project.Repository {
	return &projectRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *projectRepo) Create(ctx context.Context, p *project.Project) error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.NamedExecContext(ctx,
			`INSERT INTO projects (id, name, description, created_at, updated_at)
			 VALUES (:id, :name, :description, :created_at, :updated_at)`,
			p)
		return err
	})
}

func (r *projectRepo) Get(ctx context.Context, id string) (*project.Project, error) {
	var p project.Project
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &p,
			`SELECT id, name, description, created_at, updated_at FROM projects WHERE id=?`, id)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *projectRepo) List(ctx context.Context) ([]*project.Project, error) {
	var projects []*project.Project
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &projects,
			`SELECT id, name, description, created_at, updated_at FROM projects ORDER BY created_at ASC`)
	})
	if projects == nil {
		projects = []*project.Project{}
	}
	return projects, err
}

func (r *projectRepo) Delete(ctx context.Context, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
		return err
	})
}
