package store

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ─── Run ──────────────────────────────────────────────────────────────────────

func TestCreateAndGetRun(t *testing.T) {
	st := openTestStore(t)
	run := &Run{
		ID:           "run-1",
		OwnerID:      "alice",
		PipelineName: "train",
		Status:       "running",
		StartedAt:    time.Now().Truncate(time.Second),
		PipelineYAML: "yaml: true",
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != "alice" {
		t.Errorf("owner_id: want alice, got %q", got.OwnerID)
	}
	if got.PipelineName != "train" {
		t.Errorf("pipeline_name mismatch")
	}
	if got.Status != "running" {
		t.Errorf("status mismatch")
	}
}

func TestGetRun_notFound(t *testing.T) {
	st := openTestStore(t)
	got, err := st.GetRun("no-such-run")
	if err == nil && got != nil {
		t.Fatal("expected not found")
	}
}

func TestUpdateRunStatus(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "p", Status: "running", StartedAt: time.Now()})

	now := time.Now()
	if err := st.UpdateRunStatus("r1", "success", &now); err != nil {
		t.Fatal(err)
	}
	run, _ := st.GetRun("r1")
	if run.Status != "success" {
		t.Errorf("want success, got %s", run.Status)
	}
	if run.EndedAt == nil {
		t.Error("ended_at should be set")
	}
}

func TestListRuns_filter(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	_ = st.CreateRun(&Run{ID: "r1", OwnerID: "alice", PipelineName: "train", Status: "success", StartedAt: now})
	_ = st.CreateRun(&Run{ID: "r2", OwnerID: "bob", PipelineName: "eval", Status: "running", StartedAt: now})
	_ = st.CreateRun(&Run{ID: "r3", OwnerID: "alice", PipelineName: "eval", Status: "running", StartedAt: now})

	runs, err := st.ListRuns(RunFilter{OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("want 2, got %d", len(runs))
	}

	runs, _ = st.ListRuns(RunFilter{PipelineName: "eval"})
	if len(runs) != 2 {
		t.Errorf("want 2, got %d", len(runs))
	}

	runs, _ = st.ListRuns(RunFilter{OwnerID: "alice", PipelineName: "eval"})
	if len(runs) != 1 {
		t.Errorf("want 1, got %d", len(runs))
	}
}

// ─── Step ─────────────────────────────────────────────────────────────────────

func TestUpsertAndListSteps(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "p", Status: "running", StartedAt: time.Now()})

	step := &Step{RunID: "r1", StepName: "train", Status: "running"}
	if err := st.UpsertStep(step); err != nil {
		t.Fatal(err)
	}

	steps, err := st.ListSteps("r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].StepName != "train" {
		t.Errorf("unexpected steps: %v", steps)
	}

	// upsert update
	now := time.Now()
	step.Status = "done"
	step.EndedAt = &now
	step.Attempts = 1
	_ = st.UpsertStep(step)

	steps, _ = st.ListSteps("r1")
	if steps[0].Status != "done" {
		t.Errorf("want done, got %s", steps[0].Status)
	}
}

// ─── Log ──────────────────────────────────────────────────────────────────────

func TestAppendAndGetLogs(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "p", Status: "running", StartedAt: time.Now()})

	lines := []*LogLine{
		{RunID: "r1", StepName: "train", Ts: time.Now(), Stream: "stdout", Line: "hello"},
		{RunID: "r1", StepName: "train", Ts: time.Now(), Stream: "stdout", Line: "world"},
	}
	if err := st.AppendLogs(lines); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetLogs("r1", "train", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 logs, got %d", len(got))
	}
	if got[0].Line != "hello" || got[1].Line != "world" {
		t.Errorf("unexpected log lines")
	}
}

func TestGetLogs_afterID(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "p", Status: "running", StartedAt: time.Now()})

	for _, line := range []string{"a", "b", "c"} {
		_ = st.AppendLog(&LogLine{RunID: "r1", StepName: "s", Ts: time.Now(), Stream: "stdout", Line: line})
	}

	all, _ := st.GetLogs("r1", "s", 0)
	if len(all) != 3 {
		t.Fatalf("want 3, got %d", len(all))
	}

	// afterID = first log's ID → should return 2 remaining
	partial, _ := st.GetLogs("r1", "s", all[0].ID)
	if len(partial) != 2 {
		t.Errorf("want 2, got %d", len(partial))
	}
	if partial[0].Line != "b" {
		t.Errorf("want b, got %s", partial[0].Line)
	}
}

func TestAppendLogs_empty(t *testing.T) {
	st := openTestStore(t)
	if err := st.AppendLogs(nil); err != nil {
		t.Fatal(err)
	}
}

// ─── Store constructors ───────────────────────────────────────────────────────

func TestNew_externalDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// Close on externally managed DB should not close the db
	_ = st.Close()
	if err := db.Ping(); err != nil {
		t.Errorf("external db should still be open after store.Close: %v", err)
	}
	_ = db.Close()
}

func TestNewWithDSN(t *testing.T) {
	st, err := NewWithDSN("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	_ = st.DB() // DB() accessor
}

func TestStore_DB(t *testing.T) {
	st := openTestStore(t)
	if st.DB() == nil {
		t.Error("DB() should return non-nil")
	}
}

// ─── ListRuns no filter ───────────────────────────────────────────────────────

func TestListRuns_noFilter(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "a", Status: "success", StartedAt: now})
	_ = st.CreateRun(&Run{ID: "r2", PipelineName: "b", Status: "running", StartedAt: now})

	runs, err := st.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("want 2, got %d", len(runs))
	}
}

// ─── Step with error field ────────────────────────────────────────────────────

func TestUpsertStep_withError(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "p", Status: "running", StartedAt: time.Now()})

	now := time.Now()
	step := &Step{
		RunID:     "r1",
		StepName:  "fail-step",
		Status:    "failed",
		StartedAt: &now,
		EndedAt:   &now,
		Error:     "something went wrong",
		Attempts:  3,
	}
	if err := st.UpsertStep(step); err != nil {
		t.Fatal(err)
	}

	steps, _ := st.ListSteps("r1")
	if len(steps) != 1 {
		t.Fatal("expected 1 step")
	}
	if steps[0].Error != "something went wrong" {
		t.Errorf("error field: got %q", steps[0].Error)
	}
	if steps[0].Attempts != 3 {
		t.Errorf("attempts: got %d", steps[0].Attempts)
	}
}
