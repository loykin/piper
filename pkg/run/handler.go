package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/proto"
)

// LogQuerier abstracts log storage for the run handler.
type LogQuerier interface {
	Query(runID, stepName string, afterID int64) ([]*logstore.Line, error)
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
	Runs      Repository
	Steps     StepRepository
	Logs      LogQuerier
	StartRun  func(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars) (string, error)
	DeleteRun func(ctx context.Context, runID string) error
	Artifacts ArtifactListDownloader
	Hooks     RunHooks
}

// ArtifactListDownloader is a lower-level interface used by the run handler
// to avoid pulling in the full artifact implementation as a dependency.
// The root piper package provides the concrete wiring.
type ArtifactListDownloader interface {
	// HandleList writes the artifact listing JSON to the gin context.
	HandleList(c *gin.Context, runID string)
	// HandleDownload streams an artifact file to the gin context.
	HandleDownload(c *gin.Context, runID, step, rest string)
}

// Handler is the Gin HTTP handler for the /runs domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new run Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all /runs routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/runs", h.listRuns)
	rg.POST("/runs", h.createRun)
	rg.GET("/runs/:id", h.getRun)
	rg.DELETE("/runs/:id", h.deleteRun)
	rg.GET("/runs/:id/steps", h.listSteps)
	rg.GET("/runs/:id/steps/:step/logs", h.getLogs)
	rg.GET("/runs/:id/steps/:step/logs/stream", h.streamLogs)
	rg.POST("/runs/:id/steps/:step/logs", h.ingestLogs)
	rg.GET("/runs/:id/artifacts", h.listArtifacts)
	rg.GET("/runs/:id/artifacts/*path", h.downloadArtifact)
}

// GET /runs
func (h *Handler) listRuns(c *gin.Context) {
	filter := RunFilter{}
	if h.deps.Hooks != nil {
		f, err := h.deps.Hooks.BeforeListRuns(c.Request.Context(), c.Request)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		filter = f
	}
	filter.Status = c.Query("status")

	runs, err := h.deps.Runs.List(c.Request.Context(), filter)
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
		steps, _ := h.deps.Steps.List(c.Request.Context(), r.ID)
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
		YAML    string            `json:"yaml"`
		Params  map[string]any    `json:"params,omitempty"`
		OwnerID string            `json:"owner_id,omitempty"`
		Vars    proto.BuiltinVars `json:"vars,omitempty"`
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

	runID, err := h.deps.StartRun(c.Request.Context(), req.YAML, req.OwnerID, req.Params, req.Vars)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": runID})
}

// GET /runs/:id
func (h *Handler) getRun(c *gin.Context) {
	runID := c.Param("id")
	r, err := h.deps.Runs.Get(c.Request.Context(), runID)
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
	steps, err := h.deps.Steps.List(c.Request.Context(), runID)
	if err != nil {
		slog.Warn("list steps failed", "run_id", runID, "err", err)
	}
	c.JSON(http.StatusOK, gin.H{"run": r, "steps": steps})
}

// DELETE /runs/:id
func (h *Handler) deleteRun(c *gin.Context) {
	runID := c.Param("id")
	r, err := h.deps.Runs.Get(c.Request.Context(), runID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
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
	steps, err := h.deps.Steps.List(c.Request.Context(), runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, steps)
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
	lines, err := h.deps.Logs.Query(runID, stepName, afterID)
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
			lines, err := h.deps.Logs.Query(runID, stepName, afterID)
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
			runRec, err := h.deps.Runs.Get(c.Request.Context(), runID)
			if err == nil && runRec != nil && runRec.Status != StatusRunning {
				// Flush remaining logs
				if tail, err2 := h.deps.Logs.Query(runID, stepName, afterID); err2 == nil {
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
	runID := c.Param("id")
	stepName := c.Param("step")

	type logEntry struct {
		Ts     time.Time `json:"ts"`
		Stream string    `json:"stream"`
		Line   string    `json:"line"`
	}
	var entries []logEntry
	if err := c.ShouldBindJSON(&entries); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	if err := h.deps.Logs.Append(lines); err != nil {
		slog.Warn("append logs failed", "run_id", runID, "step", stepName, "err", err)
	}
	c.Status(http.StatusOK)
}

// GET /runs/:id/artifacts
func (h *Handler) listArtifacts(c *gin.Context) {
	if h.deps.Artifacts != nil {
		h.deps.Artifacts.HandleList(c, c.Param("id"))
		return
	}
	c.JSON(http.StatusOK, []any{})
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
	h.deps.Artifacts.HandleDownload(c, runID, step, rest)
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

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
