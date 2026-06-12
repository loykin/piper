package pipelineworker

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/proto"
)

// TaskQueuer abstracts the task queue for the worker handler.
type TaskQueuer interface {
	Complete(ctx context.Context, result proto.TaskResult) error
}

// HandlerDeps holds all dependencies required by the worker handler.
type HandlerDeps struct {
	Queue TaskQueuer
}

// Handler exposes authenticated worker callbacks.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new worker Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterCompletionRoutes mounts the task done/failed reporting routes.
// These callbacks are protected by worker authentication.
func (h *Handler) RegisterCompletionRoutes(rg *gin.RouterGroup) {
	rg.POST("/tasks/:id/done", h.taskDone)
	rg.POST("/tasks/:id/failed", h.taskFailed)
}

// POST /api/tasks/:id/done
func (h *Handler) taskDone(c *gin.Context) {
	h.handleTaskComplete(c, "done")
}

// POST /api/tasks/:id/failed
func (h *Handler) taskFailed(c *gin.Context) {
	h.handleTaskComplete(c, "failed")
}

func (h *Handler) handleTaskComplete(c *gin.Context, action string) {
	taskID := c.Param("id")
	var body struct {
		Error     string    `json:"error,omitempty"`
		WorkerID  string    `json:"worker_id,omitempty"`
		StartedAt time.Time `json:"started_at"`
		EndedAt   time.Time `json:"ended_at"`
		Attempt   int       `json:"attempt"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.deps.Queue.Complete(c.Request.Context(), proto.TaskResult{
		TaskID:    taskID,
		WorkerID:  body.WorkerID,
		Status:    action,
		Error:     body.Error,
		StartedAt: body.StartedAt,
		EndedAt:   body.EndedAt,
		Attempt:   body.Attempt,
	}); err != nil {
		slog.Warn("task complete error", "task_id", taskID, "err", err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}
