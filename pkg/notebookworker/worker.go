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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	MasterURL     string
	Addr          string // listen address, e.g. ":7701"
	AdvertiseAddr string // URL advertised to master, e.g. "https://node1.internal:7701"
	TLSCert       string // path to TLS certificate file (enables HTTPS)
	TLSKey        string // path to TLS private key file (enables HTTPS)
	JupyterBin    string // path to jupyter binary (default: "jupyter")
	NotebooksRoot string // base directory for notebook work dirs (default: "./notebooks")
	GPUs          []string
	Hostname      string
	ID            string // UUID; caller must generate
}

// Worker is the notebook worker agent.
type Worker struct {
	cfg        Config
	jupyterBin string // resolved at Run() time
	mu         sync.Mutex
	notebooks  map[string]*localNotebook
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

// Run starts the HTTP(S) server, registers with master, and runs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	augmentPATH()

	bin, err := resolveJupyter(w.cfg.JupyterBin)
	if err != nil {
		return err
	}
	w.jupyterBin = bin
	slog.Info("notebook worker: jupyter binary resolved", "bin", bin)

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

	w.killAll()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

// killAll terminates all running notebook processes managed by this worker.
func (w *Worker) killAll() {
	w.mu.Lock()
	pids := make([]int, 0, len(w.notebooks))
	for _, nb := range w.notebooks {
		pids = append(pids, nb.pid)
	}
	w.notebooks = make(map[string]*localNotebook)
	w.mu.Unlock()
	for _, pid := range pids {
		workload.KillPID(pid)
	}
}

func (w *Worker) tlsEnabled() bool {
	return w.cfg.TLSCert != "" && w.cfg.TLSKey != ""
}

// advertiseURL returns the URL the master should use to reach this worker.
// If AdvertiseAddr is set it is used as-is; otherwise it is derived from Addr.
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
					slog.Info("notebook worker registered with master", "master", w.cfg.MasterURL, "id", w.cfg.ID, "addr", w.advertiseURL())
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
	workDir := w.resolveWorkDir(name, spec.Spec.Runtime.WorkDir)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot create work_dir: " + err.Error()})
		return
	}

	token := uuid.New().String()[:8]
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", name)

	// "jupyter-lab" is a standalone binary; "jupyter" needs the "lab" subcommand.
	base := filepath.Base(w.jupyterBin)
	var command []string
	if base == "jupyter-lab" || base == "jupyter-lab.exe" {
		command = []string{w.jupyterBin}
	} else {
		command = []string{w.jupyterBin, "lab"}
	}
	command = append(command,
		"--no-browser",
		fmt.Sprintf("--port=%d", port),
		fmt.Sprintf("--notebook-dir=%s", workDir),
		fmt.Sprintf("--ServerApp.token=%s", token),
		fmt.Sprintf("--ServerApp.base_url=%s", baseURL),
	)

	pspec := workload.ProcessSpec{
		Name:    name,
		Command: command,
		Port:    port,
		GPUs:    spec.Spec.Runtime.GPUs,
	}

	pid, endpoint, cmd, err := workload.StartProcess(pspec)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "executable file not found") {
			msg += fmt.Sprintf(
				"\n\nHint: jupyter binary %q not found. Options:\n"+
					"  1. Install jupyterlab:  pip install jupyterlab\n"+
					"  2. Specify the path in .piper.yaml:\n"+
					"       notebook_worker:\n"+
					"         jupyter_bin: /path/to/jupyter-lab\n"+
					"  3. Find your install:   which jupyter-lab || which jupyter",
				w.jupyterBin,
			)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
		return
	}

	w.mu.Lock()
	w.notebooks[name] = &localNotebook{name: name, pid: pid, cmd: cmd}
	w.mu.Unlock()

	masterURL := req.MasterURL
	if masterURL == "" {
		masterURL = w.cfg.MasterURL
	}

	w.callbackStatus(masterURL, name, "running", endpoint)

	workload.WatchProcess(cmd, func(status string) {
		slog.Info("notebook process exited", "name", name, "status", status)
		w.mu.Lock()
		delete(w.notebooks, name)
		w.mu.Unlock()
		w.callbackStatus(masterURL, name, status, "")
	})

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

// resolveWorkDir determines the notebook's working directory.
// If NotebooksRoot is set, all notebooks are confined to {root}/{name} regardless
// of what the spec requests — this prevents access outside the designated tree.
// If NotebooksRoot is not set, the spec's work_dir is used (defaulting to "./notebooks/{name}").
func (w *Worker) resolveWorkDir(name, specWorkDir string) string {
	root := w.cfg.NotebooksRoot
	if root == "" {
		root = "notebooks"
	}
	root, _ = filepath.Abs(root)
	return filepath.Join(root, name)
}

// resolveJupyter returns the full path to a working jupyter binary.
// If configured explicitly, verifies it is executable. Otherwise searches
// every "jupyter-lab" and "jupyter" entry in PATH and returns the first one
// that actually runs (shebang interpreter exists).
func resolveJupyter(configured string) (string, error) {
	if configured != "" {
		if err := checkExecutable(configured); err != nil {
			return "", fmt.Errorf("notebook worker: configured jupyter binary %q: %w", configured, err)
		}
		return configured, nil
	}
	for _, candidate := range []string{"jupyter-lab", "jupyter"} {
		paths, _ := lookPathAll(candidate)
		for _, p := range paths {
			if checkExecutable(p) == nil {
				return p, nil
			}
			slog.Warn("notebook worker: skipping broken jupyter binary", "path", p)
		}
	}
	return "", fmt.Errorf(
		"notebook worker: no working jupyter binary found in PATH\n" +
			"Install jupyterlab (pip install jupyterlab) or set notebook_worker.jupyter_bin in .piper.yaml\n" +
			"Find your binary: which jupyter-lab || which jupyter",
	)
}

// checkExecutable verifies that p is executable by running it with --version.
func checkExecutable(p string) error {
	cmd := exec.Command(p, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not executable: %w", err)
	}
	return nil
}

// lookPathAll returns all matches for name in PATH plus well-known brew cellar
// locations, so broken PATH symlinks don't block a working install.
func lookPathAll(name string) ([]string, error) {
	pathEnv := os.Getenv("PATH")
	seen := map[string]bool{}
	var found []string

	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			found = append(found, p)
		}
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		add(filepath.Join(dir, name))
	}

	// Brew formula directories: /usr/local/opt/<formula>/bin and
	// /opt/homebrew/opt/<formula>/bin — search both Intel and Apple Silicon.
	for _, base := range []string{"/usr/local/opt", "/opt/homebrew/opt"} {
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			add(filepath.Join(base, e.Name(), "bin", name))
		}
	}

	return found, nil
}

// augmentPATH merges the PATH from the user's login shell into the current
// process so that tools installed via brew, conda, pyenv, etc. are reachable.
func augmentPATH() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	// Run login shell to get its PATH.
	out, err := exec.Command(shell, "-l", "-c", "echo $PATH").Output()
	if err == nil {
		shellPATH := strings.TrimSpace(string(out))
		if shellPATH != "" {
			current := os.Getenv("PATH")
			merged := mergePATH(current, shellPATH)
			_ = os.Setenv("PATH", merged)
			return
		}
	}

	// Fallback: well-known directories.
	home := os.Getenv("HOME")
	extras := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/opt/conda/bin",
		home + "/.local/bin",
		home + "/miniconda3/bin",
		home + "/anaconda3/bin",
		home + "/.pyenv/shims",
	}
	current := os.Getenv("PATH")
	for _, p := range extras {
		if p != "" && !strings.Contains(current, p) {
			current = current + string(os.PathListSeparator) + p
		}
	}
	_ = os.Setenv("PATH", current)
}

// mergePATH combines two colon-separated PATH strings, deduplicating entries
// while preserving the order of the first argument.
func mergePATH(base, extra string) string {
	seen := make(map[string]bool)
	var parts []string
	for _, p := range strings.Split(base, string(os.PathListSeparator)) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	for _, p := range strings.Split(extra, string(os.PathListSeparator)) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
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
