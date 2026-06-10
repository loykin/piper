package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/store"
	"github.com/piper/piper/pkg/pipeline/run"
)

// seedRun inserts a run with the given experiment and returns its ID.
func seedRun(t *testing.T, repo run.Repository, id, experiment string) {
	t.Helper()
	err := repo.Create(context.Background(), &run.Run{
		ID:           id,
		PipelineName: "train",
		Experiment:   experiment,
		Status:       run.StatusSuccess,
		StartedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seedRun %q: %v", id, err)
	}
}

// seedMetric inserts a single metric row.
func seedMetric(t *testing.T, ms logstore.MetricStore, runID, step, key string, value float64) {
	t.Helper()
	err := ms.AppendMetrics([]*logstore.Metric{{
		RunID:    runID,
		StepName: step,
		Key:      key,
		Value:    value,
		Ts:       time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("seedMetric run=%q key=%q: %v", runID, key, err)
	}
}

func TestList_MetricSort_Desc(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	seedRun(t, repos.Run, "run-a", "sweep-1")
	seedRun(t, repos.Run, "run-b", "sweep-1")
	seedRun(t, repos.Run, "run-c", "sweep-1")
	seedMetric(t, repos.Metric, "run-a", "train", "accuracy", 0.80)
	seedMetric(t, repos.Metric, "run-b", "train", "accuracy", 0.95)
	seedMetric(t, repos.Metric, "run-c", "train", "accuracy", 0.72)

	got, err := repos.Run.List(context.Background(), run.RunFilter{
		Experiment:  "sweep-1",
		MetricStep:  "train",
		MetricKey:   "accuracy",
		MetricOrder: "desc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(got))
	}
	want := []string{"run-b", "run-a", "run-c"}
	for i, r := range got {
		if r.ID != want[i] {
			t.Errorf("position %d: got %q, want %q", i, r.ID, want[i])
		}
	}
}

func TestList_MetricSort_Asc(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	seedRun(t, repos.Run, "run-a", "sweep-2")
	seedRun(t, repos.Run, "run-b", "sweep-2")
	seedMetric(t, repos.Metric, "run-a", "train", "val_loss", 0.31)
	seedMetric(t, repos.Metric, "run-b", "train", "val_loss", 0.18)

	got, err := repos.Run.List(context.Background(), run.RunFilter{
		Experiment:  "sweep-2",
		MetricStep:  "train",
		MetricKey:   "val_loss",
		MetricOrder: "asc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(got))
	}
	if got[0].ID != "run-b" {
		t.Errorf("first run = %q, want run-b (lowest val_loss)", got[0].ID)
	}
}

func TestList_MetricSort_NullsLast(t *testing.T) {
	// run-c has no metric — should appear last even in desc order
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	seedRun(t, repos.Run, "run-a", "sweep-3")
	seedRun(t, repos.Run, "run-b", "sweep-3")
	seedRun(t, repos.Run, "run-c", "sweep-3") // no metric
	seedMetric(t, repos.Metric, "run-a", "train", "accuracy", 0.80)
	seedMetric(t, repos.Metric, "run-b", "train", "accuracy", 0.90)

	got, err := repos.Run.List(context.Background(), run.RunFilter{
		Experiment:  "sweep-3",
		MetricStep:  "train",
		MetricKey:   "accuracy",
		MetricOrder: "desc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(got))
	}
	// run-c (no metric) must be last
	if got[2].ID != "run-c" {
		t.Errorf("last run = %q, want run-c (null metric)", got[2].ID)
	}
}
