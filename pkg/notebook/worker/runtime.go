package notebookworker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/loykin/freader"
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/process"
	"github.com/piper/piper/pkg/notebook"
)

const (
	RuntimeProcess = "process"
	RuntimeDocker  = "docker"
)

type Runtime interface {
	Start(ctx context.Context, req RuntimeStartRequest) (*StartedNotebook, error)
	Stop(ctx context.Context, name string) error
	KillAll(ctx context.Context) error
	Status(name string) string
}

type recoveredRuntime struct {
	ProjectID   string
	Name        string
	RuntimeName string
	Port        int
}

// recoverableRuntime is implemented only by runtimes whose external engine can
// survive a worker restart and be reattached, such as Docker.
type recoverableRuntime interface {
	Recover(
		ctx context.Context,
		onRecovered func(recoveredRuntime) func(status string),
		onTerminal func(recoveredRuntime, string),
	) error
}

type targetedRecoveryRuntime interface {
	RecoverTarget(name string, port int, onExit func(status string)) (bool, error)
}

type RuntimeStartRequest struct {
	Name         string
	ProjectID    string
	NotebookName string
	Spec         notebook.Notebook
	WorkDir      string
	Port         int
	Token        string
	BaseURL      string
	OnExit       func(status string)
	LogSink      logsink.LogSink
}

type StartedNotebook struct {
	Endpoint    string
	PID         int
	Token       string
	EnvPath     string
	ContainerID string
}

type processRuntime struct {
	supervisor *process.ProcessSupervisor
	pidDir     string
	mu         sync.Mutex
	collectors map[string]func() // runtime name -> freader stop function
}

func newProcessRuntime(notebooksRoot string) *processRuntime {
	if notebooksRoot == "" {
		notebooksRoot = "notebooks"
	}
	absRoot, _ := filepath.Abs(notebooksRoot)
	return &processRuntime{
		supervisor: process.NewProcessSupervisor(),
		pidDir:     filepath.Join(absRoot, ".piper", "processes"),
		collectors: make(map[string]func()),
	}
}

func (r *processRuntime) pidFile(name string) string {
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(r.pidDir, fmt.Sprintf("%x.pid", sum[:16]))
}

func (r *processRuntime) Start(_ context.Context, req RuntimeStartRequest) (*StartedNotebook, error) {
	var env, gpus string
	if req.Spec.Spec.Driver.Process != nil {
		env = req.Spec.Spec.Driver.Process.Env
		gpus = req.Spec.Spec.Driver.Process.GPUs
	}

	prepSteps, err := prepareStepsForBackend(req.Spec.Spec.Prepare, notebook.PrepareBackendProcess)
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

	var logFile string
	if req.LogSink != nil {
		logFile = filepath.Join(req.WorkDir, ".piper", "logs", req.Name+".log")
		if mkErr := os.MkdirAll(filepath.Dir(logFile), 0o755); mkErr != nil {
			return nil, fmt.Errorf("create log dir for %q: %w", req.Name, mkErr)
		}
		// Truncate before starting the collector. supervisor.Start also truncates
		// via '>', but there is a window between collector start and process start
		// where freader could read stale bytes from a previous run.
		f, mkErr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if mkErr != nil {
			req.LogSink.Stop()
			return nil, fmt.Errorf("truncate log file for %q: %w", req.Name, mkErr)
		}
		_ = f.Close()
		// Register the collector before starting the process to eliminate the
		// race where a fast-exiting process fires OnExit before we store the
		// stop function — which would leak the collector goroutine.
		stop := StartLogCollector(logFile, "nb:"+req.NotebookName, "runtime", req.LogSink)
		r.mu.Lock()
		r.collectors[req.Name] = stop
		r.mu.Unlock()
	}

	pid, endpoint, err := r.supervisor.Start(process.ProcessSpec{
		Name:    req.Name,
		Command: command,
		Dir:     req.WorkDir,
		Port:    req.Port,
		GPUs:    gpus,
		PIDFile: r.pidFile(req.Name),
		LogFile: logFile,
	}, func(status string) {
		r.mu.Lock()
		stop := r.collectors[req.Name]
		delete(r.collectors, req.Name)
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
		// Process failed to start: clean up the pre-registered collector and sink.
		r.mu.Lock()
		stop := r.collectors[req.Name]
		delete(r.collectors, req.Name)
		r.mu.Unlock()
		if stop != nil {
			stop()
		}
		if req.LogSink != nil {
			req.LogSink.Stop()
		}
		return nil, err
	}

	return &StartedNotebook{Endpoint: endpoint, PID: pid, Token: req.Token, EnvPath: envPath}, nil
}

func (r *processRuntime) RecoverTarget(name string, port int, onExit func(status string)) (bool, error) {
	_, running, err := r.supervisor.Recover(process.ProcessSpec{
		Name:    name,
		Port:    port,
		PIDFile: r.pidFile(name),
	}, onExit)
	return running, err
}

func (r *processRuntime) Stop(_ context.Context, name string) error {
	return r.supervisor.Stop(name)
}

func (r *processRuntime) KillAll(_ context.Context) error {
	return r.supervisor.KillAll()
}

func (r *processRuntime) Status(name string) string {
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

func logRuntimeStart(mode, name, workDir string, port int) {
	slog.Info("notebook runtime starting", "mode", mode, "name", name, "work_dir", workDir, "port", port)
}

// StartLogCollector starts a freader Collector that watches logFile and forwards
// each collected line to sink. Returns a stop function that halts the collector.
// Exported for use by other worker packages (e.g. serving worker).
//
// Lines are labelled "combined" because stdout and stderr are merged into a
// single file via the shell redirect added by ProcessSupervisor.
func StartLogCollector(logFile, runID, stepName string, sink logsink.LogSink) func() {
	cfg := freader.Config{
		WorkerCount:         1,
		PollInterval:        200 * time.Millisecond,
		Separator:           "\n",
		FingerprintStrategy: freader.FingerprintStrategyDeviceAndInode,
		Include:             []string{logFile},
		StoreOffsets:        false,
		OnEventFunc: func(e freader.LineEvent) {
			sink.Append(runID, stepName, "combined", e.Line, e.Ts)
		},
	}
	collector, err := freader.NewCollector(cfg)
	if err != nil {
		slog.Warn("log collector: cannot create", "log_file", logFile, "err", err)
		return func() {}
	}
	collector.Start()
	return func() { collector.Stop() }
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

func prepareStepsForBackend(spec *notebook.NotebookPrepareSpec, backend string) ([]notebook.NotebookPrepareStep, error) {
	if spec == nil {
		return nil, nil
	}
	return spec.StepsForBackend(backend)
}
