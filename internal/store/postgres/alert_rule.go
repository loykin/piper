package postgres

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

const pgAlertRuleCols = `id, project_id, name, source, event_type, when_expr, metric_key, condition_expr, notify_json, cooldown_seconds, enabled, created_by, last_matched_at, last_attempted_at, last_success_at, last_error, created_at, updated_at`

func decodePGAlertRule(rule *alerting.Rule) *alerting.Rule {
	_ = json.Unmarshal([]byte(rule.NotifyJSON), &rule.Notify)
	if rule.Notify == nil {
		rule.Notify = []string{}
	}
	return rule
}

func (r *alertRuleRepo) Create(ctx context.Context, rule *alerting.Rule) error {
	now := time.Now().UTC()
	rule.CreatedAt, rule.UpdatedAt = now, now
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		_, err := db.ExecContext(ctx, db.Rebind(`INSERT INTO alert_rules (`+pgAlertRuleCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, '', ?, ?)`), rule.ID, rule.ProjectID, rule.Name, rule.Source, rule.EventType, rule.When, rule.MetricKey, rule.Condition, rule.NotifyJSON, rule.CooldownSeconds, rule.Enabled, rule.CreatedBy, now, now)
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
			return alerting.ErrAlreadyExists
		}
		return err
	})
}
func (r *alertRuleRepo) Get(ctx context.Context, projectID, id string) (*alerting.Rule, error) {
	var rule alerting.Rule
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &rule, db.Rebind(`SELECT `+pgAlertRuleCols+` FROM alert_rules WHERE project_id=? AND id=?`), projectID, id)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodePGAlertRule(&rule), nil
}
func (r *alertRuleRepo) List(ctx context.Context, projectID string, limit, offset int) ([]*alerting.Rule, error) {
	q := `SELECT ` + pgAlertRuleCols + ` FROM alert_rules WHERE project_id=? ORDER BY created_at DESC`
	args := []any{projectID}
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	var rows []*alerting.Rule
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows, db.Rebind(q), args...)
	})
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		decodePGAlertRule(v)
	}
	return rows, nil
}
func (r *alertRuleRepo) Count(ctx context.Context, projectID string) (int, error) {
	var n int
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.GetContext(ctx, &n, db.Rebind(`SELECT COUNT(*) FROM alert_rules WHERE project_id=?`), projectID)
	})
	return n, err
}
func (r *alertRuleRepo) ListEnabled(ctx context.Context) ([]*alerting.Rule, error) {
	var rows []*alerting.Rule
	err := r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		return db.SelectContext(ctx, &rows, `SELECT `+pgAlertRuleCols+` FROM alert_rules WHERE enabled=TRUE ORDER BY project_id, created_at`)
	})
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		decodePGAlertRule(v)
	}
	return rows, nil
}
func (r *alertRuleRepo) Update(ctx context.Context, rule *alerting.Rule) error {
	rule.UpdatedAt = time.Now().UTC()
	return r.Run(ctx, func(ctx context.Context, db *sqlx.DB) error {
		res, err := db.ExecContext(ctx, db.Rebind(`UPDATE alert_rules SET name=?, source=?, event_type=?, when_expr=?, metric_key=?, condition_expr=?, notify_json=?, cooldown_seconds=?, enabled=?, updated_at=? WHERE project_id=? AND id=?`), rule.Name, rule.Source, rule.EventType, rule.When, rule.MetricKey, rule.Condition, rule.NotifyJSON, rule.CooldownSeconds, rule.Enabled, rule.UpdatedAt, rule.ProjectID, rule.ID)
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
		res, err := db.ExecContext(ctx, db.Rebind(`DELETE FROM alert_rules WHERE project_id=? AND id=?`), projectID, id)
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
		res, err := db.ExecContext(ctx, db.Rebind(`UPDATE alert_rules SET last_matched_at=?, last_attempted_at=?, last_error='', updated_at=? WHERE project_id=? AND id=? AND enabled=TRUE AND (last_attempted_at IS NULL OR last_attempted_at<=?)`), now, now, now, projectID, id, cooldownBefore)
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
			_, err := db.ExecContext(ctx, db.Rebind(`UPDATE alert_rules SET last_success_at=?, last_error='', updated_at=? WHERE project_id=? AND id=?`), at, at, projectID, id)
			return err
		}
		_, err := db.ExecContext(ctx, db.Rebind(`UPDATE alert_rules SET last_error=?, updated_at=? WHERE project_id=? AND id=?`), message, at, projectID, id)
		return err
	})
}
