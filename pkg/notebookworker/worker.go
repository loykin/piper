// Package notebookworker implements the notebook worker agent.
// Run one of these on each bare-metal node that should execute Jupyter notebook servers.
// It registers with the Master and accepts start/stop commands via HTTP.
package notebookworker

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
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/workload"
)

// Config holds configuration for a notebook worker agent.
type Config struct {
	MasterURL string
	Addr      string // HTTP listen address, e.g. ":7701"
	GPUs      []string
	Hostname  string
	ID        string // UUID; caller must generate
}

// Worker is the notebook worker agent.
type Worker struct {
	cfg       Config
	mu        sync.Mutex
	notebooks map[string]*localNotebook
}

type localNotebook struct {
	name string
	pid  int
	cmd  *exec.Cmd
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	return &Worker{
		cfg:       cfg,
		notebooks: make(map[string]*localNotebook),
	}
}

// Run starts the HTTP server, registers with master, and runs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", w.healthHandler)
	r.POST("/start", w.startHandler)
	r.DELETE("/notebook/:name", w.stopHandler)

	srv := &http.Server{
		Addr:    w.cfg.Addr,
		Handler: r,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("notebook worker HTTP server starting", "addr", w.cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	go w.registerLoop(ctx)
	go w.heartbeatLoop(ctx)

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
	payload, _ := json.Marshal(notebook.NotebookWorkerInfo{
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
			w.cfg.MasterURL+"/api/notebook-workers", bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode < 300 {
					slog.Info("notebook worker registered with master", "master", w.cfg.MasterURL, "id", w.cfg.ID)
					return
				}
			}
		}
		slog.Warn("notebook worker: registration failed, retrying in 5s", "master", w.cfg.MasterURL)
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
			url := fmt.Sprintf("%s/api/notebook-workers/%s/heartbeat", w.cfg.MasterURL, w.cfg.ID)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
			if err != nil {
				continue
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				slog.Warn("notebook worker heartbeat failed", "err", err)
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

// POST /start
func (w *Worker) startHandler(c *gin.Context) {
	var req struct {
		YAML      string `json:"yaml"`
		MasterURL string `json:"master_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var spec notebook.NotebookServerSpec
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid YAML: " + err.Error()})
		return
	}

	name := spec.Metadata.Name
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata.name is required"})
		return
	}

	port := spec.Spec.Runtime.Port
	if port == 0 {
		port = 8888
	}
	workDir := spec.Spec.Runtime.WorkDir
	if workDir == "" {
		workDir = "."
	}

	token := uuid.New().String()[:8]

	pspec := workload.ProcessSpec{
		Name: name,
		Command: []string{
			"jupyter", "lab",
			"--no-browser",
			fmt.Sprintf("--port=%d", port),
			fmt.Sprintf("--notebook-dir=%s", workDir),
			fmt.Sprintf("--ServerApp.token=%s", token),
		},
		Port: port,
		GPUs: spec.Spec.Runtime.GPUs,
	}

	pid, endpoint, cmd, err := workload.StartProcess(pspec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	w.mu.Lock()
	w.notebooks[name] = &localNotebook{name: name, pid: pid, cmd: cmd}
	w.mu.Unlock()

	masterURL := req.MasterURL
	if masterURL == "" {
		masterURL = w.cfg.MasterURL
	}

	// Report running status with endpoint to master.
	w.callbackStatus(masterURL, name, "running", endpoint)

	// Watch for exit.
	workload.WatchProcess(cmd, func(status string) {
		slog.Info("notebook process exited", "name", name, "status", status)
		w.mu.Lock()
		delete(w.notebooks, name)
		w.mu.Unlock()
		w.callbackStatus(masterURL, name, status, "")
	})

	// Best-effort health check.
	go func() {
		if err := workload.WaitReady(context.Background(), endpoint, 30*time.Second); err != nil {
			slog.Warn("notebook health check timed out", "name", name, "endpoint", endpoint)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"name": name, "pid": pid, "endpoint": endpoint, "token": token})
}

// DELETE /notebook/:name
func (w *Worker) stopHandler(c *gin.Context) {
	name := c.Param("name")
	w.mu.Lock()
	nb, ok := w.notebooks[name]
	if ok {
		delete(w.notebooks, name)
	}
	w.mu.Unlock()

	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "notebook not found"})
		return
	}
	workload.KillPID(nb.pid)
	c.Status(http.StatusNoContent)
}

func (w *Worker) callbackStatus(masterURL, name, status, endpoint string) {
	body, _ := json.Marshal(map[string]string{
		"status":   status,
		"endpoint": endpoint,
	})
	url := fmt.Sprintf("%s/api/notebooks/%s/status", masterURL, name)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("notebook worker: callback failed", "name", name, "status", status, "err", err)
		return
	}
	_ = resp.Body.Close()
}
