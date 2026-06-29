package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/connection"
)

type connectionRepo struct{ dbstore.BaseRepo }

func NewConnectionRepo(exec *dbstore.Executor, source string) connection.Repository {
	return &connectionRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

type connectionRow struct {
	ProjectID       string     `db:"project_id"`
	Name            string     `db:"name"`
	ConnType        string     `db:"type"`
	Endpoint        string     `db:"endpoint"`
	Disabled        bool       `db:"disabled"`
	LastUsedAt      *time.Time `db:"last_used_at"`
	LastTestedAt    *time.Time `db:"last_tested_at"`
	LastTestOK      *bool      `db:"last_test_ok"`
	LastTestMessage string     `db:"last_test_message"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
}

func toConnMeta(r *connectionRow) *connection.Metadata {
	return &connection.Metadata{
		ProjectID:       r.ProjectID,
		Name:            r.Name,
		Type:            connection.Type(r.ConnType),
		Endpoint:        r.Endpoint,
		Disabled:        r.Disabled,
		LastUsedAt:      r.LastUsedAt,
		LastTestedAt:    r.LastTestedAt,
		LastTestOK:      r.LastTestOK,
		LastTestMessage: r.LastTestMessage,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

const pgConnCols = `project_id, name, type, endpoint, disabled, last_used_at, last_tested_at, last_test_ok, last_test_message, created_at, updated_at`

func (r *connectionRepo) List(ctx context.Context, projectID string) ([]*connection.Metadata, error) {
	var rows []connectionRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows, db.Rebind(`SELECT `+pgConnCols+` FROM connections WHERE project_id=? ORDER BY name`), projectID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*connection.Metadata, 0, len(rows))
	for i := range rows {
		out = append(out, toConnMeta(&rows[i]))
	}
	return out, nil
}

func (r *connectionRepo) Get(ctx context.Context, projectID, name string) (*connection.Metadata, error) {
	var row connectionRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &row, db.Rebind(`SELECT `+pgConnCols+` FROM connections WHERE project_id=? AND name=?`), projectID, name)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toConnMeta(&row), nil
}

func (r *connectionRepo) Create(ctx context.Context, meta *connection.Metadata, encrypted []byte) error {
	now := time.Now().UTC()
	meta.CreatedAt = now
	meta.UpdatedAt = now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		_, err = tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO connections (project_id, name, type, endpoint, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, FALSE, ?, ?)`),
			meta.ProjectID, meta.Name, string(meta.Type), meta.Endpoint, now, now,
		)
		if err != nil {
			if isPostgresUniqueError(err) {
				return connection.ErrAlreadyExists
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO connection_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, 1, ?, TRUE, ?)`),
			meta.ProjectID, meta.Name, encrypted, now,
		); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *connectionRepo) Rotate(ctx context.Context, projectID, name string, encrypted []byte) error {
	now := time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		// Verify the connection exists before touching values (prevents FK error on insert).
		res, err := tx.ExecContext(ctx, db.Rebind(`UPDATE connections SET updated_at=? WHERE project_id=? AND name=?`), now, projectID, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return connection.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`UPDATE connection_values SET active=FALSE WHERE project_id=? AND name=?`), projectID, name); err != nil {
			return err
		}
		var version int
		if err := tx.GetContext(ctx, &version, db.Rebind(`SELECT COALESCE(MAX(version), 0) + 1 FROM connection_values WHERE project_id=? AND name=?`), projectID, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO connection_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, ?, ?, TRUE, ?)`),
			projectID, name, version, encrypted, now,
		); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *connectionRepo) Patch(ctx context.Context, projectID, name string, req connection.PatchRequest) error {
	now := time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		if req.Enabled != nil {
			res, err := db.ExecContext(ctx, db.Rebind(`UPDATE connections SET disabled=?, updated_at=? WHERE project_id=? AND name=?`), !*req.Enabled, now, projectID, name)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return connection.ErrNotFound
			}
		}
		if req.Endpoint != nil {
			res, err := db.ExecContext(ctx, db.Rebind(`UPDATE connections SET endpoint=?, updated_at=? WHERE project_id=? AND name=?`), *req.Endpoint, now, projectID, name)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return connection.ErrNotFound
			}
		}
		return nil
	})
}

func (r *connectionRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, db.Rebind(`DELETE FROM connections WHERE project_id=? AND name=?`), projectID, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return connection.ErrNotFound
		}
		return nil
	})
}

func (r *connectionRepo) GetValue(ctx context.Context, projectID, name string) ([]byte, error) {
	var ciphertext []byte
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &ciphertext,
			db.Rebind(`SELECT ciphertext FROM connection_values WHERE project_id=? AND name=? AND active=TRUE ORDER BY version DESC LIMIT 1`),
			projectID, name)
	})
	return ciphertext, err
}

func (r *connectionRepo) MarkUsed(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(`UPDATE connections SET last_used_at=? WHERE project_id=? AND name=?`), time.Now().UTC(), projectID, name)
		return err
	})
}

func (r *connectionRepo) RecordTestResult(ctx context.Context, projectID, name string, ok bool, message string) error {
	now := time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(
			`UPDATE connections SET last_tested_at=?, last_test_ok=?, last_test_message=? WHERE project_id=? AND name=?`),
			now, ok, message, projectID, name)
		return err
	})
}
