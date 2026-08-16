package notebook

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/internal/httpx"
	"github.com/loykin/piper/internal/tunnelproxy"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

type HandlerDeps struct {
	Notebooks        Repository
	Volumes          VolumeRepository
	Workspace        WorkspaceReader
	Create           func(ctx context.Context, projectID string, spec Notebook, yamlStr string) (*NotebookServer, error)
	CreateWithVolume func(ctx context.Context, projectID string, spec Notebook, volumeID, yamlStr string) (*NotebookServer, error)
	Stop             func(ctx context.Context, projectID, name string) error
	Restart          func(ctx context.Context, projectID, name string) error
	Delete           func(ctx context.Context, projectID, name string) error
	PurgeVolume      func(ctx context.Context, projectID, volumeID string) error
}

// Handler is the Gin HTTP handler for the /notebooks domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

func currentProjectID(c *gin.Context) string {
	projectContext, _ := project.FromContext(c.Request.Context())
	return projectContext.ID
}

// RegisterRoutes mounts the JSON API routes for notebooks.
// The browser proxy route is registered separately via RegisterProxyRoutes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/notebooks", h.listNotebooks)
	rg.GET("/notebooks/:name", h.getNotebook)

	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.POST("/notebooks", h.createNotebook)
	member.POST("/notebooks/:name/stop", h.stopNotebook)
	member.POST("/notebooks/:name/start", h.startNotebook)
	member.DELETE("/notebooks/:name", h.deleteNotebook)

	if h.deps.Volumes != nil {
		rg.GET("/notebook-volumes", h.listVolumes)
		rg.GET("/notebook-volumes/:id/files", h.listVolumeFiles)
		member.DELETE("/notebook-volumes/:id", h.purgeVolume)
	}
}

// RegisterProxyRoutes mounts the browser proxy at the given router group.
// Must be called on a group that already has :project_id in its path so
// proxyNotebook can build the correct Jupyter base_url and redirect prefix.
//
// Expected group path: /projects/:project_id
func (h *Handler) RegisterProxyRoutes(rg *gin.RouterGroup) {
	rg.Any("/notebooks/:name/proxy/*path", h.proxyNotebook)
}

// GET /notebooks
func (h *Handler) listNotebooks(c *gin.Context) {
	nbs, err := h.deps.Notebooks.List(c.Request.Context(), currentProjectID(c))
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

	var spec *Notebook
	var err error
	if req.VolumeID != "" {
		// Reusing an existing volume — spec.volume.size describes
		// provisioning a new one and isn't required here.
		spec, err = ParseForExistingVolume([]byte(req.YAML))
	} else {
		spec, err = Parse([]byte(req.YAML))
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid YAML: " + err.Error()})
		return
	}

	var nb *NotebookServer
	if req.VolumeID != "" {
		if h.deps.CreateWithVolume == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "CreateWithVolume not configured"})
			return
		}
		nb, err = h.deps.CreateWithVolume(c.Request.Context(), currentProjectID(c), *spec, req.VolumeID, req.YAML)
	} else {
		if h.deps.Create == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Create not configured"})
			return
		}
		nb, err = h.deps.Create(c.Request.Context(), currentProjectID(c), *spec, req.YAML)
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, nb)
}

// GET /notebooks/:name
func (h *Handler) getNotebook(c *gin.Context) {
	name := c.Param("name")
	nb, err := h.deps.Notebooks.Get(c.Request.Context(), currentProjectID(c), name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if nb == nil {
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
	if err := h.deps.Stop(c.Request.Context(), currentProjectID(c), name); err != nil {
		writeLifecycleError(c, err)
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
	if err := h.deps.Restart(c.Request.Context(), currentProjectID(c), name); err != nil {
		writeLifecycleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DELETE /notebooks/:name — removes the server record and releases the backing volume.
// The volume's work directory is preserved on disk (recoverable via the volume endpoint).
func (h *Handler) deleteNotebook(c *gin.Context) {
	name := c.Param("name")
	if h.deps.Delete == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Delete not configured"})
		return
	}
	if err := h.deps.Delete(c.Request.Context(), currentProjectID(c), name); err != nil {
		writeLifecycleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeLifecycleError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, ErrNotFound) {
		status = http.StatusNotFound
	} else if errors.Is(err, ErrConflict) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

// GET /notebook-volumes — list all volumes for the current project.
func (h *Handler) listVolumes(c *gin.Context) {
	limit, offset := httpx.ParseLimitOffset(c)
	projectID := currentProjectID(c)
	vols, err := h.deps.Volumes.List(c.Request.Context(), projectID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if limit > 0 {
		total, err := h.deps.Volumes.Count(c.Request.Context(), projectID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		httpx.SetTotalCountHeader(c, limit, total)
	}
	c.JSON(http.StatusOK, vols)
}

// GET /notebook-volumes/:id/files — list files inside the volume's workspace.
// Query params: ext (comma-separated extensions), path (subpath within volume).
// WorkspaceReader abstracts the runtime boundary: baremetal/docker read the
// host directory, while K8s execs into the currently-running notebook pod.
func (h *Handler) listVolumeFiles(c *gin.Context) {
	id := c.Param("id")
	vol, err := h.deps.Volumes.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if vol == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "volume not found"})
		return
	}
	// Verify volume belongs to current project.
	if vol.ProjectID != currentProjectID(c) {
		c.JSON(http.StatusNotFound, gin.H{"error": "volume not found"})
		return
	}
	extFilter := c.Query("ext")
	extAllowed := map[string]bool{}
	if extFilter != "" {
		for _, e := range strings.Split(extFilter, ",") {
			if e = strings.TrimSpace(e); e != "" {
				extAllowed[e] = true
			}
		}
	}

	subPath, err := CleanWorkspacePath(c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	reader := h.deps.Workspace
	if reader == nil {
		reader = LocalWorkspaceReader{}
	}
	workspaceFiles, err := reader.ListFiles(c.Request.Context(), vol, subPath)
	if err != nil {
		h.writeFilesResponse(c, UnavailableResponse(err.Error()))
		return
	}

	files := make([]string, 0, min(len(workspaceFiles), 500))
	truncated := false
	for _, wf := range workspaceFiles {
		rel, cleanErr := CleanWorkspacePath(wf.Rel)
		if cleanErr != nil || rel == "" || workspacePathHidden(rel) {
			continue
		}
		if len(extAllowed) > 0 && !extAllowed[path.Ext(rel)] {
			continue
		}
		if subPath != "" {
			rel = subPath + "/" + rel
		}
		files = append(files, rel)
		if len(files) == 500 {
			truncated = true
			break
		}
	}
	sort.Strings(files)
	h.writeFilesResponse(c, ReadyResponse(files, truncated))
}

func workspacePathHidden(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
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
	// Verify volume belongs to current project before purging.
	vol, err := h.deps.Volumes.Get(c.Request.Context(), id)
	if err != nil || vol == nil || vol.ProjectID != currentProjectID(c) {
		c.JSON(http.StatusNotFound, gin.H{"error": "volume not found"})
		return
	}
	if h.deps.PurgeVolume == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PurgeVolume not configured"})
		return
	}
	if err := h.deps.PurgeVolume(c.Request.Context(), currentProjectID(c), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ANY /notebooks/:name/proxy/*path — reverse-proxies to the notebook endpoint.
// Handles both HTTP and WebSocket (required for Jupyter kernel communication).
// Called from a group that has :project_id in scope, so the full Jupyter
// base_url is /projects/:project_id/notebooks/:name/proxy. Endpoint is
// always a plain, directly reachable URL — every direct-runtime driver
// (docker/baremetal/k8s) returns one, since Piper dispatches in-process now
// and there is no more agent tunnel to relay through.
func (h *Handler) proxyNotebook(c *gin.Context) {
	projectID := currentProjectID(c)
	name := c.Param("name")
	nb, err := h.deps.Notebooks.Get(c.Request.Context(), projectID, name)
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

	proxyPrefix := "/projects/" + projectID + "/notebooks/" + name + "/proxy"
	upstreamPath := tunnelproxy.JoinPathPrefix(proxyPrefix, c.Param("path"))

	policy, err := tunnelproxy.BuildPolicy("notebook", tunnelproxy.PolicyContext{
		Request:     c.Request,
		Name:        name,
		Token:       nb.Token,
		Host:        c.Request.Host,
		Scheme:      tunnelproxy.RequestScheme(c.Request),
		ProxyPrefix: proxyPrefix,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	r2 := c.Request.Clone(context.Background())
	r2.URL.Path = upstreamPath
	r2.URL.RawPath = ""
	tunnelproxy.ServeReverseProxy(c.Writer, r2, target, policy)
}
