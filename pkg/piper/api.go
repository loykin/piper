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

	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/store"
)

// apiHandler is the unified HTTP API for both the frontend and workers
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

	// ModelService API
	h.mux.HandleFunc("/services", h.handleServices)
	h.mux.HandleFunc("/services/predict/", h.p.serving.proxy.ServeHTTP)
	h.mux.HandleFunc("/services/", h.handleServiceSub)
	h.mux.HandleFunc("/schedules", h.handleSchedules)
	h.mux.HandleFunc("/schedules/", h.handleScheduleSub)
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth hook — applied to all requests
	if err := h.p.cfg.Hooks.callAuth(r); err != nil {
		jsonErr(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// User-injected extra handler
	if h.extra != nil {
		rw := &responseRecorder{ResponseWriter: w}
		h.extra.ServeHTTP(rw, r)
		if rw.written {
			return
		}
	}
	h.mux.ServeHTTP(w, r)
}

// GET  /runs       list runs
// POST /runs       start a pipeline run (add to queue)
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
		type runWithSteps struct {
			*store.Run
			Steps []*store.Step `json:"steps"`
		}
		result := make([]runWithSteps, 0, len(runs))
		for _, run := range runs {
			steps, _ := h.store.ListSteps(run.ID)
			if steps == nil {
				steps = []*store.Step{}
			}
			result = append(result, runWithSteps{Run: run, Steps: steps})
		}
		jsonOK(w, result)

	case http.MethodPost:
		var req struct {
			YAML    string            `json:"yaml"`
			Params  map[string]any    `json:"params,omitempty"`
			OwnerID string            `json:"owner_id,omitempty"`
			Vars    proto.BuiltinVars `json:"vars,omitempty"`
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
		now := time.Now().UTC()
		runStatus := "running"
		if req.Vars.ScheduledAt != nil && req.Vars.ScheduledAt.After(now) {
			runStatus = "scheduled"
		}

		run := &store.Run{
			ID:           runID,
			OwnerID:      req.OwnerID,
			PipelineName: pl.Metadata.Name,
			Status:       runStatus,
			StartedAt:    now,
			ScheduledAt:  req.Vars.ScheduledAt,
			PipelineYAML: req.YAML,
		}
		if err := h.store.CreateRun(run); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Add to queue immediately unless this is a one-time future-scheduled run.
		if runStatus == "running" {
			// Initialize steps only when run is actually starting.
			for _, s := range pl.Spec.Steps {
				if err := h.store.UpsertStep(&store.Step{
					RunID:    runID,
					StepName: s.Name,
					Status:   "pending",
				}); err != nil {
					slog.Warn("init step failed", "run_id", runID, "step", s.Name, "err", err)
				}
			}

			h.p.queue.add(pl, dag, runID, ".", outputDir, req.Vars, req.Params)

			if h.p.cfg.Hooks.OnRunStart != nil {
				go h.p.cfg.Hooks.OnRunStart(context.Background(), runID, pl)
			}
		}

		jsonOK(w, map[string]string{"run_id": runID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRunSub handles sub-paths under /runs/{id}.
//
// GET  /runs/{id}                          — fetch run + steps
// GET  /runs/{id}/steps                    — fetch steps
// GET  /runs/{id}/steps/{step}/logs        — log snapshot
// GET  /runs/{id}/steps/{step}/logs/stream — SSE streaming
// POST /runs/{id}/steps/{step}/logs        — ingest worker logs
func (h *apiHandler) handleRunSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/runs/")
	parts := strings.SplitN(path, "/", 4)
	runID := parts[0]

	// POST /runs/{id}/steps/{step}/logs — ingest worker logs
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
		lines, err := h.p.logs.Query(runID, stepName, afterID)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, lines)

	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

// POST /api/workers        — register worker
// GET  /api/workers        — list active workers
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

// GET  /schedules          — list schedules
// POST /schedules          — create cron schedule
func (h *apiHandler) handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		schedules, err := h.store.ListSchedules()
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, schedules)

	case http.MethodPost:
		var req struct {
			Name    string         `json:"name"`
			YAML    string         `json:"yaml"`
			Type    string         `json:"type"` // immediate | once | cron
			Cron    string         `json:"cron"`
			RunAt   *time.Time     `json:"run_at,omitempty"`
			OwnerID string         `json:"owner_id,omitempty"`
			Params  map[string]any `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Type == "" {
			req.Type = "immediate"
		}
		if req.Type != "immediate" && req.Type != "once" && req.Type != "cron" {
			jsonErr(w, "type must be immediate, once, or cron", http.StatusBadRequest)
			return
		}

		pl, err := h.p.Parse([]byte(req.YAML))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		now := time.Now().UTC()
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = pl.Metadata.Name
		}

		paramsJSON := "{}"
		if req.Params != nil {
			if b, err := json.Marshal(req.Params); err == nil {
				paramsJSON = string(b)
			}
		}

		sc := &store.Schedule{
			ID:           "sch-" + genRunID(),
			Name:         name,
			OwnerID:      req.OwnerID,
			PipelineYAML: req.YAML,
			ScheduleType: req.Type,
			ParamsJSON:   paramsJSON,
			Enabled:      true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		switch req.Type {
		case "cron":
			if strings.TrimSpace(req.Cron) == "" {
				jsonErr(w, "cron is required for type=cron", http.StatusBadRequest)
				return
			}
			nextRunAt, err := nextScheduleTime(req.Cron, now)
			if err != nil {
				jsonErr(w, "invalid cron expression", http.StatusBadRequest)
				return
			}
			sc.CronExpr = req.Cron
			sc.NextRunAt = nextRunAt

		case "once":
			if req.RunAt == nil || req.RunAt.IsZero() {
				jsonErr(w, "run_at is required for type=once", http.StatusBadRequest)
				return
			}
			sc.NextRunAt = req.RunAt.UTC()

		case "immediate":
			sc.NextRunAt = now
		}

		if err := h.store.CreateSchedule(sc); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// For immediate type: trigger a run right now.
		if req.Type == "immediate" {
			go h.p.triggerSchedule(sc)
		}

		jsonOK(w, map[string]string{"schedule_id": sc.ID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET    /schedules/{id}      — get a single schedule
// GET    /schedules/{id}/runs — list runs for this schedule
// PATCH  /schedules/{id}      — enable/disable
// DELETE /schedules/{id}      — delete
func (h *apiHandler) handleScheduleSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/schedules/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	if id == "" {
		jsonErr(w, "schedule id required", http.StatusBadRequest)
		return
	}

	// GET /schedules/{id}/runs
	if sub == "runs" && r.Method == http.MethodGet {
		runs, err := h.store.ListRuns(store.RunFilter{ScheduleID: id})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, runs)
		return
	}

	switch r.Method {
	case http.MethodGet:
		sc, err := h.store.GetSchedule(id)
		if err != nil {
			jsonErr(w, "schedule not found", http.StatusNotFound)
			return
		}
		jsonOK(w, sc)

	case http.MethodPatch:
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Enabled == nil {
			jsonErr(w, "enabled is required", http.StatusBadRequest)
			return
		}
		if err := h.store.SetScheduleEnabled(id, *req.Enabled); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"id": id, "enabled": *req.Enabled})

	case http.MethodDelete:
		if err := h.store.DeleteSchedule(id); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST /api/workers/{id}/heartbeat — worker keepalive signal
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
	_ = json.NewDecoder(r.Body).Decode(&body) // body is optional

	if err := h.p.registry.heartbeat(workerID, body.InFlight); err != nil {
		// Unregistered worker — prompt re-registration
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// GET /api/tasks/next?worker_id=xxx&label=gpu — worker polls for the next task
func (h *apiHandler) handleTaskNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workerID := r.URL.Query().Get("worker_id")
	h.p.registry.touch(workerID) // polling itself updates last_seen

	label := r.URL.Query().Get("label")
	task := h.p.queue.next(label)
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	jsonOK(w, task)
}

// POST /api/tasks/{id}/done   — worker reports success
// POST /api/tasks/{id}/failed — worker reports failure
func (h *apiHandler) handleTaskOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// /api/tasks/{id}/done|failed path parsing
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

// POST /runs/{id}/steps/{step}/logs — ingest a batch of worker logs
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

	lines := make([]*logstore.Line, len(entries))
	for i, e := range entries {
		lines[i] = &logstore.Line{
			RunID:    runID,
			StepName: stepName,
			Ts:       e.Ts,
			Stream:   e.Stream,
			Line:     e.Line,
		}
	}
	if err := h.p.logs.Append(lines); err != nil {
		slog.Warn("append logs failed", "run_id", runID, "step", stepName, "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

// handleLogStream streams logs in real time via SSE.
// GET /runs/{id}/steps/{step}/logs/stream
//
// Events:
//
//	data: <LogLine JSON>                             — new log line
//	event: done\ndata: {"status":"success"|"failed"} — run finished
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
			lines, err := h.p.logs.Query(runID, stepName, afterID)
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

			// If the run has ended, flush remaining logs and emit the done event
			run, err := h.store.GetRun(runID)
			if err == nil && run != nil && run.Status != "running" {
				if tail, err2 := h.p.logs.Query(runID, stepName, afterID); err2 == nil {
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

// GET  /services       — list services
// POST /services       — deploy a ModelService from YAML
func (h *apiHandler) handleServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		svcs, err := h.store.ListServices()
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, svcs)

	case http.MethodPost:
		var req struct {
			YAML string `json:"yaml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		svc, err := h.p.DeployService(r.Context(), []byte(req.YAML))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, svc)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET    /services/{name}         — get service status
// DELETE /services/{name}         — stop and delete service
// POST   /services/{name}/restart — restart service
func (h *apiHandler) handleServiceSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/services/")
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	if name == "" {
		jsonErr(w, "service name required", http.StatusBadRequest)
		return
	}

	// POST /services/{name}/restart
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "restart" {
		if err := h.p.RestartService(r.Context(), name); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case http.MethodGet:
		svc, err := h.store.GetService(name)
		if err != nil || svc == nil {
			jsonErr(w, "service not found", http.StatusNotFound)
			return
		}
		jsonOK(w, svc)

	case http.MethodDelete:
		if err := h.p.StopService(r.Context(), name); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.store.DeleteService(name); err != nil {
			slog.Warn("delete service record failed", "name", name, "err", err)
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
