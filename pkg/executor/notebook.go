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
	run := step.Run
	// Notebook field is a shorthand for: type=notebook, source=local, path=<value>
	if run.Notebook != "" && run.Path == "" {
		if run.Source == "" {
			run.Source = "local"
		}
		run.Path = run.Notebook
	}
	fetcher, err := source.New(run, cfg.SourceCfg)
	if err != nil {
		return err
	}

	notebookPath, err := fetcher.Fetch(ctx, run, cfg.fetchDir(run))
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}
	notebookPath, err = filepath.Abs(notebookPath)
	if err != nil {
		return fmt.Errorf("resolve notebook path: %w", err)
	}

	outputNb := filepath.Join(cfg.OutputDir, filepath.Base(notebookPath))

	args := []string{notebookPath, outputNb}
	for k, v := range cfg.Params {
		args = append(args, "-p", k, fmt.Sprintf("%v", v))
	}

	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	workDir, err := filepath.Abs(cfg.fetchDir(run))
	if err != nil {
		return fmt.Errorf("resolve notebook work dir: %w", err)
	}
	if err := runPrepare(ctx, step, cfg, workDir, stdout, stderr); err != nil {
		return err
	}

	slog.Info("running papermill", "notebook", notebookPath, "output", outputNb)

	cmd := exec.CommandContext(ctx, "papermill", args...)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), append(stepEnv(step.Options.Env), cfg.Env()...)...)

	return cmd.Run()
}
