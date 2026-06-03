// Package notebookworker implements the notebook worker agent.
// Run one of these on each node that should execute Jupyter notebook servers.
// It connects to the master via gRPC and handles notebook lifecycle commands.
package notebookworker

import (
	"context"
	"fmt"
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

	return w
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

type failingRuntime struct{ err error }

func (r *failingRuntime) Start(context.Context, RuntimeStartRequest) (*StartedNotebook, error) {
	return nil, r.err
}
func (r *failingRuntime) Stop(context.Context, string) error { return r.err }
func (r *failingRuntime) KillAll(context.Context) error      { return r.err }
func (r *failingRuntime) Status(string) string               { return notebook.StatusStopped }

// Run connects to the master via gRPC and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
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

	go func() {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			slog.Error("notebook worker: cannot create work dir", "name", name, "err", err)
			w.pushStatus(name, notebook.StatusFailed, "", workDir, "", 0, "")
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
				nb := w.notebooks[name]
				delete(w.notebooks, name)
				w.mu.Unlock()
				if nb != nil {
					w.releasePort(nb.port)
				}
				w.pushStatus(name, status, "", "", "", 0, "")
			},
		})
		if err != nil {
			slog.Error("notebook worker: start failed", "name", name, "err", err)
			w.pushStatus(name, notebook.StatusFailed, "", workDir, "", 0, "")
			w.releasePort(port)
			return
		}

		w.mu.Lock()
		w.notebooks[name] = &localNotebook{name: name, port: port}
		w.mu.Unlock()

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
	if ok {
		delete(w.notebooks, name)
	}
	w.mu.Unlock()

	if !ok {
		return nil
	}
	w.releasePort(nb.port)
	return w.runtime.Stop(context.Background(), nb.name)
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

func (w *Worker) syncStatus(_ context.Context, req notebook.WorkerSyncStatusRequest) (notebook.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Names))
	for _, name := range req.Names {
		statuses[name] = w.runtime.Status(name)
	}
	return notebook.WorkerSyncStatusResponse{Statuses: statuses}, nil
}

func (w *Worker) pushStatus(name, status, endpoint, workDir, token string, pid int, env string) {
	type payload struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Endpoint string `json:"endpoint,omitempty"`
		WorkDir  string `json:"work_dir,omitempty"`
		Token    string `json:"token,omitempty"`
		PID      int    `json:"pid,omitempty"`
		Env      string `json:"env,omitempty"`
	}
	if err := w.client.SendPush(iagent.MethodNotebookStatusUpdate, payload{
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
