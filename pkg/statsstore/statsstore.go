// Package statsstore defines Piper's backend-neutral contracts for execution
// logs and metrics. A Member owns the statistics produced by its runs; Home
// routes queries to that Member and does not replicate the underlying data.
package statsstore

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrBackendUnavailable = errors.New("statistics backend unavailable")

const (
	DefaultPageLimit = 1000
	MaxPageLimit     = 5000
)

func NormalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}

type LogLine struct {
	ID        int64     `json:"id"`
	EventID   string    `json:"event_id,omitempty"`
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id"`
	StepName  string    `json:"step_name"`
	Ts        time.Time `json:"ts"`
	Stream    string    `json:"stream"`
	Line      string    `json:"line"`
}

type MetricPoint struct {
	ID        int64     `json:"id"`
	EventID   string    `json:"event_id,omitempty"`
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id"`
	StepName  string    `json:"step_name"`
	Key       string    `json:"key"`
	Value     float64   `json:"value"`
	Ts        time.Time `json:"ts"`
}

type LogQuery struct {
	ProjectID string
	RunID     string
	StepName  string
	Cursor    string
	Since     time.Time
	Until     time.Time
	Search    string
	Limit     int
}

type MetricQuery struct {
	ProjectID string
	RunID     string
	StepName  string
	Keys      []string
	Cursor    string
	Since     time.Time
	Until     time.Time
	Limit     int
}

type LogPage struct {
	Lines      []LogLine `json:"lines"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type MetricPage struct {
	Points     []MetricPoint `json:"points"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type LogBackend interface {
	AppendLogs(ctx context.Context, lines []LogLine) error
	QueryLogs(ctx context.Context, query LogQuery) (LogPage, error)
}

type MetricBackend interface {
	AppendMetrics(ctx context.Context, points []MetricPoint) error
	QueryMetrics(ctx context.Context, query MetricQuery) (MetricPage, error)
}

// Purger is intentionally separate from normal append/query. Run retention
// must never call it; explicit project deletion or compliance workflows may.
type Purger interface {
	PurgeProject(ctx context.Context, projectID string) error
	PurgeRun(ctx context.Context, projectID, runID string) error
}

type Capabilities struct {
	FullTextSearch  bool   `json:"full_text_search"`
	TimeRange       bool   `json:"time_range"`
	MetricKeyFilter bool   `json:"metric_key_filter"`
	Healthy         bool   `json:"healthy"`
	Degraded        bool   `json:"degraded"`
	PendingBytes    int64  `json:"pending_bytes"`
	LastError       string `json:"last_error,omitempty"`
	// LogsBackend and MetricsBackend name which backend is actually serving
	// each half of stats right now — "database" for the built-in SQL
	// fallback, or the external backend's scheme ("elasticsearch",
	// "clickhouse", "influxdb") when stats.logs.url/stats.metrics.url is
	// configured. Surfaced so an admin can see which backend is live
	// without reading piper.yaml on the server.
	LogsBackend    string `json:"logs_backend"`
	MetricsBackend string `json:"metrics_backend"`
}

type Health struct {
	Healthy      bool   `json:"healthy"`
	Degraded     bool   `json:"degraded"`
	PendingBytes int64  `json:"pending_bytes"`
	LastError    string `json:"last_error,omitempty"`
}

// Store owns the lifecycle shared by its log and metric backends. closeFn is
// invoked at most once even when both interfaces share one physical client.
type Store struct {
	Logs         LogBackend
	Metrics      MetricBackend
	Capabilities Capabilities

	closeOnce sync.Once
	closeFn   func() error
	closeErr  error
	healthFn  func() Health
}

func NewStore(logs LogBackend, metrics MetricBackend, capabilities Capabilities, closeFn func() error) *Store {
	return &Store{Logs: logs, Metrics: metrics, Capabilities: capabilities, closeFn: closeFn}
}

func (s *Store) Health() Health {
	if s == nil || s.healthFn == nil {
		return Health{Healthy: true}
	}
	return s.healthFn()
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.closeFn != nil {
			s.closeErr = s.closeFn()
		}
	})
	return s.closeErr
}
