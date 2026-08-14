// Package localdriver implements notebook.Driver directly in-process for
// docker and baremetal (process) runtimes — no remote worker/tunnel
// involved, mirroring fed.md §13.2's Pipeline direct-runtime treatment.
//
// K8s is implemented by the sibling localdriver/k8s package at the higher
// notebook.Driver boundary because StatefulSet/PVC lifecycle does not fit
// the docker/process driver's process-like contract.
package localdriver

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

	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/notebook"
	notebookdriver "github.com/loykin/piper/pkg/notebook/worker/driver"
	notebookdocker "github.com/loykin/piper/pkg/notebook/worker/driver/docker"
	notebookprocess "github.com/loykin/piper/pkg/notebook/worker/driver/process"
)

// EnvResolver resolves manifest.EnvVar entries (including credentialRef)
// into "KEY=value" strings. Mirrors pkg/notebook/dispatch.EnvResolver,
// redefined locally so this package has no dependency on the remote-dispatch
// package.
type EnvResolver func(ctx context.Context, projectID string, env []manifest.EnvVar) ([]string, error)

// Config configures a direct, in-process notebook driver.
type Config struct {
	// WorkerID is a fixed local identity used to populate NotebookServer/
	// NotebookVolume.WorkerID. A real per-worker ID is meaningless once
	// dispatch is in-process (there is only ever one owner), but the field
	// stays populated so Manager's existing ownership-check code
	// (UpdateStatus comparing nb.WorkerID) keeps working unchanged.
	WorkerID string
	// Infrastructure selects the underlying notebookdriver.Driver: must be
	// notebookworkerdriver.ModeDocker or notebookworkerdriver.ModeProcess
	// ("docker"/"process") — not the same string space as PlacementRuntime.
	Infrastructure string
	// PlacementRuntime is the runtime.type value this driver instance owns
	// ("docker" or "baremetal", matching driver.placement.runtime in
	// manifests) — used only for ValidateDirectPlacement, kept separate
	// from Infrastructure because that field's "baremetal" case uses the
	// low-level driver mode string "process", not "baremetal".
	PlacementRuntime string
	Docker           notebookdocker.Config
	NotebooksRoot    string
	PortRange        string
	LogClient        logsink.PushClient
	// EnvResolver expands credentialRef entries in spec.Options.Env. Optional.
	EnvResolver EnvResolver
	// ReportStatus delivers an async status update once a backgrounded
	// Start actually completes or fails — mirrors
	// pkg/notebook/worker/worker.go's pushStatus, called in-process instead
	// of over a tunnel. Required.
	ReportStatus func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error
}

type activeNotebook struct {
	projectID string
	name      string
	port      int
	gen       uint64 // distinguishes stale OnExit callbacks from a superseded start
}

// Driver implements notebook.Driver directly against a local
// notebookdriver.Driver (docker or process). Call Recover once at startup
// to reattach to notebooks that survived a restart.
type Driver struct {
	cfg    Config
	driver notebookdriver.Driver

	mu            sync.Mutex
	notebooks     map[string]*activeNotebook // "projectID:name" → active
	reservedPorts map[int]struct{}
	terminal      map[string]string
	nextGen       uint64
}

// New constructs a Driver. cfg.ReportStatus is required.
func New(cfg Config) (*Driver, error) {
	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("localdriver: WorkerID is required")
	}
	if cfg.ReportStatus == nil {
		return nil, fmt.Errorf("localdriver: ReportStatus is required")
	}
	var drv notebookdriver.Driver
	switch cfg.Infrastructure {
	case notebookdriver.ModeDocker:
		d, err := notebookdocker.New(cfg.Docker, cfg.WorkerID)
		if err != nil {
			return nil, fmt.Errorf("localdriver: docker driver: %w", err)
		}
		drv = d
	case notebookdriver.ModeProcess:
		drv = notebookprocess.New(cfg.NotebooksRoot)
	default:
		return nil, fmt.Errorf("localdriver: unsupported infrastructure %q", cfg.Infrastructure)
	}
	return &Driver{
		cfg:           cfg,
		driver:        drv,
		notebooks:     make(map[string]*activeNotebook),
		reservedPorts: make(map[int]struct{}),
		terminal:      make(map[string]string),
	}, nil
}

func notebookKey(projectID, name string) string { return projectID + ":" + name }

// runtimeName returns a name safe for process supervisors and Docker
// containers. Uses "__" separator since ":" is invalid in many runtime contexts.
func runtimeName(projectID, name string) string {
	if projectID == "" {
		return name
	}
	return projectID + "__" + name
}

func (d *Driver) notebooksRoot() string {
	if d.cfg.NotebooksRoot != "" {
		return d.cfg.NotebooksRoot
	}
	return "notebooks"
}

func (d *Driver) volumeDir(volumeID string) string {
	abs, _ := filepath.Abs(d.notebooksRoot())
	return filepath.Join(abs, volumeID)
}

// ProvisionVolume creates the host work directory backing vol.
//
// vol.WorkerID is deliberately left empty rather than set to cfg.WorkerID.
// pkg/notebook/handler.go's listVolumeFiles and pkg/template/handler.go's
// Direct-runtime volumes are owned by the Piper installation rather than by
// a separately addressable worker, so no worker identity is persisted. File
// access is selected through the configured WorkspaceReader.
func (d *Driver) ProvisionVolume(_ context.Context, vol *notebook.NotebookVolume, _ notebook.Notebook) error {
	if vol.ID == "" {
		return fmt.Errorf("localdriver: volume id is required")
	}
	dir := d.volumeDir(vol.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("localdriver: create volume dir: %w", err)
	}
	vol.WorkDir = dir
	slog.Info("notebook volume provisioned", "volume_id", vol.ID, "dir", dir)
	return nil
}

// DeprovisionVolume removes the host work directory backing vol.
func (d *Driver) DeprovisionVolume(_ context.Context, vol *notebook.NotebookVolume) error {
	if vol.ID == "" {
		return nil
	}
	dir := d.volumeDir(vol.ID)
	root := d.notebooksRoot()
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
	slog.Info("notebook volume deleted", "volume_id", vol.ID, "dir", absDir)
	return nil
}

// Start reserves a port and registers the notebook as starting, then
// launches the runtime in the background — Manager already runs Start
// itself inside a goroutine and only trusts a later status update (not
// Start's return value) to mark a notebook "running", so this mirrors the
// exact two-phase shape pkg/notebook/worker/worker.go's startNotebook used
// over the tunnel: return fast with placeholder info, report the real
// outcome async via cfg.ReportStatus.
func (d *Driver) Start(_ context.Context, spec notebook.Notebook, vol *notebook.NotebookVolume, yamlStr string) (*notebook.NotebookServer, error) {
	projectID := spec.Metadata.ProjectID
	name := spec.Metadata.Name
	if projectID == "" {
		return nil, fmt.Errorf("localdriver: project_id is required")
	}
	if name == "" {
		return nil, fmt.Errorf("localdriver: metadata.name is required")
	}
	if err := notebook.ValidateDirectPlacement(spec, d.cfg.PlacementRuntime); err != nil {
		return nil, fmt.Errorf("localdriver: %w", err)
	}
	key := notebookKey(projectID, name)

	port, err := d.allocatePort()
	if err != nil {
		return nil, err
	}

	workDir := ""
	if vol != nil {
		workDir = vol.WorkDir
	}
	if workDir == "" {
		if vol == nil || vol.ID == "" {
			d.releasePort(port)
			return nil, fmt.Errorf("localdriver: volume is required when work_dir is empty")
		}
		workDir = d.volumeDir(vol.ID)
	}

	token := uuid.New().String()
	baseURL := fmt.Sprintf("/projects/%s/notebooks/%s/proxy/", projectID, name)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Register before starting so a fast-exit OnExit callback cannot race
	// the registration below.
	d.mu.Lock()
	if _, exists := d.notebooks[key]; exists {
		d.mu.Unlock()
		d.releasePort(port)
		return nil, fmt.Errorf("notebook %q is already active", name)
	}
	d.nextGen++
	gen := d.nextGen
	delete(d.terminal, key)
	d.notebooks[key] = &activeNotebook{projectID: projectID, name: name, port: port, gen: gen}
	d.mu.Unlock()

	go d.startAsync(spec, projectID, name, key, gen, workDir, port, token, baseURL, endpoint, yamlStr)

	return &notebook.NotebookServer{
		WorkerID: d.cfg.WorkerID,
		Token:    token,
		WorkDir:  workDir,
		Endpoint: endpoint,
	}, nil
}

func (d *Driver) startAsync(spec notebook.Notebook, projectID, name, key string, gen uint64, workDir string, port int, token, baseURL, endpoint, _ string) {
	if err := os.MkdirAll(workDir, 0755); err != nil {
		slog.Error("localdriver: cannot create work dir", "name", name, "err", err)
		d.failStart(key, gen, port, projectID, name, workDir)
		return
	}

	extraEnv := buildNotebookEnv(spec.Spec.Options.Env, nil)
	if d.cfg.EnvResolver != nil && len(spec.Spec.Options.Env) > 0 {
		resolved, err := d.cfg.EnvResolver(context.Background(), projectID, spec.Spec.Options.Env)
		if err != nil {
			slog.Error("localdriver: env resolution failed", "name", name, "err", err)
			d.failStart(key, gen, port, projectID, name, workDir)
			return
		}
		extraEnv = buildNotebookEnv(spec.Spec.Options.Env, resolved)
	}

	rn := runtimeName(projectID, name)
	logRuntimeStart(d.cfg.Infrastructure, rn, workDir, port)
	var nbSink logsink.LogSink
	if d.cfg.LogClient != nil {
		nbSink = logsink.NewRedactingSink(logsink.NewBufferedLogSink(projectID, d.cfg.LogClient), logsink.ValuesFromEnv(extraEnv))
	}

	started, err := d.driver.Start(context.Background(), notebookdriver.StartRequest{
		RuntimeName: rn,
		ProjectID:   projectID,
		Name:        name,
		Spec:        spec,
		WorkDir:     workDir,
		Port:        port,
		Token:       token,
		BaseURL:     baseURL,
		ExtraEnv:    extraEnv,
		LogSink:     nbSink,
		OnExit: func(status string) {
			d.mu.Lock()
			nb := d.notebooks[key]
			current := nb != nil && nb.gen == gen
			if current {
				delete(d.notebooks, key)
				d.terminal[key] = status
			}
			d.mu.Unlock()
			d.releasePort(port)
			if current {
				d.report(projectID, name, status, "", "", "", 0, "")
			}
		},
	})
	if err != nil {
		if nbSink != nil {
			nbSink.Stop()
		}
		slog.Error("localdriver: start failed", "name", name, "err", err)
		d.failStart(key, gen, port, projectID, name, workDir)
		return
	}

	// Guard against a fast exit that already fired OnExit before we get here.
	d.mu.Lock()
	nb := d.notebooks[key]
	stillCurrent := nb != nil && nb.gen == gen
	d.mu.Unlock()
	if !stillCurrent {
		return
	}

	statusToken := started.Token
	if statusToken == "" {
		statusToken = token
	}
	d.report(projectID, name, notebook.StatusRunning, endpoint, workDir, statusToken, started.PID, started.EnvPath)
}

func (d *Driver) failStart(key string, gen uint64, port int, projectID, name, workDir string) {
	d.mu.Lock()
	current := d.notebooks[key] != nil && d.notebooks[key].gen == gen
	if current {
		delete(d.notebooks, key)
		d.terminal[key] = notebook.StatusFailed
	}
	d.mu.Unlock()
	d.releasePort(port)
	if current {
		d.report(projectID, name, notebook.StatusFailed, "", workDir, "", 0, "")
	}
}

func (d *Driver) report(projectID, name, status, endpoint, workDir, token string, pid int, env string) {
	if err := d.cfg.ReportStatus(projectID, name, status, endpoint, workDir, token, pid, env); err != nil {
		slog.Warn("localdriver: report status failed", "name", name, "status", status, "err", err)
	}
}

// Stop terminates the runtime instance without touching storage.
func (d *Driver) Stop(_ context.Context, nb *notebook.NotebookServer) error {
	key := notebookKey(nb.ProjectID, nb.Name)

	d.mu.Lock()
	active, ok := d.notebooks[key]
	d.mu.Unlock()

	if !ok {
		// Best-effort stop: it may be running but missing from our map
		// after a recovery miss. If it's already stopped, Stop is a no-op.
		_ = d.driver.Stop(context.Background(), runtimeName(nb.ProjectID, nb.Name))
		return nil
	}

	if err := d.driver.Stop(context.Background(), runtimeName(active.projectID, active.name)); err != nil {
		return err
	}

	d.mu.Lock()
	current := d.notebooks[key] != nil && d.notebooks[key].gen == active.gen
	if current {
		delete(d.notebooks, key)
		d.terminal[key] = notebook.StatusStopped
	}
	d.mu.Unlock()
	d.releasePort(active.port)
	return nil
}

// Recover reattaches to notebooks that survived a process restart. Call
// once at startup (the remote-worker path never had an in-process
// equivalent to wire since it's the same concern as any driver's Recover —
// see fed.md §13.1's already-frozen Recover contract for pipeline drivers).
func (d *Driver) Recover(ctx context.Context) error {
	recoverable, ok := d.driver.(notebookdriver.Recoverable)
	if !ok {
		return nil
	}
	onRecovered := func(rec notebookdriver.RecoveredHandle) func(status string) {
		key := notebookKey(rec.ProjectID, rec.Name)
		d.mu.Lock()
		d.nextGen++
		gen := d.nextGen
		delete(d.terminal, key)
		d.notebooks[key] = &activeNotebook{projectID: rec.ProjectID, name: rec.Name, port: rec.Port, gen: gen}
		if rec.Port > 0 {
			d.reservedPorts[rec.Port] = struct{}{}
		}
		d.mu.Unlock()

		return func(status string) {
			d.mu.Lock()
			nb := d.notebooks[key]
			current := nb != nil && nb.gen == gen
			if current {
				delete(d.notebooks, key)
				d.terminal[key] = status
			}
			d.mu.Unlock()
			d.releasePort(rec.Port)
			if current {
				d.report(rec.ProjectID, rec.Name, status, "", "", "", 0, "")
			}
		}
	}
	onTerminal := func(rec notebookdriver.RecoveredHandle, status string) {
		key := notebookKey(rec.ProjectID, rec.Name)
		d.mu.Lock()
		_, active := d.notebooks[key]
		if !active {
			d.terminal[key] = status
		}
		d.mu.Unlock()
		if !active {
			d.report(rec.ProjectID, rec.Name, status, "", "", "", 0, "")
		}
	}
	return recoverable.Recover(ctx, onRecovered, onTerminal)
}

func (d *Driver) allocatePort() (int, error) {
	portRange := d.cfg.PortRange
	if portRange == "" {
		portRange = "8888-9900"
	}
	start, end, err := parsePortRange(portRange)
	if err != nil {
		return 0, err
	}

	d.mu.Lock()
	used := make(map[int]bool, len(d.notebooks))
	for _, nb := range d.notebooks {
		used[nb.port] = true
	}
	for port := range d.reservedPorts {
		used[port] = true
	}
	d.mu.Unlock()

	for port := start; port <= end; port++ {
		if used[port] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			d.mu.Lock()
			if _, already := d.reservedPorts[port]; already {
				d.mu.Unlock()
				continue
			}
			d.reservedPorts[port] = struct{}{}
			d.mu.Unlock()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port in range %s", portRange)
}

func (d *Driver) releasePort(port int) {
	if port <= 0 {
		return
	}
	d.mu.Lock()
	delete(d.reservedPorts, port)
	d.mu.Unlock()
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

// buildNotebookEnv merges plain (non-secret) options env with already
// resolved secret env, skipping ValueFrom placeholders (those are what
// EnvResolver resolves into resolvedEnv).
func buildNotebookEnv(optionsEnv []manifest.EnvVar, resolvedEnv []string) []string {
	out := make([]string, 0, len(optionsEnv)+len(resolvedEnv))
	for _, e := range optionsEnv {
		if e.ValueFrom != nil {
			continue
		}
		if e.Name != "" && e.Value != "" {
			out = append(out, e.Name+"="+e.Value)
		}
	}
	out = append(out, resolvedEnv...)
	return out
}

func logRuntimeStart(mode, name, workDir string, port int) {
	slog.Info("notebook runtime starting", "mode", mode, "name", name, "work_dir", workDir, "port", port)
}

var _ notebook.Driver = (*Driver)(nil)
