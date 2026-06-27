package piper

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/piper/piper/internal/event"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/run"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/security"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/storage"
	"github.com/piper/piper/pkg/template"
	"github.com/piper/piper/pkg/ui"
	"github.com/piper/piper/pkg/viewer"
	viewerhtml "github.com/piper/piper/pkg/viewer/driver/html"
	viewertb "github.com/piper/piper/pkg/viewer/driver/tensorboard"
)

const maxRequestBodyBytes int64 = 1 << 20

// ServeOption customizes the behavior of Serve
type ServeOption struct {
	// Extra is an additional http.Handler injected by the caller.
	// It is invoked before the piper API (auth, custom routes, etc.).
	//
	//   p.Serve(ctx, piper.ServeOption{
	//       Extra: myRouter,  // chi, gin, echo, etc.
	//   })
	Extra http.Handler

	// Addr overrides Config.Server.Addr when non-empty.
	Addr string
}

// Serve runs the piper HTTP server.
// Supports both HTTP and HTTPS. Library users can call this directly or
// mount it on their own server using Handler().
func (p *Piper) Serve(ctx context.Context, opt ServeOption) error {
	// Build the viewer manager once and share it between the cleanup loop and the HTTP handler.
	viewerMgr := viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir)
	viewerMgr.RegisterDriver(viewertb.New())
	viewerMgr.RegisterDriver(viewerhtml.New())

	// Mark viewers left in starting/running from a previous run as failed.
	viewerMgr.MarkStaleFailed(ctx)

	// TTL cleanup: stop expired viewers every 5 minutes.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				viewerMgr.CleanupExpired(ctx)
			}
		}
	}()

	handler := p.newRouter(opt.Extra, viewerMgr)

	// Apply middleware chain (Config.Hooks.Middleware)
	for i := len(p.cfg.Hooks.Middleware) - 1; i >= 0; i-- {
		handler = p.cfg.Hooks.Middleware[i](handler)
	}

	addr := p.cfg.Server.Addr
	if opt.Addr != "" {
		addr = opt.Addr
	}
	if addr == "" {
		addr = ":8080"
	}

	grpcSrv := p.grpcAgentServer.GRPCServer(p.workerTokenGRPCOptions()...)
	combinedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcSrv.ServeHTTP(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           combinedHandler,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally unset: SSE streaming endpoints require
		// an unbounded write deadline. ReadTimeout is also unset because the
		// worker's bidirectional gRPC request body stays open for its lifetime.
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		// gRPC is hosted through ServeHTTP on the shared listener. Its handler
		// transport does not implement graceful drain; Stop closes active streams.
		grpcSrv.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	tlsCfg := p.cfg.Server.TLS
	if tlsCfg.Enabled {
		if tlsCfg.CertFile == "" || tlsCfg.KeyFile == "" {
			return fmt.Errorf("TLS enabled but cert_file or key_file not set")
		}
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS cert: %w", err)
		}
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		slog.Info("piper server starting (HTTPS)", "addr", srv.Addr)
		if err := srv.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			return err
		}
		return nil
	}

	slog.Info("piper server starting (HTTP)", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// newRouter builds the Gin router wired with all domain handlers.
func (p *Piper) newRouter(extra http.Handler, viewerMgr *viewer.Manager) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(limitRequestBody(maxRequestBodyBytes))

	// Caller-provided routes run before Piper routes. Authentication is applied
	// only to user-facing groups below; worker routes have a separate credential.
	r.Use(func(c *gin.Context) {
		if extra != nil {
			rw := &responseRecorder{ResponseWriter: c.Writer}
			extra.ServeHTTP(rw, c.Request)
			if rw.written {
				c.Abort()
				return
			}
		}
		c.Next()
	})

	userAPI := r.Group("/api", p.authenticateUser())
	p.registerAuthRoutes(r, userAPI)
	sysAdmin := p.registerAdminRoutes(userAPI)

	// Project management — logged-in users can list; create/delete is system-admin.
	project.NewHandler(p.repos.Project, p.cfg.Auth.Authorizer).RegisterRoutes(userAPI)
	projectStorage := userAPI.Group("/projects/:project_id/storage", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer))
	projectStorageMember := projectStorage.Group("", project.RequireRole(security.ProjectRoleMember))
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
			httpStatus := http.StatusInternalServerError
			if p.store == nil {
				httpStatus = http.StatusServiceUnavailable
			}
			c.JSON(httpStatus, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"key": key})
	})
	projectStorage.GET("/objects", func(c *gin.Context) {
		prefix := c.Query("prefix")
		objs, err := p.ListStorageObjects(c.Request.Context(), prefix)
		if err != nil {
			httpStatus := http.StatusInternalServerError
			if p.store == nil {
				httpStatus = http.StatusServiceUnavailable
			}
			c.JSON(httpStatus, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, objs)
	})
	projectStorage.GET("/object", func(c *gin.Context) {
		key := strings.TrimSpace(c.Query("key"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
			return
		}
		rc, filename, err := p.OpenStorageObject(c.Request.Context(), key)
		if err != nil {
			httpStatus := http.StatusInternalServerError
			if err == storage.ErrNotFound {
				httpStatus = http.StatusNotFound
			} else if p.store == nil {
				httpStatus = http.StatusServiceUnavailable
			}
			c.JSON(httpStatus, gin.H{"error": err.Error()})
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
			httpStatus := http.StatusInternalServerError
			if p.store == nil {
				httpStatus = http.StatusServiceUnavailable
			}
			c.JSON(httpStatus, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})

	// Run domain
	runHandler := run.NewHandler(run.HandlerDeps{
		Runs:    p.repos.Run,
		Steps:   p.repos.Step,
		Logs:    p.logs,
		Metrics: p.metrics,
		StartRun: func(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars, experiment string) (string, error) {
			return p.startRunFromAPI(ctx, yaml, params, vars, experiment)
		},
		StartSweep: func(ctx context.Context, req run.SweepRequest) (run.SweepResponse, error) {
			projectContext, _ := project.FromContext(ctx)
			return p.startSweep(ctx, projectContext.ID, req)
		},
		CancelRun: p.CancelRun,
		RerunRun:  p.RerunRun,
		RetryStep: p.RetryStep,
		DeleteRun: p.DeleteRun,
		Artifacts: &piperArtifacts{p: p},
		Hooks:     &piperRunHooks{p: p},
	})
	runHandler.RegisterRoutes(userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))

	// Schedule domain
	schedule.NewHandler(schedule.HandlerDeps{
		Schedules: p.repos.Schedule,
		Runs:      p.repos.Run,
		Parse: func(yaml []byte) (*pipeline.Pipeline, error) {
			return p.Parse(yaml)
		},
		Trigger: func(ctx context.Context, sc *schedule.Schedule) {
			p.triggerSchedule(p.ctx, sc)
		},
		NextTime: nextScheduleTime,
		Backfill: p.BackfillSchedule,
		GenID:    genScheduleID,
	}).RegisterRoutes(userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))

	// Serving domain (JSON API + browser predict proxy)
	servingHandler := serving.NewHandler(serving.HandlerDeps{
		Services: p.repos.Serving,
		Deploy:   p.DeployService,
		Stop:     p.StopService,
		Restart:  p.RestartService,
		Proxy:    p.serving.proxy,
	})
	servingHandler.RegisterRoutes(userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))
	servingHandler.RegisterProxyRoutes(r.Group("/projects/:project_id", p.authenticateUser(), project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))

	// Notebook domain (JSON API + browser proxy)
	notebookHandler := notebook.NewHandler(notebook.HandlerDeps{
		Notebooks:        p.repos.Notebook,
		Volumes:          p.repos.NotebookVolume,
		Create:           p.notebookManager.Create,
		CreateWithVolume: p.notebookManager.CreateWithVolume,
		Stop:             p.notebookManager.Stop,
		Restart:          p.notebookManager.Restart,
		Delete:           p.notebookManager.Delete,
		PurgeVolume:      p.notebookManager.PurgeVolume,
		AgentRegistry:    p.agentRegistry,
		ProxyDialer:      p.grpcAgentServer,
		RPCSender:        p.grpcAgentServer,
	})
	notebookHandler.RegisterRoutes(userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))
	notebookHandler.RegisterProxyRoutes(r.Group("/projects/:project_id", p.authenticateUser(), project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))

	// Viewer domain (artifact viewer: TensorBoard, HTML, etc.)
	viewerHandler := viewer.NewHandler(viewerMgr, p.repos.Viewer)
	viewerHandler.RegisterRoutes(userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))
	viewerHandler.RegisterProxyRoutes(r.Group("/projects/:project_id", p.authenticateUser(), project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))

	// Pipeline template domain
	template.NewHandler(template.HandlerDeps{
		Templates: p.repos.PipelineTemplate,
		Volumes:   p.repos.NotebookVolume,
		Schedules: p.repos.Schedule,
		Store:     p.store,
		Parse: func(yaml []byte) (*pipeline.Pipeline, error) {
			return p.Parse(yaml)
		},
		StartRun: func(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars, experiment string) (string, error) {
			return p.startRunFromAPI(ctx, yaml, params, vars, experiment)
		},
		NextTime: nextScheduleTime,
		GenID:    genScheduleID,
	}).RegisterRoutes(userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer)))

	// Built-in file server: expose /store/* routes only when using a LocalStore.
	// Workers and K8s pods reach the store via HTTP using the master URL.
	p.registerStoreRoutes(r)

	p.registerWorkerRoutes(sysAdmin)

	// JupyterLab requests /custom/custom.css as an absolute path (no base_url prefix).
	// The file is empty by convention — it is a user customization hook.
	r.GET("/custom/*path", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/css; charset=utf-8", nil)
	})

	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", p.authenticateUser(), p.requireSystemAdmin(), p.metricsHandler)
	r.GET("/events", p.authenticateUser(), p.eventsHandler) // filtered by project_id param; see eventsHandler

	// SPA — served under /ui/; root redirects for convenience
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/ui/") })
	r.GET("/ui", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/ui/") })
	r.GET("/ui/*filepath", gin.WrapH(http.StripPrefix("/ui", ui.Handler())))

	return r
}

func limitRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if c.Request.ContentLength > maxBytes {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				c.Abort()
				return
			}
			if c.Request.Body != nil {
				c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
			}
		}
		c.Next()
	}
}

// Handler returns a handler whose gRPC lifecycle is tied to Piper.Close.
// Library users that own an HTTP server should prefer HandlerContext and cancel
// its context when that server shuts down.
//
//	mux.Handle("/piper/", http.StripPrefix("/piper", p.Handler(nil)))
func (p *Piper) Handler(extra http.Handler) http.Handler {
	return p.HandlerContext(p.ctx, extra)
}

// HandlerContext returns the Piper HTTP/gRPC handler and stops its gRPC server
// when ctx is cancelled. The context should share the parent HTTP server's
// lifecycle so connected workers receive a deterministic shutdown.
//
// The caller's http.Server must enable unencrypted HTTP/2 (H2C) so that gRPC
// workers can connect:
//
//	srv.Protocols = new(http.Protocols)
//	srv.Protocols.SetHTTP1(true)
//	srv.Protocols.SetUnencryptedHTTP2(true)
func (p *Piper) HandlerContext(ctx context.Context, extra http.Handler) http.Handler {
	handler, _ := p.handlerContext(ctx, extra)
	return handler
}

func (p *Piper) handlerContext(ctx context.Context, extra http.Handler) (http.Handler, <-chan struct{}) {
	mgr := viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir)
	mgr.RegisterDriver(viewertb.New())
	mgr.RegisterDriver(viewerhtml.New())
	handler := p.newRouter(extra, mgr)
	grpcSrv := p.grpcAgentServer.GRPCServer(p.workerTokenGRPCOptions()...)
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		grpcSrv.Stop()
		close(stopped)
	}()
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcSrv.ServeHTTP(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
	return combined, stopped
}

// ── responseRecorder ────────────────────────────────────────────────────────

type responseRecorder struct {
	gin.ResponseWriter
	written bool
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.written = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	rw.written = true
	return rw.ResponseWriter.Write(b)
}

// ── built-in file server ──────────────────────────────────────────────────────

// registerStoreRoutes mounts /store/* routes when using the built-in LocalStore.
// K8s pods and remote workers can upload/download artifacts over HTTP without MinIO.
func (p *Piper) registerStoreRoutes(r *gin.Engine) {
	ls, ok := p.store.(*storage.LocalStore)
	if !ok {
		return // external store (S3, HTTP) — no need for built-in server routes
	}
	// Built-in store is accessed by workers only; protect with the worker token.
	rg := r.Group("/store", p.workerTokenMiddleware())
	rg.PUT("/*key", func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		if err := ls.Put(c.Request.Context(), key, c.Request.Body, c.Request.ContentLength); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})
	rg.GET("/*key", func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		if c.Query("list") == "1" {
			// List keys under prefix query param
			prefix := c.Query("prefix")
			objs, err := ls.List(c.Request.Context(), prefix)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			keys := make([]string, len(objs))
			for i, o := range objs {
				keys[i] = o.Key
			}
			c.JSON(http.StatusOK, keys)
			return
		}
		rc, err := ls.Get(c.Request.Context(), key)
		if err != nil {
			if err == storage.ErrNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = rc.Close() }()
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, rc)
	})
	rg.DELETE("/*key", func(c *gin.Context) {
		key := strings.TrimPrefix(c.Param("key"), "/")
		if err := ls.Delete(c.Request.Context(), key); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (p *Piper) authenticateUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticator := p.cfg.Auth.Authenticator
		if authenticator == nil {
			c.Next()
			return
		}
		identity, err := authenticator.Authenticate(c.Request.Context(), c.Request)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		if identity != nil {
			c.Request = c.Request.WithContext(
				security.WithIdentity(c.Request.Context(), identity),
			)
		}
		c.Next()
	}
}

// requireSystemAdmin returns a Gin middleware that allows only system admins.
// In trusted mode all requests pass through.
func (p *Piper) requireSystemAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		authorizer := p.cfg.Auth.Authorizer
		if authorizer == nil {
			c.Next()
			return
		}
		identity, _ := security.IdentityFromContext(c.Request.Context())
		if err := authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "system admin required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// workerTokenGRPCOptions returns gRPC server options that install unary and
// stream interceptors to verify the worker token in Authorization metadata.
// Returns nil when no token is configured (trusted mode).
func (p *Piper) workerTokenGRPCOptions() []grpc.ServerOption {
	token := p.cfg.Server.WorkerToken
	if token == "" {
		return nil
	}
	check := func(ctx context.Context) error {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			for _, v := range md.Get("authorization") {
				if strings.TrimPrefix(v, "Bearer ") == token {
					return nil
				}
			}
		}
		return status.Error(codes.Unauthenticated, "invalid worker token")
	}
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := check(ctx); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := check(ss.Context()); err != nil {
				return err
			}
			return handler(srv, ss)
		}),
	}
}

// workerTokenMiddleware returns a Gin middleware that requires the request to
// carry the configured worker token. When WorkerToken is empty the check is
// skipped for trusted/dev mode.
func (p *Piper) workerTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := p.cfg.Server.WorkerToken
		if token == "" {
			c.Next()
			return
		}
		auth := c.Request.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid worker token"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (p *Piper) eventsHandler(c *gin.Context) {
	// ?project_id=xxx filters to events scoped to that project plus infra events.
	// Without project_id only system admins receive all events.
	filterProject := strings.TrimSpace(c.Query("project_id"))

	if p.cfg.Auth.Authorizer != nil {
		identity, _ := security.IdentityFromContext(c.Request.Context())
		if filterProject != "" {
			// Verify caller can access the requested project.
			role, err := p.cfg.Auth.Authorizer.ProjectRole(c.Request.Context(), identity, filterProject)
			if err != nil || role < security.ProjectRoleViewer {
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
				return
			}
		} else {
			// No project filter → require system admin to avoid info leak.
			if err := p.cfg.Auth.Authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "system admin required for global event stream"})
				return
			}
		}
	}

	events, cancel := p.events.Subscribe()
	defer cancel()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case ev := <-events:
			if filterProject != "" {
				// Project-scoped stream: deliver only events for this project.
				// Infra events (ProjectID=="") are infrastructure-level and must not
				// leak to project users — they are available on the unfiltered stream.
				if ev.ProjectID != filterProject {
					return true
				}
			}
			_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, event.Encode(ev))
			return true
		}
	})
}

func genRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}

func genScheduleID() string {
	return fmt.Sprintf("sch-%s", genRunID())
}

// startRunFromAPI handles creating a run from the HTTP API, including
// future-scheduled runs and immediate dispatch.
func (p *Piper) startRunFromAPI(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars, experiment string) (string, error) {
	projectContext, _ := project.FromContext(ctx)

	pl, err := p.Parse([]byte(yaml))
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return "", fmt.Errorf("build dag: %w", err)
	}

	// Future-scheduled runs are stored but not enqueued yet.
	now := time.Now().UTC()
	if vars.ScheduledAt != nil && vars.ScheduledAt.After(now) {
		runID := genRunID()
		newRun := &run.Run{
			ID:           runID,
			ProjectID:    projectContext.ID,
			Experiment:   experiment,
			PipelineName: pl.Metadata.Name,
			Status:       run.StatusScheduled,
			StartedAt:    now,
			ScheduledAt:  vars.ScheduledAt,
			PipelineYAML: yaml,
			ParamsJSON:   encodeParams(params),
		}
		if err := p.repos.Run.Create(ctx, newRun); err != nil {
			return "", err
		}
		return runID, nil
	}

	return p.startRun(ctx, pl, dag, StartRunOptions{
		ProjectID:  projectContext.ID,
		Experiment: experiment,
		Params:     params,
		Vars:       vars,
		YAML:       yaml,
	})
}

// StartRun is the exported entry point for creating a run from the HTTP API.
func (p *Piper) StartRun(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars) (string, error) {
	return p.startRunFromAPI(ctx, yaml, params, vars, "")
}

// CancelRun cancels a queued or running run.
func (p *Piper) CancelRun(ctx context.Context, runID string) error {
	projectContext, _ := project.FromContext(ctx)
	return p.queue.Cancel(ctx, projectContext.ID, runID)
}

// RerunRun re-executes a run, optionally limiting to failed steps only.
func (p *Piper) RerunRun(ctx context.Context, runID string, failedOnly bool) (string, error) {
	return p.rerunRun(ctx, runID, failedOnly)
}

// RetryStep retries a single failed step within a run.
func (p *Piper) RetryStep(ctx context.Context, runID, stepName string) (string, error) {
	return p.retryStep(ctx, runID, stepName)
}

// DeleteRun deletes a run and its artifacts.
func (p *Piper) DeleteRun(ctx context.Context, runID string) error {
	return p.deleteRunWithArtifacts(ctx, runID)
}

func (p *Piper) cancelRun(ctx context.Context, runID string) error {
	projectContext, _ := project.FromContext(ctx)
	return p.queue.Cancel(ctx, projectContext.ID, runID)
}

func (p *Piper) metricsHandler(c *gin.Context) {
	runs, err := p.listRunsAcrossProjects(c.Request.Context(), run.RunFilter{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	counts := map[string]int{}
	var totalDurationSeconds float64
	var completed int
	for _, r := range runs {
		counts[r.Status]++
		if r.EndedAt != nil {
			totalDurationSeconds += r.EndedAt.Sub(r.StartedAt).Seconds()
			completed++
		}
	}
	stats := p.queue.Stats()
	c.Header("Content-Type", "text/plain; version=0.0.4")
	for runStatus, count := range counts {
		_, _ = fmt.Fprintf(c.Writer, "piper_runs_total{status=%q} %d\n", runStatus, count)
	}
	_, _ = fmt.Fprintf(c.Writer, "piper_run_duration_seconds_sum %.3f\n", totalDurationSeconds)
	_, _ = fmt.Fprintf(c.Writer, "piper_run_duration_seconds_count %d\n", completed)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_runs %d\n", stats.Runs)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_tasks{status=\"pending\"} %d\n", stats.Pending)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_tasks{status=\"ready\"} %d\n", stats.Ready)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_tasks{status=\"running\"} %d\n", stats.Running)
	workerCount := len(p.agentRegistry.List())
	_, _ = fmt.Fprintf(c.Writer, "piper_workers %d\n", workerCount)
}

func (p *Piper) rerunRun(ctx context.Context, runID string, failedOnly bool) (string, error) {
	projectContext, _ := project.FromContext(ctx)
	prev, err := p.repos.Run.Get(ctx, projectContext.ID, runID)
	if err != nil || prev == nil {
		return "", fmt.Errorf("run %q not found", runID)
	}
	if prev.PipelineYAML == "" {
		return "", fmt.Errorf("run %q has no stored pipeline yaml", runID)
	}
	var params map[string]any
	if prev.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(prev.ParamsJSON), &params)
	}
	pl, err := p.Parse([]byte(prev.PipelineYAML))
	if err != nil {
		return "", fmt.Errorf("parse previous run yaml: %w", err)
	}
	if failedOnly {
		steps, err := p.repos.Step.List(ctx, projectContext.ID, runID)
		if err != nil {
			return "", err
		}
		failed := map[string]bool{}
		for _, s := range steps {
			if s.Status == "failed" {
				failed[s.StepName] = true
			}
		}
		if len(failed) == 0 {
			return "", fmt.Errorf("run %q has no failed steps", runID)
		}
		var filtered []pipeline.Step
		for _, s := range pl.Spec.Steps {
			if failed[s.Name] {
				s.DependsOn = nil
				filtered = append(filtered, s)
			}
		}
		pl.Spec.Steps = filtered
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return "", fmt.Errorf("build dag: %w", err)
	}
	return p.startRun(ctx, pl, dag, StartRunOptions{
		ProjectID: prev.ProjectID,
		Params:    params,
		YAML:      prev.PipelineYAML,
	})
}

func (p *Piper) retryStep(ctx context.Context, runID, stepName string) (string, error) {
	projectContext, _ := project.FromContext(ctx)
	prev, err := p.repos.Run.Get(ctx, projectContext.ID, runID)
	if err != nil || prev == nil {
		return "", fmt.Errorf("run %q not found", runID)
	}
	steps, err := p.repos.Step.List(ctx, projectContext.ID, runID)
	if err != nil {
		return "", err
	}
	foundFailed := false
	for _, s := range steps {
		if s.StepName == stepName && s.Status == "failed" {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		return "", fmt.Errorf("step %q is not failed in run %q", stepName, runID)
	}
	if prev.PipelineYAML == "" {
		return "", fmt.Errorf("run %q has no stored pipeline yaml", runID)
	}
	var params map[string]any
	if prev.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(prev.ParamsJSON), &params)
	}
	pl, err := p.Parse([]byte(prev.PipelineYAML))
	if err != nil {
		return "", fmt.Errorf("parse previous run yaml: %w", err)
	}
	for _, s := range pl.Spec.Steps {
		if s.Name == stepName {
			s.DependsOn = nil
			pl.Spec.Steps = []pipeline.Step{s}
			dag, err := pipeline.BuildDAG(pl)
			if err != nil {
				return "", fmt.Errorf("build dag: %w", err)
			}
			return p.startRun(ctx, pl, dag, StartRunOptions{ProjectID: prev.ProjectID, Params: params, YAML: prev.PipelineYAML})
		}
	}
	return "", fmt.Errorf("step %q not found in pipeline yaml", stepName)
}

// deleteRunWithArtifacts deletes the run's artifacts and then the run record.
func (p *Piper) deleteRunWithArtifacts(ctx context.Context, runID string) error {
	if err := deleteArtifacts(ctx, p.store, p.cfg.OutputDir, runID); err != nil {
		slog.Warn("delete artifacts failed", "run_id", runID, "err", err)
	}
	projectContext, _ := project.FromContext(ctx)
	return p.repos.DeleteRun(ctx, projectContext.ID, runID)
}

// ── piperRunHooks — bridges Hooks into run.RunHooks ──────────────────────────

type piperRunHooks struct {
	p *Piper
}

func (h *piperRunHooks) BeforeListRuns(ctx context.Context, r *http.Request) (run.RunFilter, error) {
	f, err := h.p.cfg.Hooks.callBeforeListRuns(ctx, r)
	return run.RunFilter{PipelineName: f.PipelineName}, err
}

func (h *piperRunHooks) BeforeCreateRun(ctx context.Context, r *http.Request, yaml string) error {
	return h.p.cfg.Hooks.callBeforeCreateRun(ctx, r, yaml)
}

func (h *piperRunHooks) BeforeGetRun(ctx context.Context, r *http.Request, id string) error {
	return h.p.cfg.Hooks.callBeforeGetRun(ctx, r, id)
}

func (h *piperRunHooks) BeforeGetLogs(ctx context.Context, r *http.Request, runID, step string) error {
	return h.p.cfg.Hooks.callBeforeGetLogs(ctx, r, runID, step)
}

// ── piperArtifacts — implements run.ArtifactProvider ─────────────────────────

type piperArtifacts struct {
	p *Piper
}

func (a *piperArtifacts) List(ctx context.Context, runID string) ([]any, error) {
	var result []stepArtifacts
	var err error
	if a.p.store != nil {
		result, err = listArtifactsStore(ctx, a.p.store, runID)
	} else {
		result, err = listArtifactsLocal(a.p.cfg.OutputDir, runID)
	}
	if err != nil {
		return nil, err
	}

	// Enrich artifact entries with viewer type hints from the pipeline YAML.
	typeHints := a.artifactTypeHints(ctx, runID)
	for i := range result {
		for j := range result[i].Artifacts {
			key := result[i].Step + "/" + result[i].Artifacts[j].Name
			if t, ok := typeHints[key]; ok {
				result[i].Artifacts[j].Type = t
			}
		}
	}

	out := make([]any, len(result))
	for i, v := range result {
		out[i] = v
	}
	return out, nil
}

// artifactTypeHints parses the run's stored pipeline YAML and returns a map of
// "stepName/artifactName" → viewer type for all outputs that declare a type.
func (a *piperArtifacts) artifactTypeHints(ctx context.Context, runID string) map[string]string {
	pctx, _ := project.FromContext(ctx)
	r, err := a.p.repos.Run.Get(ctx, pctx.ID, runID)
	if err != nil || r.PipelineYAML == "" {
		return nil
	}
	pl, err := a.p.Parse([]byte(r.PipelineYAML))
	if err != nil {
		return nil
	}
	hints := make(map[string]string)
	for _, step := range pl.Spec.Steps {
		for _, out := range step.Outputs {
			if out.Type != "" {
				hints[step.Name+"/"+out.Name] = out.Type
			}
		}
	}
	return hints
}

func (a *piperArtifacts) ServeDownload(w http.ResponseWriter, r *http.Request, runID, step, rest string) {
	if containsDotDot(rest) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if a.p.store != nil {
		downloadArtifactStore(w, r, a.p.store, runID, step, rest)
		return
	}
	downloadArtifactLocal(w, r, a.p.cfg.OutputDir, runID, step, rest)
}
