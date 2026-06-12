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
		ProjectID: "project-a",
		ID:        "task-1",
		RunID:     runID,
		StepName:  step.Name,
		Step:      b,
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
	var logPath string
	var authorization string
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs") {
			logPath = r.URL.Path
			authorization = r.Header.Get("Authorization")
			logBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{
		MasterURL:   srv.URL,
		WorkerToken: "worker-secret",
		OutputDir:   t.TempDir(),
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
	if logPath != "/api/projects/project-a/runs/run-test/steps/log-step/logs" {
		t.Fatalf("log path = %q", logPath)
	}
	if authorization != "Bearer worker-secret" {
		t.Fatalf("authorization = %q", authorization)
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

// ─── .metrics.json reporting ─────────────────────────────────────────────────

func TestRun_reports_final_metrics_on_success(t *testing.T) {
	var receivedPath string
	var receivedBody []byte
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "final-metrics") {
			receivedPath = r.URL.Path
			receivedBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))

	outDir := t.TempDir()
	r, err := agent.New(agent.Config{MasterURL: srv.URL, OutputDir: outDir})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTaskWithRunID(t, pipeline.Step{
		Name: "train",
		Run: pipeline.Run{
			// write .metrics.json as part of the command
			Command: []string{"sh", "-c", `echo '{"accuracy":0.94,"val_loss":0.23}' > "$PIPER_OUTPUT_DIR/.metrics.json"`},
		},
	}, "run-metrics-test")

	r.Run(context.Background(), task)

	wantPath := "/api/projects/project-a/runs/run-metrics-test/steps/train/final-metrics"
	if receivedPath != wantPath {
		t.Fatalf("final-metrics path = %q, want %q", receivedPath, wantPath)
	}
	if !strings.Contains(string(receivedBody), "accuracy") {
		t.Errorf("body missing accuracy: %s", receivedBody)
	}
	if !strings.Contains(string(receivedBody), "val_loss") {
		t.Errorf("body missing val_loss: %s", receivedBody)
	}
}

func TestRun_no_metrics_file_no_final_metrics_call(t *testing.T) {
	finalMetricsCalled := false
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "final-metrics") {
			finalMetricsCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{MasterURL: srv.URL, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "no-metrics",
		Run:  pipeline.Run{Command: []string{"echo", "done"}},
	})

	r.Run(context.Background(), task)

	if finalMetricsCalled {
		t.Error("final-metrics should not be called when .metrics.json is absent")
	}
}

func TestRun_failed_step_does_not_report_metrics(t *testing.T) {
	finalMetricsCalled := false
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "final-metrics") {
			finalMetricsCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))

	r, err := agent.New(agent.Config{MasterURL: srv.URL, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	// Command fails — metrics must not be reported
	task := makeTask(t, pipeline.Step{
		Name: "fail-step",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "exit 1"}},
	})

	r.Run(context.Background(), task)

	if finalMetricsCalled {
		t.Error("final-metrics must not be called when step fails")
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
