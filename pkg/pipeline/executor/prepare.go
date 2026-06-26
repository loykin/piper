package executor

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/piper/piper/pkg/pipeline"
)

func runPrepare(ctx context.Context, step *pipeline.Step, cfg ExecConfig, workDir string, stdout, stderr io.Writer) error {
	for i, command := range step.Run.Prepare {
		if len(command) == 0 {
			return fmt.Errorf("step %q: prepare[%d] command is empty", step.Name, i)
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = workDir
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = cfg.Environ(step.Options.Env)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("step %q: prepare[%d] failed: %w", step.Name, i, err)
		}
	}
	return nil
}
