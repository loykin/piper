// Package logstore preserves Piper's legacy repository interfaces while the
// public backend-neutral contracts live in pkg/statsstore.
package logstore

import (
	"context"
	"time"

	"github.com/loykin/piper/pkg/statsstore"
)

type Line = statsstore.LogLine
type Metric = statsstore.MetricPoint

// LogStore is the interface for appending and querying step logs.
type LogStore interface {
	// Append persists a batch of log lines.
	// ctx is respected for cancellation and timeout; callers should pass a meaningful deadline.
	Append(ctx context.Context, lines []*Line) error

	// Query returns log lines for a step.
	// If afterID > 0, only lines with ID > afterID are returned (for incremental polling).
	Query(projectID, runID, stepName string, afterID int64) ([]*Line, error)
}

type MetricStore interface {
	AppendMetrics(ctx context.Context, metrics []*Metric) error
	QueryMetrics(projectID, runID, stepName string) ([]*Metric, error)
}

// LogPageStore is the bounded query extension implemented by bundled stores.
// Legacy injected stores may omit it; callers retain a compatibility path.
type LogPageStore interface {
	QueryLogPage(ctx context.Context, query statsstore.LogQuery) (statsstore.LogPage, error)
}

type MetricPageStore interface {
	QueryMetricPage(ctx context.Context, query statsstore.MetricQuery) (statsstore.MetricPage, error)
}

// LogRetention deletes log rows solely by their own timestamp. Run deletion,
// RunTTL, and schedule max_runs must never call it.
type LogRetention interface {
	SweepLogs(ctx context.Context, before time.Time, limit int) (int64, error)
}

// MetricRetention is the metric counterpart to LogRetention.
type MetricRetention interface {
	SweepMetrics(ctx context.Context, before time.Time, limit int) (int64, error)
}
