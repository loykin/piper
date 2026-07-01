package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/piper/piper/pkg/credential"
)

type credentialRepo struct{ dbstore.BaseRepo }

func NewCredentialRepo(exec *dbstore.Executor, source string) credential.Repository {
	return &credentialRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

const pgCredentialCols = `project_id, name, kind, endpoint, keys_json, disabled, last_used_at, last_tested_at, last_test_ok, last_test_message, created_at, updated_at`

func (r *credentialRepo) List(ctx context.Context, projectID string) ([]*credential.Metadata, error) {
	var rows []credential.Record
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows, db.Rebind(`SELECT `+pgCredentialCols+` FROM credentials WHERE project_id=? ORDER BY name`), projectID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*credential.Metadata, 0, len(rows))
	for i := range rows {
		out = append(out, pgCredentialRecordMeta(&rows[i]))
	}
	return out, nil
}

func (r *credentialRepo) Get(ctx context.Context, projectID, name string) (*credential.Metadata, error) {
	var row credential.Record
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &row, db.Rebind(`SELECT `+pgCredentialCols+` FROM credentials WHERE project_id=? AND name=?`), projectID, name)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pgCredentialRecordMeta(&row), nil
}

func (r *credentialRepo) Create(ctx context.Context, meta *credential.Metadata, encrypted []byte) error {
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
		_, err = tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO credentials (project_id, name, kind, endpoint, keys_json, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, FALSE, ?, ?)`),
			meta.ProjectID, meta.Name, string(meta.Kind), meta.Endpoint, string(keysJSON), now, now,
		)
		if err != nil {
			if credentialPostgresUniqueError(err) {
				return credential.ErrAlreadyExists
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO credential_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, 1, ?, TRUE, ?)`),
			meta.ProjectID, meta.Name, encrypted, now,
		); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *credentialRepo) Rotate(ctx context.Context, projectID, name string, encrypted []byte, keys []string) error {
	now := time.Now().UTC()
	keysJSON, _ := json.Marshal(keys)
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.ExecContext(ctx, db.Rebind(`UPDATE credentials SET keys_json=?, updated_at=? WHERE project_id=? AND name=?`), string(keysJSON), now, projectID, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return credential.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(`UPDATE credential_values SET active=FALSE WHERE project_id=? AND name=?`), projectID, name); err != nil {
			return err
		}
		var version int
		if err := tx.GetContext(ctx, &version, db.Rebind(`SELECT COALESCE(MAX(version), 0) + 1 FROM credential_values WHERE project_id=? AND name=?`), projectID, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Rebind(
			`INSERT INTO credential_values (project_id, name, version, ciphertext, active, created_at) VALUES (?, ?, ?, ?, TRUE, ?)`),
			projectID, name, version, encrypted, now,
		); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (r *credentialRepo) Patch(ctx context.Context, projectID, name string, req credential.PatchRequest) error {
	now := time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		updated := false
		if req.Enabled != nil {
			res, err := db.ExecContext(ctx, db.Rebind(`UPDATE credentials SET disabled=?, updated_at=? WHERE project_id=? AND name=?`), !*req.Enabled, now, projectID, name)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return credential.ErrNotFound
			}
			updated = true
		}
		if req.Endpoint != nil {
			res, err := db.ExecContext(ctx, db.Rebind(`UPDATE credentials SET endpoint=?, updated_at=? WHERE project_id=? AND name=?`), *req.Endpoint, now, projectID, name)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return credential.ErrNotFound
			}
			updated = true
		}
		if !updated {
			var exists int
			if err := db.GetContext(ctx, &exists, db.Rebind(`SELECT COUNT(*) FROM credentials WHERE project_id=? AND name=?`), projectID, name); err != nil {
				return err
			}
			if exists == 0 {
				return credential.ErrNotFound
			}
		}
		return nil
	})
}

func (r *credentialRepo) Delete(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, db.Rebind(`DELETE FROM credentials WHERE project_id=? AND name=?`), projectID, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return credential.ErrNotFound
		}
		return nil
	})
}

func (r *credentialRepo) GetValue(ctx context.Context, projectID, name string) ([]byte, error) {
	var ciphertext []byte
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &ciphertext,
			db.Rebind(`SELECT ciphertext FROM credential_values WHERE project_id=? AND name=? AND active=TRUE ORDER BY version DESC LIMIT 1`),
			projectID, name)
	})
	return ciphertext, err
}

func (r *credentialRepo) MarkUsed(ctx context.Context, projectID, name string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(`UPDATE credentials SET last_used_at=? WHERE project_id=? AND name=?`), time.Now().UTC(), projectID, name)
		return err
	})
}

func (r *credentialRepo) RecordTestResult(ctx context.Context, projectID, name string, ok bool, message string) error {
	now := time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(
			`UPDATE credentials SET last_tested_at=?, last_test_ok=?, last_test_message=? WHERE project_id=? AND name=?`),
			now, ok, message, projectID, name)
		return err
	})
}

func pgCredentialRecordMeta(row *credential.Record) *credential.Metadata {
	var keys []string
	_ = json.Unmarshal([]byte(row.KeysJSON), &keys)
	return &credential.Metadata{
		ProjectID:       row.ProjectID,
		Name:            row.Name,
		Kind:            row.Kind,
		Endpoint:        row.Endpoint,
		Keys:            keys,
		Disabled:        row.Disabled,
		LastUsedAt:      row.LastUsedAt,
		LastTestedAt:    row.LastTestedAt,
		LastTestOK:      row.LastTestOK,
		LastTestMessage: row.LastTestMessage,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func credentialPostgresUniqueError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
