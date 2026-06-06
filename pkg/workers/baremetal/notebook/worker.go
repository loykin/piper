// Package notebookworker implements the notebook worker agent.
// Run one of these on each node that should execute Jupyter notebook servers.
// It connects to the master via gRPC and handles notebook lifecycle commands.
package notebookworker

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/notebook"
)

// Config holds configuration for a notebook worker agent.
type Config struct {
	AgentAddr     string // gRPC address of master agent server, e.g. "master:9090"
	NotebooksRoot string // base directory for notebook work dirs (default: "./notebooks")
	PortRange     string // "START-END", e.g. "8888-9900"
	Mode          string // process | docker
	Docker        DockerConfig
	GPUs          []string
	Hostname      string
	ID            string // stable worker identity assigned by the caller
}

// Worker is the notebook worker agent.
type Worker struct {
	cfg           Config
	runtime       Runtime
	client        *grpcagent.Client
	portAllocator func() (int, error)
	mu            sync.Mutex
	notebooks     map[string]*localNotebook
	reservedPorts map[int]struct{}
	terminal      map[string]string
	nextGen       uint64
}

type localNotebook struct {
	name string
	port int
	gen  uint64 // incremented on each start to distinguish stale OnExit callbacks
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	runtime, err := newRuntime(cfg)
	if err != nil {
		runtime = &failingRuntime{err: err}
	}

	client := grpcagent.NewClient(grpcagent.ClientConfig{
		AgentAddr:    cfg.AgentAddr,
		AgentID:      cfg.ID,
		Kind:         iagent.KindBareMetal,
		Mode:         cfg.Mode,
		Hostname:     cfg.Hostname,
		GPUs:         cfg.GPUs,
		Capabilities: []string{iagent.CapabilityNotebook},
	})

	w := &Worker{
		cfg:           cfg,
		runtime:       runtime,
		client:        client,
		portAllocator: nil,
		notebooks:     make(map[string]*localNotebook),
		reservedPorts: make(map[int]struct{}),
		terminal:      make(map[string]string),
	}
	w.portAllocator = w.allocatePort

	d := client.Dispatcher()
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookProvisionVolume, w.provisionVolume)
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookStart, w.startNotebook)
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookStop, func(ctx context.Context, req notebook.WorkerStopRequest) (any, error) {
		return nil, w.stopNotebook(ctx, req.Name)
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookDeprovision, func(ctx context.Context, req notebook.WorkerDeprovisionVolumeRequest) (any, error) {
		return nil, w.deprovisionVolume(ctx, req)
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookSyncStatus, w.syncStatus)
	_ = grpcagent.RegisterJSON(d, iagent.MethodFSListFiles, w.listFiles)

	return w
}

func newRuntime(cfg Config) (Runtime, error) {
	switch cfg.Mode {
	case "", RuntimeProcess:
		return newProcessRuntime(cfg.NotebooksRoot), nil
	case RuntimeDocker:
		return newDockerRuntime(cfg.Docker, cfg.ID)
	default:
		return nil, fmt.Errorf("unsupported notebook worker mode %q", cfg.Mode)
	}
}

type failingRuntime struct{ err error }

func (r *failingRuntime) Start(context.Context, RuntimeStartRequest) (*StartedNotebook, error) {
	return nil, r.err
}
func (r *failingRuntime) Stop(context.Context, string) error { return r.err }
func (r *failingRuntime) KillAll(context.Context) error      { return r.err }
func (r *failingRuntime) Status(string) string               { return notebook.StatusStopped }

// Run connects to the master via gRPC and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.recoverContainers(ctx)
	defer func() {
		if err := w.runtime.KillAll(context.Background()); err != nil {
			slog.Warn("notebook worker cleanup failed", "err", err)
		}
	}()
	return w.client.Run(ctx)
}

func (w *Worker) provisionVolume(_ context.Context, req notebook.WorkerProvisionVolumeRequest) (*notebook.WorkerProvisionVolumeResponse, error) {
	if req.VolumeID == "" {
		return nil, fmt.Errorf("volume_id is required")
	}
	dir := w.volumeDir(req.VolumeID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create volume dir: %w", err)
	}
	slog.Info("notebook volume provisioned", "volume_id", req.VolumeID, "dir", dir)
	return &notebook.WorkerProvisionVolumeResponse{WorkDir: dir}, nil
}

func (w *Worker) startNotebook(_ context.Context, req notebook.WorkerStartRequest) (*notebook.WorkerStartResponse, error) {
	var spec notebook.NotebookServerSpec
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	name := spec.Metadata.Name
	if name == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}

	if recoverable, ok := w.runtime.(targetedRecoveryRuntime); ok {
		w.mu.Lock()
		_, active := w.notebooks[name]
		w.mu.Unlock()
		if !active {
			if err := w.recoverProcessTarget(recoverable, notebook.WorkerSyncStatusTarget{Name: name}); err != nil {
				return nil, err
			}
		}
		w.mu.Lock()
		_, active = w.notebooks[name]
		w.mu.Unlock()
		if active {
			return nil, fmt.Errorf("notebook %q is already active", name)
		}
	}

	port, err := w.pickPort()
	if err != nil {
		return nil, err
	}

	workDir := req.WorkDir
	if workDir == "" {
		if req.VolumeID == "" {
			w.releasePort(port)
			return nil, fmt.Errorf("volume_id is required when work_dir is empty")
		}
		workDir = w.volumeDir(req.VolumeID)
	}

	token := uuid.New().String()
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", name)
	target := fmt.Sprintf("127.0.0.1:%d", port)

	// Register the notebook entry before starting so that an OnExit callback that
	// fires before runtime.Start returns cannot race with the post-start registration.
	w.mu.Lock()
	if _, exists := w.notebooks[name]; exists {
		w.mu.Unlock()
		w.releasePort(port)
		return nil, fmt.Errorf("notebook %q is already active", name)
	}
	w.nextGen++
	gen := w.nextGen
	delete(w.terminal, name)
	w.notebooks[name] = &localNotebook{name: name, port: port, gen: gen}
	w.mu.Unlock()

	go func() {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			slog.Error("notebook worker: cannot create work dir", "name", name, "err", err)
			w.mu.Lock()
			current := w.notebooks[name] != nil && w.notebooks[name].gen == gen
			if current {
				delete(w.notebooks, name)
				w.terminal[name] = notebook.StatusFailed
			}
			w.mu.Unlock()
			w.releasePort(port)
			if current {
				w.pushStatus(name, notebook.StatusFailed, "", workDir, "", 0, "")
			}
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
				nb := w.notebooks[name]
				current := nb != nil && nb.gen == gen
				if current {
					delete(w.notebooks, name)
					w.terminal[name] = status
				}
				w.mu.Unlock()
				// Always release this start's port regardless of generation.
				// State/status updates are only pushed for the current generation.
				w.releasePort(port)
				if current {
					w.pushStatus(name, status, "", "", "", 0, "")
				}
			},
		})
		if err != nil {
			slog.Error("notebook worker: start failed", "name", name, "err", err)
			w.mu.Lock()
			current := w.notebooks[name] != nil && w.notebooks[name].gen == gen
			if current {
				delete(w.notebooks, name)
				w.terminal[name] = notebook.StatusFailed
			}
			w.mu.Unlock()
			w.releasePort(port)
			if current {
				w.pushStatus(name, notebook.StatusFailed, "", workDir, "", 0, "")
			}
			return
		}

		// Guard against a fast exit that already fired OnExit before we get here.
		w.mu.Lock()
		nb := w.notebooks[name]
		w.mu.Unlock()
		if nb == nil || nb.gen != gen {
			return
		}

		endpoint := fmt.Sprintf("tunnel://%s?target=%s", w.cfg.ID, target)
		statusToken := started.Token
		if statusToken == "" {
			statusToken = token
		}

		w.pushStatus(name, notebook.StatusRunning, endpoint, workDir, statusToken, started.PID, started.EnvPath)
	}()

	return &notebook.WorkerStartResponse{
		Token:    token,
		WorkDir:  workDir,
		Endpoint: fmt.Sprintf("tunnel://%s?target=%s", w.cfg.ID, target),
	}, nil
}

func (w *Worker) stopNotebook(_ context.Context, name string) error {
	w.mu.Lock()
	nb, ok := w.notebooks[name]
	w.mu.Unlock()

	if !ok {
		if recoverable, recoverableOK := w.runtime.(targetedRecoveryRuntime); recoverableOK {
			if err := w.recoverProcessTarget(recoverable, notebook.WorkerSyncStatusTarget{Name: name}); err != nil {
				return err
			}
			w.mu.Lock()
			nb, ok = w.notebooks[name]
			w.mu.Unlock()
		}
	}
	if !ok {
		return nil
	}

	// Stop runtime first. Clean up local state only after success so that
	// a failed stop leaves the notebook in a recoverable, tracked state.
	if err := w.runtime.Stop(context.Background(), nb.name); err != nil {
		return err
	}

	w.mu.Lock()
	current := w.notebooks[name] != nil && w.notebooks[name].gen == nb.gen
	if current {
		delete(w.notebooks, name)
		w.terminal[name] = notebook.StatusStopped
	}
	w.mu.Unlock()
	w.releasePort(nb.port)
	return nil
}

func (w *Worker) deprovisionVolume(_ context.Context, req notebook.WorkerDeprovisionVolumeRequest) error {
	if req.VolumeID == "" {
		return nil
	}
	dir := w.volumeDir(req.VolumeID)
	root := w.notebooksRoot()
	absRoot, _ := filepath.Abs(root)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid work_dir: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("work_dir is outside notebooks root")
	}
	if err := os.RemoveAll(absDir); err != nil {
		return err
	}
	slog.Info("notebook volume deleted", "volume_id", req.VolumeID, "dir", absDir)
	return nil
}

func (w *Worker) listFiles(_ context.Context, req notebook.FSListFilesRequest) (*notebook.FSListFilesResponse, error) {
	root := req.WorkDir
	if root == "" {
		return &notebook.FSListFilesResponse{Files: []string{}}, nil
	}

	allowed := make(map[string]bool, len(req.Ext))
	for _, e := range req.Ext {
		allowed[e] = true
	}
	maxFiles := req.MaxFiles
	if maxFiles <= 0 || maxFiles > 1000 {
		maxFiles = 500
	}

	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(allowed) > 0 && !allowed[filepath.Ext(d.Name())] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		files = append(files, rel)
		if len(files) >= maxFiles {
			return fs.SkipAll
		}
		return nil
	})

	if files == nil {
		files = []string{}
	}
	return &notebook.FSListFilesResponse{Files: files}, nil
}

func (w *Worker) syncStatus(_ context.Context, req notebook.WorkerSyncStatusRequest) (notebook.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Targets))
	for _, target := range req.Targets {
		name := target.Name
		w.mu.Lock()
		terminal := w.terminal[name]
		activeNotebook := w.notebooks[name]
		active := activeNotebook != nil
		if active && activeNotebook.port == 0 && target.Port > 0 {
			activeNotebook.port = target.Port
			w.reservedPorts[target.Port] = struct{}{}
		}
		w.mu.Unlock()
		if terminal != "" {
			statuses[name] = terminal
			continue
		}

		if !active {
			if recoverable, ok := w.runtime.(targetedRecoveryRuntime); ok {
				if err := w.recoverProcessTarget(recoverable, target); err != nil {
					return notebook.WorkerSyncStatusResponse{}, err
				}
			}
		}
		statuses[name] = w.runtime.Status(name)
	}
	return notebook.WorkerSyncStatusResponse{Statuses: statuses}, nil
}

func (w *Worker) pushStatus(name, status, endpoint, workDir, token string, pid int, env string) {
	if err := w.client.SendPush(iagent.MethodNotebookStatusUpdate, notebook.WorkerStatusUpdate{
		Name:     name,
		Status:   status,
		Endpoint: endpoint,
		WorkDir:  workDir,
		Token:    token,
		PID:      pid,
		Env:      env,
	}); err != nil {
		slog.Warn("notebook worker: status push failed", "name", name, "status", status, "err", err)
	}
}

func (w *Worker) notebooksRoot() string {
	if w.cfg.NotebooksRoot != "" {
		return w.cfg.NotebooksRoot
	}
	return "notebooks"
}

func (w *Worker) volumeDir(volumeID string) string {
	abs, _ := filepath.Abs(w.notebooksRoot())
	return filepath.Join(abs, volumeID)
}

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
	for port := range w.reservedPorts {
		used[port] = true
	}
	w.mu.Unlock()

	for port := start; port <= end; port++ {
		if used[port] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			w.mu.Lock()
			if _, already := w.reservedPorts[port]; already {
				w.mu.Unlock()
				continue
			}
			w.reservedPorts[port] = struct{}{}
			w.mu.Unlock()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port in range %s", portRange)
}

func (w *Worker) releasePort(port int) {
	if port <= 0 {
		return
	}
	w.mu.Lock()
	delete(w.reservedPorts, port)
	w.mu.Unlock()
}

func (w *Worker) pickPort() (int, error) {
	if w.portAllocator != nil {
		return w.portAllocator()
	}
	return w.allocatePort()
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

// recoverContainers reconnects to Docker containers running from a previous
// worker instance. Process recovery is target-driven during status sync.
func (w *Worker) recoverContainers(ctx context.Context) {
	recoverable, ok := w.runtime.(recoverableRuntime)
	if !ok {
		return
	}
	onRecovered := func(rec recoveredRuntime) func(status string) {
		w.mu.Lock()
		w.nextGen++
		gen := w.nextGen
		delete(w.terminal, rec.Name)
		w.notebooks[rec.Name] = &localNotebook{name: rec.Name, port: rec.Port, gen: gen}
		if rec.Port > 0 {
			w.reservedPorts[rec.Port] = struct{}{}
		}
		w.mu.Unlock()

		return func(status string) {
			w.mu.Lock()
			nb := w.notebooks[rec.Name]
			current := nb != nil && nb.gen == gen
			if current {
				delete(w.notebooks, rec.Name)
				w.terminal[rec.Name] = status
			}
			w.mu.Unlock()
			w.releasePort(rec.Port)
			if current {
				w.pushStatus(rec.Name, status, "", "", "", 0, "")
			}
		}
	}
	onTerminal := func(name, status string) {
		w.mu.Lock()
		_, active := w.notebooks[name]
		if !active {
			w.terminal[name] = status
		}
		w.mu.Unlock()
		if !active {
			w.pushStatus(name, status, "", "", "", 0, "")
		}
	}

	if err := recoverable.Recover(ctx, onRecovered, onTerminal); err != nil {
		slog.Warn("notebook worker: runtime recovery failed", "err", err)
		return
	}
}

func (w *Worker) recoverProcessTarget(runtime targetedRecoveryRuntime, target notebook.WorkerSyncStatusTarget) error {
	if target.Name == "" {
		return nil
	}

	w.mu.Lock()
	w.nextGen++
	gen := w.nextGen
	delete(w.terminal, target.Name)
	recoveredNotebook := &localNotebook{name: target.Name, port: target.Port, gen: gen}
	w.notebooks[target.Name] = recoveredNotebook
	if target.Port > 0 {
		w.reservedPorts[target.Port] = struct{}{}
	}
	w.mu.Unlock()

	running, err := runtime.RecoverTarget(target.Name, target.Port, func(status string) {
		w.mu.Lock()
		nb := w.notebooks[target.Name]
		current := nb != nil && nb.gen == gen
		port := recoveredNotebook.port
		if current {
			delete(w.notebooks, target.Name)
			w.terminal[target.Name] = status
		}
		w.mu.Unlock()
		w.releasePort(port)
		if current {
			w.pushStatus(target.Name, status, "", "", "", 0, "")
		}
	})
	if err != nil {
		w.mu.Lock()
		port := recoveredNotebook.port
		if nb := w.notebooks[target.Name]; nb != nil && nb.gen == gen {
			delete(w.notebooks, target.Name)
		}
		w.mu.Unlock()
		w.releasePort(port)
		return fmt.Errorf("recover notebook %q: %w", target.Name, err)
	}
	if !running {
		w.mu.Lock()
		port := recoveredNotebook.port
		if nb := w.notebooks[target.Name]; nb != nil && nb.gen == gen {
			delete(w.notebooks, target.Name)
		}
		w.mu.Unlock()
		w.releasePort(port)
		return nil
	}

	return nil
}
