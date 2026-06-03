package notebook

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/internal/agent"
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

type HandlerDeps struct {
	Notebooks        Repository
	Volumes          VolumeRepository
	Promotions       PromotionService
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
	rg.GET("/api/notebooks/:name/promotion", h.getPromotion)
	rg.POST("/api/notebooks/:name/promotion/validate", h.validatePromotion)
	rg.POST("/api/notebooks/:name/promotion/export", h.exportPromotion)
	rg.GET("/api/notebooks/:name/promotion/exports", h.listPromotionExports)
	rg.GET("/api/notebooks/:name/promotion/exports/:file", h.downloadPromotionExport)

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

func (h *Handler) promotionTarget(c *gin.Context) PromotionTarget {
	if raw := strings.TrimSpace(c.Query("target")); raw != "" {
		return PromotionTarget(raw)
	}
	var req PromotionRequest
	if err := c.ShouldBindJSON(&req); err == nil {
		if raw := strings.TrimSpace(string(req.Target)); raw != "" {
			return PromotionTarget(raw)
		}
	}
	return PromotionTargetDraft
}

func (h *Handler) promotionService() PromotionService {
	return h.deps.Promotions
}

// GET /api/notebooks/:name/promotion
func (h *Handler) getPromotion(c *gin.Context) {
	if h.promotionService() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "promotion not configured"})
		return
	}
	target := h.promotionTarget(c)
	preview, err := h.promotionService().Preview(c.Request.Context(), c.Param("name"), target)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

// POST /api/notebooks/:name/promotion/validate
func (h *Handler) validatePromotion(c *gin.Context) {
	if h.promotionService() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "promotion not configured"})
		return
	}
	target := h.promotionTarget(c)
	validation, err := h.promotionService().Validate(c.Request.Context(), c.Param("name"), target)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, validation)
}

// POST /api/notebooks/:name/promotion/export
func (h *Handler) exportPromotion(c *gin.Context) {
	if h.promotionService() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "promotion not configured"})
		return
	}
	target := h.promotionTarget(c)
	result, err := h.promotionService().Export(c.Request.Context(), c.Param("name"), target)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// GET /api/notebooks/:name/promotion/exports
func (h *Handler) listPromotionExports(c *gin.Context) {
	if h.promotionService() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "promotion not configured"})
		return
	}
	records, err := h.promotionService().ListExports(c.Request.Context(), c.Param("name"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, records)
}

// GET /api/notebooks/:name/promotion/exports/:file
func (h *Handler) downloadPromotionExport(c *gin.Context) {
	if h.promotionService() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "promotion not configured"})
		return
	}
	dl, err := h.promotionService().DownloadExport(c.Request.Context(), c.Param("name"), c.Param("file"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = dl.Reader.Close() }()
	c.Header("Content-Type", dl.ContentType)
	c.Header("Content-Disposition", `attachment; filename="`+dl.Name+`"`)
	if _, err := io.Copy(c.Writer, dl.Reader); err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
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
