package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/source"
)

type PythonExecutor struct{}

func (e *PythonExecutor) Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) error {
	fetcher, err := source.New(step.Run, cfg.SourceCfg)
	if err != nil {
		return err
	}

	scriptPath, err := fetcher.Fetch(ctx, step.Run, cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}

	slog.Info("running python", "script", scriptPath)

	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.CommandContext(ctx, "python", scriptPath)
	cmd.Dir = cfg.WorkDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PIPER_INPUT_DIR=%s", cfg.InputDir),
		fmt.Sprintf("PIPER_OUTPUT_DIR=%s", cfg.OutputDir),
	)
	for k, v := range cfg.Params {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PIPER_PARAM_%s=%v", k, v))
	}

	return cmd.Run()
}
