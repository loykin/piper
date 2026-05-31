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

// jupyterLogin performs a server-side login to JupyterLab and returns the
// session cookies. It fetches the XSRF token first, then POSTs the known token.
// basePrefix is the ServerApp.base_url path prefix, e.g. "/notebooks/foo/proxy".
func jupyterLogin(endpoint *url.URL, token, basePrefix string) []*http.Cookie {
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	loginURL := endpoint.Scheme + "://" + endpoint.Host + basePrefix + "/login"

	// GET /login to obtain the _xsrf token required by the POST.
	getResp, err := client.Get(loginURL)
	if err != nil {
		return nil
	}
	body, _ := io.ReadAll(getResp.Body)
	_ = getResp.Body.Close()

	xsrf := extractXSRFValue(string(body))
	if xsrf == "" {
		for _, c := range getResp.Cookies() {
			if c.Name == "_xsrf" {
				xsrf = c.Value
				break
			}
		}
	}

	// POST /login with the token (submitted as "password") and _xsrf.
	form := url.Values{"password": {token}, "_xsrf": {xsrf}}
	req, err := http.NewRequest(http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range getResp.Cookies() {
		req.AddCookie(c)
	}

	postResp, err := client.Do(req)
	if err != nil {
		return nil
	}
	_ = postResp.Body.Close()

	if postResp.StatusCode == http.StatusFound {
		return postResp.Cookies()
	}
	return nil
}

// extractXSRFValue parses the _xsrf hidden input value from JupyterLab's login HTML.
func extractXSRFValue(html string) string {
	const marker = `name="_xsrf" value="`
	idx := strings.Index(html, marker)
	if idx < 0 {
		return ""
	}
	rest := html[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// HandlerDeps holds all dependencies for the notebook HTTP handler.
type HandlerDeps struct {
	Notebooks        Repository
	Volumes          VolumeRepository
	Create           func(ctx context.Context, spec NotebookServerSpec, yamlStr string) (*NotebookServer, error)
	CreateWithVolume func(ctx context.Context, spec NotebookServerSpec, volumeID, yamlStr string) (*NotebookServer, error)
	Stop             func(ctx context.Context, name string) error
	Restart          func(ctx context.Context, name string) error
	Delete           func(ctx context.Context, name string) error
	PurgeVolume      func(ctx context.Context, volumeID string) error
	UpdateStatus     func(ctx context.Context, name, status, endpoint, workDir, token string, pid int, env string) error
	WorkerRegistry   *NotebookWorkerRegistry // nil disables worker registration routes
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

	// Volume routes.
	if h.deps.Volumes != nil {
		rg.GET("/notebook-volumes", h.listVolumes)
		rg.DELETE("/notebook-volumes/:id", h.purgeVolume)
	}

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
	// JupyterLab blocks cross-origin requests; rewrite Origin to match upstream.
	if r2.Header.Get("Origin") != "" {
		r2.Header.Set("Origin", target.Scheme+"://"+target.Host)
	}

	basePrefix := fmt.Sprintf("/notebooks/%s/proxy", name)
	loginPrefix := basePrefix + "/login"
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		loc := resp.Header.Get("Location")
		if loc == "" || strings.HasPrefix(loc, "http") {
			return nil
		}
		if !strings.HasPrefix(loc, "/") {
			loc = "/" + loc
		}

		// JupyterLab (with base_url) redirects to {basePrefix}/login.
		// Intercept it and perform server-side transparent auth so the browser
		// goes straight to the destination without ever seeing the login page.
		if strings.HasPrefix(loc, "/login") || strings.HasPrefix(loc, loginPrefix) {
			if nb.Token == "" {
				// No token stored — notebook was created without token support.
				// Return 503 to break the loop; user should recreate the notebook.
				resp.StatusCode = http.StatusServiceUnavailable
				resp.Header.Del("Location")
				return nil
			}
			parsedLoc, _ := url.Parse(loc)
			next := parsedLoc.Query().Get("next")
			if next == "" {
				next = basePrefix + "/lab"
			}
			cookies := jupyterLogin(target, nb.Token, basePrefix)
			for _, c := range cookies {
				c.Path = "/"
				resp.Header.Add("Set-Cookie", c.String())
			}
			resp.Header.Set("Location", next)
			return nil
		}

		// Pass through redirects that are already under base_url (non-login).
		if strings.HasPrefix(loc, basePrefix) {
			return nil
		}

		// Other bare redirects (e.g. /static/…): prefix with base_url.
		parsed, err := url.Parse(loc)
		if err != nil {
			resp.Header.Set("Location", basePrefix+loc)
			return nil
		}
		parsed.Path = basePrefix + parsed.Path
		resp.Header.Set("Location", parsed.String())
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
	defer func() { _ = upstream.Close() }()

	// Rewrite the request line to point at the upstream path.
	r2 := c.Request.Clone(context.Background())
	r2.URL.Path = subPath
	r2.URL.RawQuery = c.Request.URL.RawQuery
	r2.Host = target.Host
	r2.Header.Set("Origin", target.Scheme+"://"+target.Host)
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
	defer func() { _ = conn.Close() }()

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
// Accepts status, endpoint, work_dir, token, pid, and env from worker callbacks.
func (h *Handler) updateNotebookStatus(c *gin.Context) {
	name := c.Param("name")
	var body struct {
		Status   string `json:"status"`
		Endpoint string `json:"endpoint"`
		WorkDir  string `json:"work_dir"`
		Token    string `json:"token"`
		PID      int    `json:"pid"`
		Env      string `json:"env"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.deps.UpdateStatus(c.Request.Context(), name, body.Status, body.Endpoint, body.WorkDir, body.Token, body.PID, body.Env); err != nil {
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
