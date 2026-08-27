package logstore

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
)

func purgeRelationalStats(ctx context.Context, exec *dbstore.Executor[*sqlx.DB], source, projectID, runID string) error {
	return sqlxadapter.RunTx(exec, ctx, source, func(ctx context.Context, tx *sqlx.Tx) error {
		where := "project_id=?"
		args := []any{projectID}
		if runID != "" {
			where += " AND run_id=?"
			args = append(args, runID)
		}
		for _, table := range []string{"logs", "run_metrics"} {
			if _, err := tx.ExecContext(ctx, tx.Rebind("DELETE FROM "+table+" WHERE "+where), args...); err != nil {
				return err
			}
		}
		return nil
	})
}
