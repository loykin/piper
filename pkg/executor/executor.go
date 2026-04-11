package executor

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/source"
)

type ExecConfig struct {
	WorkDir   string
	SourceDir string // source fetch root (defaults to WorkDir/_source)
	InputDir  string
	OutputDir string
	RunID     string
	StepName  string
	Params    map[string]any
	SourceCfg source.Config
	Stdout    io.Writer         // if nil, defaults to os.Stdout
	Stderr    io.Writer         // if nil, defaults to os.Stderr
	Vars      proto.BuiltinVars // system-injected builtin variables
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
	return env
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
