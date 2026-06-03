package piper

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/blobstore"
	"github.com/piper/piper/pkg/event"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/ui"
	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
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

	// AgentAddr overrides Config.Server.AgentAddr for the gRPC agent server.
	// Useful in --local mode where the gRPC address must be determined at runtime.
	AgentAddr string
}

// Serve runs the piper HTTP server.
// Supports both HTTP and HTTPS. Library users can call this directly or
// mount it on their own server using Handler().
func (p *Piper) Serve(ctx context.Context, opt ServeOption) error {
	handler := p.newRouter(opt.Extra)

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
		Addr:        addr,
		Handler:     handler,
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
		// WriteTimeout is intentionally unset: SSE streaming endpoints require
		// an unbounded write deadline.
	}

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

	agentAddr := p.cfg.Server.AgentAddr
	if opt.AgentAddr != "" {
		agentAddr = opt.AgentAddr
	}
	if agentAddr != "" {
		go p.serveGRPCAgents(ctx, agentAddr)
	}

	slog.Info("piper server starting (HTTP)", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// serveGRPCAgents runs the gRPC agent server on addr until ctx is cancelled.
func (p *Piper) serveGRPCAgents(ctx context.Context, addr string) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("grpc agent server: listen failed", "addr", addr, "err", err)
		return
	}
	grpcSrv := p.grpcAgentServer.GRPCServer()
	go func() {
		<-ctx.Done()
		grpcSrv.GracefulStop()
	}()
	slog.Info("grpc agent server starting", "addr", addr)
	if err := grpcSrv.Serve(lis); err != nil {
		slog.Error("grpc agent server stopped", "err", err)
	}
}

// newRouter builds the Gin router wired with all domain handlers.
func (p *Piper) newRouter(extra http.Handler) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(limitRequestBody(maxRequestBodyBytes))

	// Auth + extra handler middleware
	r.Use(func(c *gin.Context) {
		if err := p.checkBearerToken(c.Request); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		ctx, err := p.cfg.Hooks.callAuth(c.Request)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		// Replace request context so downstream hooks receive the enriched context
		// (e.g. verified user identity injected by the Auth hook).
		c.Request = c.Request.WithContext(ctx)
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

	r.GET("/api/settings", func(c *gin.Context) {
		c.JSON(http.StatusOK, p.Settings())
	})
	r.GET("/api/storage/settings", func(c *gin.Context) {
		view, err := p.StorageSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	})
	r.PUT("/api/storage/settings", func(c *gin.Context) {
		var cfg StorageConfig
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		view, err := p.UpdateStorageSettings(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	})
	r.POST("/api/storage/object", func(c *gin.Context) {
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
	r.GET("/api/storage/objects", func(c *gin.Context) {
		prefix := c.Query("prefix")
		objs, err := p.ListStorageObjects(c.Request.Context(), prefix)
		if err != nil {
			status := http.StatusInternalServerError
			if p.store == nil {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, objs)
	})
	r.GET("/api/storage/object", func(c *gin.Context) {
		key := strings.TrimSpace(c.Query("key"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
			return
		}
		rc, filename, err := p.OpenStorageObject(c.Request.Context(), key)
		if err != nil {
			status := http.StatusInternalServerError
			if err == blobstore.ErrNotFound {
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
	r.DELETE("/api/storage/object", func(c *gin.Context) {
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

	// Run domain
	run.NewHandler(run.HandlerDeps{
		Runs:    p.repos.Run,
		Steps:   p.repos.Step,
		Logs:    p.logs,
		Metrics: p.metrics,
		StartRun: func(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error) {
			return p.startRunFromAPI(ctx, yaml, ownerID, params, vars, experiment)
		},
		CancelRun: p.CancelRun,
		RerunRun:  p.RerunRun,
		RetryStep: p.RetryStep,
		DeleteRun: p.DeleteRun,
		Artifacts: &piperArtifacts{p: p},
		Hooks:     &piperRunHooks{p: p},
		OwnerID:   p.ownerIDFromRequest,
	}).RegisterRoutes(r.Group(""))

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
		OwnerID:  p.ownerIDFromRequest,
		Hooks:    &piperScheduleHooks{p: p},
		GenID:    genScheduleID,
	}).RegisterRoutes(r.Group(""))

	// Serving domain
	serving.NewHandler(serving.HandlerDeps{
		Services:     p.repos.Serving,
		Deploy:       p.DeployServiceAs,
		Stop:         p.StopService,
		Restart:      p.RestartService,
		UpdateStatus: p.serving.manager.UpdateStatus,
		Proxy:        p.serving.proxy,
		OwnerID:      p.ownerIDFromRequest,
		Hooks:        &piperServingHooks{p: p},
	}).RegisterRoutes(r.Group(""))

	// Notebook domain
	notebook.NewHandler(notebook.HandlerDeps{
		Notebooks:        p.repos.Notebook,
		Volumes:          p.repos.NotebookVolume,
		Promotions:       p,
		Create:           p.notebookManager.Create,
		CreateWithVolume: p.notebookManager.CreateWithVolume,
		Stop:             p.notebookManager.Stop,
		Restart:          p.notebookManager.Restart,
		Delete:           p.notebookManager.Delete,
		PurgeVolume:      p.notebookManager.PurgeVolume,
		AgentRegistry:    p.agentRegistry,
		ProxyDialer:      p.grpcAgentServer,
	}).RegisterRoutes(r.Group(""))

	// Built-in file server: expose /store/* routes only when using a LocalStore.
	// Workers and K8s pods reach the store via HTTP using the master URL.
	p.registerStoreRoutes(r)

	// Task completion routes must always be registered so that piper agent exec
	// inside K8s Job pods can report results back regardless of backend mode.
	worker.NewHandler(worker.HandlerDeps{Queue: p.queue}).RegisterCompletionRoutes(r.Group("/api"))

	// Worker polling domain (mounted only when no active backend is configured)
	if p.backend == nil {
		worker.NewHandler(worker.HandlerDeps{
			Registry: p.workerRegistry(),
			Queue:    p.queue,
		}).RegisterPollRoutes(r.Group("/api"))
	}

	// Unified agent registry domain.
	iagent.NewHandler(p.agentRegistry).RegisterRoutes(r.Group("/api"))

	// JupyterLab requests /custom/custom.css as an absolute path (no base_url prefix).
	// The file is empty by convention — it is a user customization hook.
	r.GET("/custom/*path", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/css; charset=utf-8", nil)
	})

	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", p.metricsHandler)
	r.GET("/events", p.eventsHandler)

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

// Handler returns the piper HTTP API handler.
// Library users can mount it on their own router.
//
//	mux.Handle("/piper/", http.StripPrefix("/piper", p.Handler(nil)))
func (p *Piper) Handler(extra http.Handler) http.Handler {
	return p.newRouter(extra)
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
	ls, ok := p.store.(*blobstore.LocalStore)
	if !ok {
		return // external store (S3, HTTP) — no need for built-in server routes
	}
	rg := r.Group("/store")
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
			if err == blobstore.ErrNotFound {
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

func (p *Piper) checkBearerToken(r *http.Request) error {
	if p.cfg.Server.Token == "" || r.URL.Path == "/health" {
		return nil
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != p.cfg.Server.Token {
		return fmt.Errorf("invalid bearer token")
	}
	return nil
}

func (p *Piper) eventsHandler(c *gin.Context) {
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
			_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, event.Encode(ev))
			return true
		}
	})
}

// ownerIDFromRequest returns the caller's owner ID. When Hooks.ExtractOwnerID
// is set it delegates to that function, so library users can derive identity
// from JWT claims or sessions instead of trusting a raw header.
func (p *Piper) ownerIDFromRequest(r *http.Request) string {
	if p.cfg.Hooks.ExtractOwnerID != nil {
		return p.cfg.Hooks.ExtractOwnerID(r)
	}
	return defaultOwnerIDFromRequest(r)
}

func defaultOwnerIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ownerID := strings.TrimSpace(r.Header.Get("X-Piper-Owner-ID")); ownerID != "" {
		return ownerID
	}
	return strings.TrimSpace(r.URL.Query().Get("owner_id"))
}

func genRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}

func genScheduleID() string {
	return fmt.Sprintf("sch-%s", genRunID())
}

// startRunFromAPI handles creating a run from the HTTP API, including
// future-scheduled runs and immediate dispatch.
func (p *Piper) startRunFromAPI(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars, experiment string) (string, error) {
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
			OwnerID:      ownerID,
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
		OwnerID:    ownerID,
		Experiment: experiment,
		Params:     params,
		Vars:       vars,
		YAML:       yaml,
	})
}

// StartRun is the exported entry point for creating a run from the HTTP API.
func (p *Piper) StartRun(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars) (string, error) {
	return p.startRunFromAPI(ctx, yaml, ownerID, params, vars, "")
}

// CancelRun cancels a queued or running run.
func (p *Piper) CancelRun(ctx context.Context, runID string) error {
	return p.queue.Cancel(ctx, runID)
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

// DeployServiceAs deploys a service on behalf of the given owner.
func (p *Piper) DeployServiceAs(ctx context.Context, yamlBytes []byte, ownerID string) (*serving.Service, error) {
	return p.deployService(ctx, yamlBytes, ownerID)
}

func (p *Piper) cancelRun(ctx context.Context, runID string) error {
	return p.queue.Cancel(ctx, runID)
}

func (p *Piper) metricsHandler(c *gin.Context) {
	runs, err := p.repos.Run.List(c.Request.Context(), run.RunFilter{})
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
	for status, count := range counts {
		_, _ = fmt.Fprintf(c.Writer, "piper_runs_total{status=%q} %d\n", status, count)
	}
	_, _ = fmt.Fprintf(c.Writer, "piper_run_duration_seconds_sum %.3f\n", totalDurationSeconds)
	_, _ = fmt.Fprintf(c.Writer, "piper_run_duration_seconds_count %d\n", completed)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_runs %d\n", stats.Runs)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_tasks{status=\"pending\"} %d\n", stats.Pending)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_tasks{status=\"ready\"} %d\n", stats.Ready)
	_, _ = fmt.Fprintf(c.Writer, "piper_queue_tasks{status=\"running\"} %d\n", stats.Running)
	workerCount := 0
	if p.registry != nil {
		workerCount = len(p.registry.List())
	}
	_, _ = fmt.Fprintf(c.Writer, "piper_workers %d\n", workerCount)
}

func (p *Piper) rerunRun(ctx context.Context, runID string, failedOnly bool) (string, error) {
	prev, err := p.repos.Run.Get(ctx, runID)
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
		steps, err := p.repos.Step.List(ctx, runID)
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
		OwnerID: prev.OwnerID,
		Params:  params,
		YAML:    prev.PipelineYAML,
	})
}

func (p *Piper) retryStep(ctx context.Context, runID, stepName string) (string, error) {
	prev, err := p.repos.Run.Get(ctx, runID)
	if err != nil || prev == nil {
		return "", fmt.Errorf("run %q not found", runID)
	}
	steps, err := p.repos.Step.List(ctx, runID)
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
			return p.startRun(ctx, pl, dag, StartRunOptions{OwnerID: prev.OwnerID, Params: params, YAML: prev.PipelineYAML})
		}
	}
	return "", fmt.Errorf("step %q not found in pipeline yaml", stepName)
}

// deleteRunWithArtifacts deletes the run's artifacts and then the run record.
func (p *Piper) deleteRunWithArtifacts(ctx context.Context, runID string) error {
	if err := deleteArtifacts(ctx, p.store, p.cfg.OutputDir, runID); err != nil {
		slog.Warn("delete artifacts failed", "run_id", runID, "err", err)
	}
	return p.repos.DeleteRun(ctx, runID)
}

// ── piperRunHooks — bridges Hooks into run.RunHooks ──────────────────────────

type piperRunHooks struct {
	p *Piper
}

func (h *piperRunHooks) BeforeListRuns(ctx context.Context, r *http.Request) (run.RunFilter, error) {
	f, err := h.p.cfg.Hooks.callBeforeListRuns(ctx, r)
	if f.OwnerID == "" {
		f.OwnerID = h.p.ownerIDFromRequest(r)
	}
	return run.RunFilter{
		OwnerID:      f.OwnerID,
		PipelineName: f.PipelineName,
	}, err
}

func (h *piperRunHooks) BeforeCreateRun(ctx context.Context, r *http.Request, yaml string) error {
	return h.p.cfg.Hooks.callBeforeCreateRun(ctx, r, yaml)
}

func (h *piperRunHooks) BeforeGetRun(ctx context.Context, r *http.Request, id string) error {
	if err := h.p.cfg.Hooks.callBeforeGetRun(ctx, r, id); err != nil {
		return err
	}
	return h.checkRunOwner(ctx, r, id)
}

func (h *piperRunHooks) BeforeGetLogs(ctx context.Context, r *http.Request, runID, step string) error {
	if err := h.p.cfg.Hooks.callBeforeGetLogs(ctx, r, runID, step); err != nil {
		return err
	}
	return h.checkRunOwner(ctx, r, runID)
}

func (h *piperRunHooks) checkRunOwner(ctx context.Context, r *http.Request, runID string) error {
	ownerID := h.p.ownerIDFromRequest(r)
	if ownerID == "" {
		return nil
	}
	rec, err := h.p.repos.Run.Get(ctx, runID)
	if err != nil || rec == nil {
		return nil
	}
	if rec.OwnerID != "" && rec.OwnerID != ownerID {
		return fmt.Errorf("forbidden")
	}
	return nil
}

// ── piperScheduleHooks — bridges Hooks into schedule.ScheduleHooks ──────────

type piperScheduleHooks struct {
	p *Piper
}

func (h *piperScheduleHooks) BeforeCreateSchedule(ctx context.Context, r *http.Request, yaml string) error {
	return h.p.cfg.Hooks.callBeforeCreateSchedule(ctx, r, yaml)
}

func (h *piperScheduleHooks) BeforeListSchedules(ctx context.Context, r *http.Request) (schedule.ScheduleFilter, error) {
	f, err := h.p.cfg.Hooks.callBeforeListSchedules(ctx, r)
	ownerID := f.OwnerID
	if ownerID == "" {
		ownerID = h.p.ownerIDFromRequest(r)
	}
	return schedule.ScheduleFilter{OwnerID: ownerID}, err
}

func (h *piperScheduleHooks) BeforeGetSchedule(ctx context.Context, r *http.Request, id string) error {
	return h.p.cfg.Hooks.callBeforeGetSchedule(ctx, r, id)
}

// ── piperServingHooks — bridges Hooks into serving.ServingHooks ──────────────

type piperServingHooks struct {
	p *Piper
}

func (h *piperServingHooks) BeforeCreateService(ctx context.Context, r *http.Request, yaml string) error {
	return h.p.cfg.Hooks.callBeforeCreateService(ctx, r, yaml)
}

func (h *piperServingHooks) BeforeListServices(ctx context.Context, r *http.Request) (serving.ServingFilter, error) {
	f, err := h.p.cfg.Hooks.callBeforeListServices(ctx, r)
	ownerID := f.OwnerID
	if ownerID == "" {
		ownerID = h.p.ownerIDFromRequest(r)
	}
	return serving.ServingFilter{OwnerID: ownerID}, err
}

func (h *piperServingHooks) BeforeGetService(ctx context.Context, r *http.Request, name string) error {
	return h.p.cfg.Hooks.callBeforeGetService(ctx, r, name)
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
	out := make([]any, len(result))
	for i, v := range result {
		out[i] = v
	}
	return out, nil
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
