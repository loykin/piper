package logstore

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
)

func purgeRelationalTable(ctx context.Context, exec *dbstore.Executor[*sqlx.DB], source, table, projectID string) error {
	return sqlxadapter.RunTx(exec, ctx, source, func(ctx context.Context, tx *sqlx.Tx) error {
		where := "project_id=?"
		args := []any{projectID}
		_, err := tx.ExecContext(ctx, tx.Rebind("DELETE FROM "+table+" WHERE "+where), args...)
		return err
	})
}
