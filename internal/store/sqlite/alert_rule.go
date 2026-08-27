package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	"github.com/loykin/piper/pkg/alerting"
)

type alertRuleRepo struct{ sqlxadapter.Source }

func NewAlertRuleRepo(exec *dbstore.Executor[*sqlx.DB], source string) alerting.Repository {
	return &alertRuleRepo{Source: sqlxadapter.NewSource(source, exec)}
}

type alertRuleRow struct {
	alerting.Rule
	EnabledInt int `db:"enabled"`
}

const alertRuleCols = `id, project_id, name, source, event_type, when_expr, metric_key, condition_expr, notify_json, cooldown_seconds, enabled, created_by, last_matched_at, last_attempted_at, last_success_at, last_error, created_at, updated_at`

func decodeAlertRule(row alertRuleRow) *alerting.Rule {
	rule := row.Rule
	rule.Enabled = row.EnabledInt == 1
	_ = json.Unmarshal([]byte(rule.NotifyJSON), &rule.Notify)
	if rule.Notify == nil {
		rule.Notify = []string{}
	}
	return &rule
}

func (r *alertRuleRepo) Create(ctx context.Context, rule *alerting.Rule) error {
	now := time.Now().UTC()
	rule.CreatedAt, rule.UpdatedAt = now, now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, `INSERT INTO alert_rules (`+alertRuleCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, '', ?, ?)`, rule.ID, rule.ProjectID, rule.Name, rule.Source, rule.EventType, rule.When, rule.MetricKey, rule.Condition, rule.NotifyJSON, rule.CooldownSeconds, boolToInt(rule.Enabled), rule.CreatedBy, now, now)
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
			return alerting.ErrAlreadyExists
		}
		return err
	})
}

func (r *alertRuleRepo) Get(ctx context.Context, projectID, id string) (*alerting.Rule, error) {
	var row alertRuleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &row, `SELECT `+alertRuleCols+` FROM alert_rules WHERE project_id=? AND id=?`, projectID, id)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeAlertRule(row), nil
}

func (r *alertRuleRepo) List(ctx context.Context, projectID string, limit, offset int) ([]*alerting.Rule, error) {
	query := `SELECT ` + alertRuleCols + ` FROM alert_rules WHERE project_id=? ORDER BY created_at DESC`
	args := []any{projectID}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	var rows []alertRuleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error { return db.SelectContext(ctx, &rows, query, args...) })
	if err != nil {
		return nil, err
	}
	out := make([]*alerting.Rule, len(rows))
	for i := range rows {
		out[i] = decodeAlertRule(rows[i])
	}
	return out, nil
}

func (r *alertRuleRepo) Count(ctx context.Context, projectID string) (int, error) {
	var count int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &count, `SELECT COUNT(*) FROM alert_rules WHERE project_id=?`, projectID)
	})
	return count, err
}

func (r *alertRuleRepo) ListEnabled(ctx context.Context) ([]*alerting.Rule, error) {
	var rows []alertRuleRow
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows, `SELECT `+alertRuleCols+` FROM alert_rules WHERE enabled=1 ORDER BY project_id, created_at`)
	})
	if err != nil {
		return nil, err
	}
	out := make([]*alerting.Rule, len(rows))
	for i := range rows {
		out[i] = decodeAlertRule(rows[i])
	}
	return out, nil
}

func (r *alertRuleRepo) Update(ctx context.Context, rule *alerting.Rule) error {
	rule.UpdatedAt = time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, `UPDATE alert_rules SET name=?, source=?, event_type=?, when_expr=?, metric_key=?, condition_expr=?, notify_json=?, cooldown_seconds=?, enabled=?, updated_at=? WHERE project_id=? AND id=?`, rule.Name, rule.Source, rule.EventType, rule.When, rule.MetricKey, rule.Condition, rule.NotifyJSON, rule.CooldownSeconds, boolToInt(rule.Enabled), rule.UpdatedAt, rule.ProjectID, rule.ID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return alerting.ErrAlreadyExists
			}
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return alerting.ErrNotFound
		}
		return nil
	})
}

func (r *alertRuleRepo) Delete(ctx context.Context, projectID, id string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, `DELETE FROM alert_rules WHERE project_id=? AND id=?`, projectID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return alerting.ErrNotFound
		}
		return nil
	})
}

func (r *alertRuleRepo) TryClaimFire(ctx context.Context, projectID, id string, now, cooldownBefore time.Time) (bool, error) {
	var affected int64
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, `UPDATE alert_rules SET last_matched_at=?, last_attempted_at=?, last_error='', updated_at=? WHERE project_id=? AND id=? AND enabled=1 AND (last_attempted_at IS NULL OR last_attempted_at<=?)`, now, now, now, projectID, id, cooldownBefore)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	return affected == 1, err
}

func (r *alertRuleRepo) RecordDelivery(ctx context.Context, projectID, id string, at time.Time, success bool, message string) error {
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		if success {
			_, err := db.ExecContext(ctx, `UPDATE alert_rules SET last_success_at=?, last_error='', updated_at=? WHERE project_id=? AND id=?`, at, at, projectID, id)
			return err
		}
		_, err := db.ExecContext(ctx, `UPDATE alert_rules SET last_error=?, updated_at=? WHERE project_id=? AND id=?`, message, at, projectID, id)
		return err
	})
}
