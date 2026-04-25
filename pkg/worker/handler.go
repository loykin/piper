package worker

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/proto"
)

// TaskQueuer abstracts the task queue for the worker handler.
type TaskQueuer interface {
	Next(label string) *proto.Task
	Complete(ctx context.Context, taskID, action, errMsg string, startedAt, endedAt time.Time, attempts int) error
}

// HandlerDeps holds all dependencies required by the worker handler.
type HandlerDeps struct {
	Registry *Registry
	Queue    TaskQueuer
}

// Handler is the Gin HTTP handler for the /api/workers and /api/tasks domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new worker Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all worker and task routes onto the given router group.
// rg should be pre-configured as the /api group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/workers", h.registerWorker)
	rg.GET("/workers", h.listWorkers)
	rg.POST("/workers/:id/heartbeat", h.workerHeartbeat)
	rg.GET("/tasks/next", h.taskNext)
	rg.POST("/tasks/:id/done", h.taskDone)
	rg.POST("/tasks/:id/failed", h.taskFailed)
}

// POST /api/workers
func (h *Handler) registerWorker(c *gin.Context) {
	var info Info
	if err := c.ShouldBindJSON(&info); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if info.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	h.deps.Registry.Register(info)
	slog.Info("worker registered", "id", info.ID, "label", info.Label, "hostname", info.Hostname)
	c.JSON(http.StatusOK, gin.H{"worker_id": info.ID})
}

// GET /api/workers
func (h *Handler) listWorkers(c *gin.Context) {
	c.JSON(http.StatusOK, h.deps.Registry.List())
}

// POST /api/workers/:id/heartbeat
func (h *Handler) workerHeartbeat(c *gin.Context) {
	workerID := c.Param("id")
	var body struct {
		InFlight int `json:"in_flight"`
	}
	_ = c.ShouldBindJSON(&body) // body is optional

	if err := h.deps.Registry.Heartbeat(workerID, body.InFlight); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}

// GET /api/tasks/next
func (h *Handler) taskNext(c *gin.Context) {
	workerID := c.Query("worker_id")
	h.deps.Registry.Touch(workerID)

	label := c.Query("label")
	task := h.deps.Queue.Next(label)
	if task == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, task)
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
	var result struct {
		Error     string    `json:"error,omitempty"`
		StartedAt time.Time `json:"started_at"`
		EndedAt   time.Time `json:"ended_at"`
		Attempts  int       `json:"attempts"`
	}
	if err := c.ShouldBindJSON(&result); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.deps.Queue.Complete(c.Request.Context(), taskID, action, result.Error, result.StartedAt, result.EndedAt, result.Attempts); err != nil {
		slog.Warn("task complete error", "task_id", taskID, "err", err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}
