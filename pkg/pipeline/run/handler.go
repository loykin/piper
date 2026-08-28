package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/statsstore"
)

// RunHooks provides pre-request authorization hooks.
type RunHooks interface {
	BeforeListRuns(ctx context.Context, r *http.Request) (RunFilter, error)
	BeforeCreateRun(ctx context.Context, r *http.Request, yaml string) error
	BeforeGetRun(ctx context.Context, r *http.Request, id string) error
	BeforeGetLogs(ctx context.Context, r *http.Request, runID, step string) error
}

// HandlerDeps holds all dependencies required by the run handler. All
// Run-domain data access goes through Member — the handler never holds a
// direct Repository/Queue reference (fed.md §11.3: Home must not access a
// Member's execution repository directly).
type HandlerDeps struct {
	Member memberclient.Client
	// ProjectRef resolves the :project_id path value to the ProjectRef Member
	// calls are scoped to. LocalRef for the single-install case.
	ProjectRef func(projectID string) project.ProjectRef
	Hooks      RunHooks
}

// Handler is the Gin HTTP handler for the /runs domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new run Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

func projectID(c *gin.Context) string {
	ctx, _ := project.FromContext(c.Request.Context())
	return ctx.ID
}

// authFrom builds the AuthContext Member receives, from the identity/role
// Home's own middleware already resolved for this request.
func authFrom(c *gin.Context) memberclient.AuthContext {
	pctx, _ := project.FromContext(c.Request.Context())
	auth := memberclient.AuthContext{Role: pctx.Role, IssuedAt: time.Now()}
	if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
		auth.ActorID = identity.ID
	}
	return auth
}

func (h *Handler) ref(c *gin.Context) project.ProjectRef {
	projectContext, _ := project.FromContext(c.Request.Context())
	ref := h.deps.ProjectRef(projectContext.ID)
	if projectContext.OwnerMemberID != "" {
		ref.MemberID = projectContext.OwnerMemberID
	}
	return ref
}

func writeMemberError(c *gin.Context, err error, fallbackStatus int, fallbackMessage string) {
	if errors.Is(err, memberclient.ErrMemberUnavailable) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": memberclient.ErrorCodeMemberUnavailable, "retryable": true})
		return
	}
	if errors.Is(err, statsstore.ErrBackendUnavailable) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "statistics backend unavailable", "code": memberclient.ErrorCodeStatsBackendUnavailable, "retryable": true})
		return
	}
	if errors.Is(err, memberclient.ErrStorageBackendMismatch) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": memberclient.ErrorCodeStorageBackendMismatch, "retryable": false})
		return
	}
	if fallbackMessage == "" {
		fallbackMessage = err.Error()
	}
	c.JSON(fallbackStatus, gin.H{"error": fallbackMessage})
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
	rg.GET("/stats/capabilities", h.statsCapabilities)
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

// runWithSteps flattens a run alongside its steps for the include_steps=true
// list shape — RunSummary's fields are promoted into the outer JSON object,
// matching the pre-memberclient run.Run-embedding shape exactly.
type runWithSteps struct {
	memberclient.RunSummary
	Steps []memberclient.StepSummary `json:"steps"`
}

// GET /runs
func (h *Handler) listRuns(c *gin.Context) {
	req, ok := h.resolveFilter(c)
	if !ok {
		return
	}
	req.Status = c.Query("status")
	req.Experiment = c.Query("experiment")
	req.MetricStep = c.Query("metric_step")
	req.MetricKey = c.Query("metric_key")
	req.MetricOrder = c.Query("metric_order")
	req.ScheduleID = c.Query("schedule_id")
	if pipelineName := c.Query("pipeline_name"); pipelineName != "" {
		if req.PipelineName != "" && req.PipelineName != pipelineName {
			c.JSON(http.StatusOK, []any{})
			return
		}
		req.PipelineName = pipelineName
	}
	if limit, err := strconv.Atoi(c.Query("limit")); err == nil && limit > 0 {
		req.Limit = limit
		if offset, err := strconv.Atoi(c.Query("offset")); err == nil && offset > 0 {
			req.Offset = offset
		}
	}
	req.IncludeSteps = c.Query("include_steps") == "true" || c.Query("include_steps") == "1"

	resp, err := h.deps.Member.ListRuns(c.Request.Context(), authFrom(c), h.ref(c), req)
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	if req.Limit > 0 {
		c.Header("X-Total-Count", strconv.Itoa(resp.Total))
	}

	if !req.IncludeSteps {
		c.JSON(http.StatusOK, resp.Runs)
		return
	}
	result := make([]runWithSteps, 0, len(resp.Runs))
	for _, r := range resp.Runs {
		steps := resp.Steps[r.ID]
		if steps == nil {
			steps = []memberclient.StepSummary{}
		}
		result = append(result, runWithSteps{RunSummary: r, Steps: steps})
	}
	c.JSON(http.StatusOK, result)
}

// POST /runs
func (h *Handler) createRun(c *gin.Context) {
	var body struct {
		YAML       string            `json:"yaml"`
		Params     map[string]any    `json:"params,omitempty"`
		Experiment string            `json:"experiment,omitempty"`
		Vars       proto.BuiltinVars `json:"vars,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeCreateRun(c.Request.Context(), c.Request, body.YAML); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Idempotency-Key must be at most 128 characters"})
		return
	}
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}

	resp, err := h.deps.Member.SubmitRun(c.Request.Context(), authFrom(c), h.ref(c), memberclient.SubmitRunRequest{
		IdempotencyKey: idempotencyKey, YAML: body.YAML, Params: body.Params, Experiment: body.Experiment, Vars: body.Vars,
	})
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.Header("Idempotency-Key", idempotencyKey)
	c.JSON(http.StatusCreated, gin.H{"run_id": resp.RunID})
}

// POST /runs/sweep
func (h *Handler) createSweep(c *gin.Context) {
	var body SweepRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Experiment == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "experiment is required"})
		return
	}
	if len(body.Runs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runs must not be empty"})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeCreateRun(c.Request.Context(), c.Request, body.YAML); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	trials := make([]memberclient.SweepTrial, 0, len(body.Runs))
	for _, t := range body.Runs {
		trials = append(trials, memberclient.SweepTrial{Params: t.Params})
	}
	resp, err := h.deps.Member.SubmitSweep(c.Request.Context(), authFrom(c), h.ref(c), memberclient.SubmitSweepRequest{
		YAML: body.YAML, Experiment: body.Experiment, Runs: trials,
	})
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"experiment": resp.Experiment, "run_ids": resp.RunIDs})
}

// GET /runs/:id
func (h *Handler) getRun(c *gin.Context) {
	runID := c.Param("id")
	detail, err := h.deps.Member.GetRun(c.Request.Context(), authFrom(c), h.ref(c), runID)
	if err != nil {
		writeMemberError(c, err, http.StatusNotFound, "run not found")
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(c.Request.Context(), c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"run": detail.Run, "steps": detail.Steps})
}

// POST /runs/:id/cancel
func (h *Handler) cancelRun(c *gin.Context) {
	runID := c.Param("id")
	ctx := c.Request.Context()
	auth, ref := authFrom(c), h.ref(c)

	detail, err := h.deps.Member.GetRun(ctx, auth, ref, runID)
	if err != nil {
		writeMemberError(c, err, http.StatusNotFound, "run not found")
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(ctx, c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	switch detail.Run.Status {
	case StatusCanceled, StatusSuccess, StatusFailed:
		c.JSON(http.StatusOK, gin.H{"status": detail.Run.Status})
		return
	}
	if err := h.deps.Member.CancelRun(ctx, auth, ref, runID); err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": StatusCanceled})
}

// POST /runs/:id/rerun
func (h *Handler) rerunRun(c *gin.Context) {
	runID := c.Param("id")
	ctx := c.Request.Context()
	auth, ref := authFrom(c), h.ref(c)
	var body struct {
		FailedOnly bool `json:"failed_only"`
	}
	_ = c.ShouldBindJSON(&body)

	if _, err := h.deps.Member.GetRun(ctx, auth, ref, runID); err != nil {
		writeMemberError(c, err, http.StatusNotFound, "run not found")
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(ctx, c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	newRunID, err := h.deps.Member.RerunRun(ctx, auth, ref, runID, body.FailedOnly)
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"run_id": newRunID})
}

// DELETE /runs/:id
func (h *Handler) deleteRun(c *gin.Context) {
	runID := c.Param("id")
	ctx := c.Request.Context()
	auth, ref := authFrom(c), h.ref(c)

	detail, err := h.deps.Member.GetRun(ctx, auth, ref, runID)
	if err != nil {
		writeMemberError(c, err, http.StatusNotFound, "run not found")
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(ctx, c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if detail.Run.Status == StatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete a running run"})
		return
	}
	if err := h.deps.Member.DeleteRun(ctx, auth, ref, runID); err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /runs/:id/steps
func (h *Handler) listSteps(c *gin.Context) {
	steps, err := h.deps.Member.ListSteps(c.Request.Context(), authFrom(c), h.ref(c), c.Param("id"))
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.JSON(http.StatusOK, steps)
}

// POST /runs/:id/steps/:step/retry
func (h *Handler) retryStep(c *gin.Context) {
	runID := c.Param("id")
	stepName := c.Param("step")
	ctx := c.Request.Context()
	auth, ref := authFrom(c), h.ref(c)

	if _, err := h.deps.Member.GetRun(ctx, auth, ref, runID); err != nil {
		writeMemberError(c, err, http.StatusNotFound, "run not found")
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetRun(ctx, c.Request, runID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	newRunID, err := h.deps.Member.RetryStep(ctx, auth, ref, runID, stepName)
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"run_id": newRunID})
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
	req, err := logQueryFromRequest(c, runID, stepName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, err := h.deps.Member.QueryLogs(c.Request.Context(), authFrom(c), h.ref(c), req)
	if err != nil {
		if errors.Is(err, statsstore.ErrInvalidCursor) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	if page.Lines == nil {
		page.Lines = []*statsstore.LogLine{}
	}
	if page.NextCursor != "" {
		c.Header("X-Next-Cursor", page.NextCursor)
	}
	c.JSON(http.StatusOK, page.Lines)
}

func logQueryFromRequest(c *gin.Context, runID, stepName string) (memberclient.QueryLogsRequest, error) {
	req := memberclient.QueryLogsRequest{RunID: runID, StepName: stepName, Cursor: c.Query("cursor"), Search: c.Query("search")}
	if raw := c.Query("after_id"); raw != "" {
		afterID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || afterID < 0 {
			return req, fmt.Errorf("after_id must be a non-negative integer")
		}
		req.AfterID = afterID
	}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return req, fmt.Errorf("limit must be a positive integer")
		}
		req.Limit = limit
	}
	for name, target := range map[string]*time.Time{"since": &req.Since, "until": &req.Until} {
		if value := c.Query(name); value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return req, fmt.Errorf("%s must be an RFC3339 timestamp", name)
			}
			*target = parsed
		}
	}
	if !req.Since.IsZero() && !req.Until.IsZero() && req.Since.After(req.Until) {
		return req, fmt.Errorf("since must not be after until")
	}
	return req, nil
}

func (h *Handler) statsCapabilities(c *gin.Context) {
	capabilities, err := h.deps.Member.StatsCapabilities(c.Request.Context(), authFrom(c), h.ref(c))
	if err != nil {
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	c.JSON(http.StatusOK, capabilities)
}

// GET /runs/:id/steps/:step/logs/stream  — SSE
func (h *Handler) streamLogs(c *gin.Context) {
	runID := c.Param("id")
	stepName := c.Param("step")
	auth, ref := authFrom(c), h.ref(c)
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

	cursor := c.GetHeader("Last-Event-ID")
	var afterID int64
	if cursor == "" {
		cursor = c.Query("cursor")
	}
	if cursor == "" {
		afterID, _ = strconv.ParseInt(c.Query("after_id"), 10, 64)
	} else if legacyID, err := strconv.ParseInt(cursor, 10, 64); err == nil {
		afterID = legacyID
		cursor = ""
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case <-ticker.C:
			page, err := h.deps.Member.QueryLogs(c.Request.Context(), auth, ref, memberclient.QueryLogsRequest{RunID: runID, StepName: stepName, Cursor: cursor, AfterID: afterID})
			if err != nil {
				code := "stats_backend_unavailable"
				message := "statistics backend unavailable"
				if errors.Is(err, memberclient.ErrMemberUnavailable) {
					code = "member_unavailable"
					message = "member unavailable"
				} else if errors.Is(err, statsstore.ErrInvalidCursor) {
					code = "invalid_cursor"
					message = "invalid statistics cursor"
				}
				payload, _ := json.Marshal(gin.H{
					"code":      code,
					"message":   message,
					"retryable": true,
				})
				_, _ = fmt.Fprintf(w, "event: stats_error\ndata: %s\n\n", payload)
				return false
			}
			for _, l := range page.Lines {
				b, _ := json.Marshal(l)
				cursor = statsstore.CursorForLogQuery(l.ID, statsstore.LogQuery{ProjectID: ref.ProjectID, RunID: runID, StepName: stepName})
				_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", cursor, b)
				afterID = l.ID
			}

			// Check if run has ended
			detail, err := h.deps.Member.GetRun(c.Request.Context(), auth, ref, runID)
			if err == nil && detail.Run.Status != StatusRunning {
				// Flush remaining logs
				if tail, err2 := h.deps.Member.QueryLogs(c.Request.Context(), auth, ref, memberclient.QueryLogsRequest{RunID: runID, StepName: stepName, Cursor: cursor, AfterID: afterID}); err2 == nil {
					for _, l := range tail.Lines {
						b, _ := json.Marshal(l)
						cursor = statsstore.CursorForLogQuery(l.ID, statsstore.LogQuery{ProjectID: ref.ProjectID, RunID: runID, StepName: stepName})
						_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", cursor, b)
						afterID = l.ID
					}
				}
				_, _ = fmt.Fprintf(w, "event: done\ndata: {\"status\":%q}\n\n", detail.Run.Status)
				return false
			}
			return true
		}
	})
}

func (h *Handler) getMetrics(c *gin.Context) {
	req := memberclient.QueryMetricsRequest{RunID: c.Param("id"), StepName: c.Query("step"), Keys: c.QueryArray("key"), Cursor: c.Query("cursor")}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		req.Limit = limit
	}
	for name, target := range map[string]*time.Time{"since": &req.Since, "until": &req.Until} {
		if value := c.Query(name); value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s must be an RFC3339 timestamp", name)})
				return
			}
			*target = parsed
		}
	}
	if !req.Since.IsZero() && !req.Until.IsZero() && req.Since.After(req.Until) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "since must not be after until"})
		return
	}
	page, err := h.deps.Member.QueryMetrics(c.Request.Context(), authFrom(c), h.ref(c), req)
	if err != nil {
		if errors.Is(err, statsstore.ErrInvalidCursor) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	if page.NextCursor != "" {
		c.Header("X-Next-Cursor", page.NextCursor)
	}
	if page.Points == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	c.JSON(http.StatusOK, page.Points)
}

// GET /runs/:id/artifacts
func (h *Handler) listArtifacts(c *gin.Context) {
	result, err := h.deps.Member.ListArtifacts(c.Request.Context(), authFrom(c), h.ref(c), c.Param("id"))
	if err != nil {
		slog.Warn("list artifacts failed", "run_id", c.Param("id"), "err", err)
		writeMemberError(c, err, http.StatusInternalServerError, "")
		return
	}
	if result == nil {
		result = []any{}
	}
	c.JSON(http.StatusOK, result)
}

// GET /runs/:id/artifacts/*path
func (h *Handler) downloadArtifact(c *gin.Context) {
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
	h.deps.Member.ServeArtifact(c.Request.Context(), authFrom(c), h.ref(c), c.Writer, c.Request, runID, step, rest)
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

// resolveFilter returns the effective ListRunsRequest for a list request,
// applying the BeforeListRuns hook when present.
// Returns (zero value, false) if the hook rejected the request (response already written).
func (h *Handler) resolveFilter(c *gin.Context) (memberclient.ListRunsRequest, bool) {
	if h.deps.Hooks == nil {
		return memberclient.ListRunsRequest{}, true
	}
	f, err := h.deps.Hooks.BeforeListRuns(c.Request.Context(), c.Request)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return memberclient.ListRunsRequest{}, false
	}
	return memberclient.ListRunsRequest{PipelineName: f.PipelineName}, true
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
