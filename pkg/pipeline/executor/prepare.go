package executor

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/loykin/piper/pkg/pipeline"
)

// runPrepare runs each run.prepare command in workDir with the same
// environment the step command gets. Prepare is how a step customizes its
// environment (pip, uv, a venv on baremetal); Piper does not interpret it.
func runPrepare(ctx context.Context, step *pipeline.Step, env []string, workDir string, stdout, stderr io.Writer) error {
	for i, command := range step.Run.Prepare {
		if len(command) == 0 {
			return fmt.Errorf("step %q: prepare[%d] command is empty", step.Name, i)
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = workDir
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = env
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("step %q: prepare[%d] failed: %w", step.Name, i, err)
		}
	}
	return nil
}
