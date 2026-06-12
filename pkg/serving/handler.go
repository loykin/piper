package serving

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

// HandlerDeps holds all dependencies required by the serving handler.
type HandlerDeps struct {
	Services Repository
	Deploy   func(ctx context.Context, projectID string, yaml []byte) (*Service, error)
	Stop     func(ctx context.Context, projectID, name string) error
	Restart  func(ctx context.Context, projectID, name string) error
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

func currentProjectID(c *gin.Context) string {
	projectContext, _ := project.FromContext(c.Request.Context())
	return projectContext.ID
}

// RegisterRoutes mounts the JSON API routes for serving.
// The browser predict proxy is registered separately via RegisterProxyRoutes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/serving", h.listServices)
	rg.GET("/serving/history", h.listServiceHistory)
	rg.GET("/serving/:name", h.getService)

	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.POST("/serving", h.createService)
	member.DELETE("/serving/:name", h.deleteService)
	member.POST("/serving/:name/restart", h.restartService)
}

// RegisterProxyRoutes mounts the browser predict proxy at the given router group.
// Expected group path: /projects/:project_id
func (h *Handler) RegisterProxyRoutes(rg *gin.RouterGroup) {
	if h.deps.Proxy != nil {
		rg.Any("/serving/predict/*path", gin.WrapH(h.deps.Proxy))
	}
}

// GET /serving
func (h *Handler) listServices(c *gin.Context) {
	svcs, err := h.deps.Services.List(c.Request.Context(), currentProjectID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]*Service, 0, len(svcs))
	for _, svc := range svcs {
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
	if h.deps.Deploy == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Deploy not configured"})
		return
	}
	svc, err := h.deps.Deploy(c.Request.Context(), currentProjectID(c), []byte(req.YAML))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, svc.Redact())
}

// GET /serving/history
func (h *Handler) listServiceHistory(c *gin.Context) {
	history, err := h.deps.Services.ListHistory(c.Request.Context(), currentProjectID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, history)
}

// GET /serving/:name
func (h *Handler) getService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), currentProjectID(c), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	c.JSON(http.StatusOK, svc.Redact())
}

// DELETE /serving/:name
func (h *Handler) deleteService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), currentProjectID(c), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	if h.deps.Stop != nil {
		if err := h.deps.Stop(c.Request.Context(), currentProjectID(c), name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.deps.Services.Delete(c.Request.Context(), currentProjectID(c), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// POST /serving/:name/restart
func (h *Handler) restartService(c *gin.Context) {
	name := c.Param("name")
	svc, err := h.deps.Services.Get(c.Request.Context(), currentProjectID(c), name)
	if err != nil || svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	if h.deps.Restart != nil {
		if err := h.deps.Restart(c.Request.Context(), currentProjectID(c), name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.Status(http.StatusOK)
}
