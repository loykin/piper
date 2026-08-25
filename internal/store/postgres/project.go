package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/project"
)

type projectRepo struct{ sqlxadapter.Source }

func NewProjectRepo(exec *dbstore.Executor[*sqlx.DB], source string) project.Repository {
	return &projectRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *projectRepo) Create(ctx context.Context, p *project.Project) error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.OwnerMemberID == "" {
		p.OwnerMemberID = project.LocalMemberID
	}
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.NamedExecContext(ctx,
			`INSERT INTO projects (id, name, description, owner_member_id, created_at, updated_at)
			 VALUES (:id, :name, :description, :owner_member_id, :created_at, :updated_at)`,
			p)
		return err
	})
}

func (r *projectRepo) Get(ctx context.Context, id string) (*project.Project, error) {
	var p project.Project
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT id, name, description, owner_member_id, created_at, updated_at FROM projects WHERE id=?`)
		return db.GetContext(ctx, &p, q, id)
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
			`SELECT id, name, description, owner_member_id, created_at, updated_at FROM projects ORDER BY created_at ASC`)
	})
	if projects == nil {
		projects = []*project.Project{}
	}
	return projects, err
}

func (r *projectRepo) SetOwner(ctx context.Context, id, memberID string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`UPDATE projects SET owner_member_id=?, updated_at=? WHERE id=?`)
		_, err := db.ExecContext(ctx, q, memberID, time.Now().UTC(), id)
		return err
	})
}

func (r *projectRepo) Delete(ctx context.Context, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM projects WHERE id=?`)
		_, err := db.ExecContext(ctx, q, id)
		return err
	})
}
