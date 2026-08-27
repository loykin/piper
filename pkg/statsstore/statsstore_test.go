package statsstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type stubBackend struct{}

func (stubBackend) AppendLogs(context.Context, []LogLine) error          { return nil }
func (stubBackend) QueryLogs(context.Context, LogQuery) (LogPage, error) { return LogPage{}, nil }
func (stubBackend) AppendMetrics(context.Context, []MetricPoint) error   { return nil }
func (stubBackend) QueryMetrics(context.Context, MetricQuery) (MetricPage, error) {
	return MetricPage{}, nil
}

func TestCursorRoundTripAndValidation(t *testing.T) {
	cursor := CursorFromID(42)
	if cursor == "" || cursor == "42" {
		t.Fatalf("cursor is not opaque: %q", cursor)
	}
	id, err := IDFromCursor(cursor)
	if err != nil || id != 42 {
		t.Fatalf("round trip = %d, %v", id, err)
	}
	if _, err := IDFromCursor("not-a-cursor"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor error = %v", err)
	}
}

func TestQueryCursorRejectsFilterReuse(t *testing.T) {
	query := LogQuery{ProjectID: "p", RunID: "r", StepName: "s", Search: "needle"}
	cursor := CursorForLogQuery(9, query)
	if id, err := LogIDFromCursor(cursor, query); err != nil || id != 9 {
		t.Fatalf("round trip=%d,%v", id, err)
	}
	changed := query
	changed.Search = "other"
	if _, err := LogIDFromCursor(cursor, changed); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("reused cursor error=%v", err)
	}
}

func TestStoreClosesSharedResourcesOnce(t *testing.T) {
	var calls atomic.Int32
	store := NewStore(nil, nil, Capabilities{}, func() error {
		calls.Add(1)
		return nil
	})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", calls.Load())
	}
}

func TestOpenUsesFallbackAndGatesExternalURLs(t *testing.T) {
	backend := stubBackend{}
	store, err := Open(Config{}, Fallback{Logs: backend, Metrics: backend, Capabilities: Capabilities{TimeRange: true}})
	if err != nil {
		t.Fatal(err)
	}
	if store.Logs == nil || store.Metrics == nil || !store.Capabilities.TimeRange {
		t.Fatalf("store = %+v", store)
	}
	if _, err := Open(Config{Logs: BackendConfig{URL: "elasticsearch://stats"}}, Fallback{Logs: backend, Metrics: backend}); err == nil {
		t.Fatal("external backend was accepted without durable spool")
	}
}
