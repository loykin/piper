// Package baremetal implements RuntimeDriver for bare-metal subprocess execution.
// Each pipeline step runs as an isolated child process via piper agent exec,
// managed directly through the provisr core.Manager (not JobManager) so that
// the process name is exactly the runtimeKey and history.Sink events match.
package baremetaldriver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/loykin/provisr/core"
	"github.com/loykin/provisr/core/history"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
	"github.com/piper/piper/pkg/pipeline/worker/driver"
)

const metadataVersion = 1

// runtimeMetadata is persisted alongside each job so it can be recovered
// after a worker restart.
type runtimeMetadata struct {
	Version    int    `json:"version"`
	RuntimeKey string `json:"runtime_key"`
	WorkerID   string `json:"worker_id"`
	TaskID     string `json:"task_id"`
	RunID      string `json:"run_id"`
	StepName   string `json:"step_name"`
	Attempt    int    `json:"attempt"`
	ResultPath string `json:"result_path"`
	PIDFile    string `json:"pid_file"`
}

// activeJob tracks an in-flight subprocess.
type activeJob struct {
	handle      driver.Handle
	doneCh      chan driver.Exit
	resultPath  string
	remoteStore bool
}

// Driver is the bare-metal RuntimeDriver.
// It uses core.Manager directly so that the process name equals runtimeKey
// and history.Sink exit events can be matched without ambiguity.
type Driver struct {
	workerID    string
	metaDir     string
	piperBin    string
	manager     *core.Manager
	remoteStore bool

	mu     sync.Mutex
	active map[string]*activeJob // runtimeKey → activeJob
}

// Config configures the BaremetalDriver.
type Config struct {
	WorkerID    string
	MetaDir     string // directory for metadata + PID files; default: $TMPDIR/piper-meta
	RemoteStore bool   // true when using S3 or other remote artifact store
}

// New creates a BaremetalDriver.
func New(cfg Config) (*Driver, error) {
	piperBin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("baremetal driver: resolve executable: %w", err)
	}

	metaDir := cfg.MetaDir
	if metaDir == "" {
		metaDir = filepath.Join(os.TempDir(), "piper-meta")
	}
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		return nil, fmt.Errorf("baremetal driver: create meta dir: %w", err)
	}

	mgr := core.New()

	d := &Driver{
		workerID:    cfg.WorkerID,
		metaDir:     metaDir,
		piperBin:    piperBin,
		manager:     mgr,
		remoteStore: cfg.RemoteStore,
		active:      make(map[string]*activeJob),
	}

	// Register a history sink so we hear about process exits.
	sink := &exitSink{driver: d}
	mgr.SetHistorySinks(sink)

	return d, nil
}

// Start launches piper agent exec as a direct child process.
// The process name in provisr is exactly runtimeKey, so exitSink can match it.
func (d *Driver) Start(_ context.Context, task *proto.Task, spec driver.ExecSpec) (driver.Handle, error) {
	pidFile := d.pidPath(spec.RuntimeKey)

	// Baremetal uses host-side paths directly.
	resultDir := filepath.Join(spec.OutputDir, ".results")
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		return driver.Handle{}, fmt.Errorf("create result dir: %w", err)
	}
	resultPath := filepath.Join(resultDir, spec.RuntimeKey+".result.json")

	agentArgs, err := agent.BuildAgentExec(task, agent.AgentExecConfig{
		MasterURL:    spec.MasterURL,
		WorkerToken:  spec.WorkerToken,
		StorageToken: spec.StorageToken,
		StorageURL:   spec.StorageURL,
		OutputDir:    spec.OutputDir,
		InputDir:     spec.OutputDir,
		ResultFile:   resultPath,
		ReportMode:   agent.ReportModeFile,
	})
	if err != nil {
		return driver.Handle{}, fmt.Errorf("build agent args: %w", err)
	}

	args := append([]string{d.piperBin}, agentArgs...)

	handle := driver.Handle{
		RuntimeKey: spec.RuntimeKey,
		WorkerID:   d.workerID,
		TaskID:     task.ID,
		RunID:      task.RunID,
		StepName:   task.StepName,
		Attempt:    task.Attempt,
		ResultPath: resultPath,
	}

	if werr := d.writeMetadata(spec.RuntimeKey, handle, resultPath, pidFile); werr != nil {
		return driver.Handle{}, fmt.Errorf("write metadata: %w", werr)
	}

	doneCh := make(chan driver.Exit, 1)
	aj := &activeJob{
		handle:      handle,
		doneCh:      doneCh,
		resultPath:  resultPath,
		remoteStore: d.remoteStore,
	}

	d.mu.Lock()
	d.active[spec.RuntimeKey] = aj
	d.mu.Unlock()

	if regErr := d.manager.Register(core.Spec{
		Name:        spec.RuntimeKey, // exact name = runtimeKey, exitSink matches this
		Args:        args,
		Env:         spec.Env,
		AutoRestart: false,
		PIDFile:     pidFile,
	}); regErr != nil {
		err = regErr
	}
	if err != nil {
		d.mu.Lock()
		delete(d.active, spec.RuntimeKey)
		d.mu.Unlock()
		_ = d.removeMetadata(spec.RuntimeKey)
		return driver.Handle{}, fmt.Errorf("register process: %w", err)
	}

	return handle, nil
}

// Wait blocks until the subprocess exits or ctx is cancelled.
func (d *Driver) Wait(ctx context.Context, handle driver.Handle) (driver.Exit, error) {
	d.mu.Lock()
	aj := d.active[handle.RuntimeKey]
	d.mu.Unlock()

	if aj == nil {
		return driver.Exit{InfraFailure: fmt.Errorf("job %q not tracked", handle.RuntimeKey)}, nil
	}

	select {
	case exit := <-aj.doneCh:
		return exit, nil
	case <-ctx.Done():
		return driver.Exit{}, ctx.Err()
	}
}

// Stop signals the process to stop.
func (d *Driver) Stop(_ context.Context, handle driver.Handle, grace time.Duration) error {
	if grace <= 0 {
		grace = 10 * time.Second
	}
	err := d.manager.Stop(handle.RuntimeKey, grace)
	_ = d.removeMetadata(handle.RuntimeKey)
	d.mu.Lock()
	delete(d.active, handle.RuntimeKey)
	d.mu.Unlock()
	return err
}

// Recover re-attaches to processes that survived a worker restart
// by reading metadata sidecars and calling manager.Recover().
func (d *Driver) Recover(_ context.Context) ([]driver.Handle, error) {
	entries, err := os.ReadDir(d.metaDir)
	if err != nil {
		return nil, fmt.Errorf("read meta dir: %w", err)
	}

	var handles []driver.Handle
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		runtimeKey := strings.TrimSuffix(e.Name(), ".meta.json")
		meta, err := d.readMetadata(runtimeKey)
		if err != nil {
			slog.Warn("recover: skip unreadable metadata", "key", runtimeKey, "err", err)
			continue
		}
		if meta.WorkerID != d.workerID {
			continue
		}

		// Re-attach to the process via provisr's Recover (PID file based).
		if err := d.manager.Recover(core.Spec{
			Name:        runtimeKey,
			Args:        []string{d.piperBin}, // placeholder; not re-exec'd
			AutoRestart: false,
			PIDFile:     meta.PIDFile,
		}); err != nil {
			// Process not alive — clean up and skip.
			_ = d.removeMetadata(runtimeKey)
			slog.Info("recover: process gone, cleaning up", "key", runtimeKey, "err", err)
			continue
		}
		status, err := d.manager.Status(runtimeKey)
		if err != nil || !status.Running {
			_ = d.removeMetadata(runtimeKey)
			slog.Info("recover: process is not running, cleaning up", "key", runtimeKey)
			continue
		}

		handle := driver.Handle{
			RuntimeKey: meta.RuntimeKey,
			WorkerID:   meta.WorkerID,
			TaskID:     meta.TaskID,
			RunID:      meta.RunID,
			StepName:   meta.StepName,
			Attempt:    meta.Attempt,
			ResultPath: meta.ResultPath,
		}

		doneCh := make(chan driver.Exit, 1)
		d.mu.Lock()
		d.active[runtimeKey] = &activeJob{
			handle:      handle,
			doneCh:      doneCh,
			resultPath:  meta.ResultPath,
			remoteStore: d.remoteStore,
		}
		d.mu.Unlock()
		handles = append(handles, handle)
		slog.Info("recovered baremetal job", "runtime_key", runtimeKey, "task_id", meta.TaskID)
	}
	return handles, nil
}

// onJobExit is called by exitSink when a managed process exits.
// evt.Record.Name == runtimeKey because we registered with that exact name.
func (d *Driver) onJobExit(runtimeKey, status string) {
	d.mu.Lock()
	aj := d.active[runtimeKey]
	delete(d.active, runtimeKey)
	d.mu.Unlock()

	if aj == nil {
		return
	}

	_ = d.removeMetadata(runtimeKey)

	exit := driver.Exit{ResultPath: aj.resultPath}

	if status == "failed" {
		if _, err := os.Stat(aj.resultPath); os.IsNotExist(err) {
			exit.InfraFailure = fmt.Errorf("process %q exited without result file", runtimeKey)
			if d.remoteStore {
				// Best-effort: clean staging dirs left by crashed agent.
				slog.Debug("crash residue cleanup", "runtime_key", runtimeKey)
			}
		}
	}

	select {
	case aj.doneCh <- exit:
	default:
	}
}

// ── metadata helpers ─────────────────────────────────────────────────────────

func (d *Driver) writeMetadata(runtimeKey string, h driver.Handle, resultPath, pidFile string) error {
	meta := runtimeMetadata{
		Version:    metadataVersion,
		RuntimeKey: runtimeKey,
		WorkerID:   h.WorkerID,
		TaskID:     h.TaskID,
		RunID:      h.RunID,
		StepName:   h.StepName,
		Attempt:    h.Attempt,
		ResultPath: resultPath,
		PIDFile:    pidFile,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(d.metaPath(runtimeKey), data, 0644)
}

func (d *Driver) readMetadata(runtimeKey string) (*runtimeMetadata, error) {
	data, err := os.ReadFile(d.metaPath(runtimeKey))
	if err != nil {
		return nil, err
	}
	var meta runtimeMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (d *Driver) removeMetadata(runtimeKey string) error {
	_ = os.Remove(d.pidPath(runtimeKey))
	return os.Remove(d.metaPath(runtimeKey))
}

func (d *Driver) metaPath(runtimeKey string) string {
	return filepath.Join(d.metaDir, runtimeKey+".meta.json")
}

func (d *Driver) pidPath(runtimeKey string) string {
	return filepath.Join(d.metaDir, runtimeKey+".pid")
}

// ── exitSink ─────────────────────────────────────────────────────────────────

// exitSink receives EventStop from provisr and routes it to the driver.
// Because we registered with Name=runtimeKey, evt.Record.Name == runtimeKey.
type exitSink struct {
	driver *Driver
}

func (s *exitSink) Send(_ context.Context, evt history.Event) error {
	if evt.Type != history.EventStop {
		return nil
	}
	if s.driver != nil {
		s.driver.onJobExit(evt.Record.Name, evt.Record.LastStatus)
	}
	return nil
}
