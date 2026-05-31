package notebook

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// HandlerDeps holds all dependencies for the notebook HTTP handler.
type HandlerDeps struct {
	Notebooks      Repository
	Start          func(ctx context.Context, spec NotebookServerSpec, yamlStr string) (*NotebookServer, error)
	Stop           func(ctx context.Context, name string) error
	UpdateStatus   func(ctx context.Context, name, status, endpoint string) error
	WorkerRegistry *NotebookWorkerRegistry // nil disables worker registration routes
}

// Handler is the Gin HTTP handler for the /notebooks domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all /notebooks routes on the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/notebooks", h.listNotebooks)
	rg.POST("/notebooks", h.createNotebook)
	rg.GET("/notebooks/:name", h.getNotebook)
	rg.DELETE("/notebooks/:name", h.deleteNotebook)
	rg.Any("/notebooks/:name/proxy/*path", h.proxyNotebook)

	// Worker callback: status update from notebook worker agent.
	if h.deps.UpdateStatus != nil {
		rg.PATCH("/api/notebooks/:name/status", h.updateNotebookStatus)
	}

	// Worker registration routes.
	if h.deps.WorkerRegistry != nil {
		rg.POST("/api/notebook-workers", h.registerWorker)
		rg.POST("/api/notebook-workers/:id/heartbeat", h.heartbeatWorker)
		rg.GET("/api/notebook-workers", h.listWorkers)
	}
}

// GET /notebooks
func (h *Handler) listNotebooks(c *gin.Context) {
	nbs, err := h.deps.Notebooks.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, nbs)
}

// POST /notebooks — body: {"yaml": "..."}
func (h *Handler) createNotebook(c *gin.Context) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.YAML == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "yaml field is required"})
		return
	}

	var spec NotebookServerSpec
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid YAML: " + err.Error()})
		return
	}

	if h.deps.Start == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Start not configured"})
		return
	}

	nb, err := h.deps.Start(c.Request.Context(), spec, req.YAML)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, nb)
}

// GET /notebooks/:name
func (h *Handler) getNotebook(c *gin.Context) {
	name := c.Param("name")
	nb, err := h.deps.Notebooks.Get(c.Request.Context(), name)
	if err != nil || nb == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "notebook not found"})
		return
	}
	c.JSON(http.StatusOK, nb)
}

// DELETE /notebooks/:name — stops the process and removes the record.
func (h *Handler) deleteNotebook(c *gin.Context) {
	name := c.Param("name")
	nb, err := h.deps.Notebooks.Get(c.Request.Context(), name)
	if err != nil || nb == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "notebook not found"})
		return
	}
	if h.deps.Stop != nil {
		if err := h.deps.Stop(c.Request.Context(), name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.deps.Notebooks.Delete(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ANY /notebooks/:name/proxy/*path — reverse-proxies to the notebook endpoint.
// Handles both HTTP and WebSocket (required for Jupyter kernel communication).
func (h *Handler) proxyNotebook(c *gin.Context) {
	name := c.Param("name")
	nb, err := h.deps.Notebooks.Get(c.Request.Context(), name)
	if err != nil || nb == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "notebook not found"})
		return
	}
	if nb.Status != StatusRunning || nb.Endpoint == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "notebook is not running"})
		return
	}

	target, err := url.Parse(nb.Endpoint)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid notebook endpoint"})
		return
	}

	subPath := c.Param("path")
	if subPath == "" || !strings.HasPrefix(subPath, "/") {
		subPath = "/" + subPath
	}
	// JupyterLab is started with --ServerApp.base_url=/notebooks/<name>/proxy/
	// so the upstream expects the full path including that prefix.
	upstreamPath := fmt.Sprintf("/notebooks/%s/proxy%s", name, subPath)

	// WebSocket upgrade: tunnel raw TCP between client and upstream.
	if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
		h.proxyWebSocket(c, target, upstreamPath)
		return
	}

	r2 := c.Request.Clone(c.Request.Context())
	r2.URL.Path = upstreamPath
	r2.URL.RawPath = ""
	r2.URL.RawQuery = c.Request.URL.RawQuery
	r2.Host = target.Host

	basePrefix := fmt.Sprintf("/notebooks/%s/proxy", name)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		loc := resp.Header.Get("Location")
		if loc == "" || strings.HasPrefix(loc, "http") || strings.HasPrefix(loc, basePrefix) {
			return nil
		}
		// JupyterLab sometimes emits bare redirects like /login?next=... without
		// the base_url prefix. Rewrite them so the browser stays under the proxy path.
		if !strings.HasPrefix(loc, "/") {
			loc = "/" + loc
		}
		resp.Header.Set("Location", basePrefix+loc)
		return nil
	}
	proxy.ServeHTTP(c.Writer, r2)
}

// proxyWebSocket tunnels a WebSocket connection to the upstream Jupyter server.
func (h *Handler) proxyWebSocket(c *gin.Context, target *url.URL, subPath string) {
	upstreamAddr := target.Host
	upstream, err := net.Dial("tcp", upstreamAddr)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "cannot connect to notebook"})
		return
	}
	defer upstream.Close()

	// Rewrite the request line to point at the upstream path.
	r2 := c.Request.Clone(context.Background())
	r2.URL.Path = subPath
	r2.URL.RawQuery = c.Request.URL.RawQuery
	r2.Host = target.Host
	if err := r2.Write(upstream); err != nil {
		return
	}

	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "websocket not supported"})
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	// Flush any buffered data from the client side.
	if buf.Reader.Buffered() > 0 {
		buffered := make([]byte, buf.Reader.Buffered())
		_, _ = buf.Read(buffered)
		_, _ = upstream.Write(buffered)
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	<-done
}

// PATCH /api/notebooks/:name/status — called by notebook worker agents.
func (h *Handler) updateNotebookStatus(c *gin.Context) {
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

// POST /api/notebook-workers
func (h *Handler) registerWorker(c *gin.Context) {
	var info NotebookWorkerInfo
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

// POST /api/notebook-workers/:id/heartbeat
func (h *Handler) heartbeatWorker(c *gin.Context) {
	id := c.Param("id")
	h.deps.WorkerRegistry.Heartbeat(id)
	c.Status(http.StatusNoContent)
}

// GET /api/notebook-workers
func (h *Handler) listWorkers(c *gin.Context) {
	c.JSON(http.StatusOK, h.deps.WorkerRegistry.List())
}
