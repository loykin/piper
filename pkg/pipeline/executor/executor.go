package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/srcfetch"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
)

type ExecConfig struct {
	WorkDir        string
	SourceDir      string // source fetch root (defaults to WorkDir/_source)
	InputDir       string
	OutputDir      string
	RunID          string
	StepName       string
	GPUs           string   // CUDA_VISIBLE_DEVICES value, e.g. "0,1" (bare-metal only)
	PythonBin      string   // python executable for isolated task environments
	PapermillBin   string   // papermill executable for notebook steps
	JupyterDataDir string   // Jupyter data directory for isolated notebook kernels
	EnvPathPrepend []string // directories prepended to PATH for child processes
	Params         map[string]any
	SourceCfg      srcfetch.Config
	Stdout         io.Writer         // if nil, defaults to os.Stdout
	Stderr         io.Writer         // if nil, defaults to os.Stderr
	Vars           proto.BuiltinVars // system-injected builtin variables
}

// fetchDir returns the directory into which this step's source will be fetched.
// The subdirectory name is determined by Run.Dir first, then StepName.
func (c ExecConfig) fetchDir(run pipeline.Run) string {
	base := c.SourceDir
	if base == "" {
		base = filepath.Join(c.WorkDir, "_source")
	}
	sub := run.Dir
	if sub == "" {
		sub = c.StepName
	}
	return filepath.Join(base, sub)
}

// Env returns the slice of environment variables to inject across all executors.
// Fixed PIPER_* vars come first, followed by BuiltinVars fields.
// To add a new builtin variable: add a field to proto.BuiltinVars and a case here.
func (c ExecConfig) Env() []string {
	env := []string{
		"PIPER_INPUT_DIR=" + c.InputDir,
		"PIPER_OUTPUT_DIR=" + c.OutputDir,
		"PIPER_RUN_ID=" + c.RunID,
		"PIPER_STEP_NAME=" + c.StepName,
	}
	if v := c.Vars.ScheduledAt; v != nil {
		// RFC3339 UTC matches Airflow's execution_date semantics:
		// the logical/scheduled time regardless of when the run actually started.
		env = append(env, "PIPER_SCHEDULED_AT="+v.UTC().Format(time.RFC3339))
	}
	if c.GPUs != "" {
		env = append(env, "CUDA_VISIBLE_DEVICES="+c.GPUs)
	}
	if c.PythonBin != "" {
		env = append(env, "PIPER_PYTHON_BIN="+c.PythonBin)
	}
	if c.JupyterDataDir != "" {
		env = append(env,
			"JUPYTER_DATA_DIR="+c.JupyterDataDir,
			"JUPYTER_CONFIG_DIR="+filepath.Join(filepath.Dir(c.JupyterDataDir), "jupyter-config"),
			"JUPYTER_RUNTIME_DIR="+filepath.Join(filepath.Dir(c.JupyterDataDir), "jupyter-runtime"),
		)
	}
	return env
}

func (c ExecConfig) Environ(stepVars []manifest.EnvVar) []string {
	env := os.Environ()
	for _, entry := range append(stepEnv(stepVars), c.Env()...) {
		env = setEnv(env, entry)
	}
	if len(c.EnvPathPrepend) > 0 {
		path := getEnv(env, "PATH")
		prefix := strings.Join(c.EnvPathPrepend, string(os.PathListSeparator))
		if path != "" {
			path = prefix + string(os.PathListSeparator) + path
		} else {
			path = prefix
		}
		env = setEnv(env, "PATH="+path)
	}
	return env
}

func (c ExecConfig) PapermillCommand() string {
	if c.PapermillBin != "" {
		return c.PapermillBin
	}
	if v := os.Getenv("PIPER_PAPERMILL"); v != "" {
		return v
	}
	return "papermill"
}

func setEnv(env []string, entry string) []string {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return env
	}
	prefix := name + "="
	for i, existing := range env {
		if strings.HasPrefix(existing, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func getEnv(env []string, name string) string {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func stepEnv(env []manifest.EnvVar) []string {
	if len(env) == 0 {
		return nil
	}
	vars := make([]manifest.EnvVar, len(env))
	copy(vars, env)
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
	out := make([]string, 0, len(vars))
	for _, e := range vars {
		out = append(out, e.Name+"="+e.Value)
	}
	return out
}

type Executor interface {
	Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) error
}

func New(step *pipeline.Step) Executor {
	switch step.Run.Type {
	case "notebook":
		return &NotebookExecutor{}
	default:
		return &CommandExecutor{}
	}
}
