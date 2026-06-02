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
	mu        sync.Mutex
	notebooks map[string]*processNotebook
}

type processNotebook struct {
	pid int
	cmd *exec.Cmd
}

func newProcessRuntime() *processRuntime {
	return &processRuntime{notebooks: make(map[string]*processNotebook)}
}

func (r *processRuntime) Start(_ context.Context, req RuntimeStartRequest) (*StartedNotebook, error) {
	bin, extraArgs, envPath, err := prepareProcessEnv(req.Spec.Spec.Env, req.WorkDir)
	if err != nil {
		return nil, err
	}

	command := append(extraArgs, notebook.JupyterStartArgs(req.BaseURL, req.Token, req.WorkDir, req.Port)...)
	command = append([]string{bin}, command...)

	pid, endpoint, cmd, err := workload.StartProcess(workload.ProcessSpec{
		Name:    req.Name,
		Command: command,
		Dir:     req.WorkDir,
		Port:    req.Port,
		GPUs:    req.Spec.Spec.GPUs,
	})
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.notebooks[req.Name] = &processNotebook{pid: pid, cmd: cmd}
	r.mu.Unlock()

	workload.WatchProcess(cmd, func(status string) {
		r.mu.Lock()
		delete(r.notebooks, req.Name)
		r.mu.Unlock()
		if req.OnExit != nil {
			req.OnExit(status)
		}
	})

	return &StartedNotebook{Endpoint: endpoint, PID: pid, EnvPath: envPath}, nil
}

func (r *processRuntime) Stop(_ context.Context, name string) error {
	r.mu.Lock()
	nb, ok := r.notebooks[name]
	if ok {
		delete(r.notebooks, name)
	}
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("notebook not found")
	}
	workload.KillPID(nb.pid)
	return nil
}

func (r *processRuntime) KillAll(_ context.Context) error {
	r.mu.Lock()
	pids := make([]int, 0, len(r.notebooks))
	for _, nb := range r.notebooks {
		pids = append(pids, nb.pid)
	}
	r.notebooks = make(map[string]*processNotebook)
	r.mu.Unlock()
	for _, pid := range pids {
		workload.KillPID(pid)
	}
	return nil
}

// prepareProcessEnv resolves or auto-creates the Python environment for a notebook.
//
//   - empty        -> auto-create venv at {workDir}/.venv, install jupyterlab if needed
//   - conda:name   -> use existing conda env
//   - /path/to/venv -> use existing venv (must have jupyterlab installed)
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
