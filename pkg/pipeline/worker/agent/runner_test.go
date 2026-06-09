package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/testutil"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
)

// makeTask creates a proto.Task for testing.
func makeTask(t *testing.T, step pipeline.Step) *proto.Task {
	t.Helper()
	return makeTaskWithRunID(t, step, "run-test")
}

// makeTaskWithRunID creates a proto.Task with a specified runID.
func makeTaskWithRunID(t *testing.T, step pipeline.Step, runID string) *proto.Task {
	t.Helper()
	b, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	return &proto.Task{
		ID:       "task-1",
		RunID:    runID,
		StepName: step.Name,
		Step:     b,
	}
}

// fakeMasterServer returns a test HTTP server that accepts done/failed/logs requests.
func fakeMasterServer(t *testing.T) *testutil.Server {
	t.Helper()
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return srv
}

// ─── New ──────────────────────────────────────────────────────────────────────

func TestNew_defaults(t *testing.T) {
	r, err := agent.New(agent.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("runner is nil")
	}
}

func TestNew_no_store_when_url_empty(t *testing.T) {
	// No store is created when StorageURL is absent — creates without error
	r, err := agent.New(agent.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("runner is nil")
	}
}

// ─── Run: success ─────────────────────────────────────────────────────────────

func TestRun_success_echo(t *testing.T) {
	var doneTaskID string
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/done") {
			doneTaskID = strings.Split(r.URL.Path, "/")[3] // /api/tasks/{id}/done
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Discard.Write(nil)
	}))

	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "echo-step",
		Run: pipeline.Run{
			Command: []string{"echo", "hello runner"},
		},
	})

	result := r.Run(context.Background(), task)
	r.Report(result)

	if doneTaskID != "task-1" {
		t.Errorf("expected done report for task-1, got %q", doneTaskID)
	}
}

func TestRun_output_dir_created(t *testing.T) {
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	outDir := t.TempDir()
	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "mkdir-step",
		Run:  pipeline.Run{Command: []string{"echo", "out"}},
	})

	r.Run(context.Background(), task)

	expected := filepath.Join(outDir, "run-test", "mkdir-step")
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Errorf("output dir not created: %s", expected)
	}
}

// ─── Run: failure ─────────────────────────────────────────────────────────────

func TestRun_failed_bad_command(t *testing.T) {
	var failedTaskID string
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/failed") {
			failedTaskID = strings.Split(r.URL.Path, "/")[3]
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "fail-step",
		Run:  pipeline.Run{Command: []string{"__nonexistent_command__"}},
	})

	result := r.Run(context.Background(), task)
	r.Report(result)

	if failedTaskID != "task-1" {
		t.Errorf("expected failed report for task-1, got %q", failedTaskID)
	}
}

func TestRun_failed_no_command(t *testing.T) {
	var failedCalled bool
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/failed") {
			failedCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// No command
	task := makeTask(t, pipeline.Step{Name: "no-cmd"})
	result := r.Run(context.Background(), task)
	r.Report(result)

	if !failedCalled {
		t.Error("expected failed report")
	}
}

func TestRun_failed_invalid_step_json(t *testing.T) {
	var failedCalled bool
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/failed") {
			failedCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := &proto.Task{
		ID:       "bad-task",
		RunID:    "run-bad",
		StepName: "bad",
		Step:     []byte("not json"),
	}
	result := r.Run(context.Background(), task)
	r.Report(result)

	if !failedCalled {
		t.Error("expected failed report for bad step JSON")
	}
}

// ─── Run: log forwarding ──────────────────────────────────────────────────────

func TestRun_logs_sent_to_master(t *testing.T) {
	var logBody []byte
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs") {
			logBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "log-step",
		Run:  pipeline.Run{Command: []string{"echo", "log line"}},
	})
	r.Run(context.Background(), task)

	if !strings.Contains(string(logBody), "log line") {
		t.Errorf("expected log body to contain 'log line', got: %s", logBody)
	}
}

func TestRun_logs_final_line_without_newline(t *testing.T) {
	var logBody []byte
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs") {
			logBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "log-step",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "printf final-line"}},
	})
	r.Run(context.Background(), task)

	if !strings.Contains(string(logBody), "final-line") {
		t.Errorf("expected log body to contain final line without newline, got: %s", logBody)
	}
}

// ─── Run: skip reporting when MasterURL is absent ─────────────────────────────

func TestRun_no_master_no_panic(t *testing.T) {
	r, err := agent.New(agent.Config{
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "silent-step",
		Run:  pipeline.Run{Command: []string{"echo", "ok"}},
	})

	// Completes without panic even when MasterURL is absent
	r.Run(context.Background(), task)
}
