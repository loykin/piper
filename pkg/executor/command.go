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

type CommandExecutor struct{}

func (e *CommandExecutor) Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) error {
	if len(step.Run.Command) == 0 {
		return fmt.Errorf("step %q: command is empty", step.Name)
	}

	workDir := cfg.WorkDir
	extraEnv := cfg.Env()

	// source가 지정된 경우 fetch 후 fetchDir에서 실행
	if step.Run.Source != "" && step.Run.Source != "local" {
		fetcher, err := source.New(step.Run, cfg.SourceCfg)
		if err != nil {
			return err
		}
		fetchDir := cfg.fetchDir(step.Run)
		scriptPath, err := fetcher.Fetch(ctx, step.Run, fetchDir)
		if err != nil {
			return fmt.Errorf("fetch failed: %w", err)
		}
		workDir = fetchDir
		extraEnv = append(extraEnv, "PIPER_SCRIPT_PATH="+scriptPath)
	}

	slog.Info("running command", "step", step.Name, "cmd", step.Run.Command, "workDir", workDir)

	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.CommandContext(ctx, step.Run.Command[0], step.Run.Command[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), extraEnv...)
	for k, v := range cfg.Params {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PIPER_PARAM_%s=%v", k, v))
	}

	return cmd.Run()
}
