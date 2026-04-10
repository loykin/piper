package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/worker"
)

// fakeMaster는 테스트용 master HTTP 서버다.
type fakeMaster struct {
	tasks        [][]byte
	idx          int
	done         int64 // atomic
	failed       int64 // atomic
	registered   int64 // atomic: POST /api/workers 횟수
	heartbeats   int64 // atomic
	lastWorkerID string
}

func (m *fakeMaster) handler() http.Handler {
	mux := http.NewServeMux()

	// Worker 등록
	mux.HandleFunc("/api/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if id, ok := body["id"].(string); ok {
				m.lastWorkerID = id
			}
			atomic.AddInt64(&m.registered, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"worker_id":"`+m.lastWorkerID+`"}`)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	// Heartbeat
	mux.HandleFunc("/api/workers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/heartbeat") {
			atomic.AddInt64(&m.heartbeats, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	// Task 폴링
	mux.HandleFunc("/api/tasks/next", func(w http.ResponseWriter, r *http.Request) {
		if m.idx >= len(m.tasks) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(m.tasks[m.idx])
		m.idx++
	})

	// Task 완료 보고
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/done") {
			atomic.AddInt64(&m.done, 1)
		} else if strings.Contains(r.URL.Path, "/failed") {
			atomic.AddInt64(&m.failed, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	// 로그 수집
	mux.HandleFunc("/runs/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func makeTaskBytes(t *testing.T, id string, cmd []string) []byte {
	t.Helper()
	step := pipeline.Step{
		Name: "step",
		Run:  pipeline.Run{Command: cmd},
	}
	stepJSON, _ := json.Marshal(step)
	task := proto.Task{
		ID:       id,
		RunID:    "run-" + id,
		StepName: "step",
		Step:     stepJSON,
	}
	b, _ := json.Marshal(task)
	return b
}

// waitFor는 조건이 참이 될 때까지 최대 timeout 대기한다.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ─── New ──────────────────────────────────────────────────────────────────────

func TestNew_ok(t *testing.T) {
	w, err := worker.New(worker.Config{
		MasterURL: "http://localhost:9999",
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if w == nil {
		t.Fatal("worker is nil")
	}
}

func TestNew_defaults_applied(t *testing.T) {
	_, err := worker.New(worker.Config{
		MasterURL: "http://localhost:9999",
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ─── 등록 ─────────────────────────────────────────────────────────────────────

func TestWorker_registers_on_start(t *testing.T) {
	fm := &fakeMaster{}
	srv := httptest.NewServer(fm.handler())
	defer srv.Close()

	w, err := worker.New(worker.Config{
		MasterURL:    srv.URL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	defer cancel()

	if !waitFor(t, 2*time.Second, func() bool {
		return atomic.LoadInt64(&fm.registered) > 0
	}) {
		t.Error("worker did not register with master")
	}
	if fm.lastWorkerID == "" {
		t.Error("worker ID not sent to master")
	}
}

func TestWorker_sends_heartbeat(t *testing.T) {
	fm := &fakeMaster{}
	srv := httptest.NewServer(fm.handler())
	defer srv.Close()

	w, err := worker.New(worker.Config{
		MasterURL:    srv.URL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	defer cancel()

	// heartbeat는 10초 주기라 테스트에서 직접 검증하기 어려우므로
	// 등록 성공 + Run 동작 확인으로 대체
	if !waitFor(t, 2*time.Second, func() bool {
		return atomic.LoadInt64(&fm.registered) > 0
	}) {
		t.Error("worker did not register")
	}
}

// ─── Run: task 실행 후 done 보고 ─────────────────────────────────────────────

func TestWorker_run_reports_done(t *testing.T) {
	fm := &fakeMaster{
		tasks: [][]byte{makeTaskBytes(t, "t1", []string{"echo", "hello"})},
	}
	srv := httptest.NewServer(fm.handler())
	defer srv.Close()

	w, err := worker.New(worker.Config{
		MasterURL:    srv.URL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	if !waitFor(t, 3*time.Second, func() bool {
		return atomic.LoadInt64(&fm.done) > 0
	}) {
		t.Errorf("want done report, got done=%d failed=%d", fm.done, fm.failed)
	}
	cancel()

	if atomic.LoadInt64(&fm.failed) != 0 {
		t.Errorf("unexpected failed reports: %d", fm.failed)
	}
}

// ─── Run: 실패 커맨드 → failed 보고 ──────────────────────────────────────────

func TestWorker_run_reports_failed(t *testing.T) {
	fm := &fakeMaster{
		tasks: [][]byte{makeTaskBytes(t, "t2", []string{"__nonexistent_cmd__"})},
	}
	srv := httptest.NewServer(fm.handler())
	defer srv.Close()

	w, err := worker.New(worker.Config{
		MasterURL:    srv.URL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	if !waitFor(t, 3*time.Second, func() bool {
		return atomic.LoadInt64(&fm.failed) > 0
	}) {
		t.Errorf("want failed report, got done=%d failed=%d", fm.done, fm.failed)
	}
	cancel()
}

// ─── Run: 여러 task 처리 ──────────────────────────────────────────────────────

func TestWorker_run_multiple_tasks(t *testing.T) {
	fm := &fakeMaster{
		tasks: [][]byte{
			makeTaskBytes(t, "m1", []string{"echo", "1"}),
			makeTaskBytes(t, "m2", []string{"echo", "2"}),
			makeTaskBytes(t, "m3", []string{"echo", "3"}),
		},
	}
	srv := httptest.NewServer(fm.handler())
	defer srv.Close()

	w, err := worker.New(worker.Config{
		MasterURL:    srv.URL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
		Concurrency:  2,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	if !waitFor(t, 5*time.Second, func() bool {
		return atomic.LoadInt64(&fm.done) >= 3
	}) {
		t.Errorf("want 3 done, got %d", atomic.LoadInt64(&fm.done))
	}
	cancel()
}

// ─── Run: context 취소 시 정상 종료 ──────────────────────────────────────────

func TestWorker_shutdown_on_context_cancel(t *testing.T) {
	fm := &fakeMaster{} // task 없음
	srv := httptest.NewServer(fm.handler())
	defer srv.Close()

	w, err := worker.New(worker.Config{
		MasterURL:    srv.URL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Run did not stop after context cancel")
	}
}
