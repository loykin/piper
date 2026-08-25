package postgres

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/pipeline/run"
)

type submissionRepo struct{ sqlxadapter.Source }

func NewSubmissionRepo(exec *dbstore.Executor[*sqlx.DB], source string) run.SubmissionRepository {
	return &submissionRepo{Source: sqlxadapter.NewSource(source, exec)}
}

func (r *submissionRepo) Claim(ctx context.Context, value *run.Submission) (*run.Submission, bool, error) {
	var existing run.Submission
	claimed := false
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		result, err := db.NamedExecContext(ctx, `
			INSERT INTO run_submissions (project_id, idempotency_key, request_hash, run_id, created_at)
			VALUES (:project_id, :idempotency_key, :request_hash, :run_id, :created_at)
			ON CONFLICT(project_id, idempotency_key) DO NOTHING`, value)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 1 {
			claimed = true
			existing = *value
			return nil
		}
		return db.GetContext(ctx, &existing, db.Rebind(`
			SELECT project_id, idempotency_key, request_hash, run_id, created_at
			FROM run_submissions WHERE project_id=? AND idempotency_key=?`), value.ProjectID, value.Key)
	})
	return &existing, claimed, err
}

func (r *submissionRepo) Delete(ctx context.Context, projectID, key string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(`DELETE FROM run_submissions WHERE project_id=? AND idempotency_key=?`), projectID, key)
		return err
	})
}
