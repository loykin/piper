package pipelineworker

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/proto"

	iworker "github.com/piper/piper/internal/worker"
)

// TaskQueuer abstracts the task queue for the worker handler.
type TaskQueuer interface {
	Next(label string) *proto.Task
	NextForWorker(workerID, label string) *proto.Task
	Complete(ctx context.Context, result proto.TaskResult) error
	RenewLeases(workerID string, taskIDs []string)
}

// WorkerRegistrar abstracts the worker registry for the worker handler.
type WorkerRegistrar interface {
	Register(info iworker.Info)
	Heartbeat(id string, inFlight int) error
	Touch(id string)
	List() []iworker.Info
}

// HandlerDeps holds all dependencies required by the worker handler.
type HandlerDeps struct {
	Registry WorkerRegistrar
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
	h.RegisterPollRoutes(rg)
	h.RegisterCompletionRoutes(rg)
}

// RegisterPollRoutes mounts worker registration and task polling routes.
// Only needed when workers pull tasks via HTTP (no active backend).
func (h *Handler) RegisterPollRoutes(rg *gin.RouterGroup) {
	rg.POST("/workers", h.registerWorker)
	rg.GET("/workers", h.listWorkers)
	rg.POST("/workers/:id/heartbeat", h.workerHeartbeat)
	rg.GET("/tasks/next", h.taskNext)
}

// RegisterCompletionRoutes mounts the task done/failed reporting routes.
// Must always be registered so that piper agent exec in K8s Job pods can
// report results back to the master regardless of backend mode.
func (h *Handler) RegisterCompletionRoutes(rg *gin.RouterGroup) {
	rg.POST("/tasks/:id/done", h.taskDone)
	rg.POST("/tasks/:id/failed", h.taskFailed)
}

// POST /api/workers
func (h *Handler) registerWorker(c *gin.Context) {
	var info iworker.Info
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
		InFlight int      `json:"in_flight"`
		TaskIDs  []string `json:"task_ids"`
	}
	_ = c.ShouldBindJSON(&body) // body is optional

	if err := h.deps.Registry.Heartbeat(workerID, body.InFlight); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.deps.Queue.RenewLeases(workerID, body.TaskIDs)
	c.Status(http.StatusOK)
}

// GET /api/tasks/next
func (h *Handler) taskNext(c *gin.Context) {
	workerID := c.Query("worker_id")
	h.deps.Registry.Touch(workerID)

	label := c.Query("label")
	task := h.deps.Queue.NextForWorker(workerID, label)
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
	var body struct {
		Error     string    `json:"error,omitempty"`
		WorkerID  string    `json:"worker_id,omitempty"`
		StartedAt time.Time `json:"started_at"`
		EndedAt   time.Time `json:"ended_at"`
		Attempts  int       `json:"attempts"`
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
		Attempts:  body.Attempts,
	}); err != nil {
		slog.Warn("task complete error", "task_id", taskID, "err", err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}
