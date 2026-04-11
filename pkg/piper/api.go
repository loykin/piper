package piper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/store"
)

// apiHandler는 frontend + worker용 통합 HTTP API
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
	// Frontend API
	h.mux.HandleFunc("/runs", h.handleRuns)
	h.mux.HandleFunc("/runs/", h.handleRunSub)
	h.mux.HandleFunc("/health", h.handleHealth)

	// Worker API
	h.mux.HandleFunc("/api/workers", h.handleWorkers)
	h.mux.HandleFunc("/api/workers/", h.handleWorkerOp)
	h.mux.HandleFunc("/api/tasks/next", h.handleTaskNext)
	h.mux.HandleFunc("/api/tasks/", h.handleTaskOp)
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth 훅 — 모든 요청에 적용
	if err := h.p.cfg.Hooks.callAuth(r); err != nil {
		jsonErr(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// 사용자 주입 extra 핸들러
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
// POST /runs       파이프라인 실행 (큐에 추가)
func (h *apiHandler) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filter, err := h.p.cfg.Hooks.callBeforeListRuns(r.Context(), r)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusForbidden)
			return
		}
		runs, err := h.store.ListRuns(store.RunFilter{
			OwnerID:      filter.OwnerID,
			PipelineName: filter.PipelineName,
		})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, runs)

	case http.MethodPost:
		var req struct {
			YAML    string         `json:"yaml"`
			Params  map[string]any `json:"params,omitempty"`
			OwnerID string         `json:"owner_id,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := h.p.cfg.Hooks.callBeforeCreateRun(r.Context(), r, req.YAML); err != nil {
			jsonErr(w, err.Error(), http.StatusForbidden)
			return
		}

		pl, err := h.p.Parse([]byte(req.YAML))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		dag, err := pipeline.BuildDAG(pl)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		runID := genRunID()
		outputDir := filepath.Join(h.p.cfg.OutputDir, runID)

		run := &store.Run{
			ID:           runID,
			OwnerID:      req.OwnerID,
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
			if err := h.store.UpsertStep(&store.Step{
				RunID:    runID,
				StepName: s.Name,
				Status:   "pending",
			}); err != nil {
				slog.Warn("init step failed", "run_id", runID, "step", s.Name, "err", err)
			}
		}

		// 큐에 추가 — worker가 폴링해서 실행
		h.p.queue.add(pl, dag, runID, ".", outputDir)

		if h.p.cfg.Hooks.OnRunStart != nil {
			go h.p.cfg.Hooks.OnRunStart(context.Background(), runID, pl)
		}

		jsonOK(w, map[string]string{"run_id": runID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRunSub는 /runs/{id} 하위 경로를 처리한다.
//
// GET  /runs/{id}                         — run + steps 조회
// GET  /runs/{id}/steps                   — steps 조회
// GET  /runs/{id}/steps/{step}/logs       — 로그 snapshot
// GET  /runs/{id}/steps/{step}/logs/stream — SSE 스트리밍
// POST /runs/{id}/steps/{step}/logs       — worker 로그 수집
func (h *apiHandler) handleRunSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/runs/")
	parts := strings.SplitN(path, "/", 4)
	runID := parts[0]

	// POST /runs/{id}/steps/{step}/logs — worker 로그 수집
	if r.Method == http.MethodPost {
		if len(parts) == 4 && parts[1] == "steps" && parts[3] == "logs" {
			h.handleLogIngest(w, r, runID, parts[2])
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	run, err := h.store.GetRun(runID)
	if err != nil || run == nil {
		jsonErr(w, "run not found", http.StatusNotFound)
		return
	}

	if err := h.p.cfg.Hooks.callBeforeGetRun(r.Context(), r, runID); err != nil {
		jsonErr(w, err.Error(), http.StatusForbidden)
		return
	}

	switch {
	// GET /runs/{id}
	case len(parts) == 1:
		steps, err := h.store.ListSteps(runID)
		if err != nil {
			slog.Warn("list steps failed", "run_id", runID, "err", err)
		}
		jsonOK(w, map[string]any{"run": run, "steps": steps})

	// GET /runs/{id}/steps
	case len(parts) == 2 && parts[1] == "steps":
		steps, err := h.store.ListSteps(runID)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, steps)

	// GET /runs/{id}/steps/{step}/logs  or  /logs/stream
	case len(parts) == 4 && parts[1] == "steps" && (parts[3] == "logs" || parts[3] == "logs/stream"):
		stepName := parts[2]
		if err := h.p.cfg.Hooks.callBeforeGetLogs(r.Context(), r, runID, stepName); err != nil {
			jsonErr(w, err.Error(), http.StatusForbidden)
			return
		}
		if parts[3] == "logs/stream" {
			h.handleLogStream(w, r, runID, stepName)
			return
		}
		afterID, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		lines, err := h.store.GetLogs(runID, stepName, afterID)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, lines)

	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

// POST /api/workers        — worker 등록
// GET  /api/workers        — 활성 worker 목록
func (h *apiHandler) handleWorkers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var info WorkerInfo
		if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if info.ID == "" {
			jsonErr(w, "id is required", http.StatusBadRequest)
			return
		}
		h.p.registry.register(info)
		slog.Info("worker registered", "id", info.ID, "label", info.Label, "hostname", info.Hostname)
		jsonOK(w, map[string]string{"worker_id": info.ID})

	case http.MethodGet:
		jsonOK(w, h.p.registry.list())

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST /api/workers/{id}/heartbeat — worker 생존 신호
func (h *apiHandler) handleWorkerOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// /api/workers/{id}/heartbeat
	rest := strings.TrimPrefix(r.URL.Path, "/api/workers/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[1] != "heartbeat" {
		http.NotFound(w, r)
		return
	}
	workerID := parts[0]

	var body struct {
		InFlight int `json:"in_flight"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // 바디 없어도 허용

	if err := h.p.registry.heartbeat(workerID, body.InFlight); err != nil {
		// 미등록 worker — 재등록 유도
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// GET /api/tasks/next?worker_id=xxx&label=gpu — worker가 다음 task를 폴링
func (h *apiHandler) handleTaskNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workerID := r.URL.Query().Get("worker_id")
	h.p.registry.touch(workerID) // 폴링 자체도 last_seen 갱신

	label := r.URL.Query().Get("label")
	task := h.p.queue.next(label)
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	jsonOK(w, task)
}

// POST /api/tasks/{id}/done   — worker 성공 보고
// POST /api/tasks/{id}/failed — worker 실패 보고
func (h *apiHandler) handleTaskOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// /api/tasks/{id}/done|failed
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	taskID, action := parts[0], parts[1]
	if action != "done" && action != "failed" {
		jsonErr(w, "unknown action: use done or failed", http.StatusBadRequest)
		return
	}

	var result struct {
		Error     string    `json:"error,omitempty"`
		StartedAt time.Time `json:"started_at"`
		EndedAt   time.Time `json:"ended_at"`
		Attempts  int       `json:"attempts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.p.queue.complete(taskID, action, result.Error, result.StartedAt, result.EndedAt, result.Attempts); err != nil {
		slog.Warn("task complete error", "task_id", taskID, "err", err)
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// POST /runs/{id}/steps/{step}/logs — worker 배치 로그 수집
func (h *apiHandler) handleLogIngest(w http.ResponseWriter, r *http.Request, runID, stepName string) {
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
		slog.Warn("append logs failed", "run_id", runID, "step", stepName, "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

// handleLogStream은 SSE로 로그를 실시간 스트리밍한다.
// GET /runs/{id}/steps/{step}/logs/stream
//
// 이벤트:
//
//	data: <LogLine JSON>                         — 새 로그 라인
//	event: done\ndata: {"status":"success"|"failed"} — run 완료
func (h *apiHandler) handleLogStream(w http.ResponseWriter, r *http.Request, runID, stepName string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var afterID int64
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			lines, err := h.store.GetLogs(runID, stepName, afterID)
			if err != nil {
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				flusher.Flush()
				return
			}
			for _, l := range lines {
				b, _ := json.Marshal(l)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				afterID = l.ID
			}
			if len(lines) > 0 {
				flusher.Flush()
			}

			// run이 종료됐으면 남은 로그 flush 후 done 이벤트
			run, err := h.store.GetRun(runID)
			if err == nil && run != nil && run.Status != "running" {
				if tail, err2 := h.store.GetLogs(runID, stepName, afterID); err2 == nil {
					for _, l := range tail {
						b, _ := json.Marshal(l)
						_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
					}
				}
				_, _ = fmt.Fprintf(w, "event: done\ndata: {\"status\":%q}\n\n", run.Status)
				flusher.Flush()
				return
			}
		}
	}
}

func (h *apiHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, map[string]string{"status": "ok"})
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
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func genRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}
