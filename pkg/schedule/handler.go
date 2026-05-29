package schedule

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/secret"
)

// HandlerDeps holds all dependencies required by the schedule handler.
type HandlerDeps struct {
	Schedules Repository
	Runs      run.Repository
	Parse     func(yaml []byte) (*pipeline.Pipeline, error)
	Trigger   func(ctx context.Context, sc *Schedule)
	NextTime  func(expr string, from time.Time) (time.Time, error)
	Backfill  func(ctx context.Context, id string, from, to time.Time) ([]string, error)
	OwnerID   func(r *http.Request) string
	// GenID generates a unique schedule ID prefix (e.g. "sch-run-xxx").
	GenID func() string
}

// Handler is the Gin HTTP handler for the /schedules domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new schedule Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all /schedules routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/schedules", h.listSchedules)
	rg.POST("/schedules", h.createSchedule)
	rg.GET("/schedules/:id", h.getSchedule)
	rg.PATCH("/schedules/:id", h.patchSchedule)
	rg.DELETE("/schedules/:id", h.deleteSchedule)
	rg.POST("/schedules/:id/backfill", h.backfillSchedule)
	rg.GET("/schedules/:id/runs", h.listScheduleRuns)
}

// GET /schedules
func (h *Handler) listSchedules(c *gin.Context) {
	schedules, err := h.deps.Schedules.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ownerID := h.ownerID(c.Request)
	out := make([]*Schedule, 0, len(schedules))
	for _, sc := range schedules {
		if ownerID != "" && sc.OwnerID != "" && sc.OwnerID != ownerID {
			continue
		}
		out = append(out, redactSchedule(sc))
	}
	c.JSON(http.StatusOK, out)
}

// POST /schedules
func (h *Handler) createSchedule(c *gin.Context) {
	var req struct {
		Name    string         `json:"name"`
		YAML    string         `json:"yaml"`
		Type    string         `json:"type"` // immediate | once | cron
		Cron    string         `json:"cron"`
		RunAt   *time.Time     `json:"run_at,omitempty"`
		OwnerID string         `json:"owner_id,omitempty"`
		Params  map[string]any `json:"params,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Type == "" {
		req.Type = "immediate"
	}
	if req.Type != "immediate" && req.Type != "once" && req.Type != "cron" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be immediate, once, or cron"})
		return
	}

	var pl *pipeline.Pipeline
	if h.deps.Parse != nil {
		var err error
		pl, err = h.deps.Parse([]byte(req.YAML))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	now := time.Now().UTC()
	name := strings.TrimSpace(req.Name)
	if name == "" && pl != nil {
		name = pl.Metadata.Name
	}

	paramsJSON := "{}"
	if req.Params != nil {
		if b, err := json.Marshal(req.Params); err == nil {
			paramsJSON = string(b)
		}
	}

	id := "sch-" + time.Now().Format("20060102150405.000000000")
	if h.deps.GenID != nil {
		id = h.deps.GenID()
	}

	sc := &Schedule{
		ID:           id,
		Name:         name,
		OwnerID:      req.OwnerID,
		PipelineYAML: req.YAML,
		ScheduleType: req.Type,
		ParamsJSON:   paramsJSON,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if sc.OwnerID == "" {
		sc.OwnerID = h.ownerID(c.Request)
	}

	switch req.Type {
	case "cron":
		if strings.TrimSpace(req.Cron) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cron is required for type=cron"})
			return
		}
		if h.deps.NextTime == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "NextTime not configured"})
			return
		}
		nextRunAt, err := h.deps.NextTime(req.Cron, now)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cron expression"})
			return
		}
		sc.CronExpr = req.Cron
		sc.NextRunAt = nextRunAt

	case "once":
		if req.RunAt == nil || req.RunAt.IsZero() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "run_at is required for type=once"})
			return
		}
		sc.NextRunAt = req.RunAt.UTC()

	case "immediate":
		sc.NextRunAt = now
	}

	if err := h.deps.Schedules.Create(c.Request.Context(), sc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// For immediate type: trigger a run right now.
	if req.Type == "immediate" && h.deps.Trigger != nil {
		go h.deps.Trigger(c.Request.Context(), sc)
	}

	c.JSON(http.StatusOK, gin.H{"schedule_id": sc.ID})
}

// GET /schedules/:id
func (h *Handler) getSchedule(c *gin.Context) {
	id := c.Param("id")
	sc, err := h.deps.Schedules.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "schedule not found"})
		return
	}
	if !h.canAccess(c.Request, sc) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, redactSchedule(sc))
}

// PATCH /schedules/:id
func (h *Handler) patchSchedule(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled is required"})
		return
	}
	if sc, err := h.deps.Schedules.Get(c.Request.Context(), id); err != nil || !h.canAccess(c.Request, sc) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err := h.deps.Schedules.SetEnabled(c.Request.Context(), id, *req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "enabled": *req.Enabled})
}

// DELETE /schedules/:id
func (h *Handler) deleteSchedule(c *gin.Context) {
	id := c.Param("id")
	if sc, err := h.deps.Schedules.Get(c.Request.Context(), id); err != nil || !h.canAccess(c.Request, sc) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err := h.deps.Schedules.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// POST /schedules/:id/backfill
func (h *Handler) backfillSchedule(c *gin.Context) {
	id := c.Param("id")
	sc, err := h.deps.Schedules.Get(c.Request.Context(), id)
	if err != nil || !h.canAccess(c.Request, sc) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	var req struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.From.IsZero() || req.To.IsZero() || req.To.Before(req.From) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from and to must define a valid range"})
		return
	}
	if h.deps.Backfill == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Backfill not configured"})
		return
	}
	runIDs, err := h.deps.Backfill(c.Request.Context(), id, req.From, req.To)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_ids": runIDs})
}

// GET /schedules/:id/runs
func (h *Handler) listScheduleRuns(c *gin.Context) {
	id := c.Param("id")
	if sc, err := h.deps.Schedules.Get(c.Request.Context(), id); err != nil || !h.canAccess(c.Request, sc) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	runs, err := h.deps.Runs.List(c.Request.Context(), run.RunFilter{ScheduleID: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, r := range runs {
		r.PipelineYAML = secret.RedactString(r.PipelineYAML)
		r.ParamsJSON = secret.RedactString(r.ParamsJSON)
	}
	c.JSON(http.StatusOK, runs)
}

func (h *Handler) ownerID(r *http.Request) string {
	if h.deps.OwnerID == nil {
		return ""
	}
	return h.deps.OwnerID(r)
}

func (h *Handler) canAccess(r *http.Request, sc *Schedule) bool {
	if sc == nil {
		return false
	}
	ownerID := h.ownerID(r)
	return ownerID == "" || sc.OwnerID == "" || sc.OwnerID == ownerID
}

func redactSchedule(sc *Schedule) *Schedule {
	if sc == nil {
		return nil
	}
	cp := *sc
	cp.PipelineYAML = secret.RedactString(cp.PipelineYAML)
	cp.ParamsJSON = secret.RedactString(cp.ParamsJSON)
	return &cp
}
