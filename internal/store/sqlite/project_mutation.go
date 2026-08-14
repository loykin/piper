package sqlite

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/loykin/piper/internal/projectclient"
)

type projectMutationRepo struct{ dbstore.BaseRepo }

func NewProjectMutationRepo(exec *dbstore.Executor, source string) projectclient.MutationRepository {
	return &projectMutationRepo{dbstore.NewBaseRepo(source, exec)}
}
func (r *projectMutationRepo) Claim(ctx context.Context, v *projectclient.Mutation) (out *projectclient.Mutation, claimed bool, err error) {
	out = &projectclient.Mutation{}
	err = r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, e := db.NamedExecContext(ctx, `INSERT INTO project_mutations (project_id,idempotency_key,request_hash,response_status,response_headers,response_body,completed,created_at) VALUES (:project_id,:idempotency_key,:request_hash,:response_status,:response_headers,:response_body,:completed,:created_at) ON CONFLICT(project_id,idempotency_key) DO NOTHING`, v)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n == 1 {
			claimed = true
			*out = *v
			return nil
		}
		return db.GetContext(ctx, out, `SELECT project_id,idempotency_key,request_hash,response_status,response_headers,response_body,completed,created_at FROM project_mutations WHERE project_id=? AND idempotency_key=?`, v.ProjectID, v.Key)
	})
	return
}
func (r *projectMutationRepo) Complete(ctx context.Context, v *projectclient.Mutation) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		result, err := db.NamedExecContext(ctx, `UPDATE project_mutations SET response_status=:response_status,response_headers=:response_headers,response_body=:response_body,completed=1 WHERE project_id=:project_id AND idempotency_key=:idempotency_key AND request_hash=:request_hash`, v)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("complete project mutation: updated %d rows", updated)
		}
		return nil
	})
}
