package piper

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/federation"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/storage"
	"github.com/loykin/piper/pkg/ui"
	"github.com/loykin/piper/pkg/viewer"
	viewerhtml "github.com/loykin/piper/pkg/viewer/driver/html"
	viewertb "github.com/loykin/piper/pkg/viewer/driver/tensorboard"
)

const maxRequestBodyBytes int64 = 1 << 20

// maxBlobRequestBodyBytes bounds the built-in file store's PUT and the
// project storage object upload — both carry real artifacts (model
// checkpoints, log bundles) that routinely exceed maxRequestBodyBytes, which
// exists to bound JSON API payload abuse, not blob transfer.
const maxBlobRequestBodyBytes int64 = 4 << 30 // 4 GiB

// isBlobRoute reports whether fullPath is one of the artifact-transfer routes
// that must use maxBlobRequestBodyBytes instead of the JSON-API default.
// Matched by suffix because the same handlers are mounted under different
// prefixes: "/store/*key" on the main engine, and ".../storage/object" both
// on the main engine (behind /api/projects/:project_id) and on the Member
// tunnel's own router (behind /projects/:project_id, no /api prefix).
func isBlobRoute(fullPath string) bool {
	return strings.HasPrefix(fullPath, "/store/") ||
		strings.HasSuffix(fullPath, "/storage/object")
}

var (
	httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "piper_http_requests_total", Help: "HTTP requests handled by Piper."},
		[]string{"method", "route", "status"},
	)
	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "piper_http_request_duration_seconds", Help: "HTTP request latency by route."},
		[]string{"method", "route"},
	)
)

func init() {
	prometheus.MustRegister(httpRequests, httpDuration)
}

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

	// Member overrides the in-process Local Member for Run-domain requests.
	// ProjectRef must also be set when requests may target remote Members.
	Member memberclient.Client

	// ProjectRef resolves a Home project ID to its execution owner.
	ProjectRef func(projectID string) project.ProjectRef

	// ProjectOwner validates the Owner Member selected when creating a Home
	// project. Nil keeps standalone behavior (Local Member ownership).
	ProjectOwner project.OwnerResolver

	// ProjectMember relays non-Run Member-owned Project APIs. Run keeps its
	// typed Member contract; streaming proxy routes use a separate channel.
	ProjectMember projectclient.Client

	// HomeID enables Home-owned federation directory endpoints. Empty keeps
	// embedded/standalone servers free of federation control-plane routes.
	HomeID string
}

// Serve runs the piper HTTP server.
// Supports both HTTP and HTTPS. Library users can call this directly or
// mount it on their own server using Handler().
func (p *Piper) Serve(ctx context.Context, opt ServeOption) error {
	// Build the viewer manager once and share it between the cleanup loop and the HTTP handler.
	viewerMgr := viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir)
	viewerMgr.SetStorageDiagnostics(p.runStorageBackend, p.storageIdentity)
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

	handler := p.newRouterWithFederation(opt.Extra, viewerMgr, opt.Member, opt.ProjectMember, opt.ProjectRef, opt.ProjectOwner, opt.HomeID)

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

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally unset: SSE streaming endpoints require
		// an unbounded write deadline.
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	// Graceful shutdown
	go func() {
		<-ctx.Done()
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
	return p.newRouterWithMember(extra, viewerMgr, nil, nil)
}

func (p *Piper) newRouterWithMember(extra http.Handler, viewerMgr *viewer.Manager, member memberclient.Client, projectRef func(string) project.ProjectRef) http.Handler {
	return p.newRouterWithFederation(extra, viewerMgr, member, nil, projectRef, nil, "")
}

func (p *Piper) newRouterWithFederation(extra http.Handler, viewerMgr *viewer.Manager, member memberclient.Client, projectMember projectclient.Client, projectRef func(string) project.ProjectRef, projectOwner project.OwnerResolver, homeID string) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(limitRequestBody(maxRequestBodyBytes))
	r.Use(prometheusHTTPMetrics())

	// Caller-provided routes run before Piper routes. Authentication is applied
	// only to user-facing groups below; workload routes use a separate credential.
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
	p.registerAdminRoutes(userAPI)
	if member == nil {
		member = NewLocalMemberClient(p)
	}
	if projectRef == nil {
		projectRef = project.LocalRef
	}

	// Project management — logged-in users can list; create/delete is system-admin.
	var projectCreator project.Creator
	if homeID != "" && p.repos.Federation != nil {
		projectCreator = func(ctx context.Context, value *project.Project, actorID string) error {
			return p.federationSvc.CreateProject(ctx, homeID, value, actorID)
		}
	}
	projectHandler := project.NewHandlerWithDirectory(p.repos.Project, p.cfg.Auth.Authorizer, projectOwner, projectCreator)
	projectHandler.WithBeforeDelete(func(ctx context.Context, value *project.Project) error {
		ref := projectRef(value.ID)
		if value.OwnerMemberID != "" {
			ref.MemberID = value.OwnerMemberID
		}
		return member.PurgeProjectStats(ctx, memberclient.AuthContext{Role: security.ProjectRoleAdmin, IssuedAt: time.Now()}, ref)
	})
	projectHandler.RegisterRoutes(userAPI)
	if homeID != "" && p.repos.Federation != nil {
		federation.NewHandler(p.repos.Federation, homeID, p.cfg.Auth.Authorizer).RegisterRoutes(userAPI)
	}
	projectAPI := userAPI.Group("/projects/:project_id", project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer))
	// System-scoped credentials (e.g. the artifact-storage s3 credential).
	credential.NewHandler(p.credentials).RegisterRoutes(userAPI.Group("/system", p.requireSystemAdmin(), project.SystemContext()))

	// Run domain — Home reaches Member only through memberclient.Client
	// (fed.md §11.3/§13.3); for the single-install case that Member is
	// in-process (NewLocalMemberClient).
	runHandler := run.NewHandler(run.HandlerDeps{
		Member:     member,
		ProjectRef: projectRef,
		Hooks:      &piperRunHooks{p: p},
	})
	runHandler.RegisterRoutes(projectAPI)

	// Every route registered by the Member-owned composition root is relayed
	// for a remotely owned Project. Keeping the middleware on this group makes
	// routing fail closed: a newly added Member API cannot silently fall through
	// to Home merely because somebody forgot to extend a path allowlist.
	memberProjectAPI := projectAPI.Group("", relayRemoteProject(projectMember, projectRef))
	// Storage gets its own sibling group relayed through the streaming path
	// (relayRemoteProjectHTTP) instead of memberProjectAPI's buffered one —
	// see registerMemberProjectRoutes' doc comment for why.
	storageRelayAPI := projectAPI.Group("", relayRemoteProjectHTTP(projectMember, projectRef))
	p.registerMemberStorageRoutes(storageRelayAPI)
	memberHandlers := p.registerMemberProjectRoutes(memberProjectAPI, viewerMgr, func(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars, experiment string) (string, error) {
		projectContext, _ := project.FromContext(ctx)
		auth := memberclient.AuthContext{Role: projectContext.Role, IssuedAt: time.Now()}
		if identity, ok := security.IdentityFromContext(ctx); ok && identity != nil {
			auth.ActorID = identity.ID
		}
		ref := projectRef(projectContext.ID)
		if projectContext.OwnerMemberID != "" {
			ref.MemberID = projectContext.OwnerMemberID
		}
		resp, err := member.SubmitRun(ctx, auth, ref, memberclient.SubmitRunRequest{
			IdempotencyKey: uuid.NewString(), YAML: yaml, Params: params, Experiment: experiment, Vars: vars,
		})
		return resp.RunID, err
	})
	proxyAPI := r.Group("/projects/:project_id", p.authenticateUser(), project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer), relayRemoteProjectHTTP(projectMember, projectRef))
	memberHandlers.serving.RegisterProxyRoutes(proxyAPI)
	memberHandlers.notebook.RegisterProxyRoutes(proxyAPI)
	memberHandlers.viewer.RegisterProxyRoutes(proxyAPI)

	// Built-in file server: expose /store/* routes only when using a LocalStore.
	// K8s pods and Docker containers reach the store via HTTP using
	// runtime.workload_url when Piper's built-in file store is used.
	p.registerStoreRoutes(r)

	// JupyterLab requests /custom/custom.css as an absolute path (no base_url prefix).
	// The file is empty by convention — it is a user customization hook.
	r.GET("/custom/*path", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/css; charset=utf-8", nil)
	})

	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", p.metricsAuth(), p.metricsHandler)
	r.GET("/events", p.authenticateUser(), p.eventsHandler) // filtered by project_id param; see eventsHandler

	// SPA — served under /ui/; root redirects for convenience
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/ui/") })
	r.GET("/ui", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/ui/") })
	r.GET("/ui/*filepath", gin.WrapH(http.StripPrefix("/ui", ui.Handler())))

	return r
}

func relayRemoteProjectHTTP(client projectclient.Client, resolveRef func(string) project.ProjectRef) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectContext, _ := project.FromContext(c.Request.Context())
		if client == nil || resolveRef == nil || projectContext.OwnerMemberID == "" || projectContext.OwnerMemberID == project.LocalMemberID {
			c.Next()
			return
		}
		stream, ok := client.(projectclient.StreamClient)
		if !ok {
			c.JSON(http.StatusBadGateway, gin.H{"error": "remote Member has no HTTP stream"})
			c.Abort()
			return
		}
		auth := memberclient.AuthContext{Role: projectContext.Role}
		if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
			auth.ActorID = identity.ID
		}
		ref := resolveRef(projectContext.ID)
		ref.MemberID = projectContext.OwnerMemberID
		if err := stream.ServeProjectHTTP(c.Request.Context(), auth, ref, c.Writer, c.Request); err != nil && !c.Writer.Written() {
			status := http.StatusBadGateway
			if errors.Is(err, memberclient.ErrMemberUnavailable) {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
		}
		c.Abort()
	}
}

func prometheusHTTPMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		httpRequests.WithLabelValues(c.Request.Method, route, fmt.Sprintf("%d", c.Writer.Status())).Inc()
		httpDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(started).Seconds())
	}
}

func limitRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			max := maxBytes
			if isBlobRoute(c.FullPath()) {
				max = maxBlobRequestBodyBytes
			}
			if c.Request.ContentLength > max {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": fmt.Sprintf("request body too large (max %d bytes)", max),
				})
				c.Abort()
				return
			}
			if c.Request.Body != nil {
				c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
			}
		}
		c.Next()
	}
}

func relayRemoteProject(client projectclient.Client, resolveRef func(string) project.ProjectRef) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil || resolveRef == nil {
			c.Next()
			return
		}
		projectContext, _ := project.FromContext(c.Request.Context())
		if projectContext.OwnerMemberID == "" || projectContext.OwnerMemberID == project.LocalMemberID {
			c.Next()
			return
		}
		path := strings.TrimPrefix(c.Request.URL.Path, "/api/projects/"+projectContext.ID)
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "read request body failed"})
			c.Abort()
			return
		}
		headers := make(http.Header)
		for _, name := range []string{"Accept", "Content-Type", "If-Match", "If-None-Match", "Idempotency-Key", "Range"} {
			if values := c.Request.Header.Values(name); len(values) > 0 {
				headers[name] = append([]string(nil), values...)
			}
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && headers.Get("Idempotency-Key") == "" {
			headers.Set("Idempotency-Key", uuid.NewString())
		}
		if key := headers.Get("Idempotency-Key"); key != "" {
			c.Header("Idempotency-Key", key)
		}
		auth := memberclient.AuthContext{Role: projectContext.Role, IssuedAt: time.Now()}
		if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
			auth.ActorID = identity.ID
		}
		ref := resolveRef(projectContext.ID)
		ref.MemberID = projectContext.OwnerMemberID
		resp, err := client.DoProjectRequest(c.Request.Context(), auth, ref, projectclient.Request{
			Method: c.Request.Method, Path: path, RawQuery: c.Request.URL.RawQuery,
			Header: headers, Body: body,
		})
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, memberclient.ErrMemberUnavailable) {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		responseHeader := http.Header(resp.Header)
		for _, name := range []string{"Cache-Control", "Content-Disposition", "Content-Type", "ETag", "Last-Modified", "Location", "X-Total-Count"} {
			for _, value := range responseHeader.Values(name) {
				c.Writer.Header().Add(name, value)
			}
		}
		status := resp.Status
		if status < 100 || status > 599 {
			status = http.StatusBadGateway
		}
		c.Status(status)
		if len(resp.Body) > 0 {
			_, _ = c.Writer.Write(resp.Body)
		}
		c.Abort()
	}
}

// Handler returns the Piper HTTP handler for mounting on a caller-owned
// http.Server (e.g. inside a larger application).
//
//	mux.Handle("/piper/", http.StripPrefix("/piper", p.Handler(nil)))
func (p *Piper) Handler(extra http.Handler) http.Handler {
	return p.HandlerContext(p.ctx, extra)
}

// HandlerContext returns the Piper HTTP handler. ctx is accepted for
// backward-compatible call sites but is otherwise unused — the handler has
// no background lifecycle of its own to tie to it.
func (p *Piper) HandlerContext(_ context.Context, extra http.Handler) http.Handler {
	return p.handlerContext(extra)
}

func (p *Piper) handlerContext(extra http.Handler) http.Handler {
	mgr := viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir)
	mgr.SetStorageDiagnostics(p.runStorageBackend, p.storageIdentity)
	mgr.RegisterDriver(viewertb.New())
	mgr.RegisterDriver(viewerhtml.New())
	return p.newRouter(extra, mgr)
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
// K8s pods and Docker workloads can upload/download artifacts over HTTP without MinIO.
func (p *Piper) registerStoreRoutes(r *gin.Engine) {
	ls, ok := p.store.(*storage.LocalStore)
	if !ok {
		return // external store (S3, HTTP) — no need for built-in server routes
	}
	// Built-in store is accessed by workloads only; protect with the workload token.
	rg := r.Group("/store", p.workloadTokenMiddleware())
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
			delimiter := c.Query("delimiter")
			objs, err := ls.List(c.Request.Context(), prefix, delimiter)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, objs)
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
			security.RespondUnauthorized(c, err.Error())
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
		if identity == nil {
			security.RespondUnauthorized(c, "")
			return
		}
		if err := authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
			security.RespondForbidden(c, "system admin required")
			return
		}
		c.Next()
	}
}

// workloadTokenMiddleware returns a Gin middleware that requires the request to
// carry the configured workload token. When WorkloadToken is empty the check is
// skipped for trusted/dev mode.
func (p *Piper) workloadTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := p.cfg.Server.WorkloadToken
		if token == "" {
			c.Next()
			return
		}
		auth := c.Request.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
			security.RespondUnauthorized(c, "invalid workload token")
			return
		}
		c.Next()
	}
}

// metricsAuth accepts the scrape bearer token used by workloads, or an
// authenticated system-admin session. This keeps metrics on the existing
// server endpoint while making ordinary Prometheus bearer-token scraping work.
func (p *Piper) metricsAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if token := p.cfg.Server.WorkloadToken; token != "" {
			if auth := c.GetHeader("Authorization"); auth == "Bearer "+token {
				c.Next()
				return
			}
		}
		if p.cfg.Auth.Authenticator == nil || p.cfg.Auth.Authorizer == nil {
			c.Next()
			return
		}
		identity, err := p.cfg.Auth.Authenticator.Authenticate(c.Request.Context(), c.Request)
		if err != nil || identity == nil {
			security.RespondUnauthorized(c, "metrics authentication required")
			return
		}
		if err := p.cfg.Auth.Authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
			security.RespondForbidden(c, "system admin required")
			return
		}
		c.Request = c.Request.WithContext(security.WithIdentity(c.Request.Context(), identity))
		c.Next()
	}
}

func (p *Piper) eventsHandler(c *gin.Context) {
	// ?project_id=xxx filters to events scoped to that project plus infra events.
	// Without project_id only system admins receive all events.
	filterProject := strings.TrimSpace(c.Query("project_id"))

	if p.cfg.Auth.Authorizer != nil {
		identity, _ := security.IdentityFromContext(c.Request.Context())
		if identity == nil {
			security.RespondUnauthorized(c, "")
			return
		}
		if filterProject != "" {
			// Verify caller can access the requested project.
			role, err := p.cfg.Auth.Authorizer.ProjectRole(c.Request.Context(), identity, filterProject)
			if err != nil || role < security.ProjectRoleViewer {
				security.RespondForbidden(c, "forbidden")
				return
			}
		} else {
			// No project filter → require system admin to avoid info leak.
			if err := p.cfg.Auth.Authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
				security.RespondForbidden(c, "system admin required for global event stream")
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

// StartRun is the exported entry point for creating a run from the HTTP API.
func (p *Piper) StartRun(ctx context.Context, yaml string, params map[string]any, vars BuiltinVars) (string, error) {
	return p.runs.StartRunFromAPI(ctx, yaml, params, vars, "")
}

// CancelRun cancels a queued or running run.
func (p *Piper) CancelRun(ctx context.Context, runID string) error {
	return p.runs.CancelRun(ctx, runID)
}

// RerunRun re-executes a run, optionally limiting to failed steps only.
func (p *Piper) RerunRun(ctx context.Context, runID string, failedOnly bool) (string, error) {
	return p.runs.RerunRun(ctx, runID, failedOnly)
}

// RetryStep retries a single failed step within a run.
func (p *Piper) RetryStep(ctx context.Context, runID, stepName string) (string, error) {
	return p.runs.RetryStep(ctx, runID, stepName)
}

// DeleteRun deletes a run and its artifacts.
func (p *Piper) DeleteRun(ctx context.Context, runID string) error {
	return p.runs.DeleteRunWithArtifacts(ctx, runID)
}

// BackfillSchedule creates runs for every cron tick a schedule would have
// fired in [from, to).
func (p *Piper) BackfillSchedule(ctx context.Context, id string, from, to time.Time) ([]string, error) {
	return p.runs.BackfillSchedule(ctx, id, from, to)
}

type piperCollector struct {
	p *Piper
}

func (c *piperCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

func (c *piperCollector) Collect(ch chan<- prometheus.Metric) {
	runs, err := c.p.runs.ListRunsAcrossProjects(context.Background(), run.RunFilter{})
	if err != nil {
		slog.Error("collect piper metrics", "err", err)
		return
	}
	counts := map[string]int{}
	var totalDurationSeconds float64
	var completed int
	for _, item := range runs {
		counts[item.Status]++
		if item.EndedAt != nil {
			totalDurationSeconds += item.EndedAt.Sub(item.StartedAt).Seconds()
			completed++
		}
	}
	runDesc := prometheus.NewDesc("piper_runs_total", "Stored Piper runs by status.", []string{"status"}, nil)
	for runStatus, count := range counts {
		ch <- prometheus.MustNewConstMetric(runDesc, prometheus.GaugeValue, float64(count), runStatus)
	}
	durationDesc := prometheus.NewDesc("piper_run_duration_seconds", "Completed Piper run duration.", nil, nil)
	ch <- prometheus.MustNewConstSummary(
		durationDesc,
		uint64(completed),
		totalDurationSeconds,
		map[float64]float64{},
	)
	stats := c.p.queue.Stats()
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc("piper_queue_runs", "Runs held by the in-memory queue.", nil, nil), prometheus.GaugeValue, float64(stats.Runs))
	taskDesc := prometheus.NewDesc("piper_queue_tasks", "Queued tasks by status.", []string{"status"}, nil)
	ch <- prometheus.MustNewConstMetric(taskDesc, prometheus.GaugeValue, float64(stats.Pending), "pending")
	ch <- prometheus.MustNewConstMetric(taskDesc, prometheus.GaugeValue, float64(stats.Ready), "ready")
	ch <- prometheus.MustNewConstMetric(taskDesc, prometheus.GaugeValue, float64(stats.Running), "running")
}

func (p *Piper) metricsHandler(c *gin.Context) {
	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(&piperCollector{p: p}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	gatherers := prometheus.Gatherers{prometheus.DefaultGatherer, registry}
	promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{}).ServeHTTP(c.Writer, c.Request)
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
	// Checked before any read, not only as a fallback explanation for an
	// empty result: if the live backend happens to hold different data at
	// this run's key (e.g. after a migration to a backend pre-seeded from a
	// stale copy), listing first and only checking on emptiness would leave
	// that wrong data looking like a normal, non-empty artifact list.
	if a.p.storageBackendMismatch(ctx, runID) {
		return nil, memberclient.ErrStorageBackendMismatch
	}

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
	// Checked before any read, not only as a fallback explanation for a
	// not-found: if the live backend happens to hold different data at this
	// exact runID/step/path key (e.g. after a migration to a backend
	// pre-seeded from a stale copy), downloading first and only checking on
	// failure would silently serve that wrong data as this run's artifact —
	// see storageBackendMismatch's doc comment on pkg/viewer.Manager for the
	// same reasoning applied there.
	if a.p.storageBackendMismatch(r.Context(), runID) {
		writeStorageBackendMismatchJSON(w)
		return
	}
	var notFound bool
	if a.p.store != nil {
		notFound = downloadArtifactStore(w, r, a.p.store, runID, step, rest)
	} else {
		notFound = downloadArtifactLocal(w, r, a.p.cfg.OutputDir, runID, step, rest)
	}
	if notFound {
		http.Error(w, "artifact not found", http.StatusNotFound)
	}
}
