package serving

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ServingFilter is the list filter returned by ServingHooks.BeforeListServices.
type ServingFilter struct {
	// OwnerID, when set, returns only services belonging to that owner.
	OwnerID string
}

// ServingHooks provides pre-request authorization hooks for the serving domain.
// All methods are called with the request context enriched by the Auth hook,
// so implementations can extract verified user identity via ctx.Value.
type ServingHooks interface {
	BeforeCreateService(ctx context.Context, r *http.Request, yaml string) error
	BeforeListServices(ctx context.Context, r *http.Request) (ServingFilter, error)
	BeforeGetService(ctx context.Context, r *http.Request, name string) error
}

// HandlerDeps holds all dependencies required by the serving handler.
type HandlerDeps struct {
	Services       Repository
	Deploy         func(ctx context.Context, yaml []byte, ownerID string) (*Service, error)
	Stop           func(ctx context.Context, name string) error
	Restart        func(ctx context.Context, name string) error
	UpdateStatus   func(ctx context.Context, name, status, endpoint string) error
	Proxy          http.Handler
	OwnerID        func(r *http.Request) string
	Hooks          ServingHooks
	WorkerRegistry *ServingWorkerRegistry // nil disables worker registration routes
}

// Handler is the Gin HTTP handler for the /services domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new serving Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all /serving routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/serving", h.listServices)
	rg.POST("/serving", h.createService)
	// Static sub-paths must be registered before /:name to avoid param capture.
	rg.GET("/serving/history", h.listServiceHistory)
	if h.deps.Proxy != nil {
		rg.Any("/serving/predict/*path", gin.WrapH(h.deps.Proxy))
	}
	rg.GET("/serving/:name", h.getService)
	rg.DELETE("/serving/:name", h.deleteService)
	rg.POST("/serving/:name/restart", h.restartService)

	// Worker callback: status update from serving worker agent.
	if h.deps.UpdateStatus != nil {
		rg.PATCH("/api/servings/:name/status", h.updateServiceStatus)
	}

	// Worker registration routes.
	if h.deps.WorkerRegistry != nil {
		rg.POST("/api/serving-workers", h.registerWorker)
		rg.POST("/api/serving-workers/:id/heartbeat", h.heartbeatWorker)
		rg.GET("/api/serving-workers", h.listWorkers)
	}
}

// GET /serving
func (h *Handler) listServices(c *gin.Context) {
	ownerID := h.ownerID(c.Request)
	if h.deps.Hooks != nil {
		f, err := h.deps.Hooks.BeforeListServices(c.Request.Context(), c.Request)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		if f.OwnerID != "" {
			ownerID = f.OwnerID
		}
	}
	svcs, err := h.deps.Services.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]*Service, 0, len(svcs))
	for _, svc := range svcs {
		if ownerID != "" && svc.OwnerID != "" && svc.OwnerID != ownerID {
			continue
		}
		out = append(out, svc.Redact())
	}
	c.JSON(http.StatusOK, out)
}

// POST /serving
func (h *Handler) createService(c *gin.Context) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeCreateService(c.Request.Context(), c.Request, req.YAML); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if h.deps.Deploy == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Deploy not configured"})
		return
	}
	svc, err := h.deps.Deploy(c.Request.Context(), []byte(req.YAML), h.ownerID(c.Request))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, svc.Redact())
}

// GET /serving/history
func (h *Handler) listServiceHistory(c *gin.Context) {
	history, err := h.deps.Services.ListHistory(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, history)
}

// GET /serving/:name
func (h *Handler) getService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	if err := h.checkServiceAccess(c, name, svc); err != nil {
		return
	}
	c.JSON(http.StatusOK, svc.Redact())
}

// DELETE /serving/:name
func (h *Handler) deleteService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	if err := h.checkServiceAccess(c, name, svc); err != nil {
		return
	}
	if h.deps.Stop != nil {
		if err := h.deps.Stop(c.Request.Context(), name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.deps.Services.Delete(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// POST /serving/:name/restart
func (h *Handler) restartService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	if err := h.checkServiceAccess(c, name, svc); err != nil {
		return
	}
	if h.deps.Restart != nil {
		if err := h.deps.Restart(c.Request.Context(), name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.Status(http.StatusOK)
}

// PATCH /api/servings/:name/status — called by serving worker agents.
func (h *Handler) updateServiceStatus(c *gin.Context) {
	name := c.Param("name")
	var body struct {
		Status   string `json:"status"`
		Endpoint string `json:"endpoint"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.deps.UpdateStatus(c.Request.Context(), name, body.Status, body.Endpoint); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// POST /api/serving-workers
func (h *Handler) registerWorker(c *gin.Context) {
	var info ServingWorkerInfo
	if err := c.ShouldBindJSON(&info); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if info.ID == "" || info.Addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id and addr are required"})
		return
	}
	h.deps.WorkerRegistry.Register(&info)
	c.JSON(http.StatusOK, gin.H{"id": info.ID})
}

// POST /api/serving-workers/:id/heartbeat
func (h *Handler) heartbeatWorker(c *gin.Context) {
	id := c.Param("id")
	h.deps.WorkerRegistry.Heartbeat(id)
	c.Status(http.StatusNoContent)
}

// GET /api/serving-workers
func (h *Handler) listWorkers(c *gin.Context) {
	c.JSON(http.StatusOK, h.deps.WorkerRegistry.List())
}

// checkServiceAccess calls BeforeGetService hook (if set) and then the
// built-in ownership check.
func (h *Handler) checkServiceAccess(c *gin.Context, name string, svc *Service) error {
	if h.deps.Hooks != nil {
		if err := h.deps.Hooks.BeforeGetService(c.Request.Context(), c.Request, name); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return err
		}
	}
	if !h.canAccess(c.Request, svc) {
		err := fmt.Errorf("forbidden")
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return err
	}
	return nil
}

func (h *Handler) ownerID(r *http.Request) string {
	if h.deps.OwnerID == nil {
		return ""
	}
	return h.deps.OwnerID(r)
}

func (h *Handler) canAccess(r *http.Request, svc *Service) bool {
	if svc == nil {
		return false
	}
	ownerID := h.ownerID(r)
	return ownerID == "" || svc.OwnerID == "" || svc.OwnerID == ownerID
}
