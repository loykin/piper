// Package notebookworker implements the notebook worker agent.
// Run one of these on each node that should execute Jupyter notebook servers.
// It registers with the master and accepts start/stop commands via HTTP.
package notebookworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/piper/piper/pkg/notebook"
)

// Config holds configuration for a notebook worker agent.
type Config struct {
	MasterURL     string
	Addr          string // listen address, e.g. ":7701"
	AdvertiseAddr string // URL advertised to master, e.g. "https://node1.internal:7701"
	TLSCert       string // path to TLS certificate file (enables HTTPS)
	TLSKey        string // path to TLS private key file (enables HTTPS)
	NotebooksRoot string // base directory for notebook work dirs (default: "./notebooks")
	PortRange     string // "START-END", e.g. "8888-9900"
	Mode          string // process | docker
	Docker        DockerConfig
	GPUs          []string
	Hostname      string
	ID            string // UUID; caller must generate
}

// Worker is the notebook worker agent.
type Worker struct {
	cfg       Config
	runtime   Runtime
	mu        sync.Mutex
	notebooks map[string]*localNotebook
}

type localNotebook struct {
	name string
	port int
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	runtime, err := newRuntime(cfg)
	if err != nil {
		runtime = &failingRuntime{err: err}
	}
	return &Worker{
		cfg:       cfg,
		runtime:   runtime,
		notebooks: make(map[string]*localNotebook),
	}
}

func newRuntime(cfg Config) (Runtime, error) {
	switch cfg.Mode {
	case "", RuntimeProcess:
		return newProcessRuntime(), nil
	case RuntimeDocker:
		return newDockerRuntime(cfg.Docker)
	default:
		return nil, fmt.Errorf("unsupported notebook worker mode %q", cfg.Mode)
	}
}

type failingRuntime struct {
	err error
}

func (r *failingRuntime) Start(context.Context, RuntimeStartRequest) (*StartedNotebook, error) {
	return nil, r.err
}

func (r *failingRuntime) Stop(context.Context, string) error { return r.err }

func (r *failingRuntime) KillAll(context.Context) error { return r.err }

// Run starts the HTTP(S) server, registers with master, and runs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", w.healthHandler)
	r.POST("/volume", w.provisionVolumeHandler)
	r.POST("/start", w.startHandler)
	r.DELETE("/notebook/:name", w.stopHandler)
	r.DELETE("/volume/:name", w.deleteVolumeHandler)

	srv := &http.Server{
		Addr:    w.cfg.Addr,
		Handler: r,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("notebook worker HTTP server starting", "addr", w.cfg.Addr, "tls", w.tlsEnabled())
		var err error
		if w.tlsEnabled() {
			err = srv.ListenAndServeTLS(w.cfg.TLSCert, w.cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
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

	if err := w.runtime.KillAll(context.Background()); err != nil {
		slog.Warn("notebook worker cleanup failed", "err", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

func (w *Worker) tlsEnabled() bool {
	return w.cfg.TLSCert != "" && w.cfg.TLSKey != ""
}

func (w *Worker) advertiseURL() string {
	if w.cfg.AdvertiseAddr != "" {
		return w.cfg.AdvertiseAddr
	}
	scheme := "http"
	if w.tlsEnabled() {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(w.cfg.Addr)
	if err != nil {
		return scheme + "://" + w.cfg.Addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func (w *Worker) registerLoop(ctx context.Context) {
	payload, _ := json.Marshal(notebook.NotebookWorkerInfo{
		ID:       w.cfg.ID,
		Addr:     w.advertiseURL(),
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
					slog.Info("notebook worker registered with master",
						"master", w.cfg.MasterURL, "id", w.cfg.ID, "addr", w.advertiseURL())
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

// POST /start — accepts a notebook start request and returns 202 Accepted immediately.
// Env preparation, process start, and master callback all happen in a background goroutine.
func (w *Worker) startHandler(c *gin.Context) {
	var req struct {
		YAML      string `json:"yaml"`
		MasterURL string `json:"master_url"`
		WorkDir   string `json:"work_dir"`  // non-empty = reuse this exact directory
		VolumeID  string `json:"volume_id"` // UUID of the backing NotebookVolume
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

	port, err := w.allocatePort()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	workDir := req.WorkDir
	if workDir == "" {
		if req.VolumeID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "volume_id is required when work_dir is empty"})
			w.releasePort(port)
			return
		}
		workDir = w.volumeDir(req.VolumeID)
	}

	token := uuid.New().String()
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", name)

	masterURL := req.MasterURL
	if masterURL == "" {
		masterURL = w.cfg.MasterURL
	}

	// Return 202 immediately so the master can persist the token/workDir while
	// the potentially slow env preparation runs in the background.
	c.JSON(http.StatusAccepted, gin.H{
		"token":    token,
		"work_dir": workDir,
	})

	go func() {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			slog.Error("notebook worker: cannot create work dir", "name", name, "err", err)
			w.callbackStatus(masterURL, name, notebook.StatusFailed, "", workDir)
			w.releasePort(port)
			return
		}

		mode := w.cfg.Mode
		if mode == "" {
			mode = RuntimeProcess
		}
		logRuntimeStart(mode, name, workDir, port)
		started, err := w.runtime.Start(context.Background(), RuntimeStartRequest{
			Name:    name,
			Spec:    spec,
			WorkDir: workDir,
			Port:    port,
			Token:   token,
			BaseURL: baseURL,
			OnExit: func(status string) {
				slog.Info("notebook runtime exited", "name", name, "status", status)
				w.mu.Lock()
				delete(w.notebooks, name)
				w.mu.Unlock()
				w.callbackStatus(masterURL, name, status, "", "")
			},
		})
		if err != nil {
			slog.Error("notebook worker: start failed", "name", name, "err", err)
			w.callbackStatus(masterURL, name, notebook.StatusFailed, "", workDir)
			w.releasePort(port)
			return
		}

		w.mu.Lock()
		w.notebooks[name] = &localNotebook{name: name, port: port}
		w.mu.Unlock()

		w.callbackStatusFull(masterURL, name, notebook.StatusRunning, started.Endpoint, workDir, token, started.PID, started.EnvPath)
	}()
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
	if err := w.runtime.Stop(context.Background(), nb.name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// POST /volume — provisions a new notebook volume directory on this worker.
// Returns {"work_dir": "<path>"} on success.
func (w *Worker) provisionVolumeHandler(c *gin.Context) {
	var req struct {
		VolumeID string `json:"volume_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.VolumeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "volume_id is required"})
		return
	}
	dir := w.volumeDir(req.VolumeID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot create volume dir: " + err.Error()})
		return
	}
	slog.Info("notebook volume provisioned", "volume_id", req.VolumeID, "dir", dir)
	c.JSON(http.StatusOK, gin.H{"work_dir": dir})
}

// DELETE /volume/:name — removes a notebook volume's work directory.
// The "work_dir" query parameter must be provided; the path is validated to be
// under the notebooks root before deletion. The :name segment is informational only.
func (w *Worker) deleteVolumeHandler(c *gin.Context) {
	name := c.Param("name")

	wd := c.Query("work_dir")
	if wd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "work_dir query parameter is required"})
		return
	}

	root := w.cfg.NotebooksRoot
	if root == "" {
		root = "notebooks"
	}
	absRoot, _ := filepath.Abs(root)
	absWD, err := filepath.Abs(wd)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid work_dir: " + err.Error()})
		return
	}
	// Guard against directory traversal.
	rel, err := filepath.Rel(absRoot, absWD)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		c.JSON(http.StatusForbidden, gin.H{"error": "work_dir is outside notebooks root"})
		return
	}

	if err := os.RemoveAll(absWD); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	slog.Info("notebook volume deleted", "name", name, "dir", absWD)
	c.Status(http.StatusNoContent)
}

// volumeDir returns the canonical work directory for a volume: {notebooks_root}/{volume_id}.
// Using the volume UUID as the directory name ensures the path is decoupled from the
// server name, matching the K8s PV model where storage identity != workload name.
func (w *Worker) volumeDir(volumeID string) string {
	root := w.cfg.NotebooksRoot
	if root == "" {
		root = "notebooks"
	}
	abs, _ := filepath.Abs(root)
	return filepath.Join(abs, volumeID)
}

// allocatePort finds a free port within the configured port range.
func (w *Worker) allocatePort() (int, error) {
	portRange := w.cfg.PortRange
	if portRange == "" {
		portRange = "8888-9900"
	}
	start, end, err := parsePortRange(portRange)
	if err != nil {
		return 0, err
	}

	w.mu.Lock()
	used := make(map[int]bool, len(w.notebooks))
	for _, nb := range w.notebooks {
		used[nb.port] = true
	}
	w.mu.Unlock()

	for port := start; port <= end; port++ {
		if used[port] {
			continue
		}
		// Test on 127.0.0.1 to match JupyterLab's actual bind address.
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port in range %s", portRange)
}

func parsePortRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid port_range %q: expected START-END", s)
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || start <= 0 || end < start {
		return 0, 0, fmt.Errorf("invalid port_range %q", s)
	}
	return start, end, nil
}

// ensureVenv creates the venv at venvPath if it doesn't exist, then installs
// jupyterlab if it isn't already present. Idempotent.
func ensureVenv(venvPath string) error {
	jupyterLab := filepath.Join(venvPath, "bin", "jupyter-lab")
	if info, err := os.Stat(jupyterLab); err == nil && !info.IsDir() {
		return nil
	}

	python := filepath.Join(venvPath, "bin", "python")
	if _, err := os.Stat(python); err != nil {
		slog.Info("creating venv", "path", venvPath)
		out, err := exec.Command("python3", "-m", "venv", venvPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("create venv %q: %w: %s", venvPath, err, bytes.TrimSpace(out))
		}
	}

	slog.Info("installing jupyterlab", "venv", venvPath)
	pip := filepath.Join(venvPath, "bin", "pip")
	out, err := exec.Command(pip, "install", "--quiet", "jupyterlab").CombinedOutput()
	if err != nil {
		return fmt.Errorf("install jupyterlab in %q: %w: %s", venvPath, err, bytes.TrimSpace(out))
	}
	return nil
}

// callbackStatusFull sends a full status update to the master, including endpoint,
// workDir, token, pid, and env. Used when the process has started successfully.
func (w *Worker) callbackStatusFull(masterURL, name, status, endpoint, workDir, token string, pid int, env string) {
	payload, _ := json.Marshal(map[string]any{
		"status":   status,
		"endpoint": endpoint,
		"work_dir": workDir,
		"token":    token,
		"pid":      pid,
		"env":      env,
	})
	url := fmt.Sprintf("%s/api/notebooks/%s/status", masterURL, name)
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("notebook worker: status callback failed", "name", name, "err", err)
		return
	}
	_ = resp.Body.Close()
}

// callbackStatus sends a minimal status update to the master (no token/pid/env).
// Used for process exit events and error states.
func (w *Worker) callbackStatus(masterURL, name, status, endpoint, workDir string) {
	w.callbackStatusFull(masterURL, name, status, endpoint, workDir, "", 0, "")
}

// releasePort removes the port reservation when a background start fails before
// the localNotebook entry is registered in w.notebooks.
func (w *Worker) releasePort(_ int) {
	// Port is allocated by scanning w.notebooks; once the process is not added
	// to the map (start failure path), the port becomes available automatically
	// on the next allocatePort scan. No explicit release is needed.
}
