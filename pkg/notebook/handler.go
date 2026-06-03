package notebook

import (
	"bufio"
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

	subPath := normalizeNotebookProxySubPath(c.Param("path"))
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

	// Inject token into query only when the browser has no JupyterLab session cookie.
	// On first visit (no cookie): ?token=<uuid> lets JupyterLab authenticate, set a
	// username-* cookie, and redirect back without the token — one redirect, no loop.
	// On subsequent visits (cookie present): no injection — cookie auth works directly.
	// This applies to both HTTP and WebSocket; for WS the token in query acts as
	// fallback when the connection is attempted before the HTTP session sets a cookie.
	r2.URL.RawQuery = injectNotebookToken(r2, nb.Token)

	upgrade := isWebSocketUpgrade(r2)
	if !upgrade {
		// Must close after each response. Without this the browser reuses the
		// hijacked TCP connection for its next request (HTTP keep-alive), and that
		// next request gets piped straight to JupyterLab instead of Piper's router.
		r2.Close = true
	}

	// Host stays as the browser's address (e.g. localhost:8080) so JupyterLab embeds
	// the correct public URL in its HTML page config (wsUrl, baseUrl). If we set
	// Host=dialTarget (127.0.0.1:PORT) JupyterLab would embed that address and the
	// browser's JS would connect WebSocket directly to JupyterLab, bypassing the proxy.
	r2.Header.Set("X-Forwarded-Host", c.Request.Host)
	r2.Header.Set("X-Forwarded-Proto", schemeFromRequest(c.Request))
	r2.Header.Del("Origin")

	if err := r2.Write(upstream); err != nil {
		return
	}

	if upgrade {
		// WebSocket: hijack the browser connection and pipe bytes in both directions.
		// This is the only case where hijacking is correct — WebSocket is a raw byte
		// stream after the 101 handshake, so we can't use Gin's response writer.
		hijacker, ok := c.Writer.(http.Hijacker)
		if !ok {
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
		return
	}

	// Regular HTTP: read the response and write it via Gin's normal response pipeline.
	// Do NOT hijack — hijacking hands over the raw TCP connection, so subsequent
	// keep-alive requests from the browser would be piped to JupyterLab instead of
	// going through Gin's router.
	resp, err := http.ReadResponse(bufio.NewReader(upstream), r2)
	if err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	_ = rewriteNotebookRedirectLocation(resp, name)

	// Forward headers (skip hop-by-hop).
	for k, vv := range resp.Header {
		if k == "Transfer-Encoding" || k == "Connection" {
			continue
		}
		for _, v := range vv {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

// injectNotebookToken adds the JupyterLab token to the upstream query so that the
// master can authenticate on behalf of the browser without exposing the token in URLs.
// The token is only injected when the browser has no existing Jupyter auth cookie,
// avoiding a redirect loop (JupyterLab redirects after token validation to strip it
// from the URL, which would repeat on every request if we always injected).
func injectNotebookToken(req *http.Request, token string) string {
	rawQuery := req.URL.RawQuery
	if token == "" {
		return rawQuery
	}
	for _, c := range req.Cookies() {
		if strings.HasPrefix(c.Name, "username-") {
			return rawQuery
		}
	}
	values, _ := url.ParseQuery(rawQuery)
	if values == nil {
		values = url.Values{}
	}
	values.Set("token", token)
	return values.Encode()
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}

func schemeFromRequest(r *http.Request) string {
	if r != nil && r.TLS != nil {
		return "https"
	}
	return "http"
}

func rewriteNotebookRedirectLocation(resp *http.Response, name string) error {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil
	}
	u, err := url.Parse(loc)
	if err != nil {
		return nil
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.HasPrefix(path, "/notebooks/"+name+"/proxy/") {
		redirect := path
		if q := stripTokenQuery(u.RawQuery); q != "" {
			redirect += "?" + q
		}
		resp.Header.Set("Location", redirect)
		return nil
	}
	redirect := "/notebooks/" + name + "/proxy" + path
	if q := stripTokenQuery(u.RawQuery); q != "" {
		redirect += "?" + q
	}
	resp.Header.Set("Location", redirect)
	return nil
}

func stripTokenQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	values.Del("token")
	return values.Encode()
}

func normalizeNotebookProxySubPath(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}
