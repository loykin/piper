package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
)

type workerPodPolicyRepo struct{ dbstore.BaseRepo }

func NewWorkerPodPolicyRepo(exec *dbstore.Executor, source string) iagent.WorkerPodPolicyRepository {
	return &workerPodPolicyRepo{BaseRepo: dbstore.NewBaseRepo(source, exec)}
}

func (r *workerPodPolicyRepo) List(ctx context.Context) ([]iagent.WorkerPodPolicy, error) {
	var rows []struct {
		WorkerID    string    `db:"worker_id"`
		PodTemplate string    `db:"pod_template"`
		UpdatedAt   time.Time `db:"updated_at"`
		UpdatedBy   string    `db:"updated_by"`
	}
	if err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT worker_id, pod_template, updated_at, updated_by
		                 FROM worker_pod_policies ORDER BY updated_at DESC`)
		return db.SelectContext(ctx, &rows, q)
	}); err != nil {
		return nil, err
	}
	out := make([]iagent.WorkerPodPolicy, 0, len(rows))
	for _, row := range rows {
		var pt corev1.PodTemplateSpec
		if err := json.Unmarshal([]byte(row.PodTemplate), &pt); err != nil {
			return nil, err
		}
		out = append(out, iagent.WorkerPodPolicy{
			WorkerID:    row.WorkerID,
			PodTemplate: pt,
			RawTemplate: row.PodTemplate,
			UpdatedAt:   row.UpdatedAt,
			UpdatedBy:   row.UpdatedBy,
		})
	}
	return out, nil
}

func (r *workerPodPolicyRepo) Get(ctx context.Context, workerID string) (*iagent.WorkerPodPolicy, error) {
	var row struct {
		WorkerID    string    `db:"worker_id"`
		PodTemplate string    `db:"pod_template"`
		UpdatedAt   time.Time `db:"updated_at"`
		UpdatedBy   string    `db:"updated_by"`
	}
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`SELECT worker_id, pod_template, updated_at, updated_by
		                 FROM worker_pod_policies WHERE worker_id = ?`)
		return db.GetContext(ctx, &row, q, workerID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pt corev1.PodTemplateSpec
	if err := json.Unmarshal([]byte(row.PodTemplate), &pt); err != nil {
		return nil, err
	}
	return &iagent.WorkerPodPolicy{
		WorkerID:    row.WorkerID,
		PodTemplate: pt,
		RawTemplate: row.PodTemplate,
		UpdatedAt:   row.UpdatedAt,
		UpdatedBy:   row.UpdatedBy,
	}, nil
}

func (r *workerPodPolicyRepo) Set(ctx context.Context, p iagent.WorkerPodPolicy) error {
	raw, err := json.Marshal(p.PodTemplate)
	if err != nil {
		return err
	}
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`
			INSERT INTO worker_pod_policies (worker_id, pod_template, updated_at, updated_by)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (worker_id) DO UPDATE SET
			    pod_template = EXCLUDED.pod_template,
			    updated_at   = EXCLUDED.updated_at,
			    updated_by   = EXCLUDED.updated_by`)
		_, err := db.ExecContext(ctx, q, p.WorkerID, string(raw), time.Now(), p.UpdatedBy)
		return err
	})
}

func (r *workerPodPolicyRepo) Delete(ctx context.Context, workerID string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		q := db.Rebind(`DELETE FROM worker_pod_policies WHERE worker_id = ?`)
		_, err := db.ExecContext(ctx, q, workerID)
		return err
	})
}
