package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/source"
)

type NotebookExecutor struct{}

func (e *NotebookExecutor) Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) error {
	fetcher, err := source.New(step.Run, cfg.SourceCfg)
	if err != nil {
		return err
	}

	notebookPath, err := fetcher.Fetch(ctx, step.Run, cfg.fetchDir(step.Run))
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}

	outputNb := filepath.Join(cfg.OutputDir, filepath.Base(notebookPath))

	args := []string{notebookPath, outputNb}
	for k, v := range cfg.Params {
		args = append(args, "-p", k, fmt.Sprintf("%v", v))
	}

	slog.Info("running papermill", "notebook", notebookPath, "output", outputNb)

	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.CommandContext(ctx, "papermill", args...)
	cmd.Dir = cfg.WorkDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), cfg.Env()...)

	return cmd.Run()
}
