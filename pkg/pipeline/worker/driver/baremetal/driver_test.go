package baremetaldriver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	pipelinedriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	"github.com/loykin/piper/pkg/pipeline/worker/driver/drivertest"
	"github.com/loykin/provisr/core"
)

var _ pipelinedriver.Driver = (*Driver)(nil)

func TestBaremetalDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() pipelinedriver.Driver {
		d, err := New(Config{WorkerID: "contract-test", MetaDir: t.TempDir()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	})
}

// newFakeAgent writes an executable shell script standing in for `piper agent
// exec`, so Start/Wait/Stop/Recover can be exercised under go test without
// re-execing the compiled test binary (see Config.PiperBin).
func newFakeAgent(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-agent.sh")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent script: %v", err)
	}
	return path
}

// fakeAgentSuccessBody writes the file passed via --result-file=... and exits 0.
const fakeAgentSuccessBody = `
result=""
for arg in "$@"; do
  case "$arg" in
    --result-file=*) result="${arg#--result-file=}" ;;
  esac
done
if [ -n "$result" ]; then
  printf '{}' > "$result"
fi
exit 0
`

// fakeAgentCrashBody exits non-zero without writing a result file.
const fakeAgentCrashBody = `exit 1`

// fakeAgentSleepBody execs into a long-lived process so its PID represents
// the actual running workload, matching how provisr's PID-file recovery
// mechanics expect to address the right process.
const fakeAgentSleepBody = `exec sleep 5`

func pollUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func testTask(runtimeKey string) (*proto.Task, pipelinedriver.ExecSpec) {
	task := &proto.Task{ID: "run-1:step", RunID: "run-1", StepName: "step", Attempt: 1}
	spec := pipelinedriver.ExecSpec{RuntimeKey: runtimeKey}
	return task, spec
}

// TestBaremetalDriverStartWaitTerminalCompletion freezes fed.md 13.1's
// "Start and terminal completion" behavior: a clean exit surfaces no
// InfraFailure and cleans up metadata/task files.
func TestBaremetalDriverStartWaitTerminalCompletion(t *testing.T) {
	agentPath := newFakeAgent(t, fakeAgentSuccessBody)
	d, err := New(Config{WorkerID: "worker-1", MetaDir: t.TempDir(), PiperBin: agentPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task, spec := testTask("worker-1-run-1-step-a1")
	spec.OutputDir = t.TempDir()
	handle, err := d.Start(context.Background(), task, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exit, err := d.Wait(ctx, handle)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if exit.InfraFailure != nil {
		t.Fatalf("unexpected InfraFailure: %v", exit.InfraFailure)
	}
	if exit.ResultPath != handle.ResultPath {
		t.Fatalf("exit.ResultPath = %q, want %q", exit.ResultPath, handle.ResultPath)
	}
	if _, err := os.Stat(handle.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("task file still exists after terminal completion: %v", err)
	}
	if _, err := os.Stat(d.metaPath(handle.RuntimeKey)); !os.IsNotExist(err) {
		t.Fatalf("metadata file still exists after terminal completion: %v", err)
	}
}

// TestBaremetalDriverStartWaitInfraFailureWithoutResultFile freezes the
// terminal-completion failure branch: a nonzero exit without a result file
// must surface as InfraFailure (onJobExit's currently-untested "failed" path).
func TestBaremetalDriverStartWaitInfraFailureWithoutResultFile(t *testing.T) {
	agentPath := newFakeAgent(t, fakeAgentCrashBody)
	d, err := New(Config{WorkerID: "worker-1", MetaDir: t.TempDir(), PiperBin: agentPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task, spec := testTask("worker-1-run-1-step-a1")
	spec.OutputDir = t.TempDir()
	handle, err := d.Start(context.Background(), task, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exit, err := d.Wait(ctx, handle)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if exit.InfraFailure == nil {
		t.Fatal("expected InfraFailure for a crash without a result file")
	}
}

// TestBaremetalDriverCancelDuringStartStopsJustStartedProcess mirrors
// worker.go dispatch's canceledMidStart sequence (Start followed immediately
// by Stop) and freezes fed.md 13.1's "cancel during start" behavior.
func TestBaremetalDriverCancelDuringStartStopsJustStartedProcess(t *testing.T) {
	agentPath := newFakeAgent(t, fakeAgentSleepBody)
	d, err := New(Config{WorkerID: "worker-1", MetaDir: t.TempDir(), PiperBin: agentPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task, spec := testTask("worker-1-run-1-step-a1")
	spec.OutputDir = t.TempDir()
	handle, err := d.Start(context.Background(), task, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := d.Stop(context.Background(), handle, time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertBaremetalCleanedUp(t, d, handle)
}

// TestBaremetalDriverCancelWhileRunningUnblocksWaitAndCleansUp mirrors
// worker.go's cancelRun (cancel the shared ctx, then Stop the handle) and
// freezes fed.md 13.1's "cancel while running" behavior, including resource
// cleanup.
func TestBaremetalDriverCancelWhileRunningUnblocksWaitAndCleansUp(t *testing.T) {
	agentPath := newFakeAgent(t, fakeAgentSleepBody)
	d, err := New(Config{WorkerID: "worker-1", MetaDir: t.TempDir(), PiperBin: agentPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task, spec := testTask("worker-1-run-1-step-a1")
	spec.OutputDir = t.TempDir()
	handle, err := d.Start(context.Background(), task, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Already-cancelled ctx: deterministic, no goroutine race — the fake
	// process never signals doneCh on its own within the test window.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Wait(ctx, handle); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}

	if err := d.Stop(context.Background(), handle, time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertBaremetalCleanedUp(t, d, handle)
}

func assertBaremetalCleanedUp(t *testing.T, d *Driver, handle pipelinedriver.Handle) {
	t.Helper()
	d.mu.Lock()
	_, active := d.active[handle.RuntimeKey]
	d.mu.Unlock()
	if active {
		t.Fatalf("runtime key %q still active after Stop", handle.RuntimeKey)
	}
	if _, err := os.Stat(handle.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("task file still exists after Stop: %v", err)
	}
	if _, err := os.Stat(d.metaPath(handle.RuntimeKey)); !os.IsNotExist(err) {
		t.Fatalf("metadata file still exists after Stop: %v", err)
	}
	if !pollUntil(2*time.Second, func() bool {
		status, err := d.manager.Status(handle.RuntimeKey)
		return err != nil || !status.Running
	}) {
		t.Fatal("process still running after Stop")
	}
}

// TestBaremetalDriverRecoverReattachesRunningProcess freezes fed.md 13.1's
// "recovery after restart" behavior for a still-running (non-empty) handle:
// a fresh Driver instance (simulating a worker restart) must re-attach to a
// process that survived via its metadata sidecar and PID file.
func TestBaremetalDriverRecoverReattachesRunningProcess(t *testing.T) {
	metaDir := t.TempDir()
	d1, err := New(Config{WorkerID: "worker-1", MetaDir: metaDir})
	if err != nil {
		t.Fatalf("New (d1): %v", err)
	}
	runtimeKey := "worker-1-run-1-step-a1"
	pidFile := d1.pidPath(runtimeKey)
	if err := d1.manager.Register(core.Spec{
		Name: runtimeKey,
		Args: []string{"sleep", "5"},
		// AutoRestart intentionally false: recovery here re-attaches to the
		// still-running process, it does not restart anything.
		PIDFile: pidFile,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	handle := pipelinedriver.Handle{
		RuntimeKey: runtimeKey,
		WorkerID:   "worker-1",
		TaskID:     "run-1:step",
		RunID:      "run-1",
		StepName:   "step",
		Attempt:    1,
		ResultPath: filepath.Join(t.TempDir(), "result.json"),
		TaskPath:   filepath.Join(t.TempDir(), "task.json"),
	}
	if err := d1.writeMetadata(runtimeKey, handle, handle.ResultPath, pidFile); err != nil {
		t.Fatalf("writeMetadata: %v", err)
	}

	// Simulate a worker restart: a brand new Driver instance, same metaDir.
	d2, err := New(Config{WorkerID: "worker-1", MetaDir: metaDir})
	if err != nil {
		t.Fatalf("New (d2): %v", err)
	}
	t.Cleanup(func() { _ = d2.Stop(context.Background(), handle, time.Second) })

	handles, err := d2.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(handles) != 1 {
		t.Fatalf("handles = %d, want 1", len(handles))
	}
	got := handles[0]
	if got.RuntimeKey != handle.RuntimeKey || got.TaskID != handle.TaskID ||
		got.RunID != handle.RunID || got.StepName != handle.StepName ||
		got.Attempt != handle.Attempt || got.ResultPath != handle.ResultPath ||
		got.TaskPath != handle.TaskPath {
		t.Fatalf("recovered handle = %#v, want %#v", got, handle)
	}
}
