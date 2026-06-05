package pipelinetemplate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piper/piper/pkg/blobstore"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/schedule"
	"gopkg.in/yaml.v3"
)

// HandlerDeps holds all external dependencies for the pipeline template handler.
type HandlerDeps struct {
	Templates Repository
	Volumes   notebook.VolumeRepository
	Schedules schedule.Repository
	Store     blobstore.Store // nil when object storage is not configured

	Parse    func(yaml []byte) (*pipeline.Pipeline, error)
	StartRun func(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error)

	OwnerID func(r *http.Request) string
	GenID   func() string // generates a schedule ID; may be nil
}

// Handler is the Gin HTTP handler for the /pipelines domain.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a new Handler.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes mounts all /pipelines routes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/pipelines", h.submit)
	rg.GET("/pipelines", h.list)
	rg.DELETE("/pipelines/:id", h.delete)
	rg.POST("/pipelines/:id/run", h.triggerRun)
	rg.POST("/pipelines/:id/deploy", h.deploy)
}

// POST /pipelines — submit a new pipeline template
func (h *Handler) submit(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		YAML     string `json:"yaml"`
		VolumeID string `json:"volume_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.YAML = strings.TrimSpace(req.YAML)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if req.YAML == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "yaml is required"})
		return
	}

	// Validate YAML
	pl, err := h.deps.Parse([]byte(req.YAML))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid pipeline yaml: %s", err)})
		return
	}

	// Collect local source paths from steps
	localPaths := extractLocalPaths(pl)

	// If any local paths exist, we need object storage and a volume
	snapshotID := uuid.New().String()
	if len(localPaths) > 0 {
		if h.deps.Store == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "object storage is not configured; cannot snapshot local source files"})
			return
		}
		if req.VolumeID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "volume_id is required when pipeline has local source steps"})
			return
		}

		vol, err := h.deps.Volumes.Get(c.Request.Context(), req.VolumeID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "volume not found"})
			return
		}

		// Upload local files to object storage
		uploaded, uploadErr := h.uploadSnapshot(c.Request.Context(), snapshotID, vol.WorkDir, localPaths)
		if uploadErr != nil {
			// Rollback: delete already-uploaded objects
			if len(uploaded) > 0 {
				_ = h.deps.Store.Delete(c.Request.Context(), uploaded...)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("snapshot upload failed: %s", uploadErr)})
			return
		}
	}

	t := &Template{
		ID:         uuid.New().String(),
		Name:       req.Name,
		YAML:       req.YAML,
		SnapshotID: snapshotID,
		VolumeID:   req.VolumeID,
		CreatedAt:  time.Now().UTC(),
	}
	if err := h.deps.Templates.Create(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, t)
}

// GET /pipelines — list templates
func (h *Handler) list(c *gin.Context) {
	f := Filter{Name: c.Query("name")}
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil && n > 0 {
			f.Limit = n
		}
	}
	templates, err := h.deps.Templates.List(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, templates)
}

// DELETE /pipelines/:id — delete template and its S3 snapshot
func (h *Handler) delete(c *gin.Context) {
	id := c.Param("id")

	t, err := h.deps.Templates.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pipeline template not found"})
		return
	}

	// Delete S3 snapshot prefix (best-effort list then delete)
	if h.deps.Store != nil && t.SnapshotID != "" {
		prefix := "snapshots/" + t.SnapshotID + "/"
		objs, _ := h.deps.Store.List(c.Request.Context(), prefix)
		if len(objs) > 0 {
			keys := make([]string, len(objs))
			for i, o := range objs {
				keys[i] = o.Key
			}
			_ = h.deps.Store.Delete(c.Request.Context(), keys...)
		}
	}

	if err := h.deps.Templates.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// POST /pipelines/:id/run — trigger an immediate run from a template
func (h *Handler) triggerRun(c *gin.Context) {
	id := c.Param("id")

	t, err := h.deps.Templates.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pipeline template not found"})
		return
	}

	var req struct {
		Params map[string]any `json:"params,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)

	rewrittenYAML := rewriteLocalSources(t.YAML, t.SnapshotID, t.Name)

	ownerID := ""
	if h.deps.OwnerID != nil {
		ownerID = h.deps.OwnerID(c.Request)
	}

	runID, err := h.deps.StartRun(c.Request.Context(), rewrittenYAML, ownerID, req.Params, proto.BuiltinVars{}, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": runID})
}

// POST /pipelines/:id/deploy — deploy a template as a schedule
func (h *Handler) deploy(c *gin.Context) {
	id := c.Param("id")

	t, err := h.deps.Templates.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pipeline template not found"})
		return
	}

	var req struct {
		Cron    string         `json:"cron"`
		Enabled bool           `json:"enabled"`
		Params  map[string]any `json:"params,omitempty"`
	}
	req.Enabled = true
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Cron == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cron is required"})
		return
	}

	rewrittenYAML := rewriteLocalSources(t.YAML, t.SnapshotID, t.Name)

	paramsJSON := "{}"
	if req.Params != nil {
		if b, err := json.Marshal(req.Params); err == nil {
			paramsJSON = string(b)
		}
	}

	// Generate schedule ID
	schedID := "sch-" + time.Now().Format("20060102150405.000000000")
	if h.deps.GenID != nil {
		schedID = h.deps.GenID()
	}

	now := time.Now().UTC()
	sc := &schedule.Schedule{
		ID:           schedID,
		Name:         t.Name,
		PipelineYAML: rewrittenYAML,
		ScheduleType: "cron",
		CronExpr:     req.Cron,
		Enabled:      req.Enabled,
		ParamsJSON:   paramsJSON,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.deps.Schedules.Create(c.Request.Context(), sc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, sc)
}

// extractLocalPaths returns all source paths from steps where source is local or empty.
func extractLocalPaths(pl *pipeline.Pipeline) []string {
	seen := map[string]bool{}
	var paths []string
	for _, step := range pl.Spec.Steps {
		src := step.Run.Source
		if src != "" && src != "local" {
			continue
		}
		p := step.Run.Path
		if step.Run.Notebook != "" {
			p = step.Run.Notebook
		}
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

// uploadSnapshot copies local source files into object storage under the snapshot prefix.
// Returns the list of successfully uploaded keys so the caller can roll back on error.
func (h *Handler) uploadSnapshot(ctx context.Context, snapshotID, workDir string, paths []string) ([]string, error) {
	var uploaded []string
	for _, rel := range paths {
		src := filepath.Join(workDir, rel)
		f, err := os.Open(src)
		if err != nil {
			return uploaded, fmt.Errorf("open %s: %w", rel, err)
		}
		info, statErr := f.Stat()
		size := int64(-1)
		if statErr == nil {
			size = info.Size()
		}
		key := "snapshots/" + snapshotID + "/" + rel
		putErr := h.deps.Store.Put(ctx, key, f, size)
		_ = f.Close()
		if putErr != nil {
			return uploaded, fmt.Errorf("upload %s: %w", rel, putErr)
		}
		uploaded = append(uploaded, key)
	}
	return uploaded, nil
}

// rewriteLocalSources rewrites `source: local` (or empty) step sources to `source: s3`
// with paths prefixed by the snapshot. It also sets metadata.name = templateName so that
// runs triggered from a template record pipeline_name = template.name, not the YAML metadata.
func rewriteLocalSources(yamlText, snapshotID, templateName string) string {
	pl, err := pipeline.Parse([]byte(yamlText))
	if err != nil {
		return yamlText
	}

	pl.Metadata.Name = templateName

	for i, step := range pl.Spec.Steps {
		if step.Run.Source != "" && step.Run.Source != "local" {
			continue
		}
		pl.Spec.Steps[i].Run.Source = "s3"
		if step.Run.Notebook != "" {
			pl.Spec.Steps[i].Run.Notebook = "snapshots/" + snapshotID + "/" + step.Run.Notebook
		} else if step.Run.Path != "" {
			pl.Spec.Steps[i].Run.Path = "snapshots/" + snapshotID + "/" + step.Run.Path
		}
	}

	b, err := yaml.Marshal(pl)
	if err != nil {
		return yamlText
	}
	return string(b)
}
