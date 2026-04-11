package piper

// Internal package test — direct access to queue and taskID helpers

import (
	"database/sql"
	"testing"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/store"
	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(db)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(); _ = db.Close() })
	return st
}

func makePipeline(steps ...pipeline.Step) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "test"},
		Spec:     pipeline.Spec{Steps: steps},
	}
}

func step(name string, deps ...string) pipeline.Step {
	return pipeline.Step{Name: name, DependsOn: deps}
}

// ─── taskID helpers ───────────────────────────────────────────────────────────

func TestMakeTaskID(t *testing.T) {
	id := makeTaskID("run-123", "train")
	if id != "run-123:train" {
		t.Errorf("got %q", id)
	}
}

func TestSplitTaskID_valid(t *testing.T) {
	runID, stepName, err := splitTaskID("run-123:train")
	if err != nil {
		t.Fatal(err)
	}
	if runID != "run-123" || stepName != "train" {
		t.Errorf("got runID=%q stepName=%q", runID, stepName)
	}
}

func TestSplitTaskID_stepWithHyphen(t *testing.T) {
	// Parses correctly even when stepName contains hyphens
	runID, stepName, err := splitTaskID("run-20260408-123456.000:data-prep")
	if err != nil {
		t.Fatal(err)
	}
	if runID != "run-20260408-123456.000" {
		t.Errorf("runID: got %q", runID)
	}
	if stepName != "data-prep" {
		t.Errorf("stepName: got %q", stepName)
	}
}

func TestSplitTaskID_invalid(t *testing.T) {
	_, _, err := splitTaskID("no-colon-here")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── queue ────────────────────────────────────────────────────────────────────

func TestQueue_addAndNext(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)

	pl := makePipeline(step("a"), step("b", "a"))
	dag, _ := pipeline.BuildDAG(pl)
	q.add(pl, dag, "run-1", "", t.TempDir(), proto.BuiltinVars{}, nil)

	// Only a is ready (b depends on a)
	task := q.next("")
	if task == nil {
		t.Fatal("expected task")
	}
	if task.StepName != "a" {
		t.Errorf("want a, got %s", task.StepName)
	}

	// a is now running, so no more tasks available
	task2 := q.next("")
	if task2 != nil {
		t.Errorf("expected no more tasks, got %s", task2.StepName)
	}
}

func TestQueue_completePromotesDownstream(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)

	pl := makePipeline(step("a"), step("b", "a"))
	dag, _ := pipeline.BuildDAG(pl)
	_ = st.CreateRun(&store.Run{ID: "run-1", PipelineName: "test", Status: "running", StartedAt: time.Now()})
	_ = st.UpsertStep(&store.Step{RunID: "run-1", StepName: "a", Status: "pending"})
	_ = st.UpsertStep(&store.Step{RunID: "run-1", StepName: "b", Status: "pending"})

	q.add(pl, dag, "run-1", "", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.next("")
	if task == nil || task.StepName != "a" {
		t.Fatal("expected task a")
	}

	now := time.Now()
	err := q.complete(task.ID, "done", "", now, now, 1)
	if err != nil {
		t.Fatal(err)
	}

	// b is now ready, so next returns it
	task2 := q.next("")
	if task2 == nil {
		t.Fatal("expected task b after a completes")
	}
	if task2.StepName != "b" {
		t.Errorf("want b, got %s", task2.StepName)
	}
}

func TestQueue_failedSkipsDownstream(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)

	pl := makePipeline(step("a"), step("b", "a"), step("c", "b"))
	dag, _ := pipeline.BuildDAG(pl)
	_ = st.CreateRun(&store.Run{ID: "run-2", PipelineName: "test", Status: "running", StartedAt: time.Now()})
	for _, s := range pl.Spec.Steps {
		_ = st.UpsertStep(&store.Step{RunID: "run-2", StepName: s.Name, Status: "pending"})
	}

	q.add(pl, dag, "run-2", "", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.next("") // a
	if task == nil {
		t.Fatal("expected task a")
	}
	now := time.Now()
	_ = q.complete(task.ID, "failed", "some error", now, now, 1)

	// b and c are skipped, so next returns nil
	task2 := q.next("")
	if task2 != nil {
		t.Errorf("expected no more tasks after fail, got %s", task2.StepName)
	}

	// Verify that b and c are stored as skipped in the DB
	steps, _ := st.ListSteps("run-2")
	byName := make(map[string]string)
	for _, s := range steps {
		byName[s.StepName] = s.Status
	}
	if byName["b"] != "skipped" {
		t.Errorf("b: want skipped, got %s", byName["b"])
	}
	if byName["c"] != "skipped" {
		t.Errorf("c: want skipped, got %s", byName["c"])
	}
}

func TestQueue_completeAllDone_runsCompleted(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)

	pl := makePipeline(step("a"))
	dag, _ := pipeline.BuildDAG(pl)
	_ = st.CreateRun(&store.Run{ID: "run-3", PipelineName: "test", Status: "running", StartedAt: time.Now()})
	_ = st.UpsertStep(&store.Step{RunID: "run-3", StepName: "a", Status: "pending"})

	q.add(pl, dag, "run-3", "", t.TempDir(), proto.BuiltinVars{}, nil)

	task := q.next("")
	if task == nil {
		t.Fatal("expected task a")
	}
	now := time.Now()
	_ = q.complete(task.ID, "done", "", now, now, 1)

	run, _ := st.GetRun("run-3")
	if run.Status != "success" {
		t.Errorf("run status: want success, got %s", run.Status)
	}
	if run.EndedAt == nil {
		t.Error("ended_at should be set")
	}
}

func TestQueue_labelFiltering(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)

	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "test"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{
			{Name: "cpu-step", Runner: pipeline.RunnerSelector{Label: "cpu"}},
			{Name: "gpu-step", Runner: pipeline.RunnerSelector{Label: "gpu"}},
		}},
	}
	dag, _ := pipeline.BuildDAG(pl)
	q.add(pl, dag, "run-4", "", t.TempDir(), proto.BuiltinVars{}, nil)

	// Request for gpu worker only
	task := q.next("gpu")
	if task == nil {
		t.Fatal("expected gpu task")
	}
	if task.StepName != "gpu-step" {
		t.Errorf("want gpu-step, got %s", task.StepName)
	}

	// Request for cpu worker
	task2 := q.next("cpu")
	if task2 == nil {
		t.Fatal("expected cpu task")
	}
	if task2.StepName != "cpu-step" {
		t.Errorf("want cpu-step, got %s", task2.StepName)
	}
}

func TestQueue_completeUnknownTask(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)
	now := time.Now()
	err := q.complete("no-such-run:step", "done", "", now, now, 1)
	if err == nil {
		t.Fatal("expected error for unknown run")
	}
}

func TestQueue_next_empty(t *testing.T) {
	st := openTestStore(t)
	q := newQueue(st)
	task := q.next("")
	if task != nil {
		t.Error("expected nil for empty queue")
	}
}
