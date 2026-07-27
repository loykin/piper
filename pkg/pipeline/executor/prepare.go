package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/piper/piper/pkg/pipeline"
)

// resolveCommandPath looks up name in dirs (e.g. cfg.EnvPathPrepend) before
// falling back to the caller's own PATH. exec.CommandContext resolves a bare
// command name via the calling process's PATH, not cfg.Environ()'s PATH, so
// without this a prepare step like ["pip", "install", ...] silently installs
// into the system Python instead of the isolated task venv on EnvPathPrepend.
func resolveCommandPath(name string, dirs []string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}
	for _, dir := range dirs {
		if resolved, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return resolved
		}
	}
	return name
}

func runPrepare(ctx context.Context, step *pipeline.Step, cfg ExecConfig, workDir string, stdout, stderr io.Writer) error {
	for i, command := range step.Run.Prepare {
		if len(command) == 0 {
			return fmt.Errorf("step %q: prepare[%d] command is empty", step.Name, i)
		}
		cmd := exec.CommandContext(ctx, resolveCommandPath(command[0], cfg.EnvPathPrepend), command[1:]...)
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
