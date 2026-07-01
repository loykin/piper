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
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/notebook"
	notebookdriver "github.com/piper/piper/pkg/notebook/worker/driver"
	notebookdocker "github.com/piper/piper/pkg/notebook/worker/driver/docker"
	notebookprocess "github.com/piper/piper/pkg/notebook/worker/driver/process"
)

// Config holds configuration for a notebook worker agent.
type Config struct {
	MasterURL      string // single HTTP(S) endpoint for the outbound master tunnel
	WorkerToken    string // bearer token sent in gRPC authorization metadata (matches server.worker_token)
	NotebooksRoot  string // base directory for notebook work dirs (default: "./notebooks")
	PortRange      string // "START-END", e.g. "8888-9900"
	Infrastructure string // baremetal | docker
	Docker         DockerConfig
	Hostname       string
	ID             string // stable worker identity assigned by the caller
	Labels         map[string]string
}

// Worker is the notebook worker agent.
type Worker struct {
	cfg           Config
	driver        notebookdriver.Driver
	client        *grpcagent.Client
	portAllocator func() (int, error)
	mu            sync.Mutex
	notebooks     map[string]*localNotebook
	reservedPorts map[int]struct{}
	terminal      map[string]string
	nextGen       uint64
}

type localNotebook struct {
	projectID string // composite key prefix; empty for single-tenant workers
	name      string
	port      int
	gen       uint64 // incremented on each start to distinguish stale OnExit callbacks
}

// notebookKey returns the composite map key "projectID:name" used in in-memory maps.
func notebookKey(projectID, name string) string { return projectID + ":" + name }

// runtimeName returns a name safe for process supervisors and Docker containers.
// Uses "__" separator since ":" is invalid in many runtime contexts.
func runtimeName(projectID, name string) string {
	if projectID == "" {
		return name
	}
	return projectID + "__" + name
}

func logRuntimeStart(mode, name, workDir string, port int) {
	slog.Info("notebook runtime starting", "mode", mode, "name", name, "work_dir", workDir, "port", port)
}

// New creates a new Worker.
func New(cfg Config) *Worker {
	drv, err := newDriver(cfg)
	if err != nil {
		drv = &failingDriver{err: err}
	}

	client := grpcagent.NewClient(grpcagent.ClientConfig{
		MasterURL:      cfg.MasterURL,
		AgentID:        cfg.ID,
		WorkerToken:    cfg.WorkerToken,
		Infrastructure: cfg.Infrastructure,
		Hostname:       cfg.Hostname,
		Capabilities:   []string{iagent.CapabilityNotebook},
		Labels:         cfg.Labels,
	})

	w := &Worker{
		cfg:           cfg,
		driver:        drv,
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
		return nil, w.stopNotebook(ctx, req)
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookDeprovision, func(ctx context.Context, req notebook.WorkerDeprovisionVolumeRequest) (any, error) {
		return nil, w.deprovisionVolume(ctx, req)
	})
	_ = grpcagent.RegisterJSON(d, iagent.MethodNotebookSyncStatus, w.syncStatus)
	_ = grpcagent.RegisterJSON(d, iagent.MethodFSListFiles, w.listFiles)

	return w
}

func newDriver(cfg Config) (notebookdriver.Driver, error) {
	switch cfg.Infrastructure {
	case InfrastructureBaremetal:
		return notebookprocess.New(cfg.NotebooksRoot), nil
	case InfrastructureDocker:
		return notebookdocker.New(cfg.Docker, cfg.ID)
	default:
		return nil, fmt.Errorf("unsupported notebook worker infrastructure %q", cfg.Infrastructure)
	}
}

type failingDriver struct{ err error }

func (r *failingDriver) Start(context.Context, notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	return nil, r.err
}
func (r *failingDriver) Stop(context.Context, string) error    { return r.err }
func (r *failingDriver) KillAll(context.Context) error         { return r.err }
func (r *failingDriver) Status(context.Context, string) string { return notebook.StatusStopped }

// Run connects to the master via gRPC and serves until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.recoverContainers(ctx)
	defer func() {
		if err := w.driver.KillAll(context.Background()); err != nil {
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
	if req.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	var spec notebook.Notebook
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	name := spec.Metadata.Name
	if name == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}
	key := notebookKey(req.ProjectID, name)

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
	baseURL := fmt.Sprintf("/projects/%s/notebooks/%s/proxy/", req.ProjectID, name)
	target := fmt.Sprintf("127.0.0.1:%d", port)

	// Register the notebook entry before starting so that an OnExit callback that
	// fires before runtime.Start returns cannot race with the post-start registration.
	w.mu.Lock()
	if _, exists := w.notebooks[key]; exists {
		w.mu.Unlock()
		w.releasePort(port)
		return nil, fmt.Errorf("notebook %q is already active", name)
	}
	w.nextGen++
	gen := w.nextGen
	delete(w.terminal, key)
	w.notebooks[key] = &localNotebook{projectID: req.ProjectID, name: name, port: port, gen: gen}
	w.mu.Unlock()

	go func() {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			slog.Error("notebook worker: cannot create work dir", "name", name, "err", err)
			w.mu.Lock()
			current := w.notebooks[key] != nil && w.notebooks[key].gen == gen
			if current {
				delete(w.notebooks, key)
				w.terminal[key] = notebook.StatusFailed
			}
			w.mu.Unlock()
			w.releasePort(port)
			if current {
				w.pushStatus(req.ProjectID, name, notebook.StatusFailed, "", workDir, "", 0, "")
			}
			return
		}

		runtime := notebookdriver.ModeProcess
		if w.cfg.Infrastructure == InfrastructureDocker {
			runtime = notebookdriver.ModeDocker
		}
		// rn is the runtime-level name. It uses "__" as separator (not ":") because
		// process supervisors and Docker container names forbid colons.
		rn := runtimeName(req.ProjectID, name)
		logRuntimeStart(runtime, rn, workDir, port)
		// Create the sink before Start so we can stop it on start error.
		// The runtime's OnExit wrapper calls sink.Stop() on process exit.
		extraEnv := buildNotebookEnv(spec.Spec.Options.Env, req.Env)
		nbSink := logsink.NewRedactingSink(logsink.NewGRPCLogSink(req.ProjectID, w.client), logsink.ValuesFromEnv(req.Env))
		started, err := w.driver.Start(context.Background(), notebookdriver.StartRequest{
			RuntimeName: rn,
			ProjectID:   req.ProjectID,
			Name:        name,
			Spec:        spec,
			WorkDir:     workDir,
			Port:        port,
			Token:       token,
			BaseURL:     baseURL,
			ExtraEnv:    extraEnv,
			LogSink:     nbSink,
			OnExit: func(status string) {
				slog.Info("notebook runtime exited", "name", name, "status", status)
				w.mu.Lock()
				nb := w.notebooks[key]
				current := nb != nil && nb.gen == gen
				if current {
					delete(w.notebooks, key)
					w.terminal[key] = status
				}
				w.mu.Unlock()
				w.releasePort(port)
				if current {
					w.pushStatus(req.ProjectID, name, status, "", "", "", 0, "")
				}
			},
		})
		if err != nil {
			nbSink.Stop()
			slog.Error("notebook worker: start failed", "name", name, "err", err)
			w.mu.Lock()
			current := w.notebooks[key] != nil && w.notebooks[key].gen == gen
			if current {
				delete(w.notebooks, key)
				w.terminal[key] = notebook.StatusFailed
			}
			w.mu.Unlock()
			w.releasePort(port)
			if current {
				w.pushStatus(req.ProjectID, name, notebook.StatusFailed, "", workDir, "", 0, "")
			}
			return
		}

		// Guard against a fast exit that already fired OnExit before we get here.
		w.mu.Lock()
		nb := w.notebooks[key]
		w.mu.Unlock()
		if nb == nil || nb.gen != gen {
			return
		}

		endpoint := fmt.Sprintf("tunnel://%s?target=%s", w.cfg.ID, target)
		statusToken := started.Token
		if statusToken == "" {
			statusToken = token
		}

		w.pushStatus(req.ProjectID, name, notebook.StatusRunning, endpoint, workDir, statusToken, started.PID, started.EnvPath)
	}()

	return &notebook.WorkerStartResponse{
		Token:    token,
		WorkDir:  workDir,
		Endpoint: fmt.Sprintf("tunnel://%s?target=%s", w.cfg.ID, target),
	}, nil
}

func (w *Worker) stopNotebook(_ context.Context, req notebook.WorkerStopRequest) error {
	if req.ProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	name := req.Name
	key := notebookKey(req.ProjectID, name)

	w.mu.Lock()
	nb, ok := w.notebooks[key]
	w.mu.Unlock()

	if !ok {
		// Best-effort stop: the notebook may be running but not in our map after
		// a recovery miss. Ignore the error — if it's already stopped, Stop is a no-op.
		_ = w.driver.Stop(context.Background(), runtimeName(req.ProjectID, name))
		return nil
	}

	if err := w.driver.Stop(context.Background(), runtimeName(nb.projectID, nb.name)); err != nil {
		return err
	}

	w.mu.Lock()
	current := w.notebooks[key] != nil && w.notebooks[key].gen == nb.gen
	if current {
		delete(w.notebooks, key)
		w.terminal[key] = notebook.StatusStopped
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
		return &notebook.FSListFilesResponse{Files: []string{}, State: notebook.FSAccessReady}, nil
	}
	subPath := req.Path
	walkRoot := root
	if subPath != "" {
		if strings.Contains(subPath, "..") {
			return notebook.ReadyResponse([]string{}, false), nil
		}
		walkRoot = filepath.Join(root, filepath.FromSlash(filepath.Clean("/"+subPath)))
	}
	files, truncated := notebook.WalkFiles(walkRoot, req.Ext, req.MaxFiles)
	if subPath != "" {
		prefix := filepath.ToSlash(filepath.Clean(subPath))
		for i, f := range files {
			files[i] = prefix + "/" + f
		}
	}
	return notebook.ReadyResponse(files, truncated), nil
}

func (w *Worker) syncStatus(_ context.Context, req notebook.WorkerSyncStatusRequest) (notebook.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Targets))
	for _, target := range req.Targets {
		name := target.Name
		key := notebookKey(target.ProjectID, name)

		w.mu.Lock()
		terminal := w.terminal[key]
		activeNotebook := w.notebooks[key]
		active := activeNotebook != nil
		if active && activeNotebook.port == 0 && target.Port > 0 {
			activeNotebook.port = target.Port
			w.reservedPorts[target.Port] = struct{}{}
		}
		w.mu.Unlock()
		if terminal != "" {
			statuses[key] = terminal
			continue
		}

		statuses[key] = w.driver.Status(context.Background(), runtimeName(target.ProjectID, name))
	}
	return notebook.WorkerSyncStatusResponse{Statuses: statuses}, nil
}

func (w *Worker) pushStatus(projectID, name, status, endpoint, workDir, token string, pid int, env string) {
	if err := w.client.SendPush(iagent.MethodNotebookStatusUpdate, notebook.WorkerStatusUpdate{
		ProjectID: projectID,
		Name:      name,
		Status:    status,
		Endpoint:  endpoint,
		WorkDir:   workDir,
		Token:     token,
		PID:       pid,
		Env:       env,
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

// recoverContainers reconnects to workloads running from a previous worker
// instance. Drivers that implement notebookdriver.Recoverable perform their own
// native discovery (PID files, container labels, K8s label selectors).
func (w *Worker) recoverContainers(ctx context.Context) {
	recoverable, ok := w.driver.(notebookdriver.Recoverable)
	if !ok {
		return
	}
	onRecovered := func(rec notebookdriver.RecoveredHandle) func(status string) {
		key := notebookKey(rec.ProjectID, rec.Name)
		w.mu.Lock()
		w.nextGen++
		gen := w.nextGen
		delete(w.terminal, key)
		w.notebooks[key] = &localNotebook{projectID: rec.ProjectID, name: rec.Name, port: rec.Port, gen: gen}
		if rec.Port > 0 {
			w.reservedPorts[rec.Port] = struct{}{}
		}
		w.mu.Unlock()

		return func(status string) {
			w.mu.Lock()
			nb := w.notebooks[key]
			current := nb != nil && nb.gen == gen
			if current {
				delete(w.notebooks, key)
				w.terminal[key] = status
			}
			w.mu.Unlock()
			w.releasePort(rec.Port)
			if current {
				w.pushStatus(rec.ProjectID, rec.Name, status, "", "", "", 0, "")
			}
		}
	}
	onTerminal := func(rec notebookdriver.RecoveredHandle, status string) {
		key := notebookKey(rec.ProjectID, rec.Name)
		w.mu.Lock()
		_, active := w.notebooks[key]
		if !active {
			w.terminal[key] = status
		}
		w.mu.Unlock()
		if !active {
			w.pushStatus(rec.ProjectID, rec.Name, status, "", "", "", 0, "")
		}
	}

	if err := recoverable.Recover(ctx, onRecovered, onTerminal); err != nil {
		slog.Warn("notebook worker: driver recovery failed", "err", err)
		return
	}
}

// buildNotebookEnv merges plain SpecOptions.Env entries with pre-resolved
// secret env vars received from the master ("KEY=value" strings).
// Secret entries from resolvedEnv take precedence over plain values.
func buildNotebookEnv(optionsEnv []manifest.EnvVar, resolvedEnv []string) []string {
	out := make([]string, 0, len(optionsEnv)+len(resolvedEnv))
	for _, e := range optionsEnv {
		if e.ValueFrom != nil {
			continue // resolved by master; skip placeholder
		}
		if e.Name != "" && e.Value != "" {
			out = append(out, e.Name+"="+e.Value)
		}
	}
	out = append(out, resolvedEnv...)
	return out
}
