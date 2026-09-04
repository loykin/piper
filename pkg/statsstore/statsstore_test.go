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

// TestOpenWrapsCredentialResolveFailureDistinctly is a regression test for
// AK: piper.New() needs to tell "the configured credential_ref doesn't
// resolve" apart from other Open failures (bad config shape, a reachable
// backend rejecting the connection) so it can degrade startup only for the
// former. errors.Is against ErrCredentialUnresolved is that signal.
func TestOpenWrapsCredentialResolveFailureDistinctly(t *testing.T) {
	backend := stubBackend{}
	resolveErr := errors.New("credential not found")
	_, err := Open(Config{
		SpoolDir: t.TempDir(),
		Logs:     BackendConfig{URL: "elasticsearch://stats", CredentialRef: "missing"},
		Resolve:  func(context.Context, string) (map[string]string, error) { return nil, resolveErr },
	}, Fallback{Logs: backend, Metrics: backend})
	if err == nil {
		t.Fatal("Open succeeded despite an unresolvable credential_ref")
	}
	if !errors.Is(err, ErrCredentialUnresolved) {
		t.Fatalf("err = %v, want it to wrap ErrCredentialUnresolved", err)
	}
	if !errors.Is(err, resolveErr) {
		t.Fatalf("err = %v, want it to still wrap the original resolve error", err)
	}
}

func TestNewDegradedStoreReportsFailureThroughHealth(t *testing.T) {
	backend := stubBackend{}
	store := NewDegradedStore(Fallback{Logs: backend, Metrics: backend, Capabilities: Capabilities{LogsBackend: "database"}}, "credential not found")
	if store.Logs == nil || store.Metrics == nil {
		t.Fatal("degraded store must still serve from the fallback backend")
	}
	health := store.Health()
	if health.Healthy {
		t.Fatal("Health().Healthy = true, want false")
	}
	if !health.Degraded || health.LastError != "credential not found" {
		t.Fatalf("Health() = %+v, want Degraded=true and the given LastError", health)
	}
}
