package process

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/piper/piper/internal/process"
	"github.com/piper/piper/internal/processlog"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/notebook/worker/driver"
	driverinternal "github.com/piper/piper/pkg/notebook/worker/driver/internal"
)

// processMeta stores the workload identity alongside the PID file so that a
// restarting worker can map from a PID file back to its project and notebook name.
type processMeta struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	RuntimeName string `json:"runtime_name"`
	Port        int    `json:"port"`
}

type Driver struct {
	supervisor *process.ProcessSupervisor
	pidDir     string
	mu         sync.Mutex
	collectors map[string]func() // runtime name -> freader stop function
}

func New(notebooksRoot string) *Driver {
	if notebooksRoot == "" {
		notebooksRoot = "notebooks"
	}
	absRoot, _ := filepath.Abs(notebooksRoot)
	return &Driver{
		supervisor: process.NewProcessSupervisor(),
		pidDir:     filepath.Join(absRoot, ".piper", "processes"),
		collectors: make(map[string]func()),
	}
}

func (r *Driver) pidFile(name string) string {
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(r.pidDir, fmt.Sprintf("%x.pid", sum[:16]))
}

func (r *Driver) metaFile(name string) string {
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(r.pidDir, fmt.Sprintf("%x.meta.json", sum[:16]))
}

func (r *Driver) writeMeta(name string, meta processMeta) error {
	if err := os.MkdirAll(r.pidDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(r.metaFile(name), data, 0o644)
}

func (r *Driver) Start(_ context.Context, req driver.StartRequest) (*driver.StartedHandle, error) {
	var env, gpus string
	if req.Spec.Spec.Driver.Process != nil {
		env = req.Spec.Spec.Driver.Process.Env
		gpus = req.Spec.Spec.Driver.Process.GPUs
	}

	prepSteps, err := driverinternal.PrepareStepsForBackend(req.Spec.Spec.Prepare, notebook.PrepareBackendProcess)
	if err != nil {
		return nil, err
	}
	bin, extraArgs, envPath, err := prepareProcessEnv(env, req.WorkDir, true)
	if err != nil {
		return nil, err
	}

	var command []string
	if strings.HasPrefix(envPath, "conda:") {
		condaName := strings.TrimPrefix(envPath, "conda:")
		if condaName == "" {
			return nil, fmt.Errorf("conda env name is empty in %q", envPath)
		}
		baseCommand := append([]string{"jupyter", "lab"}, notebook.JupyterLabArgs(req.BaseURL, "", req.WorkDir, req.Port)...)
		script, err := notebook.BuildLaunchScript(nil, prepSteps, baseCommand, req.WorkDir)
		if err != nil {
			return nil, err
		}
		command = []string{bin, "run", "--no-capture-output", "-n", condaName, "sh", "-lc", script}
	} else {
		baseCommand := append([]string{bin}, extraArgs...)
		baseCommand = append(baseCommand, notebook.JupyterLabArgs(req.BaseURL, "", req.WorkDir, req.Port)...)
		script, err := notebook.BuildLaunchScript(nil, prepSteps, baseCommand, req.WorkDir)
		if err != nil {
			return nil, err
		}
		command = []string{"sh", "-lc", script}
	}

	// Write meta file for crash recovery before starting the process.
	meta := processMeta{
		ProjectID:   req.ProjectID,
		Name:        req.Name,
		RuntimeName: req.RuntimeName,
		Port:        req.Port,
	}
	if err := r.writeMeta(req.RuntimeName, meta); err != nil {
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return nil, fmt.Errorf("write process meta for %q: %w", req.RuntimeName, err)
	}

	var logFile string
	if req.LogSink != nil {
		logFile = filepath.Join(req.WorkDir, ".piper", "logs", req.RuntimeName+".log")
		if mkErr := os.MkdirAll(filepath.Dir(logFile), 0o755); mkErr != nil {
			_ = os.Remove(r.metaFile(req.RuntimeName))
			req.LogSink.Stop()
			return nil, fmt.Errorf("create log dir for %q: %w", req.RuntimeName, mkErr)
		}
		// Truncate before starting the collector. supervisor.Start also truncates
		// via '>', but there is a window between collector start and process start
		// where freader could read stale bytes from a previous run.
		f, mkErr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if mkErr != nil {
			_ = os.Remove(r.metaFile(req.RuntimeName))
			req.LogSink.Stop()
			return nil, fmt.Errorf("truncate log file for %q: %w", req.RuntimeName, mkErr)
		}
		_ = f.Close()
		// Register the collector before starting the process to eliminate the
		// race where a fast-exiting process fires OnExit before we store the
		// stop function — which would leak the collector goroutine.
		stop := processlog.StartCollector(logFile, "nb:"+req.Name, "runtime", req.LogSink)
		r.mu.Lock()
		r.collectors[req.RuntimeName] = stop
		r.mu.Unlock()
	}

	extraEnvMap := make(map[string]string, len(req.ExtraEnv))
	for _, kv := range req.ExtraEnv {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			extraEnvMap[kv[:idx]] = kv[idx+1:]
		}
	}

	metaPath := r.metaFile(req.RuntimeName)
	pid, endpoint, err := r.supervisor.Start(process.ProcessSpec{
		Name:    req.RuntimeName,
		Command: command,
		Dir:     req.WorkDir,
		Port:    req.Port,
		GPUs:    gpus,
		Env:     extraEnvMap,
		PIDFile: r.pidFile(req.RuntimeName),
		LogFile: logFile,
	}, func(status string) {
		_ = os.Remove(metaPath)
		r.mu.Lock()
		stop := r.collectors[req.RuntimeName]
		delete(r.collectors, req.RuntimeName)
		r.mu.Unlock()
		if stop != nil {
			stop()
		}
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		if req.OnExit != nil {
			req.OnExit(status)
		}
	})
	if err != nil {
		// Process failed to start: clean up the pre-registered collector, sink, and meta file.
		_ = os.Remove(metaPath)
		r.mu.Lock()
		stop := r.collectors[req.RuntimeName]
		delete(r.collectors, req.RuntimeName)
		r.mu.Unlock()
		if stop != nil {
			stop()
		}
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return nil, err
	}

	return &driver.StartedHandle{Endpoint: endpoint, PID: pid, Token: req.Token, EnvPath: envPath}, nil
}

// Recover scans the PID metadata directory for notebooks left from a previous
// worker instance. For each found workload the process liveness is validated
// using its PID file; living processes are re-attached, dead ones are reported
// as terminal so the master receives an up-to-date stopped status.
func (r *Driver) Recover(_ context.Context, onRecovered func(driver.RecoveredHandle) func(string), onTerminal func(driver.RecoveredHandle, string)) error {
	entries, err := os.ReadDir(r.pidDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		metaPath := filepath.Join(r.pidDir, entry.Name())
		data, readErr := os.ReadFile(metaPath)
		if readErr != nil {
			_ = os.Remove(metaPath)
			continue
		}
		var meta processMeta
		if unmarshalErr := json.Unmarshal(data, &meta); unmarshalErr != nil {
			_ = os.Remove(metaPath)
			continue
		}
		rec := driver.RecoveredHandle{
			ProjectID:   meta.ProjectID,
			Name:        meta.Name,
			RuntimeName: meta.RuntimeName,
			Port:        meta.Port,
		}
		var onExit func(string)
		onExitReady := make(chan struct{})
		_, running, recoverErr := r.supervisor.Recover(process.ProcessSpec{
			Name:    meta.RuntimeName,
			Port:    meta.Port,
			PIDFile: r.pidFile(meta.RuntimeName),
		}, func(status string) {
			_ = os.Remove(metaPath)
			<-onExitReady
			if onExit != nil {
				onExit(status)
			}
		})
		if recoverErr != nil || !running {
			_ = os.Remove(metaPath)
			close(onExitReady)
			onTerminal(rec, notebook.StatusStopped)
			continue
		}
		onExit = onRecovered(rec)
		close(onExitReady)
	}
	return nil
}

func (r *Driver) Stop(_ context.Context, name string) error {
	return r.supervisor.Stop(name)
}

func (r *Driver) KillAll(_ context.Context) error {
	return r.supervisor.KillAll()
}

func (r *Driver) Status(_ context.Context, name string) string {
	if status, ok := r.supervisor.Status(name); ok {
		return status
	}
	return notebook.StatusStopped
}

// prepareProcessEnv resolves or auto-creates the Python environment for a notebook.
//
//   - empty        -> auto-create venv at {workDir}/.venv, install jupyterlab/ipykernel if needed
//   - conda:name   -> use existing conda env
//   - /path/to/venv -> use existing venv, installing missing jupyterlab/ipykernel if needed
func prepareProcessEnv(specEnv, workDir string, bootstrap bool) (bin string, extraArgs []string, envPath string, err error) {
	if strings.HasPrefix(specEnv, "conda:") {
		condaName := strings.TrimPrefix(specEnv, "conda:")
		if condaName == "" {
			return "", nil, "", fmt.Errorf("conda env name is empty in %q", specEnv)
		}
		conda, err := exec.LookPath("conda")
		if err != nil {
			return "", nil, "", fmt.Errorf("conda not found in PATH")
		}
		return conda, []string{"run", "--no-capture-output", "-n", condaName, "jupyter", "lab"}, "conda:" + condaName, nil
	}

	venvPath := specEnv
	if venvPath == "" {
		venvPath = filepath.Join(workDir, ".venv")
	}

	if err := ensureVenv(venvPath, bootstrap); err != nil {
		return "", nil, "", err
	}

	for _, candidate := range []string{
		filepath.Join(venvPath, "bin", "jupyter-lab"),
		filepath.Join(venvPath, "bin", "jupyter"),
	} {
		info, statErr := os.Stat(candidate)
		if statErr != nil || info.IsDir() {
			continue
		}
		if filepath.Base(candidate) == "jupyter" {
			return candidate, []string{"lab"}, venvPath, nil
		}
		return candidate, nil, venvPath, nil
	}
	return "", nil, "", fmt.Errorf("jupyter not found in venv %q after setup", venvPath)
}

func ensureVenv(venvPath string, bootstrap bool) error {
	python := filepath.Join(venvPath, "bin", "python")
	if _, err := os.Stat(python); err != nil {
		slog.Info("creating venv", "path", venvPath)
		out, err := exec.Command("python3", "-m", "venv", venvPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("create venv %q: %w: %s", venvPath, err, strings.TrimSpace(string(out)))
		}
	}
	if !bootstrap {
		return nil
	}
	if hasVenvCommand(venvPath, "jupyter-lab") && hasPythonModule(python, "ipykernel") {
		return nil
	}
	slog.Info("installing notebook process dependencies", "venv", venvPath)
	pip := filepath.Join(venvPath, "bin", "pip")
	out, err := exec.Command(pip, "install", "--quiet", "jupyterlab", "ipykernel").CombinedOutput()
	if err != nil {
		return fmt.Errorf("install notebook dependencies in %q: %w: %s", venvPath, err, strings.TrimSpace(string(out)))
	}
	out, err = exec.Command(python, "-m", "ipykernel", "install", "--sys-prefix", "--name", "python3", "--display-name", "Python 3").CombinedOutput()
	if err != nil {
		return fmt.Errorf("install ipykernel spec in %q: %w: %s", venvPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func hasVenvCommand(venvPath, name string) bool {
	path := filepath.Join(venvPath, "bin", name)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasPythonModule(python, module string) bool {
	return exec.Command(python, "-c", "import "+module).Run() == nil
}
