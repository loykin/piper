package viewer

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/tunnelproxy"
	"github.com/piper/piper/pkg/project"
)

type Handler struct {
	mgr  *Manager
	repo Repository
}

func NewHandler(mgr *Manager, repo Repository) *Handler {
	return &Handler{mgr: mgr, repo: repo}
}

// RegisterRoutes mounts JSON API routes (under /api/projects/:project_id).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/runs/:id/artifacts/:step/:artifact/view", h.openViewer)
	rg.GET("/viewers", h.listViewers)
	rg.GET("/viewers/:id", h.getViewer)
	rg.POST("/viewers/:id/stop", h.stopViewer)
}

// RegisterProxyRoutes mounts the browser-facing proxy (outside /api/).
func (h *Handler) RegisterProxyRoutes(rg *gin.RouterGroup) {
	rg.Any("/viewers/:id/proxy/*path", h.proxyViewer)
}

type openRequest struct {
	Type string `json:"type" binding:"required"`
}

func (h *Handler) openViewer(c *gin.Context) {
	projectID := pid(c)
	runID := c.Param("id")
	stepName := c.Param("step")
	artifact := strings.TrimPrefix(c.Param("artifact"), "/")

	var req openRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	v, err := h.mgr.Open(c.Request.Context(), projectID, runID, stepName, artifact, req.Type)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "unsupported viewer type") {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	type response struct {
		*Viewer
		URL string `json:"url"`
	}
	c.JSON(http.StatusOK, response{
		Viewer: v,
		URL:    v.ProxyURL(projectID),
	})
}

func (h *Handler) listViewers(c *gin.Context) {
	viewers, err := h.repo.List(c.Request.Context(), pid(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, viewers)
}

func (h *Handler) getViewer(c *gin.Context) {
	v, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "viewer not found"})
		return
	}
	type response struct {
		*Viewer
		URL string `json:"url"`
	}
	c.JSON(http.StatusOK, response{Viewer: v, URL: v.ProxyURL(pid(c))})
}

func (h *Handler) stopViewer(c *gin.Context) {
	if err := h.mgr.Stop(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) proxyViewer(c *gin.Context) {
	v, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if err != nil || v.Status != StatusRunning {
		c.JSON(http.StatusNotFound, gin.H{"error": "viewer not found or not running"})
		return
	}

	if v.Endpoint != "" {
		// Process-based viewer (e.g. TensorBoard): reverse proxy to process.
		target, err := url.Parse(v.Endpoint)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid endpoint"})
			return
		}
		proxyPrefix := v.ProxyURL(pid(c))
		tunnelproxy.ServeReverseProxy(c.Writer, c.Request, target, tunnelproxy.PolicyFunc{
			OnRequest: func(r *http.Request) error {
				// Strip the Piper proxy prefix so upstream sees a clean path.
				r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, proxyPrefix)
				return nil
			},
		})
		return
	}

	// File-based viewer (e.g. HTML): serve from WorkDir.
	if v.WorkDir == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "viewer has no content"})
		return
	}
	subPath := filepath.FromSlash(strings.TrimPrefix(c.Param("path"), "/"))
	if subPath == "" {
		// Redirect bare root to index.html; prevents http.ServeFile directory listing.
		http.Redirect(c.Writer, c.Request, c.Request.URL.Path+"index.html", http.StatusFound)
		return
	}
	absPath, err := filepath.Abs(filepath.Join(v.WorkDir, subPath))
	if err != nil || !(strings.HasPrefix(absPath, v.WorkDir+string(os.PathSeparator)) || absPath == v.WorkDir) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	info, statErr := os.Stat(absPath)
	if statErr == nil && info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	http.ServeFile(c.Writer, c.Request, absPath)
}

func pid(c *gin.Context) string {
	ctx, _ := project.FromContext(c.Request.Context())
	return ctx.ID
}
