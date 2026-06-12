package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/piper/piper/pkg/project"
)

type projectRepo struct{ db *sqlx.DB }

func NewProjectRepo(db *sqlx.DB) project.Repository {
	return &projectRepo{db: db}
}

func (r *projectRepo) Create(ctx context.Context, p *project.Project) error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO projects (id, name, description, created_at, updated_at)
		 VALUES (:id, :name, :description, :created_at, :updated_at)`,
		p)
	return err
}

func (r *projectRepo) Get(ctx context.Context, id string) (*project.Project, error) {
	var p project.Project
	q := r.db.Rebind(`SELECT id, name, description, created_at, updated_at FROM projects WHERE id=?`)
	err := r.db.GetContext(ctx, &p, q, id)
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
	err := r.db.SelectContext(ctx, &projects,
		`SELECT id, name, description, created_at, updated_at FROM projects ORDER BY created_at ASC`)
	if projects == nil {
		projects = []*project.Project{}
	}
	return projects, err
}

func (r *projectRepo) Delete(ctx context.Context, id string) error {
	q := r.db.Rebind(`DELETE FROM projects WHERE id=?`)
	_, err := r.db.ExecContext(ctx, q, id)
	return err
}
