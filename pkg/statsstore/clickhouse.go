package statsstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var clickhouseIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type clickhouseBackend struct {
	http                              *httpBackend
	database, logsTable, metricsTable string
}

func openClickHouse(rawURL string, credential map[string]string, logRetention, metricRetention time.Duration, manage bool) (*clickhouseBackend, error) {
	h, parsed, err := newHTTPBackend(rawURL, credential)
	if err != nil {
		return nil, err
	}
	database := strings.Trim(parsed.Path, "/")
	if database == "" {
		database = "default"
	}
	q := parsed.Query()
	logs := q.Get("logs_table")
	if logs == "" {
		logs = "piper_logs"
	}
	metrics := q.Get("metrics_table")
	if metrics == "" {
		metrics = q.Get("table")
	}
	if metrics == "" {
		metrics = "piper_run_metrics"
	}
	for _, v := range []string{database, logs, metrics} {
		if !clickhouseIdentifier.MatchString(v) {
			return nil, fmt.Errorf("invalid ClickHouse database or table identifier %q", v)
		}
	}
	h.base.Path = ""
	b := &clickhouseBackend{http: h, database: database, logsTable: logs, metricsTable: metrics}
	// Database/table creation always runs, independent of manage
	// (ManageRetention) — without it, AppendLogs/AppendMetrics fail outright
	// ("Database ... does not exist") whenever an operator points
	// stats.*.url at ClickHouse without also opting into Piper-managed
	// retention. Only the TTL clause itself — which governs automatic row
	// expiry, not table existence — is conditional on manage. ts is
	// DateTime64(9,'UTC'); ClickHouse's TTL clause rejects an expression
	// that evaluates to DateTime64 ("TTL expression result column should
	// have DateTime or Date type"), so the column must be downcast to
	// DateTime with toDateTime() before adding the interval.
	logTTL := ""
	if manage && logRetention > 0 {
		logTTL = fmt.Sprintf(" TTL toDateTime(ts) + INTERVAL %d SECOND", int64(logRetention.Seconds()))
	}
	metricTTL := ""
	if manage && metricRetention > 0 {
		metricTTL = fmt.Sprintf(" TTL toDateTime(ts) + INTERVAL %d SECOND", int64(metricRetention.Seconds()))
	}
	statements := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", database),
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (id Int64,event_id String,project_id String,run_id String,step_name String,ts DateTime64(9,'UTC'),stream LowCardinality(String),line String) ENGINE=ReplacingMergeTree ORDER BY (project_id,run_id,step_name,id,event_id)%s", database, logs, logTTL),
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (id Int64,event_id String,project_id String,run_id String,step_name String,key String,value Float64,ts DateTime64(9,'UTC')) ENGINE=ReplacingMergeTree ORDER BY (project_id,run_id,step_name,id,event_id)%s", database, metrics, metricTTL),
	}
	if manage && logRetention > 0 {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s.%s MODIFY TTL toDateTime(ts) + INTERVAL %d SECOND", database, logs, int64(logRetention.Seconds())))
	}
	if manage && metricRetention > 0 {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s.%s MODIFY TTL toDateTime(ts) + INTERVAL %d SECOND", database, metrics, int64(metricRetention.Seconds())))
	}
	for _, stmt := range statements {
		if _, err := h.request(context.Background(), http.MethodPost, "", url.Values{"query": {stmt}}, nil, "text/plain"); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *clickhouseBackend) insert(ctx context.Context, table string, rows any) error {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	switch values := rows.(type) {
	case []LogLine:
		for _, v := range values {
			row := map[string]any{"id": v.ID, "event_id": v.EventID, "project_id": v.ProjectID, "run_id": v.RunID, "step_name": v.StepName, "ts": v.Ts.UTC().Format("2006-01-02 15:04:05.999999999"), "stream": v.Stream, "line": v.Line}
			if err := enc.Encode(row); err != nil {
				return err
			}
		}
	case []MetricPoint:
		for _, v := range values {
			row := map[string]any{"id": v.ID, "event_id": v.EventID, "project_id": v.ProjectID, "run_id": v.RunID, "step_name": v.StepName, "key": v.Key, "value": v.Value, "ts": v.Ts.UTC().Format("2006-01-02 15:04:05.999999999")}
			if err := enc.Encode(row); err != nil {
				return err
			}
		}
	}
	query := fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", b.database, table)
	_, err := b.http.request(ctx, http.MethodPost, "", url.Values{"query": {query}}, body.Bytes(), "application/x-ndjson")
	return err
}
func (b *clickhouseBackend) AppendLogs(ctx context.Context, v []LogLine) error {
	return b.insert(ctx, b.logsTable, v)
}
func (b *clickhouseBackend) AppendMetrics(ctx context.Context, v []MetricPoint) error {
	return b.insert(ctx, b.metricsTable, v)
}

func chQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "\\'") + "'" }
func chWhere(projectID, runID, step string, after int64, since, until time.Time) string {
	parts := []string{"project_id=" + chQuote(projectID), "run_id=" + chQuote(runID), "step_name=" + chQuote(step), "id>" + strconv.FormatInt(after, 10)}
	if !since.IsZero() {
		parts = append(parts, "ts>=parseDateTime64BestEffort("+chQuote(since.UTC().Format(time.RFC3339Nano))+")")
	}
	if !until.IsZero() {
		parts = append(parts, "ts<=parseDateTime64BestEffort("+chQuote(until.UTC().Format(time.RFC3339Nano))+")")
	}
	return strings.Join(parts, " AND ")
}
func (b *clickhouseBackend) query(ctx context.Context, table, where string, limit int, out any) error {
	sql := fmt.Sprintf("SELECT * FROM %s.%s FINAL WHERE %s ORDER BY id ASC LIMIT %d FORMAT JSON", b.database, table, where, NormalizeLimit(limit)+1)
	// ClickHouse's JSON output format quotes Int64/UInt64 values as strings by
	// default (to protect JS float precision), which breaks unmarshaling
	// straight into LogLine/MetricPoint's int64 id field — disable that.
	data, err := b.http.request(ctx, http.MethodPost, "", url.Values{"query": {sql}, "date_time_output_format": {"iso"}, "output_format_json_quote_64bit_integers": {"0"}}, nil, "text/plain")
	if err != nil {
		return err
	}
	var response struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return err
	}
	return json.Unmarshal(response.Data, out)
}
func (b *clickhouseBackend) QueryLogs(ctx context.Context, q LogQuery) (LogPage, error) {
	after, err := LogIDFromCursor(q.Cursor, q)
	if err != nil {
		return LogPage{}, err
	}
	where := chWhere(q.ProjectID, q.RunID, q.StepName, after, q.Since, q.Until)
	if q.Search != "" {
		where += " AND positionCaseInsensitive(line," + chQuote(q.Search) + ")>0"
	}
	var lines []LogLine
	if err = b.query(ctx, b.logsTable, where, q.Limit, &lines); err != nil {
		return LogPage{}, err
	}
	return logPageFrom(lines, q), nil
}
func (b *clickhouseBackend) QueryMetrics(ctx context.Context, q MetricQuery) (MetricPage, error) {
	after, err := MetricIDFromCursor(q.Cursor, q)
	if err != nil {
		return MetricPage{}, err
	}
	where := chWhere(q.ProjectID, q.RunID, q.StepName, after, q.Since, q.Until)
	if len(q.Keys) > 0 {
		quoted := make([]string, len(q.Keys))
		for i, k := range q.Keys {
			quoted[i] = chQuote(k)
		}
		where += " AND key IN (" + strings.Join(quoted, ",") + ")"
	}
	var points []MetricPoint
	if err = b.query(ctx, b.metricsTable, where, q.Limit, &points); err != nil {
		return MetricPage{}, err
	}
	return metricPageFrom(points, q), nil
}
func (b *clickhouseBackend) PurgeProject(ctx context.Context, projectID string) error {
	return b.purge(ctx, "project_id="+chQuote(projectID))
}
func (b *clickhouseBackend) PurgeRun(ctx context.Context, projectID, runID string) error {
	return b.purge(ctx, "project_id="+chQuote(projectID)+" AND run_id="+chQuote(runID))
}
func (b *clickhouseBackend) purge(ctx context.Context, where string) error {
	for _, table := range []string{b.logsTable, b.metricsTable} {
		sql := fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE %s", b.database, table, where)
		if _, err := b.http.request(ctx, http.MethodPost, "", url.Values{"query": {sql}}, nil, "text/plain"); err != nil {
			return err
		}
	}
	return nil
}
