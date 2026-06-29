package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/secret"
)

type secretRepo struct{ dbstore.BaseRepo }

func NewSecretRepo(exec *dbstore.Executor, source string) secret.Repository {
	return &secretRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *secretRepo) List(ctx context.Context, projectID string) ([]*secret.Metadata, error) {
	var rows []secret.Record
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows, db.Rebind(`SELECT project_id, name, type, provider, keys_json, disabled, created_at, updated_at, last_used_at FROM secrets WHERE project_id=? ORDER BY name`), projectID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*secret.Metadata, 0, len(rows))
	for i := range rows {
		out = append(out, recordMeta(&rows[i]))
	}
	return out, nil
}

func (r *secretRepo) Get(ctx context.Context, projectID, name string) (*secret.Metadata, error) {
	var row secret.Record
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &row, db.Rebind(`SELECT project_id, name, type, provider, keys_json, disabled, created_at, updated_at, last_used_at FROM secrets WHERE project_id=? AND name=?`), projectID, name)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return recordMeta(&row), nil
}

func (r *secretRepo) Create(ctx context.Context, meta *secret.Metadata, encrypted []byte) error {
	now := time.Now().UTC()
	meta.CreatedAt = now
	meta.UpdatedAt = now
	keysJSON, _ := json.Marshal(meta.Keys)
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var existRow struct {
			Disabled  bool      `db:"disabled"`
			CreatedAt time.Time `db:"created_at"`
		}
		getErr := tx.GetContext(ctx, &existRow, db.Rebind(`SELECT disabled, created_at FROM secrets WHERE project_id=? AND name=? FOR UPDATE`), meta.ProjectID, meta.Name)
		if getErr == nil {
			if !existRow.Disabled {
				return secret.ErrAlreadyExists
			}
			meta.CreatedAt = existRow.CreatedAt
			if _, updErr := tx.ExecContext(ctx, db.Rebind(`UPDATE secret_values SET active=FALSE WHERE project_id=? AND name=?`), meta.ProjectID, meta.Name); updErr != nil {
				return updErr
			}
			var version int
			if verErr := tx.GetContext(ctx, &version, db.Rebind(`SELECT COALESCE(MAX(version), 0) + 1 FROM secret_values WHERE project_id=? AND name=?`), meta.ProjectID, meta.Name); verErr != nil {
				return verErr
			}
			if _, valErr := tx.ExecContext(ctx, db.Rebind(`INSERT INTO secret_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, ?, ?, TRUE, ?)`),
				meta.ProjectID, meta.Name, version, encrypted, now); valErr != nil {
				return valErr
			}
			if _, reactivateErr := tx.ExecContext(ctx, db.Rebind(`UPDATE secrets SET type=?, provider=?, keys_json=?, disabled=FALSE, updated_at=? WHERE project_id=? AND name=?`),
				meta.Type, meta.Provider, string(keysJSON), now, meta.ProjectID, meta.Name); reactivateErr != nil {
				return reactivateErr
			}
			return tx.Commit()
		}
		if getErr != sql.ErrNoRows {
			return getErr
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO secrets (project_id, name, type, provider, keys_json, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
			meta.ProjectID, meta.Name, meta.Type, meta.Provider, string(keysJSON), false, meta.CreatedAt, meta.UpdatedAt); err != nil {
			if isPostgresUniqueError(err) {
				return secret.ErrAlreadyExists
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO secret_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, 1, ?, TRUE, ?)`),
			meta.ProjectID, meta.Name, encrypted, now); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *secretRepo) Rotate(ctx context.Context, projectID, name string, encrypted []byte, keys []string) error {
	now := time.Now().UTC()
	keysJSON, _ := json.Marshal(keys)
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, db.Rebind(`UPDATE secret_values SET active=FALSE WHERE project_id=? AND name=?`), projectID, name); err != nil {
			return err
		}
		var version int
		if err := tx.GetContext(ctx, &version, db.Rebind(`SELECT COALESCE(MAX(version), 0) + 1 FROM secret_values WHERE project_id=? AND name=?`), projectID, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO secret_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, ?, ?, TRUE, ?)`), projectID, name, version, encrypted, now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, db.Rebind(`UPDATE secrets SET keys_json=?, updated_at=? WHERE project_id=? AND name=?`), string(keysJSON), now, projectID, name)
		if err != nil {
			return err
		}
		if affected, err := res.RowsAffected(); err != nil {
			return err
		} else if affected == 0 {
			return secret.ErrNotFound
		}
		return tx.Commit()
	})
}

func (r *secretRepo) SetEnabled(ctx context.Context, projectID, name string, enabled bool) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, db.Rebind(`UPDATE secrets SET disabled=?, updated_at=? WHERE project_id=? AND name=?`), !enabled, time.Now().UTC(), projectID, name)
		if err != nil {
			return err
		}
		if affected, err := res.RowsAffected(); err != nil {
			return err
		} else if affected == 0 {
			return secret.ErrNotFound
		}
		return nil
	})
}

func (r *secretRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, db.Rebind(`UPDATE secrets SET disabled=TRUE, updated_at=? WHERE project_id=? AND name=?`), time.Now().UTC(), projectID, name)
		if err != nil {
			return err
		}
		if affected, err := res.RowsAffected(); err != nil {
			return err
		} else if affected == 0 {
			return secret.ErrNotFound
		}
		return nil
	})
}

func (r *secretRepo) GetActiveValue(ctx context.Context, projectID, name string) ([]byte, error) {
	var encrypted []byte
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &encrypted, db.Rebind(`SELECT ciphertext FROM secret_values WHERE project_id=? AND name=? AND active=TRUE ORDER BY version DESC LIMIT 1`), projectID, name)
	})
	return encrypted, err
}

func (r *secretRepo) MarkUsed(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(`UPDATE secrets SET last_used_at=? WHERE project_id=? AND name=?`), time.Now().UTC(), projectID, name)
		return err
	})
}

func recordMeta(row *secret.Record) *secret.Metadata {
	var keys []string
	_ = json.Unmarshal([]byte(row.KeysJSON), &keys)
	return &secret.Metadata{
		ProjectID:  row.ProjectID,
		Name:       row.Name,
		Type:       row.Type,
		Provider:   row.Provider,
		Keys:       keys,
		Disabled:   row.Disabled,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
		LastUsedAt: row.LastUsedAt,
	}
}

func isPostgresUniqueError(err error) bool {
	if errors.Is(err, secret.ErrAlreadyExists) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
