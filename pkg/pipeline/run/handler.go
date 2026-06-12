package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

// LogQuerier abstracts log storage for the run handler.
type LogQuerier interface {
	Query(projectID, runID, stepName string, afterID int64) ([]*logstore.Line, error)
	Append(lines []*logstore.Line) error
}

// ArtifactStore abstracts artifact storage for the run handler.
type ArtifactStore interface {
	List(runID string) ([]string, error)
	Download(runID, step, file string) (io.ReadCloser, error)
}

// RunHooks provides pre-request authorization hooks.
type RunHooks interface {
	BeforeListRuns(ctx context.Context, r *http.Request) (RunFilter, error)
	BeforeCreateRun(ctx context.Context, r *http.Request, yaml string) error
	BeforeGetRun(ctx context.Context, r *http.Request, id string) error
	BeforeGetLogs(ctx context.Context, r *http.Request, runID, step string) error
}

// HandlerDeps holds all dependencies required by the run handler.
type HandlerDeps struct {
	Runs       Repository
	Steps      StepRepository
	Logs       LogQuerier
	Metrics    logstore.MetricStore
	StartRun   func(ctx context.Context, yaml string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error)
	StartSweep func(ctx context.Context, req SweepRequest) (SweepResponse, error)
	CancelRun  func(ctx context.Context, runID string) error
	RerunRun   func(ctx context.Context, runID string, failedOnly bool) (string, error)
	RetryStep  func(ctx context.Context, runID, stepName string) (string, error)
	DeleteRun  func(ctx context.Context, runID string) error
	Artifacts  ArtifactProvider
	Hooks      RunHooks
}

// ArtifactProvider is the domain interface for artifact listing and download.
// Implementations must not depend on gin.Context.
type ArtifactProvider interface {
	// List returns the artifact tree for a run as a JSON-serialisable value.
	List(ctx context.Context, runID string) ([]any, error)
	// ServeDownload streams an artifact file directly to the http.ResponseWriter.
	ServeDownload(w http.ResponseWriter, r *http.Request, runID, step, path string)
}

// Handler is the Gin HTTP handler for the /runs domain.
type Handler struct {
	deps HandlerDeps
}

type ingestedLogEntry struct {
	Ts     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

// NewHandler creates a new run Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

func projectID(c *gin.Context) string {
	ctx, _ := project.FromContext(c.Request.Context())
	return ctx.ID
}

// RegisterRoutes mounts all /runs routes onto the given router group.
// Read routes are accessible to viewers; write routes require member role.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	// Viewer routes
	rg.GET("/runs", h.listRuns)
	rg.GET("/runs/:id", h.getRun)
	rg.GET("/runs/:id/steps", h.listSteps)
	rg.GET("/runs/:id/steps/:step/logs", h.getLogs)
	rg.GET("/runs/:id/steps/:step/logs/stream", h.streamLogs)
	rg.GET("/runs/:id/metrics", h.getMetrics)
	rg.GET("/runs/:id/artifacts", h.listArtifacts)
	rg.GET("/runs/:id/artifacts/*path", h.downloadArtifact)

	// Member routes
	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.POST("/runs", h.createRun)
	member.POST("/runs/sweep", h.createSweep)
	member.POST("/runs/:id/cancel", h.cancelRun)
	member.POST("/runs/:id/rerun", h.rerunRun)
	member.DELETE("/runs/:id", h.deleteRun)
	member.POST("/runs/:id/steps/:step/retry", h.retryStep)
}

// RegisterWorkerRoutes mounts infrastructure callbacks used by pipeline workers.
// The caller must apply worker authentication and project resolution middleware.
func (h *Handler) RegisterWorkerRoutes(rg *gin.RouterGroup) {
	rg.POST("/runs/:id/steps/:step/logs", h.ingestLogs)
	rg.POST("/runs/:id/steps/:step/final-metrics", h.ingestFinalMetrics)
}

// GET /runs
func (h *Handler) listRuns(c *gin.Context) {
	filter, ok := h.resolveFilter(c)
	if !ok {
		return
	}
	filter.Status = c.Query("status")
	filter.Experiment = c.Query("experiment")
	filter.MetricStep = c.Query("metric_step")
	filter.MetricKey = c.Query("metric_key")
	filter.MetricOrder = c.Query("metric_order")
	if pipelineName := c.Query("pipeline_name"); pipelineName != "" {
		if filter.PipelineName != "" && filter.PipelineName != pipelineName {
			c.JSON(http.StatusOK, []any{})
			return
		}
		filter.PipelineName = pipelineName
	}

	projectID := projectID(c)
	runs, err := h.deps.Runs.List(c.Request.Context(), projectID, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type runWithSteps struct {
		*Run
		Steps []*Step `json:"steps"`
	}
	result := make([]runWithSteps, 0, len(runs))
	for _, r := range runs {
		r = r.Redact()
		steps, _ := h.deps.Steps.List(c.Request.Context(), projectID, r.ID)
		if steps == nil {
			steps = []*Step{}
		}
		result = append(result, runWithSteps{Run: r, Steps: steps})
	}
	c.JSON(http.StatusOK, result)
}

// POST /runs
func (h *Handler) createRun(c *gin.Context) {
	var req struct {
		YAML       string            `json:"yaml"`
		Params     map[string]any    `json:"params,omitempty"`
		Experiment string            `json:"experiment,omitempty"`
		Vars       proto.BuiltinVars `json:"vars,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeCreateRun(c.Request.Context(), c.Request, req.YAML); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}

	if h.deps.StartRun == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "StartRun not configured"})
		return
	}

	runID, err := h.deps.StartRun(c.Request.Context(), req.YAML, req.Params, req.Vars, req.Experiment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": runID})
}

// POST /runs/sweep
func (h *Handler) createSweep(c *gin.Context) {
	var req SweepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Experiment == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "experiment is required"})
		return
	}
	if len(req.Runs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runs must not be empty"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeCreateRun(c.Request.Context(), c.Request, req.YAML); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if h.deps.StartSweep == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "StartSweep not configured"})
		return
	}
	resp, err := h.deps.StartSweep(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GET /runs/:id
func (h *Handler) getRun(c *gin.Context) {
	runID := c.Param("id")
	projectID := projectID(c)
	r, err := h.deps.Runs.Get(c.Request.Context(), projectID, runID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(c.Request.Context(), c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	steps, err := h.deps.Steps.List(c.Request.Context(), projectID, runID)
	if err != nil {
		slog.Warn("list steps failed", "run_id", runID, "err", err)
	}
	c.JSON(http.StatusOK, gin.H{"run": r.Redact(), "steps": steps})
}

// POST /runs/:id/cancel
func (h *Handler) cancelRun(c *gin.Context) {
	runID := c.Param("id")
	r, err := h.deps.Runs.Get(c.Request.Context(), projectID(c), runID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(c.Request.Context(), c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	switch r.Status {
	case StatusCanceled, StatusSuccess, StatusFailed:
		c.JSON(http.StatusOK, gin.H{"status": r.Status})
		return
	}
	if h.deps.CancelRun == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "CancelRun not configured"})
		return
	}
	if err := h.deps.CancelRun(c.Request.Context(), runID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": StatusCanceled})
}

// POST /runs/:id/rerun
func (h *Handler) rerunRun(c *gin.Context) {
	runID := c.Param("id")
	var req struct {
		FailedOnly bool `json:"failed_only"`
	}
	_ = c.ShouldBindJSON(&req)

	r, err := h.deps.Runs.Get(c.Request.Context(), projectID(c), runID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(c.Request.Context(), c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if h.deps.RerunRun == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "RerunRun not configured"})
		return
	}
	newRunID, err := h.deps.RerunRun(c.Request.Context(), runID, req.FailedOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": newRunID})
}

// DELETE /runs/:id
func (h *Handler) deleteRun(c *gin.Context) {
	runID := c.Param("id")
	r, err := h.deps.Runs.Get(c.Request.Context(), projectID(c), runID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(c.Request.Context(), c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if r.Status == StatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete a running run"})
		return
	}
	if h.deps.DeleteRun != nil {
		if err := h.deps.DeleteRun(c.Request.Context(), runID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.Status(http.StatusNoContent)
}

// GET /runs/:id/steps
func (h *Handler) listSteps(c *gin.Context) {
	runID := c.Param("id")
	steps, err := h.deps.Steps.List(c.Request.Context(), projectID(c), runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, steps)
}

// POST /runs/:id/steps/:step/retry
func (h *Handler) retryStep(c *gin.Context) {
	runID := c.Param("id")
	stepName := c.Param("step")
	r, err := h.deps.Runs.Get(c.Request.Context(), projectID(c), runID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(c.Request.Context(), c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if h.deps.RetryStep == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "RetryStep not configured"})
		return
	}
	newRunID, err := h.deps.RetryStep(c.Request.Context(), runID, stepName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": newRunID})
}

// GET /runs/:id/steps/:step/logs
func (h *Handler) getLogs(c *gin.Context) {
	runID := c.Param("id")
	stepName := c.Param("step")
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetLogs(c.Request.Context(), c.Request, runID, stepName); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	afterID, _ := strconv.ParseInt(c.Query("after"), 10, 64)
	lines, err := h.deps.Logs.Query(projectID(c), runID, stepName, afterID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, lines)
}

// GET /runs/:id/steps/:step/logs/stream  — SSE
func (h *Handler) streamLogs(c *gin.Context) {
	runID := c.Param("id")
	stepName := c.Param("step")
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetLogs(c.Request.Context(), c.Request, runID, stepName); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	var afterID int64
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case <-ticker.C:
			lines, err := h.deps.Logs.Query(projectID(c), runID, stepName, afterID)
			if err != nil {
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				return false
			}
			for _, l := range lines {
				b, _ := json.Marshal(l)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				afterID = l.ID
			}

			// Check if run has ended
			runRec, err := h.deps.Runs.Get(c.Request.Context(), projectID(c), runID)
			if err == nil && runRec != nil && runRec.Status != StatusRunning {
				// Flush remaining logs
				if tail, err2 := h.deps.Logs.Query(projectID(c), runID, stepName, afterID); err2 == nil {
					for _, l := range tail {
						b, _ := json.Marshal(l)
						_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
					}
				}
				_, _ = fmt.Fprintf(w, "event: done\ndata: {\"status\":%q}\n\n", runRec.Status)
				return false
			}
			return true
		}
	})
}

// POST /runs/:id/steps/:step/logs  — ingest worker logs
func (h *Handler) ingestLogs(c *gin.Context) {
	ref := stepRef{RunID: c.Param("id"), StepName: c.Param("step")}
	if !h.workerRunExists(c, ref.RunID) {
		return
	}

	var entries []ingestedLogEntry
	if err := c.ShouldBindJSON(&entries); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lines := make([]*logstore.Line, len(entries))
	var metrics []*logstore.Metric
	currentProjectID := projectID(c)
	for i, e := range entries {
		lines[i] = &logstore.Line{
			ProjectID: currentProjectID,
			RunID:     ref.RunID,
			StepName:  ref.StepName,
			Ts:        e.Ts,
			Stream:    e.Stream,
			Line:      e.Line,
		}
		if metric, ok := parseMetricLine(ref, e); ok {
			metric.ProjectID = currentProjectID
			metrics = append(metrics, metric)
		}
	}
	if err := h.deps.Logs.Append(lines); err != nil {
		slog.Warn("append logs failed", "run_id", ref.RunID, "step", ref.StepName, "err", err)
	}
	if h.deps.Metrics != nil {
		if err := h.deps.Metrics.AppendMetrics(metrics); err != nil {
			slog.Warn("append metrics failed", "run_id", ref.RunID, "step", ref.StepName, "err", err)
		}
	}
	c.Status(http.StatusOK)
}

// POST /runs/:id/steps/:step/final-metrics — ingest .metrics.json from worker
func (h *Handler) ingestFinalMetrics(c *gin.Context) {
	if h.deps.Metrics == nil {
		c.Status(http.StatusOK)
		return
	}
	runID := c.Param("id")
	stepName := c.Param("step")
	if !h.workerRunExists(c, runID) {
		return
	}

	var vals map[string]float64
	if err := c.ShouldBindJSON(&vals); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().UTC()
	metrics := make([]*logstore.Metric, 0, len(vals))
	for k, v := range vals {
		metrics = append(metrics, &logstore.Metric{
			ProjectID: projectID(c),
			RunID:     runID,
			StepName:  stepName,
			Key:       k,
			Value:     v,
			Ts:        now,
		})
	}
	if err := h.deps.Metrics.AppendMetrics(metrics); err != nil {
		slog.Warn("final metrics ingest failed", "run_id", runID, "step", stepName, "err", err)
	}
	c.Status(http.StatusOK)
}

func (h *Handler) workerRunExists(c *gin.Context, runID string) bool {
	if h.deps.Runs == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "run repository not configured"})
		return false
	}
	runRec, err := h.deps.Runs.Get(c.Request.Context(), projectID(c), runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if runRec == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return false
	}
	return true
}

func (h *Handler) getMetrics(c *gin.Context) {
	if h.deps.Metrics == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	metrics, err := h.deps.Metrics.QueryMetrics(projectID(c), c.Param("id"), c.Query("step"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, metrics)
}

type stepRef struct {
	RunID    string
	StepName string
}

func parseMetricLine(ref stepRef, entry ingestedLogEntry) (*logstore.Metric, bool) {
	line := strings.TrimSpace(entry.Line)
	if !strings.HasPrefix(line, "PIPER_METRIC ") {
		return nil, false
	}
	key, rawValue, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "PIPER_METRIC ")), "=")
	if !ok {
		return nil, false
	}
	key = strings.TrimSpace(key)
	value, err := strconv.ParseFloat(strings.TrimSpace(rawValue), 64)
	if key == "" || err != nil {
		return nil, false
	}
	return &logstore.Metric{RunID: ref.RunID, StepName: ref.StepName, Key: key, Value: value, Ts: entry.Ts}, true
}

// GET /runs/:id/artifacts
func (h *Handler) listArtifacts(c *gin.Context) {
	if h.deps.Artifacts == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	result, err := h.deps.Artifacts.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		slog.Warn("list artifacts failed", "run_id", c.Param("id"), "err", err)
	}
	if result == nil {
		result = []any{}
	}
	c.JSON(http.StatusOK, result)
}

// GET /runs/:id/artifacts/*path
func (h *Handler) downloadArtifact(c *gin.Context) {
	if h.deps.Artifacts == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact storage not configured"})
		return
	}
	runID := c.Param("id")
	// *path starts with "/" — strip it and split into step/rest
	fullPath := c.Param("path")
	if len(fullPath) > 0 && fullPath[0] == '/' {
		fullPath = fullPath[1:]
	}
	// split: first segment = step, remainder = rest
	parts := splitN(fullPath, "/", 2)
	step := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	h.deps.Artifacts.ServeDownload(c.Writer, c.Request, runID, step, rest)
}

// splitN splits s by sep up to n parts (similar to strings.SplitN).
func splitN(s, sep string, n int) []string {
	if n == 0 {
		return nil
	}
	result := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := indexOf(s, sep)
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	result = append(result, s)
	return result
}

// resolveFilter returns the effective RunFilter for a list request, applying
// the BeforeListRuns hook when present.
// Returns (RunFilter{}, false) if the hook rejected the request (response already written).
func (h *Handler) resolveFilter(c *gin.Context) (RunFilter, bool) {
	if h.deps.Hooks == nil {
		return RunFilter{}, true
	}
	f, err := h.deps.Hooks.BeforeListRuns(c.Request.Context(), c.Request)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return RunFilter{}, false
	}
	return f, true
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
