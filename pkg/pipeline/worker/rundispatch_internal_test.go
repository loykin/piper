package pipelineworker

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	"github.com/loykin/piper/pkg/pipeline/worker/scheduler"
)

const rundispatchTestPipelineYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: rundispatch-wiring-test
spec:
  steps:
    - name: a
      run:
        command: ["true"]
`

// swapInFakeDriver replaces w's real driver with a fake one that succeeds
// instantly, and rebuilds w.registry around it — keeping every other piece
// of Worker.New()'s real wiring intact (w.cfg, w.client, w.requestOutbox,
// and critically the real w.buildExecSpec method, which reads only w.cfg/
// w.client/w.gitEnv() and never w.driver — see buildExecSpec's body).
// This is what lets this test exercise the real pipeline.run_dispatch
// wiring without depending on the test binary behaving like `piper agent
// exec` when exec'd as a real subprocess (it doesn't, and hangs) — that
// real-subprocess behavior is already covered by the existing e2e suite.
func swapInFakeDriver(w *Worker, driver pdriver.Driver) {
	w.driver = driver
	w.registry = scheduler.NewRegistry(scheduler.RegistryOptions{
		Driver:        driver,
		BuildExecSpec: w.buildExecSpec,
		BuildReporter: func(projectID, runID string) scheduler.StepReporter {
			return scheduler.NewOutboxReporter(w.requestOutbox, projectID, runID)
		},
		MaxAttempts: 1,
		WorkerID:    w.cfg.Agent.ID,
	})
}

// TestWorkerRunDispatchWiringDrivesRunToCompletion exercises the real
// Worker.New()-constructed wiring for pipeline.run_dispatch — buildExecSpec,
// the RequestOutbox-backed reporter, and Registry/RunScheduler working
// together as actually wired in New() — with a synthetic RunDispatch. This
// is distinct from the scheduler package's own fake-driver unit tests,
// which only prove the DAG engine itself is correct in isolation; this one
// proves Worker.New() actually wires it all together correctly.
func TestWorkerRunDispatchWiringDrivesRunToCompletion(t *testing.T) {
	w, err := New(Config{
		Agent:   AgentConfig{MasterURL: "http://127.0.0.1:0", ID: "wiring-test-worker", Concurrency: 4},
		Store:   StoreConfig{OutputDir: t.TempDir()},
		Runtime: RuntimeBaremetal,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.registry == nil {
		t.Fatal("Worker.New() did not construct a scheduler.Registry")
	}

	swapInFakeDriver(w, &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			return pdriver.Handle{RuntimeKey: "a"}, nil
		},
		waitFn: func(context.Context, pdriver.Handle) (pdriver.Exit, error) {
			return pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}}, nil
		},
	})

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-wiring-1", PipelineYAML: rundispatchTestPipelineYAML}
	if err := w.registry.StartRun(dispatch); err != nil {
		t.Fatalf("registry.StartRun: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w.registry.Len() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run never reached a terminal state (Registry still tracking it after 5s) — wiring likely stuck")
}

// TestWorkerRunDispatchStartRunIsIdempotentThroughRealWorker confirms the
// idempotency guarantee (see Registry.StartRun's own doc comment, and the
// Phase 2/3 design's master-restart at-least-once resend) holds through the
// real Worker wiring, not just the scheduler package's own isolated test.
func TestWorkerRunDispatchStartRunIsIdempotentThroughRealWorker(t *testing.T) {
	w, err := New(Config{
		Agent:   AgentConfig{MasterURL: "http://127.0.0.1:0", ID: "wiring-test-worker-2", Concurrency: 4},
		Store:   StoreConfig{OutputDir: t.TempDir()},
		Runtime: RuntimeBaremetal,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	started := make(chan struct{}, 8)
	swapInFakeDriver(w, &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			started <- struct{}{}
			return pdriver.Handle{RuntimeKey: "a"}, nil
		},
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			<-ctx.Done() // block: this test only cares about start-count, not completion
			return pdriver.Exit{}, ctx.Err()
		},
	})

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-wiring-2", PipelineYAML: rundispatchTestPipelineYAML}
	if err := w.registry.StartRun(dispatch); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("step never started")
	}
	if err := w.registry.StartRun(dispatch); err != nil {
		t.Fatalf("duplicate StartRun: %v", err)
	}
	select {
	case <-started:
		t.Fatal("duplicate StartRun started the step a second time")
	case <-time.After(200 * time.Millisecond):
	}
	if w.registry.Len() != 1 {
		t.Fatalf("Registry.Len() = %d after duplicate StartRun, want 1", w.registry.Len())
	}
}
