package executor

import (
	"context"
	"io"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/source"
)

type ExecConfig struct {
	WorkDir   string
	InputDir  string
	OutputDir string
	Params    map[string]any
	SourceCfg source.Config
	Stdout    io.Writer // nil이면 os.Stdout
	Stderr    io.Writer // nil이면 os.Stderr
}

type Executor interface {
	Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) error
}

func New(step *pipeline.Step) Executor {
	switch step.Run.Type {
	case "notebook":
		return &NotebookExecutor{}
	case "python":
		return &PythonExecutor{}
	default:
		return &CommandExecutor{}
	}
}
