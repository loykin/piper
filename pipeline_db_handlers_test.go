package piper

import (
	"context"
	"net"
	"testing"
	"time"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/grpcagent"
	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// newTestReposForHandlers returns an in-memory-SQLite-backed Repos with a
// single project row, ready for run/step writes in these tests.
func newTestReposForHandlers(t *testing.T) *store.Repos {
	t.Helper()
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	now := time.Now().UTC()
	if err := repos.Project.Create(context.Background(), &project.Project{
		ID: "proj-1", Name: "proj-1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return repos
}

// testHandlerRig starts a real grpcagent.Server (with registerPipelineDBHandlers
// wired) on a loopback listener and connects a real grpcagent.Client to it —
// a full round trip through the exact code path production uses (Connect's
// WorkerMessage_Request case, its context.WithValue(agentIDContextKey, ...),
// Dispatcher.handleRequest), so these tests can't pass by accident the way a
// shortcut that hand-builds a context would.
type testHandlerRig struct {
	repos  *store.Repos
	client *grpcagent.Client
}

func newTestHandlerRig(t *testing.T, agentID string) *testHandlerRig {
	t.Helper()
	repos := newTestReposForHandlers(t)

	srv := grpcagent.NewServer(nil, nil)
	registerPipelineDBHandlers(srv, repos.Run, repos.Step)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := srv.GRPCServer()
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	client := grpcagent.NewClient(grpcagent.ClientConfig{
		MasterURL:      "http://" + lis.Addr().String(),
		AgentID:        agentID,
		Infrastructure: "baremetal",
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = client.Run(ctx) }()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if err := srv.WaitConnected(waitCtx, agentID); err != nil {
		t.Fatalf("agent %q never connected: %v", agentID, err)
	}

	return &testHandlerRig{repos: repos, client: client}
}

func TestRegisterPipelineDBHandlers_StepUpsertOverwritesWorkerID(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}

	var resp stepUpsertResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineStepUpsert, run.Step{
		ProjectID: "proj-1", RunID: "run-1", StepName: "train", Status: "running", Attempts: 1,
		WorkerID: "attacker-worker", // must be overwritten by the authenticated identity
	}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Applied {
		t.Fatal("step_upsert applied = false, want true")
	}

	steps, err := rig.repos.Step.List(context.Background(), "proj-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].WorkerID != "worker-1" {
		t.Fatalf("persisted steps = %#v, want exactly one step with worker_id=worker-1", steps)
	}
}

func TestRegisterPipelineDBHandlers_StepUpsertRejectsUnboundRun(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	// No run created/bound at all.
	var resp stepUpsertResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineStepUpsert, run.Step{
		ProjectID: "proj-1", RunID: "run-1", StepName: "train", Status: "running", Attempts: 1,
	}, &resp)
	if err == nil {
		t.Fatal("expected an error upserting a step for a run that was never bound to any worker, got nil")
	}
	steps, err := rig.repos.Step.List(context.Background(), "proj-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("persisted steps = %#v, want none", steps)
	}
}

func TestRegisterPipelineDBHandlers_StepUpsertRejectsWrongWorker(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-2")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}

	// worker-2 (connected, but not the bound worker) must not be able to
	// upsert a step belonging to worker-1's run.
	var resp stepUpsertResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineStepUpsert, run.Step{
		ProjectID: "proj-1", RunID: "run-1", StepName: "train", Status: "running", Attempts: 1,
	}, &resp)
	if err == nil {
		t.Fatal("expected an error upserting a step for a run bound to a different worker, got nil")
	}
	steps, err := rig.repos.Step.List(context.Background(), "proj-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("persisted steps = %#v, want none", steps)
	}
}

func TestRegisterPipelineDBHandlers_StepUpsertRejectsInvalidStatus(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}

	for _, badStatus := range []string{"bogus", "pending", "ready", ""} {
		var resp stepUpsertResponse
		err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineStepUpsert, run.Step{
			ProjectID: "proj-1", RunID: "run-1", StepName: "train", Status: badStatus, Attempts: 1,
		}, &resp)
		if err == nil {
			t.Fatalf("status %q: expected an error, got nil", badStatus)
		}
	}
	steps, err := rig.repos.Step.List(context.Background(), "proj-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("persisted steps = %#v, want none", steps)
	}
}

func TestRegisterPipelineDBHandlers_StepUpsertRejectsTerminalRun(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}
	// Finalize the run first (via the real handler, keeping this an
	// end-to-end reproduction rather than reaching into the DB directly).
	var finalizeResp runFinalizeResponse
	if err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineRunFinalize, runFinalizeRequest{
		ProjectID: "proj-1", ID: "run-1", Status: run.StatusSuccess,
	}, &finalizeResp); err != nil {
		t.Fatal(err)
	}

	// A step_upsert for a higher attempt must still be rejected — UpsertCAS's
	// own attempts/status guard only protects one step row and doesn't know
	// the run it belongs to is already finished.
	var resp stepUpsertResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineStepUpsert, run.Step{
		ProjectID: "proj-1", RunID: "run-1", StepName: "train", Status: "running", Attempts: 99,
	}, &resp)
	if err == nil {
		t.Fatal("expected an error upserting a step for an already-terminal run, got nil")
	}
	steps, err := rig.repos.Step.List(context.Background(), "proj-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("persisted steps = %#v, want none", steps)
	}
}

func TestRegisterPipelineDBHandlers_RunFinalizeRejectsWrongWorker(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-2")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}

	// worker-2 (connected, but not the bound worker) must not be able to
	// finalize a run bound to worker-1.
	var resp runFinalizeResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineRunFinalize, runFinalizeRequest{
		ProjectID: "proj-1", ID: "run-1", Status: run.StatusSuccess,
	}, &resp)
	if err == nil {
		t.Fatal("expected an error finalizing a run bound to a different worker, got nil")
	}
	got, err := rig.repos.Run.Get(context.Background(), "proj-1", "run-1")
	if err != nil || got == nil {
		t.Fatalf("Get: %v, got=%v", err, got)
	}
	if got.Status != run.StatusRunning {
		t.Errorf("status = %q, want unchanged %q", got.Status, run.StatusRunning)
	}
}

func TestRegisterPipelineDBHandlers_RunFinalizeAppliesForBoundWorker(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}

	var resp runFinalizeResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineRunFinalize, runFinalizeRequest{
		ProjectID: "proj-1", ID: "run-1", Status: run.StatusSuccess,
	}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Applied {
		t.Fatal("run_finalize applied = false, want true")
	}
	got, err := rig.repos.Run.Get(context.Background(), "proj-1", "run-1")
	if err != nil || got == nil {
		t.Fatalf("Get after finalize: %v, got=%v", err, got)
	}
	if got.Status != run.StatusSuccess {
		t.Errorf("status = %q, want %q", got.Status, run.StatusSuccess)
	}
}

func TestRegisterPipelineDBHandlers_RunFinalizeRejectsUnboundRun(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// Deliberately not bound to any worker — an empty worker_id must not be
	// treated as "anyone may finalize."

	var resp runFinalizeResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineRunFinalize, runFinalizeRequest{
		ProjectID: "proj-1", ID: "run-1", Status: run.StatusSuccess,
	}, &resp)
	if err == nil {
		t.Fatal("expected an error finalizing an unbound run, got nil")
	}
	got, err := rig.repos.Run.Get(context.Background(), "proj-1", "run-1")
	if err != nil || got == nil {
		t.Fatalf("Get: %v, got=%v", err, got)
	}
	if got.Status != run.StatusRunning {
		t.Errorf("status = %q, want unchanged %q", got.Status, run.StatusRunning)
	}
}

func TestRegisterPipelineDBHandlers_RunFinalizeRejectsInvalidStatus(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: "run-1", ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", "run-1", "worker-1"); err != nil {
		t.Fatal(err)
	}

	for _, badStatus := range []string{"running", "scheduled", "bogus", ""} {
		var resp runFinalizeResponse
		err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineRunFinalize, runFinalizeRequest{
			ProjectID: "proj-1", ID: "run-1", Status: badStatus,
		}, &resp)
		if err == nil {
			t.Fatalf("status %q: expected an error, got nil", badStatus)
		}
	}
	got, err := rig.repos.Run.Get(context.Background(), "proj-1", "run-1")
	if err != nil || got == nil {
		t.Fatalf("Get: %v, got=%v", err, got)
	}
	if got.Status != run.StatusRunning {
		t.Errorf("status = %q, want unchanged %q", got.Status, run.StatusRunning)
	}
}

func TestRegisterPipelineDBHandlers_WorkerRecoveryQueryScopesToCaller(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	mustCreateRunningRun(t, rig, "run-1", "worker-1")
	if err := rig.repos.Step.Upsert(context.Background(), &run.Step{
		ProjectID: "proj-1", RunID: "run-1", StepName: "mine", Status: "running", WorkerID: "worker-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := rig.repos.Step.Upsert(context.Background(), &run.Step{
		ProjectID: "proj-1", RunID: "run-1", StepName: "not-mine", Status: "running", WorkerID: "worker-2",
	}); err != nil {
		t.Fatal(err)
	}

	var resp WorkerRecoveryResponse
	err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineWorkerRecoveryQuery, workerRecoveryQueryRequest{}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 1 || resp.Runs[0].Run.ID != "run-1" {
		t.Fatalf("recovery query for worker-1 = %#v, want exactly its own run %q", resp.Runs, "run-1")
	}
	if steps := resp.Runs[0].Steps; len(steps) != 1 || steps[0].StepName != "mine" {
		t.Fatalf("recovery query steps = %#v, want exactly worker-1's own step %q", steps, "mine")
	}
}

// TestRegisterPipelineDBHandlers_WorkerRecoveryQueryGroupsByRunAndEnriches
// covers the enrichment pipeline_db_handlers.go's worker_recovery_query
// handler now performs: grouping non-terminal step rows by run and attaching
// each distinct run's own row (pipeline_yaml/cancel intent) — a restarting
// worker's scheduler needs the DAG, not just bare step rows, to resume.
func TestRegisterPipelineDBHandlers_WorkerRecoveryQueryGroupsByRunAndEnriches(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	mustCreateRunningRun(t, rig, "run-a", "worker-1")
	mustCreateRunningRun(t, rig, "run-b", "worker-1")
	for _, s := range []*run.Step{
		{ProjectID: "proj-1", RunID: "run-a", StepName: "step1", Status: "running", WorkerID: "worker-1"},
		{ProjectID: "proj-1", RunID: "run-a", StepName: "step2", Status: "running", WorkerID: "worker-1"},
		{ProjectID: "proj-1", RunID: "run-b", StepName: "step1", Status: "running", WorkerID: "worker-1"},
	} {
		if err := rig.repos.Step.Upsert(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if applied, err := rig.repos.Run.SetCancelRequested(context.Background(), "proj-1", "run-b"); err != nil || !applied {
		t.Fatalf("SetCancelRequested: applied=%v err=%v", applied, err)
	}

	var resp WorkerRecoveryResponse
	if err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineWorkerRecoveryQuery, workerRecoveryQueryRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("got %d recovered runs, want 2: %#v", len(resp.Runs), resp.Runs)
	}
	byID := make(map[string]RecoveredRun, len(resp.Runs))
	for _, rr := range resp.Runs {
		byID[rr.Run.ID] = rr
	}
	runA, ok := byID["run-a"]
	if !ok {
		t.Fatalf("run-a missing from response: %#v", resp.Runs)
	}
	if len(runA.Steps) != 2 {
		t.Errorf("run-a steps = %d, want 2: %#v", len(runA.Steps), runA.Steps)
	}
	if runA.Run.PipelineYAML == "" {
		t.Error("run-a's PipelineYAML is empty — enrichment didn't attach the run row's content")
	}
	if runA.CancelRequested {
		t.Error("run-a.CancelRequested = true, want false (no cancel was requested)")
	}
	runB, ok := byID["run-b"]
	if !ok {
		t.Fatalf("run-b missing from response: %#v", resp.Runs)
	}
	if !runB.CancelRequested {
		t.Error("run-b.CancelRequested = false, want true (SetCancelRequested was called)")
	}
}

// TestRegisterPipelineDBHandlers_WorkerRecoveryQuerySkipsTerminalRun covers
// the race guard between ListNonTerminalByWorker's read and the per-run
// Get: a run that finished between the two must not come back as
// "still needs recovery."
func TestRegisterPipelineDBHandlers_WorkerRecoveryQuerySkipsTerminalRun(t *testing.T) {
	rig := newTestHandlerRig(t, "worker-1")
	mustCreateRunningRun(t, rig, "run-done", "worker-1")
	if err := rig.repos.Step.Upsert(context.Background(), &run.Step{
		ProjectID: "proj-1", RunID: "run-done", StepName: "step1", Status: "running", WorkerID: "worker-1",
	}); err != nil {
		t.Fatal(err)
	}
	if applied, err := rig.repos.Run.FinalizeStatusCAS(context.Background(), "proj-1", "run-done", run.StatusSuccess, nil); err != nil || !applied {
		t.Fatalf("FinalizeStatusCAS: applied=%v err=%v", applied, err)
	}

	var resp WorkerRecoveryResponse
	if err := rig.client.SendRequest(context.Background(), iagent.MethodPipelineWorkerRecoveryQuery, workerRecoveryQueryRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 0 {
		t.Fatalf("recovery query returned a run that already finalized: %#v", resp.Runs)
	}
}

// mustCreateRunningRun creates a StatusRunning run row bound to workerID
// with non-empty PipelineYAML, in project "proj-1" (created by
// newTestReposForHandlers). Needed because the worker_recovery_query
// handler now joins the run row for enrichment and skips step rows whose
// run doesn't exist or is already terminal.
func mustCreateRunningRun(t *testing.T, rig *testHandlerRig, runID, workerID string) {
	t.Helper()
	if err := rig.repos.Run.Create(context.Background(), &run.Run{
		ID: runID, ProjectID: "proj-1", PipelineName: "p", Status: run.StatusRunning,
		StartedAt: time.Now().UTC(), PipelineYAML: "apiVersion: piper/v1\nkind: Pipeline\n",
	}); err != nil {
		t.Fatalf("create run %s: %v", runID, err)
	}
	if applied, err := rig.repos.Run.SetWorkerID(context.Background(), "proj-1", runID, workerID); err != nil || !applied {
		t.Fatalf("bind run %s to %s: applied=%v err=%v", runID, workerID, applied, err)
	}
}
