package piper_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piper/piper/pkg/piper"
	_ "modernc.org/sqlite"
)

const testPipelineYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: test-pipeline
spec:
  steps:
    - name: step-a
      run:
        type: command
        command: [echo, hello]
    - name: step-b
      depends_on: [step-a]
      run:
        type: command
        command: [echo, world]
`

func newTestPiper(t *testing.T) *piper.Piper {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	p, err := piper.New(piper.Config{
		DB:        db,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.Close()
		_ = db.Close()
	})
	return p
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// ─── /health ─────────────────────────────────────────────────────────────────

func TestAPI_health(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodGet, "/health", nil)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

// ─── POST /runs ──────────────────────────────────────────────────────────────

func TestAPI_createRun(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{
		"yaml": testPipelineYAML,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["run_id"] == "" {
		t.Error("run_id should be set")
	}
}

func TestAPI_createRun_invalidYAML(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{
		"yaml": "not: valid: yaml: [",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAPI_createRun_invalidBody(t *testing.T) {
	p := newTestPiper(t)
	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.Handler(nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAPI_createRun_withOwnerID(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{
		"yaml":     testPipelineYAML,
		"owner_id": "alice",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// ─── GET /runs ───────────────────────────────────────────────────────────────

func TestAPI_listRuns_empty(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodGet, "/runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var runs []any
	_ = json.NewDecoder(w.Body).Decode(&runs)
	// Both empty array and null are acceptable
}

func TestAPI_listRuns_afterCreate(t *testing.T) {
	p := newTestPiper(t)
	do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})
	do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})

	w := do(t, p.Handler(nil), http.MethodGet, "/runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var runs []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&runs)
	if len(runs) != 2 {
		t.Errorf("want 2 runs, got %d", len(runs))
	}
}

// ─── GET /runs/{id} ──────────────────────────────────────────────────────────

func TestAPI_getRun(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runID := resp["run_id"]

	w2 := do(t, p.Handler(nil), http.MethodGet, "/runs/"+runID, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestAPI_getRun_notFound(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodGet, "/runs/no-such-run", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// ─── GET /runs/{id}/steps ─────────────────────────────────────────────────────

func TestAPI_getSteps(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runID := resp["run_id"]

	w2 := do(t, p.Handler(nil), http.MethodGet, "/runs/"+runID+"/steps", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var steps []any
	_ = json.NewDecoder(w2.Body).Decode(&steps)
	if len(steps) != 2 {
		t.Errorf("want 2 steps, got %d", len(steps))
	}
}

// ─── Worker API ───────────────────────────────────────────────────────────────

func TestAPI_taskNext_empty(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodGet, "/api/tasks/next", nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204, got %d", w.Code)
	}
}

func TestAPI_taskNext_afterRun(t *testing.T) {
	p := newTestPiper(t)
	do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})

	w := do(t, p.Handler(nil), http.MethodGet, "/api/tasks/next", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var task map[string]any
	_ = json.NewDecoder(w.Body).Decode(&task)
	if task["step_name"] == "" {
		t.Error("step_name should be set")
	}
}

func TestAPI_taskDone(t *testing.T) {
	p := newTestPiper(t)
	do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})

	// Acquire task
	w := do(t, p.Handler(nil), http.MethodGet, "/api/tasks/next", nil)
	var task map[string]any
	_ = json.NewDecoder(w.Body).Decode(&task)
	taskID := task["id"].(string)

	// Report done
	now := time.Now().Format(time.RFC3339)
	w2 := do(t, p.Handler(nil), http.MethodPost, "/api/tasks/"+taskID+"/done", map[string]any{
		"started_at": now,
		"ended_at":   now,
		"attempts":   1,
	})
	if w2.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestAPI_taskOp_unknownAction(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/api/tasks/some-id/restart", map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ─── Log ingest & SSE ─────────────────────────────────────────────────────────

func TestAPI_logIngest(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/runs", map[string]any{"yaml": testPipelineYAML})
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runID := resp["run_id"]

	logs := []map[string]any{
		{"ts": time.Now().Format(time.RFC3339), "stream": "stdout", "line": "hello"},
		{"ts": time.Now().Format(time.RFC3339), "stream": "stderr", "line": "world"},
	}
	w2 := do(t, p.Handler(nil), http.MethodPost, "/runs/"+runID+"/steps/step-a/logs", logs)
	if w2.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w2.Code, w2.Body.String())
	}

	// Query stored logs
	w3 := do(t, p.Handler(nil), http.MethodGet, "/runs/"+runID+"/steps/step-a/logs", nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w3.Code)
	}
	var lines []map[string]any
	_ = json.NewDecoder(w3.Body).Decode(&lines)
	if len(lines) != 2 {
		t.Errorf("want 2 log lines, got %d", len(lines))
	}
}

// ─── Auth hook ────────────────────────────────────────────────────────────────

func TestAPI_authHook_denied(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	p, _ := piper.New(piper.Config{
		DB:        db,
		OutputDir: t.TempDir(),
		Hooks: piper.Hooks{
			Auth: func(r *http.Request) error {
				return http.ErrNoCookie // arbitrary error
			},
		},
	})
	t.Cleanup(func() { _ = p.Close(); _ = db.Close() })

	w := do(t, p.Handler(nil), http.MethodGet, "/health", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

// ─── Method not allowed ───────────────────────────────────────────────────────

func TestAPI_runs_methodNotAllowed(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodDelete, "/runs", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}
