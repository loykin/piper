// Package logstore defines the LogStore interface for persisting and querying
// pipeline step logs. The default implementation uses SQLite, but the interface
// can be satisfied by any backend (Loki, S3, etc.).
package logstore

import "time"

// Line is a single log line emitted by a pipeline step.
type Line struct {
	ID        int64     `json:"id"`
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id"`
	StepName  string    `json:"step_name"`
	Ts        time.Time `json:"ts"`
	Stream    string    `json:"stream"` // stdout | stderr
	Line      string    `json:"line"`
}

type Metric struct {
	ID        int64     `json:"id"`
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id"`
	StepName  string    `json:"step_name"`
	Key       string    `json:"key"`
	Value     float64   `json:"value"`
	Ts        time.Time `json:"ts"`
}

// LogStore is the interface for appending and querying step logs.
type LogStore interface {
	// Append persists a batch of log lines.
	Append(lines []*Line) error

	// Query returns log lines for a step.
	// If afterID > 0, only lines with ID > afterID are returned (for incremental polling).
	Query(projectID, runID, stepName string, afterID int64) ([]*Line, error)
}

type MetricStore interface {
	AppendMetrics(metrics []*Metric) error
	QueryMetrics(projectID, runID, stepName string) ([]*Metric, error)
}
