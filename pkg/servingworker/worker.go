// Package servingworker implements the serving worker agent.
// Run one of these on each bare-metal node that should execute serving processes.
// It registers with the Master and accepts deploy/stop commands via HTTP.
package servingworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/workload"
)

// Config holds configuration for a serving worker agent.
type Config struct {
	MasterURL string
	Addr      string // HTTP listen address, e.g. ":7700"
	GPUs      []string
	Hostname  string
	ID        string // UUID; caller must generate
}

// Worker is the serving worker agent.
type Worker struct {
	cfg      Config
	mu       sync.Mutex
	services map[string]*localService
}

type localService struct {
	name string
	pid  int
	cmd  *exec.Cmd
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	return &Worker{
		cfg:      cfg,
		services: make(map[string]*localService),
	}
}

// Run starts the HTTP server, registers with master, and runs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", w.healthHandler)
	r.POST("/deploy", w.deployHandler)
	r.DELETE("/service/:name", w.stopHandler)
	r.POST("/service/:name/restart", w.restartHandler)

	srv := &http.Server{
		Addr:    w.cfg.Addr,
		Handler: r,
	}

	// Start HTTP server.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("serving worker HTTP server starting", "addr", w.cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Register with master (with retries).
	go w.registerLoop(ctx)

	// Heartbeat loop.
	go w.heartbeatLoop(ctx)

	// Wait for shutdown.
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

func (w *Worker) registerLoop(ctx context.Context) {
	payload, _ := json.Marshal(serving.ServingWorkerInfo{
		ID:       w.cfg.ID,
		Addr:     fmt.Sprintf("http://%s", w.cfg.Addr),
		GPUs:     w.cfg.GPUs,
		Hostname: w.cfg.Hostname,
	})

	for {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			w.cfg.MasterURL+"/api/serving-workers", bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode < 300 {
					slog.Info("serving worker registered with master", "master", w.cfg.MasterURL, "id", w.cfg.ID)
					return
				}
			}
		}
		slog.Warn("serving worker: registration failed, retrying in 5s", "master", w.cfg.MasterURL)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (w *Worker) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			url := fmt.Sprintf("%s/api/serving-workers/%s/heartbeat", w.cfg.MasterURL, w.cfg.ID)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
			if err != nil {
				continue
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				slog.Warn("serving worker heartbeat failed", "err", err)
				continue
			}
			_ = resp.Body.Close()
		}
	}
}

// GET /
func (w *Worker) healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": w.cfg.ID})
}

// POST /deploy
func (w *Worker) deployHandler(c *gin.Context) {
	var req struct {
		YAML      string `json:"yaml"`
		LocalPath string `json:"local_path"`
		S3URI     string `json:"s3_uri"`
		MasterURL string `json:"master_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var svc serving.ModelService
	if err := yaml.Unmarshal([]byte(req.YAML), &svc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid YAML: " + err.Error()})
		return
	}

	rt := svc.Spec.Runtime
	name := svc.Metadata.Name
	if len(rt.Command) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runtime.command must not be empty"})
		return
	}
	if rt.Port == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runtime.port must be set"})
		return
	}

	modelDir := req.LocalPath
	if modelDir == "" {
		modelDir = req.S3URI
	}

	envMap := map[string]string{
		"PIPER_MODEL_DIR":    modelDir,
		"PIPER_SERVICE_NAME": name,
	}

	spec := workload.ProcessSpec{
		Name:       name,
		Command:    rt.Command,
		Env:        envMap,
		Port:       rt.Port,
		HealthPath: rt.HealthPath,
		GPUs:       rt.GPUs,
	}

	pid, endpoint, cmd, err := workload.StartProcess(spec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	w.mu.Lock()
	w.services[name] = &localService{name: name, pid: pid, cmd: cmd}
	w.mu.Unlock()

	masterURL := req.MasterURL
	if masterURL == "" {
		masterURL = w.cfg.MasterURL
	}

	// Report running status with endpoint to master.
	w.callbackStatus(masterURL, name, "running", endpoint)

	// Watch for exit.
	workload.WatchProcess(cmd, func(status string) {
		slog.Info("serving process exited", "name", name, "status", status)
		w.mu.Lock()
		delete(w.services, name)
		w.mu.Unlock()
		w.callbackStatus(masterURL, name, status, "")
	})

	// Best-effort health check.
	healthPath := rt.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	go func() {
		if err := workload.WaitReady(context.Background(), endpoint+healthPath, 30*time.Second); err != nil {
			slog.Warn("serving health check timed out", "name", name, "endpoint", endpoint)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"name": name, "pid": pid, "endpoint": endpoint})
}

// DELETE /service/:name
func (w *Worker) stopHandler(c *gin.Context) {
	name := c.Param("name")
	w.mu.Lock()
	ls, ok := w.services[name]
	if ok {
		delete(w.services, name)
	}
	w.mu.Unlock()

	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	workload.KillPID(ls.pid)
	c.Status(http.StatusNoContent)
}

// POST /service/:name/restart
func (w *Worker) restartHandler(c *gin.Context) {
	name := c.Param("name")
	w.mu.Lock()
	ls, ok := w.services[name]
	if ok {
		delete(w.services, name)
	}
	w.mu.Unlock()

	if ok {
		workload.KillPID(ls.pid)
	}
	// The master will follow up with a new deploy call; just acknowledge.
	c.Status(http.StatusNoContent)
}

func (w *Worker) callbackStatus(masterURL, name, status, endpoint string) {
	body, _ := json.Marshal(map[string]string{
		"status":   status,
		"endpoint": endpoint,
	})
	url := fmt.Sprintf("%s/api/servings/%s/status", masterURL, name)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("serving worker: callback failed", "name", name, "status", status, "err", err)
		return
	}
	_ = resp.Body.Close()
}
