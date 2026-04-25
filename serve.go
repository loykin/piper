package piper

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/ui"
	"github.com/piper/piper/pkg/worker"
)

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

	slog.Info("piper server starting (HTTP)", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// newRouter builds the Gin router wired with all domain handlers.
func (p *Piper) newRouter(extra http.Handler) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Auth + extra handler middleware
	r.Use(func(c *gin.Context) {
		if err := p.cfg.Hooks.callAuth(c.Request); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
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

	// Run domain
	run.NewHandler(run.HandlerDeps{
		Runs:  p.repos.Run,
		Steps: p.repos.Step,
		Logs:  p.logs,
		StartRun: func(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars) (string, error) {
			return p.startRunFromAPI(ctx, yaml, ownerID, params, vars)
		},
		DeleteRun: func(ctx context.Context, runID string) error {
			return p.deleteRunWithArtifacts(ctx, runID)
		},
		Artifacts: &piperArtifacts{p: p},
		Hooks:     &piperRunHooks{p: p},
	}).RegisterRoutes(r.Group(""))

	// Schedule domain
	schedule.NewHandler(schedule.HandlerDeps{
		Schedules: p.repos.Schedule,
		Runs:      p.repos.Run,
		Parse: func(yaml []byte) (*pipeline.Pipeline, error) {
			return p.Parse(yaml)
		},
		Trigger: func(ctx interface{ Done() <-chan struct{} }, sc *schedule.Schedule) {
			p.triggerSchedule(p.ctx, sc)
		},
		NextTime: nextScheduleTime,
		GenID:    genScheduleID,
	}).RegisterRoutes(r.Group(""))

	// Serving domain
	serving.NewHandler(serving.HandlerDeps{
		Services: p.repos.Serving,
		Deploy: func(ctx context.Context, yaml []byte) (*serving.Service, error) {
			return p.DeployService(ctx, yaml)
		},
		Stop: func(ctx context.Context, name string) error {
			return p.StopService(ctx, name)
		},
		Restart: func(ctx context.Context, name string) error {
			return p.RestartService(ctx, name)
		},
		Proxy: p.serving.proxy,
	}).RegisterRoutes(r.Group(""))

	// Worker domain (mounted under /api)
	worker.NewHandler(worker.HandlerDeps{
		Registry: p.registry,
		Queue:    p.queue,
	}).RegisterRoutes(r.Group("/api"))

	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// SPA fallback
	r.NoRoute(gin.WrapH(ui.Handler()))

	return r
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

// ── helpers ──────────────────────────────────────────────────────────────────

func genRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}

func genScheduleID() string {
	return fmt.Sprintf("sch-%s", genRunID())
}

// startRunFromAPI handles creating a run from the HTTP API, including
// future-scheduled runs and immediate dispatch.
func (p *Piper) startRunFromAPI(ctx context.Context, yaml, ownerID string, params map[string]any, vars proto.BuiltinVars) (string, error) {
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
			PipelineName: pl.Metadata.Name,
			Status:       run.StatusScheduled,
			StartedAt:    now,
			ScheduledAt:  vars.ScheduledAt,
			PipelineYAML: yaml,
		}
		if err := p.repos.Run.Create(ctx, newRun); err != nil {
			return "", err
		}
		return runID, nil
	}

	return p.startRun(ctx, pl, dag, StartRunOptions{
		OwnerID: ownerID,
		Params:  params,
		Vars:    vars,
		YAML:    yaml,
	})
}

// deleteRunWithArtifacts deletes the run's artifacts and then the run record.
func (p *Piper) deleteRunWithArtifacts(ctx context.Context, runID string) error {
	if err := deleteArtifacts(ctx, p.s3Cli, p.cfg.S3.Bucket, p.cfg.OutputDir, runID); err != nil {
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
	return run.RunFilter{
		OwnerID:      f.OwnerID,
		PipelineName: f.PipelineName,
	}, err
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

// ── piperArtifacts — bridges artifact handling into run.ArtifactListDownloader

type piperArtifacts struct {
	p *Piper
}

func (a *piperArtifacts) HandleList(c *gin.Context, runID string) {
	var (
		result []stepArtifacts
		err    error
	)
	if a.p.s3Cli != nil && a.p.cfg.S3.Bucket != "" {
		result, err = listArtifactsS3(c.Request.Context(), a.p.s3Cli, a.p.cfg.S3.Bucket, runID)
	} else {
		result, err = listArtifactsLocal(a.p.cfg.OutputDir, runID)
	}
	if err != nil {
		slog.Warn("list artifacts failed", "run_id", runID, "err", err)
	}
	if result == nil {
		result = []stepArtifacts{}
	}
	c.JSON(http.StatusOK, result)
}

func (a *piperArtifacts) HandleDownload(c *gin.Context, runID, step, rest string) {
	downloadArtifact(c, a.p.s3Cli, a.p.cfg.S3.Bucket, a.p.cfg.OutputDir, runID, step, rest)
}

// downloadArtifact streams an artifact file to the gin context.
func downloadArtifact(c *gin.Context, s3Cli *s3.Client, bucket, outputDir, runID, step, rest string) {
	if containsDotDot(rest) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	if s3Cli != nil && bucket != "" {
		downloadArtifactS3(c, s3Cli, bucket, runID, step, rest)
		return
	}
	downloadArtifactLocal(c, outputDir, runID, step, rest)
}
