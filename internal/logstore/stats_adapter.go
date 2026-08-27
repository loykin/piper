package logstore

import (
	"context"

	"github.com/loykin/piper/internal/redact"
	"github.com/loykin/piper/pkg/statsstore"
)

// StatsAdapter keeps legacy runtime producers source-compatible while routing
// every append/query through the selected statsstore backend.
type StatsAdapter struct{ store *statsstore.Store }

func NewStatsAdapter(store *statsstore.Store) *StatsAdapter { return &StatsAdapter{store: store} }

func (a *StatsAdapter) Append(ctx context.Context, lines []*Line) error {
	values := make([]statsstore.LogLine, len(lines))
	for i, line := range lines {
		values[i] = *line
		values[i].Line = redact.String(values[i].Line)
	}
	return a.store.Logs.AppendLogs(ctx, values)
}
func (a *StatsAdapter) Query(projectID, runID, stepName string, afterID int64) ([]*Line, error) {
	page, err := a.store.Logs.QueryLogs(context.Background(), statsstore.LogQuery{ProjectID: projectID, RunID: runID, StepName: stepName, Cursor: statsstore.CursorFromID(afterID), Limit: statsstore.MaxPageLimit})
	if err != nil {
		return nil, err
	}
	values := make([]*Line, len(page.Lines))
	for i := range page.Lines {
		line := page.Lines[i]
		values[i] = &line
	}
	return values, nil
}
func (a *StatsAdapter) QueryLogPage(ctx context.Context, q statsstore.LogQuery) (statsstore.LogPage, error) {
	return a.store.Logs.QueryLogs(ctx, q)
}
func (a *StatsAdapter) AppendMetrics(ctx context.Context, points []*Metric) error {
	values := make([]statsstore.MetricPoint, len(points))
	for i, p := range points {
		values[i] = *p
		values[i].Key = redact.String(values[i].Key)
	}
	return a.store.Metrics.AppendMetrics(ctx, values)
}
func (a *StatsAdapter) QueryMetrics(projectID, runID, stepName string) ([]*Metric, error) {
	page, err := a.store.Metrics.QueryMetrics(context.Background(), statsstore.MetricQuery{ProjectID: projectID, RunID: runID, StepName: stepName, Limit: statsstore.MaxPageLimit})
	if err != nil {
		return nil, err
	}
	values := make([]*Metric, len(page.Points))
	for i := range page.Points {
		point := page.Points[i]
		values[i] = &point
	}
	return values, nil
}
func (a *StatsAdapter) QueryMetricPage(ctx context.Context, q statsstore.MetricQuery) (statsstore.MetricPage, error) {
	return a.store.Metrics.QueryMetrics(ctx, q)
}

var _ LogStore = (*StatsAdapter)(nil)
var _ MetricStore = (*StatsAdapter)(nil)
