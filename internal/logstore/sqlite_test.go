package logstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/statsstore"
)

func openTestStore(t *testing.T) *logstore.SQLiteLogStore {
	t.Helper()
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	return repos.Log.(*logstore.SQLiteLogStore)
}

func TestSQLiteLogStore_AppendAndQuery(t *testing.T) {
	ls := openTestStore(t)

	lines := []*logstore.Line{
		{ProjectID: "project-a", RunID: "r1", StepName: "train", Ts: time.Now(), Stream: "stdout", Line: "epoch 1"},
		{ProjectID: "project-a", RunID: "r1", StepName: "train", Ts: time.Now(), Stream: "stdout", Line: "epoch 2"},
	}
	if err := ls.Append(context.Background(), lines); err != nil {
		t.Fatal(err)
	}

	got, err := ls.Query("project-a", "r1", "train", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0].Line != "epoch 1" || got[1].Line != "epoch 2" {
		t.Errorf("unexpected lines: %v", got)
	}
}

func TestSQLiteLogStore_QueryAfterID(t *testing.T) {
	ls := openTestStore(t)

	for i := 0; i < 5; i++ {
		_ = ls.Append(context.Background(), []*logstore.Line{
			{ProjectID: "project-a", RunID: "r1", StepName: "s1", Ts: time.Now(), Stream: "stdout", Line: "line"},
		})
	}

	all, _ := ls.Query("project-a", "r1", "s1", 0)
	if len(all) != 5 {
		t.Fatalf("expected 5, got %d", len(all))
	}

	tail, err := ls.Query("project-a", "r1", "s1", all[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 lines after id %d, got %d", all[2].ID, len(tail))
	}
}

func TestSQLiteLogStore_QueryLogPageIsBoundedAndTimeFiltered(t *testing.T) {
	ls := openTestStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 4; i++ {
		if err := ls.Append(context.Background(), []*logstore.Line{{ProjectID: "project-a", RunID: "r1", StepName: "s1", Ts: base.Add(time.Duration(i) * time.Minute), Stream: "stdout", Line: "line"}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := ls.QueryLogPage(context.Background(), statsstore.LogQuery{ProjectID: "project-a", RunID: "r1", StepName: "s1", Since: base.Add(time.Minute), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Lines) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := ls.QueryLogPage(context.Background(), statsstore.LogQuery{ProjectID: "project-a", RunID: "r1", StepName: "s1", Cursor: first.NextCursor, Since: base.Add(time.Minute), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Lines) != 1 || second.NextCursor != "" || second.Lines[0].ID <= first.Lines[1].ID {
		t.Fatalf("second page = %+v", second)
	}
}

func TestSQLiteLogStore_EmptyAppend(t *testing.T) {
	ls := openTestStore(t)
	if err := ls.Append(context.Background(), nil); err != nil {
		t.Error("empty append should not fail")
	}
}

func TestSQLiteLogStore_QueryDifferentSteps(t *testing.T) {
	ls := openTestStore(t)

	_ = ls.Append(context.Background(), []*logstore.Line{
		{ProjectID: "project-a", RunID: "r1", StepName: "step-a", Ts: time.Now(), Stream: "stdout", Line: "a"},
		{ProjectID: "project-a", RunID: "r1", StepName: "step-b", Ts: time.Now(), Stream: "stdout", Line: "b"},
	})

	gotA, _ := ls.Query("project-a", "r1", "step-a", 0)
	gotB, _ := ls.Query("project-a", "r1", "step-b", 0)
	if len(gotA) != 1 || gotA[0].Line != "a" {
		t.Errorf("step-a: expected [a], got %v", gotA)
	}
	if len(gotB) != 1 || gotB[0].Line != "b" {
		t.Errorf("step-b: expected [b], got %v", gotB)
	}
}

func TestSQLiteLogStore_RedactsSecrets(t *testing.T) {
	ls := openTestStore(t)

	if err := ls.Append(context.Background(), []*logstore.Line{
		{ProjectID: "project-a", RunID: "r1", StepName: "s1", Ts: time.Now(), Stream: "stdout", Line: "token=supersecret"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ls.Query("project-a", "r1", "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Line != "token=[REDACTED]" {
		t.Fatalf("line = %q, want token=[REDACTED]", got[0].Line)
	}
}

func TestSQLiteLogStore_EventIDsAreStableAndDeduplicateRetries(t *testing.T) {
	ls := openTestStore(t)
	line := &logstore.Line{ProjectID: "project-a", RunID: "r1", StepName: "s1", Ts: time.Now(), Stream: "stdout", Line: "once"}
	if err := ls.Append(context.Background(), []*logstore.Line{line}); err != nil {
		t.Fatal(err)
	}
	if line.EventID == "" {
		t.Fatal("append did not assign an event ID")
	}
	if err := ls.Append(context.Background(), []*logstore.Line{line}); err != nil {
		t.Fatal(err)
	}
	got, err := ls.Query("project-a", "r1", "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EventID != line.EventID {
		t.Fatalf("deduplicated logs = %#v", got)
	}

	metric := &logstore.Metric{ProjectID: "project-a", RunID: "r1", StepName: "s1", Key: "loss", Value: 1, Ts: time.Now()}
	if err := ls.AppendMetrics(context.Background(), []*logstore.Metric{metric}); err != nil {
		t.Fatal(err)
	}
	if metric.EventID == "" {
		t.Fatal("metric append did not assign an event ID")
	}
	if err := ls.AppendMetrics(context.Background(), []*logstore.Metric{metric}); err != nil {
		t.Fatal(err)
	}
	metrics, err := ls.QueryMetrics("project-a", "r1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].EventID != metric.EventID {
		t.Fatalf("deduplicated metrics = %#v", metrics)
	}
}

func TestSQLiteLogStore_QueryMetricPageFiltersKeysAndPaginates(t *testing.T) {
	ls := openTestStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i, key := range []string{"loss", "accuracy", "loss"} {
		metric := &logstore.Metric{ProjectID: "project-a", RunID: "r1", StepName: "train", Key: key, Value: float64(i), Ts: base.Add(time.Duration(i) * time.Minute)}
		if err := ls.AppendMetrics(context.Background(), []*logstore.Metric{metric}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := ls.QueryMetricPage(context.Background(), statsstore.MetricQuery{ProjectID: "project-a", RunID: "r1", StepName: "train", Keys: []string{"loss"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Points) != 1 || first.Points[0].Key != "loss" || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := ls.QueryMetricPage(context.Background(), statsstore.MetricQuery{ProjectID: "project-a", RunID: "r1", StepName: "train", Keys: []string{"loss"}, Cursor: first.NextCursor, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Points) != 1 || second.Points[0].ID <= first.Points[0].ID || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestSQLiteLogStore_PurgeProjectRemovesBothStatsKinds(t *testing.T) {
	ls := openTestStore(t)
	ctx := context.Background()
	if err := ls.Append(ctx, []*logstore.Line{{ProjectID: "project-a", RunID: "r1", StepName: "s1", Ts: time.Now(), Stream: "stdout", Line: "log"}}); err != nil {
		t.Fatal(err)
	}
	if err := ls.AppendMetrics(ctx, []*logstore.Metric{{ProjectID: "project-a", RunID: "r1", StepName: "s1", Key: "loss", Value: 1, Ts: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if err := ls.PurgeProject(ctx, "project-a"); err != nil {
		t.Fatal(err)
	}
	logs, logErr := ls.Query("project-a", "r1", "s1", 0)
	metrics, metricErr := ls.QueryMetrics("project-a", "r1", "s1")
	if logErr != nil || metricErr != nil || len(logs) != 0 || len(metrics) != 0 {
		t.Fatalf("logs=%+v metrics=%+v logErr=%v metricErr=%v", logs, metrics, logErr, metricErr)
	}
}

func TestSQLiteLogStore_SweepRetentionIsTimestampBasedAndBounded(t *testing.T) {
	ls := openTestStore(t)
	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC()
	if err := ls.Append(context.Background(), []*logstore.Line{
		{ProjectID: "project-a", RunID: "deleted-run", StepName: "s1", Ts: old, Stream: "stdout", Line: "old-1"},
		{ProjectID: "project-a", RunID: "deleted-run", StepName: "s1", Ts: old.Add(time.Second), Stream: "stdout", Line: "old-2"},
		{ProjectID: "project-a", RunID: "live-run", StepName: "s1", Ts: recent, Stream: "stdout", Line: "recent"},
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := ls.SweepLogs(context.Background(), time.Now().UTC().Add(-24*time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	remaining, err := ls.Query("project-a", "deleted-run", "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Line != "old-2" {
		t.Fatalf("unexpected remaining old logs: %#v", remaining)
	}
	recentRows, err := ls.Query("project-a", "live-run", "s1", 0)
	if err != nil || len(recentRows) != 1 {
		t.Fatalf("recent logs changed: rows=%#v err=%v", recentRows, err)
	}
}
