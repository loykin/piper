package notebook

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/tunnelproxy"
	"gopkg.in/yaml.v3"
)

// HandlerDeps holds all dependencies for the notebook HTTP handler.
// ProxyDialer dials a target host:port through a connected gRPC agent.
// agentID is the worker's registration ID; target is "host:port" reachable inside
// the agent's network (e.g. a Jupyter pod's ClusterIP address).
type ProxyDialer interface {
	DialProxy(ctx context.Context, agentID, target string) (net.Conn, error)
}

// RPCSender sends a typed RPC to a specific worker agent and decodes the response.
type RPCSender interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type HandlerDeps struct {
	Notebooks        Repository
	Volumes          VolumeRepository
	Create           func(ctx context.Context, spec Notebook, yamlStr string) (*NotebookServer, error)
	CreateWithVolume func(ctx context.Context, spec Notebook, volumeID, yamlStr string) (*NotebookServer, error)
	Stop             func(ctx context.Context, name string) error
	Restart          func(ctx context.Context, name string) error
	Delete           func(ctx context.Context, name string) error
	PurgeVolume      func(ctx context.Context, volumeID string) error
	AgentRegistry    *iagent.Registry
	ProxyDialer      ProxyDialer
	RPCSender        RPCSender
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
		rg.GET("/notebook-volumes/:id/files", h.listVolumeFiles)
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

	var spec Notebook
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

// GET /notebook-volumes/:id/files — list files inside the volume's work_dir.
// Query params: ext (comma-separated extensions), path (subpath within volume).
// Routes through gRPC when the volume has a remote worker; falls back to local walk.
func (h *Handler) listVolumeFiles(c *gin.Context) {
	id := c.Param("id")
	vol, err := h.deps.Volumes.Get(c.Request.Context(), id)
	if err != nil || vol == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "volume not found"})
		return
	}
	if vol.WorkDir == "" {
		c.Header("X-Piper-Files-Truncated", "false")
		c.JSON(http.StatusOK, []string{})
		return
	}

	extFilter := c.Query("ext")
	var extList []string
	if extFilter != "" {
		for _, e := range strings.Split(extFilter, ",") {
			if e = strings.TrimSpace(e); e != "" {
				extList = append(extList, e)
			}
		}
	}

	// Look up the active notebook for this volume (provides Jupyter token for K8s).
	var nbName, nbToken string
	if h.deps.Notebooks != nil {
		if nb, lookupErr := h.deps.Notebooks.GetByVolumeID(c.Request.Context(), vol.ID); lookupErr == nil && nb != nil {
			nbName = nb.Name
			nbToken = nb.Token
		}
	}

	// Route through gRPC when the volume is owned by a specific remote worker.
	if vol.WorkerID != "" && h.deps.RPCSender != nil {
		req := FSListFilesRequest{
			VolumeID: vol.ID,
			WorkDir:  vol.WorkDir,
			Notebook: nbName,
			Token:    nbToken,
			Path:     c.Query("path"),
			Ext:      extList,
			MaxFiles: 500,
		}
		var resp FSListFilesResponse
		if err := h.deps.RPCSender.SendRPC(c.Request.Context(), vol.WorkerID, iagent.MethodFSListFiles, req, &resp); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":     "worker unavailable",
				"code":      "worker_offline",
				"retryable": true,
			})
			c.Header("Retry-After", "2")
			return
		}
		h.writeFilesResponse(c, &resp)
		return
	}

	// Local filesystem walk — single-node bare-metal where master and worker share filesystem.
	walkRoot := vol.WorkDir
	subPath := c.Query("path")
	if subPath != "" {
		// Check raw input before path.Clean removes .. elements.
		if strings.Contains(subPath, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
			return
		}
		walkRoot = filepath.Join(vol.WorkDir, filepath.FromSlash(path.Clean("/"+subPath)))
	}
	files, truncated := WalkFiles(walkRoot, extList, 500)
	if subPath != "" {
		prefix := path.Clean(subPath)
		for i, f := range files {
			files[i] = prefix + "/" + f
		}
	}
	h.writeFilesResponse(c, ReadyResponse(files, truncated))
}

func (h *Handler) writeFilesResponse(c *gin.Context, resp *FSListFilesResponse) {
	if resp.Truncated {
		c.Header("X-Piper-Files-Truncated", "true")
	} else {
		c.Header("X-Piper-Files-Truncated", "false")
	}

	switch resp.State {
	case FSAccessTransitioning:
		c.Header("Retry-After", "2")
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     resp.Message,
			"code":      "volume_transitioning",
			"retryable": true,
		})
	case FSAccessUnavailable:
		c.JSON(http.StatusConflict, gin.H{
			"error":     resp.Message,
			"code":      "volume_unavailable",
			"retryable": false,
		})
	default:
		files := resp.Files
		if files == nil {
			files = []string{}
		}
		c.JSON(http.StatusOK, files)
	}
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

	upstreamPath := tunnelproxy.JoinPathPrefix("/notebooks/"+name+"/proxy", c.Param("path"))

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

	r2 := c.Request.Clone(context.Background())
	r2.URL.Path = upstreamPath
	r2.URL.RawPath = ""

	policy, err := tunnelproxy.BuildPolicy("notebook", tunnelproxy.PolicyContext{
		Request:     c.Request,
		Name:        name,
		Token:       nb.Token,
		Host:        c.Request.Host,
		Scheme:      tunnelproxy.RequestScheme(c.Request),
		ProxyPrefix: "/notebooks/" + name + "/proxy",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	session := tunnelproxy.NewBuilder(upstream).WithPolicy(policy).Build()
	defer func() { _ = session.Close() }()

	if err := session.ServeHTTP(c.Writer, r2); err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
}
