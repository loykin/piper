package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/piper/piper/pkg/pipeline"
)

type CommandExecutor struct{}

func (e *CommandExecutor) Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) error {
	if len(step.Run.Command) == 0 {
		return fmt.Errorf("step %q: command is empty", step.Name)
	}

	slog.Info("running command", "step", step.Name, "cmd", step.Run.Command)

	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.CommandContext(ctx, step.Run.Command[0], step.Run.Command[1:]...)
	cmd.Dir = cfg.WorkDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), cfg.Env()...)

	return cmd.Run()
}
