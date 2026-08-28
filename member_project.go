package piper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/internal/httpx"
	"github.com/loykin/piper/internal/runlifecycle"
	"github.com/loykin/piper/pkg/alerting"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/schedule"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/serving"
	"github.com/loykin/piper/pkg/storage"
	"github.com/loykin/piper/pkg/template"
	"github.com/loykin/piper/pkg/viewer"
	viewerhtml "github.com/loykin/piper/pkg/viewer/driver/html"
	viewertb "github.com/loykin/piper/pkg/viewer/driver/tensorboard"
)

// newMemberProjectRouter is reachable only through the authenticated Member
// tunnel. The caller injects project.Context and identity before dispatch,
// so this router deliberately has no user authentication or Home directory
// middleware and cannot be mounted as a public HTTP server.
func (p *Piper) newMemberProjectRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), limitRequestBody(maxRequestBodyBytes))
	projectAPI := r.Group("/projects/:project_id")
	projectAPI.GET("/runs/:id/artifacts/*path", func(c *gin.Context) {
		fullPath := strings.TrimPrefix(c.Param("path"), "/")
		parts := strings.SplitN(fullPath, "/", 2)
		step, rest := parts[0], ""
		if len(parts) == 2 {
			rest = parts[1]
		}
		(&piperArtifacts{p: p}).ServeDownload(c.Writer, c.Request, c.Param("id"), step, rest)
	})
	viewerMgr := viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir)
	viewerMgr.RegisterDriver(viewertb.New())
	viewerMgr.RegisterDriver(viewerhtml.New())
	handlers := p.registerMemberProjectRoutes(projectAPI, viewerMgr, func(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars, experiment string) (string, error) {
		return p.runs.StartRunFromAPI(ctx, yaml, params, vars, experiment)
	})
	p.registerMemberStorageRoutes(projectAPI)
	handlers.serving.RegisterProxyRoutes(projectAPI)
	handlers.notebook.RegisterProxyRoutes(projectAPI)
	handlers.viewer.RegisterProxyRoutes(projectAPI)
	return r
}

type memberProjectHandlers struct {
	serving  *serving.Handler
	notebook *notebook.Handler
	viewer   *viewer.Handler
}

// registerMemberProjectRoutes is the composition root for Member-owned JSON
// API domains (schedules, credentials, serving, notebook, viewer, templates).
// Home mounts the same handlers after its ownership-aware relay middleware so
// Local Member projects still execute in-process; a remote Member mounts them
// only on its private tunnel router.
//
// Storage is deliberately NOT registered here — it's wired separately via
// registerMemberStorageRoutes so Home can relay it through the streaming
// path (relayRemoteProjectHTTP) instead of the buffered one (relayRemoteProject)
// this group uses. Storage object payloads are unbounded blobs; the rest of
// this group is small JSON that benefits from relayRemoteProject's
// Idempotency-Key replay/conflict protection.
func (p *Piper) registerMemberProjectRoutes(projectAPI *gin.RouterGroup, viewerMgr *viewer.Manager, startRun func(context.Context, string, map[string]any, BuiltinVars, string) (string, error)) memberProjectHandlers {
	credential.NewHandler(p.credentials).RegisterRoutes(projectAPI)
	if p.alerts != nil {
		alerting.NewHandler(p.alerts).RegisterRoutes(projectAPI)
	}

	schedule.NewHandler(schedule.HandlerDeps{
		Schedules: p.repos.Schedule,
		Runs:      p.repos.Run,
		Parse: func(yaml []byte) (*pipeline.Pipeline, error) {
			return p.Parse(yaml)
		},
		Sched:    p.scheduler,
		NextTime: runlifecycle.NextScheduleTime,
		Backfill: p.BackfillSchedule,
		GenID:    runlifecycle.GenScheduleID,
	}).RegisterRoutes(projectAPI)

	servingHandler := serving.NewHandler(serving.HandlerDeps{
		Services: p.repos.Serving,
		Deploy:   p.DeployService,
		Stop:     p.StopService,
		Restart:  p.RestartService,
		Proxy:    p.serving.proxy,
	})
	servingHandler.RegisterRoutes(projectAPI)

	notebookHandler := notebook.NewHandler(notebook.HandlerDeps{
		Notebooks:        p.repos.Notebook,
		Volumes:          p.repos.NotebookVolume,
		Workspace:        p.nbWorkspace,
		Create:           p.notebookManager.Create,
		CreateWithVolume: p.notebookManager.CreateWithVolume,
		Stop:             p.notebookManager.Stop,
		Restart:          p.notebookManager.Restart,
		Delete:           p.notebookManager.Delete,
		PurgeVolume:      p.notebookManager.PurgeVolume,
	})
	notebookHandler.RegisterRoutes(projectAPI)

	// docs/jupyter-mcp-execution.md Phase 1 — Kernel session / Notebook
	// execution REST API (§7). Registered here, not on its own group, so it
	// gets the exact same Home relay / Local-Member-in-process treatment
	// every other domain in this composition root gets — see this
	// function's doc comment. Guarded like alerting above: nil when the
	// embedding Repos didn't supply a NotebookExecution repository.
	if p.notebookExecutions != nil {
		execution.NewHandler(p.notebookExecutions).RegisterRoutes(projectAPI)
	}

	viewerHandler := viewer.NewHandler(viewerMgr, p.repos.Viewer)
	viewerHandler.RegisterRoutes(projectAPI)

	template.NewHandler(template.HandlerDeps{
		Templates:    p.repos.PipelineTemplate,
		Volumes:      p.repos.NotebookVolume,
		Notebooks:    p.repos.Notebook,
		Schedules:    p.repos.Schedule,
		Store:        p.store,
		StorageURL:   p.storageURL,
		StorageToken: p.cfg.Storage.Token,
		Workspace:    p.nbWorkspace,
		Sched:        p.scheduler,
		Parse: func(yaml []byte) (*pipeline.Pipeline, error) {
			return p.Parse(yaml)
		},
		StartRun: startRun,
		NextTime: runlifecycle.NextScheduleTime,
		GenID:    runlifecycle.GenScheduleID,
	}).RegisterRoutes(projectAPI)

	return memberProjectHandlers{serving: servingHandler, notebook: notebookHandler, viewer: viewerHandler}
}

func (p *Piper) registerMemberStorageRoutes(projectAPI *gin.RouterGroup) {
	projectStorage := projectAPI.Group("/storage")
	projectStorageMember := projectStorage.Group("", func(c *gin.Context) {
		projectContext, _ := project.FromContext(c.Request.Context())
		if projectContext.Role < security.ProjectRoleMember {
			c.JSON(http.StatusForbidden, gin.H{"error": "member role required"})
			c.Abort()
			return
		}
		c.Next()
	})
	projectStorageMember.POST("/object", func(c *gin.Context) {
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
			return
		}
		defer func() { _ = file.Close() }()
		key := strings.TrimSpace(c.PostForm("key"))
		if key == "" {
			key = header.Filename
		}
		if err := p.UploadStorageObject(c.Request.Context(), key, file, header.Size); err != nil {
			status := http.StatusInternalServerError
			if p.store == nil {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"key": key})
	})
	projectStorage.GET("/objects", func(c *gin.Context) {
		limit, offset := httpx.ParseLimitOffset(c)
		objects, total, err := p.ListStorageObjects(c.Request.Context(), c.Query("prefix"), limit, offset)
		if err != nil {
			status := http.StatusInternalServerError
			if p.store == nil {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		httpx.SetTotalCountHeader(c, limit, total)
		c.JSON(http.StatusOK, objects)
	})
	projectStorage.GET("/object", func(c *gin.Context) {
		key := strings.TrimSpace(c.Query("key"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
			return
		}
		rc, filename, err := p.OpenStorageObject(c.Request.Context(), key)
		if err != nil {
			status := http.StatusInternalServerError
			if err == storage.ErrNotFound {
				status = http.StatusNotFound
			} else if p.store == nil {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = rc.Close() }()
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, rc)
	})
	projectStorageMember.DELETE("/object", func(c *gin.Context) {
		key := strings.TrimSpace(c.Query("key"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
			return
		}
		if err := p.DeleteStorageObject(c.Request.Context(), key); err != nil {
			status := http.StatusInternalServerError
			if p.store == nil {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})
}
