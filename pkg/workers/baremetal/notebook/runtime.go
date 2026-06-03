package notebookworker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/workload"
)

const (
	RuntimeProcess = "process"
	RuntimeDocker  = "docker"
)

type Runtime interface {
	Start(ctx context.Context, req RuntimeStartRequest) (*StartedNotebook, error)
	Stop(ctx context.Context, name string) error
	KillAll(ctx context.Context) error
}

type RuntimeStartRequest struct {
	Name    string
	Spec    notebook.NotebookServerSpec
	WorkDir string
	Port    int
	Token   string
	BaseURL string
	OnExit  func(status string)
}

type StartedNotebook struct {
	Endpoint    string
	PID         int
	EnvPath     string
	ContainerID string
}

type processRuntime struct {
	mu         sync.Mutex
	notebooks  map[string]*processNotebook
	supervisor *workload.ProcessSupervisor
}

type processNotebook struct {
	port int
}

func newProcessRuntime() *processRuntime {
	return &processRuntime{
		notebooks:  make(map[string]*processNotebook),
		supervisor: workload.NewProcessSupervisor(),
	}
}

func (r *processRuntime) Start(_ context.Context, req RuntimeStartRequest) (*StartedNotebook, error) {
	var env, gpus string
	if req.Spec.Spec.Process != nil {
		env = req.Spec.Spec.Process.Env
		gpus = req.Spec.Spec.Process.GPUs
	}

	bin, extraArgs, envPath, err := prepareProcessEnv(env, req.WorkDir)
	if err != nil {
		return nil, err
	}

	command := processNotebookCommand(bin, extraArgs, req)

	r.mu.Lock()
	r.notebooks[req.Name] = &processNotebook{port: req.Port}
	r.mu.Unlock()

	pid, endpoint, err := r.supervisor.Start(workload.ProcessSpec{
		Name:    req.Name,
		Command: command,
		Dir:     req.WorkDir,
		Port:    req.Port,
		GPUs:    gpus,
	}, func(status string) {
		slog.Info("notebook runtime exited", "name", req.Name, "status", status)
		r.mu.Lock()
		delete(r.notebooks, req.Name)
		r.mu.Unlock()
		if req.OnExit != nil {
			req.OnExit(status)
		}
	})
	if err != nil {
		r.mu.Lock()
		delete(r.notebooks, req.Name)
		r.mu.Unlock()
		return nil, err
	}

	return &StartedNotebook{Endpoint: endpoint, PID: pid, EnvPath: envPath}, nil
}

func processNotebookCommand(bin string, extraArgs []string, req RuntimeStartRequest) []string {
	command := append([]string{}, extraArgs...)
	command = append(command, notebook.JupyterLabArgs(req.BaseURL, req.Token, req.WorkDir, req.Port)...)
	return append([]string{bin}, command...)
}

func (r *processRuntime) Stop(_ context.Context, name string) error {
	r.mu.Lock()
	_, ok := r.notebooks[name]
	if ok {
		delete(r.notebooks, name)
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return r.supervisor.Stop(name)
}

func (r *processRuntime) KillAll(_ context.Context) error {
	r.mu.Lock()
	r.notebooks = make(map[string]*processNotebook)
	r.mu.Unlock()
	return r.supervisor.KillAll()
}

// prepareProcessEnv resolves or auto-creates the Python environment for a notebook.
//
//   - empty        -> auto-create venv at {workDir}/.venv, install jupyterlab/ipykernel if needed
//   - conda:name   -> use existing conda env
//   - /path/to/venv -> use existing venv, installing missing jupyterlab/ipykernel if needed
func prepareProcessEnv(specEnv, workDir string) (bin string, extraArgs []string, envPath string, err error) {
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

	if err := ensureVenv(venvPath); err != nil {
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

func ensureVenv(venvPath string) error {
	python := filepath.Join(venvPath, "bin", "python")
	if _, err := os.Stat(python); err != nil {
		slog.Info("creating venv", "path", venvPath)
		out, err := exec.Command("python3", "-m", "venv", venvPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("create venv %q: %w: %s", venvPath, err, strings.TrimSpace(string(out)))
		}
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
