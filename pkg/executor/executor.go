package executor

import (
	"context"
	"io"
	"path/filepath"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/source"
)

type ExecConfig struct {
	WorkDir   string
	SourceDir string // source fetch 루트 (미설정 시 WorkDir/_source)
	InputDir  string
	OutputDir string
	RunID     string
	StepName  string
	Params    map[string]any
	SourceCfg source.Config
	Stdout    io.Writer // nil이면 os.Stdout
	Stderr    io.Writer // nil이면 os.Stderr
}

// fetchDir는 이 step의 source를 풀 디렉토리를 반환한다.
// Run.Dir > StepName 순으로 서브디렉토리 이름을 결정한다.
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

// Env는 모든 executor에서 공통으로 주입할 환경변수 슬라이스를 반환한다.
func (c ExecConfig) Env() []string {
	return []string{
		"PIPER_INPUT_DIR=" + c.InputDir,
		"PIPER_OUTPUT_DIR=" + c.OutputDir,
		"PIPER_RUN_ID=" + c.RunID,
		"PIPER_STEP_NAME=" + c.StepName,
	}
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
