package serving

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandlerDeps holds all dependencies required by the serving handler.
type HandlerDeps struct {
	Services Repository
	Deploy   func(ctx context.Context, yaml []byte) (*Service, error)
	Stop     func(ctx context.Context, name string) error
	Restart  func(ctx context.Context, name string) error
	Proxy    http.Handler
}

// Handler is the Gin HTTP handler for the /services domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new serving Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all /services routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/services", h.listServices)
	rg.POST("/services", h.createService)
	rg.GET("/services/:name", h.getService)
	rg.DELETE("/services/:name", h.deleteService)
	rg.POST("/services/:name/restart", h.restartService)

	// Proxy: /services/predict/*path — must be registered before /:name to match correctly.
	// Gin matches routes in registration order; use Any to support all methods.
	if h.deps.Proxy != nil {
		rg.Any("/services/predict/*path", gin.WrapH(h.deps.Proxy))
	}
}

// GET /services
func (h *Handler) listServices(c *gin.Context) {
	svcs, err := h.deps.Services.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, svcs)
}

// POST /services
func (h *Handler) createService(c *gin.Context) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.deps.Deploy == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Deploy not configured"})
		return
	}
	svc, err := h.deps.Deploy(c.Request.Context(), []byte(req.YAML))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, svc)
}

// GET /services/:name
func (h *Handler) getService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	c.JSON(http.StatusOK, svc)
}

// DELETE /services/:name
func (h *Handler) deleteService(c *gin.Context) {
	name := c.Param("name")
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

// POST /services/:name/restart
func (h *Handler) restartService(c *gin.Context) {
	name := c.Param("name")
	if h.deps.Restart != nil {
		if err := h.deps.Restart(c.Request.Context(), name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.Status(http.StatusOK)
}
