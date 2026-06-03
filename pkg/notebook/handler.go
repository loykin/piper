package notebook

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/agent"
	"gopkg.in/yaml.v3"
)

// HandlerDeps holds all dependencies for the notebook HTTP handler.
// ProxyDialer dials a target host:port through a connected gRPC agent.
// agentID is the worker's registration ID; target is "host:port" reachable inside
// the agent's network (e.g. a Jupyter pod's ClusterIP address).
type ProxyDialer interface {
	DialProxy(ctx context.Context, agentID, target string) (net.Conn, error)
}

type HandlerDeps struct {
	Notebooks        Repository
	Volumes          VolumeRepository
	Create           func(ctx context.Context, spec NotebookServerSpec, yamlStr string) (*NotebookServer, error)
	CreateWithVolume func(ctx context.Context, spec NotebookServerSpec, volumeID, yamlStr string) (*NotebookServer, error)
	Stop             func(ctx context.Context, name string) error
	Restart          func(ctx context.Context, name string) error
	Delete           func(ctx context.Context, name string) error
	PurgeVolume      func(ctx context.Context, volumeID string) error
	AgentRegistry    *agent.Registry
	ProxyDialer      ProxyDialer
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
	rg.POST("/notebooks/:name/stop", h.stopNotebook)
	rg.POST("/notebooks/:name/start", h.startNotebook)
	rg.DELETE("/notebooks/:name", h.deleteNotebook)
	rg.Any("/notebooks/:name/proxy/*path", h.proxyNotebook)

	if h.deps.Volumes != nil {
		rg.GET("/notebook-volumes", h.listVolumes)
		rg.DELETE("/notebook-volumes/:id", h.purgeVolume)
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

// POST /notebooks — body: {"yaml": "...", "volume_id": "optional-uuid"}
// Returns 201 Created immediately with status=provisioning (or starting when reusing a volume).
// Actual server startup happens asynchronously; poll GET /notebooks/:name for status updates.
func (h *Handler) createNotebook(c *gin.Context) {
	var req struct {
		YAML     string `json:"yaml"`
		VolumeID string `json:"volume_id"`
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

	var nb *NotebookServer
	var err error
	if req.VolumeID != "" {
		if h.deps.CreateWithVolume == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "CreateWithVolume not configured"})
			return
		}
		nb, err = h.deps.CreateWithVolume(c.Request.Context(), spec, req.VolumeID, req.YAML)
	} else {
		if h.deps.Create == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Create not configured"})
			return
		}
		nb, err = h.deps.Create(c.Request.Context(), spec, req.YAML)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, nb)
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

// POST /notebooks/:name/stop — halts the process, preserves record and work dir.
func (h *Handler) stopNotebook(c *gin.Context) {
	name := c.Param("name")
	if h.deps.Stop == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Stop not configured"})
		return
	}
	if err := h.deps.Stop(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// POST /notebooks/:name/start — restarts a stopped notebook using its existing work dir.
func (h *Handler) startNotebook(c *gin.Context) {
	name := c.Param("name")
	if h.deps.Restart == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Restart not configured"})
		return
	}
	if err := h.deps.Restart(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	nb, _ := h.deps.Notebooks.Get(c.Request.Context(), name)
	if nb != nil {
		c.JSON(http.StatusOK, nb)
	} else {
		c.Status(http.StatusNoContent)
	}
}

// DELETE /notebooks/:name — removes the server record and releases the backing volume.
// The volume's work directory is preserved on disk (recoverable via the volume endpoint).
func (h *Handler) deleteNotebook(c *gin.Context) {
	name := c.Param("name")
	if h.deps.Delete == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Delete not configured"})
		return
	}
	if err := h.deps.Delete(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /notebook-volumes — list all volumes.
func (h *Handler) listVolumes(c *gin.Context) {
	vols, err := h.deps.Volumes.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, vols)
}

// DELETE /notebook-volumes/:id — permanently delete a released volume.
func (h *Handler) purgeVolume(c *gin.Context) {
	id := c.Param("id")
	if h.deps.PurgeVolume == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PurgeVolume not configured"})
		return
	}
	if err := h.deps.PurgeVolume(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	upstreamPath := fmt.Sprintf("/notebooks/%s/proxy%s", name, subPath)

	agentID := target.Host
	dialTarget := target.Query().Get("target")
	if agentID == "" || dialTarget == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid notebook endpoint"})
		return
	}
	if h.deps.ProxyDialer == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "proxy not configured"})
		return
	}

	upstream, err := h.deps.ProxyDialer.DialProxy(c.Request.Context(), agentID, dialTarget)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "cannot connect to notebook: " + err.Error()})
		return
	}
	defer func() { _ = upstream.Close() }()

	r2 := c.Request.Clone(context.Background())
	r2.URL.Path = upstreamPath
	r2.URL.RawPath = ""
	r2.URL.RawQuery = notebookProxyRawQuery(c.Request.URL.RawQuery, nb.Token)
	r2.Host = dialTarget
	r2.Header.Set("Origin", "http://"+dialTarget)
	if err := r2.Write(upstream); err != nil {
		return
	}

	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connection hijack not supported"})
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

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

func notebookProxyRawQuery(rawQuery, token string) string {
	if token == "" {
		return rawQuery
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	if values.Get("token") == "" {
		values.Set("token", token)
	}
	return values.Encode()
}
