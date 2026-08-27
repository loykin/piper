package logstore

import (
	"context"
	"fmt"

	"github.com/loykin/piper/pkg/statsstore"
)

// Backend adapts the legacy repository interfaces to the public statsstore
// contracts while existing append producers migrate incrementally.
type Backend struct {
	logs    LogStore
	metrics MetricStore
}

func NewBackend(logs LogStore, metrics MetricStore) *Backend {
	return &Backend{logs: logs, metrics: metrics}
}

func (b *Backend) Capabilities() statsstore.Capabilities {
	return statsstore.Capabilities{TimeRange: true, MetricKeyFilter: true}
}

func (b *Backend) AppendLogs(ctx context.Context, lines []statsstore.LogLine) error {
	values := make([]*Line, len(lines))
	for i := range lines {
		values[i] = &lines[i]
	}
	return b.logs.Append(ctx, values)
}

func (b *Backend) QueryLogs(ctx context.Context, query statsstore.LogQuery) (statsstore.LogPage, error) {
	if pager, ok := b.logs.(LogPageStore); ok {
		return pager.QueryLogPage(ctx, query)
	}
	if query.Search != "" {
		return statsstore.LogPage{}, fmt.Errorf("legacy log store does not support full-text search")
	}
	afterID, err := statsstore.LogIDFromCursor(query.Cursor, query)
	if err != nil {
		return statsstore.LogPage{}, err
	}
	lines, err := b.logs.Query(query.ProjectID, query.RunID, query.StepName, afterID)
	if err != nil {
		return statsstore.LogPage{}, err
	}
	limit := statsstore.NormalizeLimit(query.Limit)
	page := statsstore.LogPage{Lines: make([]statsstore.LogLine, 0, min(len(lines), limit))}
	for _, line := range lines {
		if (!query.Since.IsZero() && line.Ts.Before(query.Since)) || (!query.Until.IsZero() && line.Ts.After(query.Until)) {
			continue
		}
		if len(page.Lines) == limit {
			page.NextCursor = statsstore.CursorForLogQuery(page.Lines[len(page.Lines)-1].ID, query)
			break
		}
		page.Lines = append(page.Lines, *line)
	}
	return page, nil
}

func (b *Backend) AppendMetrics(ctx context.Context, points []statsstore.MetricPoint) error {
	values := make([]*Metric, len(points))
	for i := range points {
		values[i] = &points[i]
	}
	return b.metrics.AppendMetrics(ctx, values)
}

func (b *Backend) QueryMetrics(ctx context.Context, query statsstore.MetricQuery) (statsstore.MetricPage, error) {
	if pager, ok := b.metrics.(MetricPageStore); ok {
		return pager.QueryMetricPage(ctx, query)
	}
	afterID, err := statsstore.MetricIDFromCursor(query.Cursor, query)
	if err != nil {
		return statsstore.MetricPage{}, err
	}
	points, err := b.metrics.QueryMetrics(query.ProjectID, query.RunID, query.StepName)
	if err != nil {
		return statsstore.MetricPage{}, err
	}
	keys := make(map[string]struct{}, len(query.Keys))
	for _, key := range query.Keys {
		keys[key] = struct{}{}
	}
	limit := statsstore.NormalizeLimit(query.Limit)
	page := statsstore.MetricPage{Points: make([]statsstore.MetricPoint, 0, min(len(points), limit))}
	for _, point := range points {
		if point.ID <= afterID || (!query.Since.IsZero() && point.Ts.Before(query.Since)) || (!query.Until.IsZero() && point.Ts.After(query.Until)) {
			continue
		}
		if len(keys) > 0 {
			if _, ok := keys[point.Key]; !ok {
				continue
			}
		}
		if len(page.Points) == limit {
			page.NextCursor = statsstore.CursorForMetricQuery(page.Points[len(page.Points)-1].ID, query)
			break
		}
		page.Points = append(page.Points, *point)
	}
	return page, nil
}

func (b *Backend) PurgeProject(ctx context.Context, projectID string) error {
	if purger, ok := b.logs.(statsstore.Purger); ok {
		return purger.PurgeProject(ctx, projectID)
	}
	if purger, ok := b.metrics.(statsstore.Purger); ok {
		return purger.PurgeProject(ctx, projectID)
	}
	return fmt.Errorf("statistics fallback does not support project purge")
}

func (b *Backend) PurgeRun(ctx context.Context, projectID, runID string) error {
	if purger, ok := b.logs.(statsstore.Purger); ok {
		return purger.PurgeRun(ctx, projectID, runID)
	}
	if purger, ok := b.metrics.(statsstore.Purger); ok {
		return purger.PurgeRun(ctx, projectID, runID)
	}
	return fmt.Errorf("statistics fallback does not support run purge")
}

var _ statsstore.LogBackend = (*Backend)(nil)
var _ statsstore.MetricBackend = (*Backend)(nil)
var _ statsstore.Purger = (*Backend)(nil)
