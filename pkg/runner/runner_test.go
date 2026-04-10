package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
)

// makeTask는 테스트용 proto.Task를 생성한다.
func makeTask(t *testing.T, step pipeline.Step) *proto.Task {
	t.Helper()
	return makeTaskWithRunID(t, step, "run-test")
}

// makeTaskWithRunID는 runID를 지정해 proto.Task를 생성한다.
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

// fakeMasterServer는 done/failed/logs 요청을 받는 테스트용 HTTP 서버를 반환한다.
func fakeMasterServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─── New ──────────────────────────────────────────────────────────────────────

func TestNew_defaults(t *testing.T) {
	r, err := runner.New(runner.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("runner is nil")
	}
}

func TestNew_s3_skipped_when_no_endpoint(t *testing.T) {
	// S3Endpoint 없으면 S3 클라이언트 생성 안 함 — 에러 없이 생성
	r, err := runner.New(runner.Config{
		S3Bucket: "my-bucket",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("runner is nil")
	}
}

// ─── Run: 성공 ────────────────────────────────────────────────────────────────

func TestRun_success_echo(t *testing.T) {
	var doneTaskID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/done") {
			doneTaskID = strings.Split(r.URL.Path, "/")[3] // /api/tasks/{id}/done
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Discard.Write(nil)
	}))
	defer srv.Close()

	r, err := runner.New(runner.Config{
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

	r.Run(context.Background(), task)

	if doneTaskID != "task-1" {
		t.Errorf("expected done report for task-1, got %q", doneTaskID)
	}
}

func TestRun_output_dir_created(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	r, err := runner.New(runner.Config{
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

// ─── Run: 실패 ────────────────────────────────────────────────────────────────

func TestRun_failed_bad_command(t *testing.T) {
	var failedTaskID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/failed") {
			failedTaskID = strings.Split(r.URL.Path, "/")[3]
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r, err := runner.New(runner.Config{
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

	r.Run(context.Background(), task)

	if failedTaskID != "task-1" {
		t.Errorf("expected failed report for task-1, got %q", failedTaskID)
	}
}

func TestRun_failed_no_command(t *testing.T) {
	var failedCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/failed") {
			failedCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r, err := runner.New(runner.Config{
		MasterURL: srv.URL,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Command 없음
	task := makeTask(t, pipeline.Step{Name: "no-cmd"})
	r.Run(context.Background(), task)

	if !failedCalled {
		t.Error("expected failed report")
	}
}

func TestRun_failed_invalid_step_json(t *testing.T) {
	var failedCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/failed") {
			failedCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r, err := runner.New(runner.Config{
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
	r.Run(context.Background(), task)

	if !failedCalled {
		t.Error("expected failed report for bad step JSON")
	}
}

// ─── Run: 로그 전송 ────────────────────────────────────────────────────────────

func TestRun_logs_sent_to_master(t *testing.T) {
	var logBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs") {
			logBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r, err := runner.New(runner.Config{
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

// ─── Run: MasterURL 없을 때 보고 스킵 ─────────────────────────────────────────

func TestRun_no_master_no_panic(t *testing.T) {
	r, err := runner.New(runner.Config{
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	task := makeTask(t, pipeline.Step{
		Name: "silent-step",
		Run:  pipeline.Run{Command: []string{"echo", "ok"}},
	})

	// MasterURL 없어도 패닉 없이 완료
	r.Run(context.Background(), task)
}
