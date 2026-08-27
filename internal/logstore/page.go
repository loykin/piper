package logstore

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	"github.com/loykin/piper/pkg/statsstore"
)

func queryRelationalLogPage(ctx context.Context, exec *dbstore.Executor[*sqlx.DB], source string, query statsstore.LogQuery) (statsstore.LogPage, error) {
	if query.Search != "" {
		return statsstore.LogPage{}, fmt.Errorf("log search is not supported by this statistics backend")
	}
	afterID, err := statsstore.LogIDFromCursor(query.Cursor, query)
	if err != nil {
		return statsstore.LogPage{}, err
	}
	limit := statsstore.NormalizeLimit(query.Limit)
	sqlQuery := `SELECT id, event_id, project_id, run_id, step_name, ts, stream, line
		FROM logs WHERE project_id=? AND run_id=? AND step_name=? AND id>?`
	args := []any{query.ProjectID, query.RunID, query.StepName, afterID}
	if !query.Since.IsZero() {
		sqlQuery += ` AND ts>=?`
		args = append(args, query.Since.UTC())
	}
	if !query.Until.IsZero() {
		sqlQuery += ` AND ts<=?`
		args = append(args, query.Until.UTC())
	}
	sqlQuery += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit+1)

	page := statsstore.LogPage{Lines: []statsstore.LogLine{}}
	err = exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
		rows, err := db.QueryContext(ctx, db.Rebind(sqlQuery), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var line statsstore.LogLine
			if err := rows.Scan(&line.ID, &line.EventID, &line.ProjectID, &line.RunID, &line.StepName, &line.Ts, &line.Stream, &line.Line); err != nil {
				return err
			}
			page.Lines = append(page.Lines, line)
		}
		return rows.Err()
	})
	if err != nil {
		return statsstore.LogPage{}, err
	}
	if len(page.Lines) > limit {
		page.Lines = page.Lines[:limit]
		page.NextCursor = statsstore.CursorForLogQuery(page.Lines[len(page.Lines)-1].ID, query)
	}
	return page, nil
}

func queryRelationalMetricPage(ctx context.Context, exec *dbstore.Executor[*sqlx.DB], source string, query statsstore.MetricQuery) (statsstore.MetricPage, error) {
	afterID, err := statsstore.MetricIDFromCursor(query.Cursor, query)
	if err != nil {
		return statsstore.MetricPage{}, err
	}
	limit := statsstore.NormalizeLimit(query.Limit)
	sqlQuery := `SELECT id, event_id, project_id, run_id, step_name, key, value, recorded_at
		FROM run_metrics WHERE project_id=? AND run_id=? AND id>?`
	args := []any{query.ProjectID, query.RunID, afterID}
	if query.StepName != "" {
		sqlQuery += ` AND step_name=?`
		args = append(args, query.StepName)
	}
	if len(query.Keys) > 0 {
		sqlQuery += ` AND key IN (?)`
		args = append(args, query.Keys)
	}
	if !query.Since.IsZero() {
		sqlQuery += ` AND recorded_at>=?`
		args = append(args, query.Since.UTC())
	}
	if !query.Until.IsZero() {
		sqlQuery += ` AND recorded_at<=?`
		args = append(args, query.Until.UTC())
	}
	sqlQuery += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit+1)
	sqlQuery, args, err = sqlx.In(sqlQuery, args...)
	if err != nil {
		return statsstore.MetricPage{}, err
	}

	page := statsstore.MetricPage{Points: []statsstore.MetricPoint{}}
	err = exec.Run(ctx, source, func(ctx context.Context, db *sqlx.DB) error {
		rows, err := db.QueryContext(ctx, db.Rebind(sqlQuery), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var point statsstore.MetricPoint
			if err := rows.Scan(&point.ID, &point.EventID, &point.ProjectID, &point.RunID, &point.StepName, &point.Key, &point.Value, &point.Ts); err != nil {
				return err
			}
			page.Points = append(page.Points, point)
		}
		return rows.Err()
	})
	if err != nil {
		return statsstore.MetricPage{}, err
	}
	if len(page.Points) > limit {
		page.Points = page.Points[:limit]
		page.NextCursor = statsstore.CursorForMetricQuery(page.Points[len(page.Points)-1].ID, query)
	}
	return page, nil
}
