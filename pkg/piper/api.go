package piper

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/store"
)

// apiHandler는 frontend용 HTTP API
type apiHandler struct {
	p     *Piper
	extra http.Handler
	mux   *http.ServeMux
	store *store.Store
}

func newAPIHandler(p *Piper, extra http.Handler, st *store.Store) *apiHandler {
	h := &apiHandler{p: p, extra: extra, mux: http.NewServeMux(), store: st}
	h.routes()
	return h
}

func (h *apiHandler) routes() {
	h.mux.HandleFunc("/runs", h.handleRuns)
	h.mux.HandleFunc("/runs/", h.handleRunSub)
	h.mux.HandleFunc("/health", h.handleHealth)

	// worker가 로그 전송하는 내부 API
	h.mux.HandleFunc("/api/tasks/", h.handleTaskLogs)
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.extra != nil {
		rw := &responseRecorder{ResponseWriter: w}
		h.extra.ServeHTTP(rw, r)
		if rw.written {
			return
		}
	}
	h.mux.ServeHTTP(w, r)
}

// GET  /runs       실행 목록
// POST /runs       파이프라인 실행
func (h *apiHandler) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		runs, err := h.store.ListRuns()
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, runs)

	case http.MethodPost:
		var req struct {
			YAML   string         `json:"yaml"`
			Params map[string]any `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		pl, err := h.p.Parse([]byte(req.YAML))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		runID := genRunID()
		run := &store.Run{
			ID:           runID,
			PipelineName: pl.Metadata.Name,
			Status:       "running",
			StartedAt:    time.Now(),
			PipelineYAML: req.YAML,
		}
		if err := h.store.CreateRun(run); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// step 초기화
		for _, s := range pl.Spec.Steps {
			h.store.UpsertStep(&store.Step{
				RunID:    runID,
				StepName: s.Name,
				Status:   "pending",
			})
		}

		go func() {
			result, err := h.p.runPipelineWithRunID(context.Background(), pl, runID)
			h.persistResult(runID, result, err)
		}()

		jsonOK(w, map[string]string{"run_id": runID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// /runs/{id}
// /runs/{id}/steps
// /runs/{id}/steps/{step}/logs?after=0
func (h *apiHandler) handleRunSub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// path: {id} | {id}/steps | {id}/steps/{step}/logs
	path := strings.TrimPrefix(r.URL.Path, "/runs/")
	parts := strings.SplitN(path, "/", 4)
	runID := parts[0]

	run, err := h.store.GetRun(runID)
	if err != nil || run == nil {
		jsonErr(w, "run not found", http.StatusNotFound)
		return
	}

	// /runs/{id}
	if len(parts) == 1 {
		steps, _ := h.store.ListSteps(runID)
		jsonOK(w, map[string]any{"run": run, "steps": steps})
		return
	}

	// /runs/{id}/steps
	if len(parts) == 2 && parts[1] == "steps" {
		steps, _ := h.store.ListSteps(runID)
		jsonOK(w, steps)
		return
	}

	// /runs/{id}/steps/{step}/logs
	if len(parts) == 4 && parts[1] == "steps" && parts[3] == "logs" {
		stepName := parts[2]
		afterID, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		lines, err := h.store.GetLogs(runID, stepName, afterID)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, lines)
		return
	}

	jsonErr(w, "not found", http.StatusNotFound)
}

// POST /api/tasks/{id}/logs — worker가 로그 배치 전송
func (h *apiHandler) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// /api/tasks/{id}/logs
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if len(parts) != 2 || parts[1] != "logs" {
		http.NotFound(w, r)
		return
	}
	taskID := parts[0]

	// taskID = "{runID}-{stepName}" 형태
	idx := strings.LastIndex(taskID, "-")
	if idx < 0 {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}
	// run-20260408-050614.675-prepare 형태이므로 마지막 - 기준으로 split
	runID := taskID[:idx]
	stepName := taskID[idx+1:]

	type logEntry struct {
		Ts     time.Time `json:"ts"`
		Stream string    `json:"stream"`
		Line   string    `json:"line"`
	}
	var entries []logEntry
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	lines := make([]*store.LogLine, len(entries))
	for i, e := range entries {
		lines[i] = &store.LogLine{
			RunID:    runID,
			StepName: stepName,
			Ts:       e.Ts,
			Stream:   e.Stream,
			Line:     e.Line,
		}
	}
	if err := h.store.AppendLogs(lines); err != nil {
		slog.Warn("append logs failed", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *apiHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"status": "ok"})
}

// persistResult는 run 완료 후 DB에 결과 저장
func (h *apiHandler) persistResult(runID string, result *pipeline.RunResult, runErr error) {
	now := time.Now()
	status := "success"
	if runErr != nil || result.Failed() {
		status = "failed"
	}
	h.store.UpdateRunStatus(runID, status, &now)

	for _, sr := range result.Steps {
		step := &store.Step{
			RunID:     runID,
			StepName:  sr.StepName,
			Status:    string(sr.Status),
			StartedAt: &sr.StartedAt,
			EndedAt:   &sr.EndedAt,
			Attempts:  sr.Attempts,
		}
		if sr.ErrMsg != "" {
			step.Error = sr.ErrMsg
		}
		h.store.UpsertStep(step)
	}
}

type responseRecorder struct {
	http.ResponseWriter
	written bool
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.written = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	rw.written = true
	return rw.ResponseWriter.Write(b)
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func genRunID() string {
	return "run-" + time.Now().Format("20060102-150405.000")
}
