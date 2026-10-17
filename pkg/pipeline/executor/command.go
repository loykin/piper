package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/loykin/piper/internal/redact"
	"github.com/loykin/piper/internal/srcfetch"
	"github.com/loykin/piper/pkg/pipeline"
	"gopkg.in/yaml.v3"
)

type CommandExecutor struct{}

func (e *CommandExecutor) Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) (string, error) {
	if len(step.Run.Command) == 0 {
		return "", fmt.Errorf("step %q: command is empty", step.Name)
	}

	workDir := cfg.WorkDir
	scriptPath := ""

	// If a source is specified, fetch it and run from fetchDir. The
	// command's actual cwd — and therefore where any relative-path output
	// it writes (.metrics.json, outputs: artifacts) ends up — moves to
	// fetchDir along with it. Callers must read those outputs back from the
	// same workDir this function returns, not from cfg.OutputDir.
	if step.Run.Source != "" && step.Run.Source != "local" {
		fetcher, err := srcfetch.New(step.Run, cfg.SourceCfg)
		if err != nil {
			return "", err
		}
		fetchDir := cfg.fetchDir(step.Run)
		fetched, err := fetcher.Fetch(ctx, step.Run, fetchDir)
		if err != nil {
			return "", fmt.Errorf("fetch failed: %w", err)
		}
		if scriptPath, err = filepath.Abs(fetched); err != nil {
			return "", fmt.Errorf("resolve source path: %w", err)
		}
		if workDir, err = filepath.Abs(fetchDir); err != nil {
			return "", fmt.Errorf("resolve source work dir: %w", err)
		}
	} else if step.Run.Path != "" {
		abs, err := filepath.Abs(filepath.Join(workDir, step.Run.Path))
		if err != nil {
			return "", fmt.Errorf("resolve source path: %w", err)
		}
		scriptPath = abs
	}

	paramsFile, err := writeParamsFile(cfg)
	if err != nil {
		return "", err
	}

	// prepare and command share one environment, so anything a template
	// needs (script path, params) is available to both.
	env := cfg.Environ(step.Options.Env)
	env = setEnv(env, "PIPER_PARAMS_FILE="+paramsFile)
	if scriptPath != "" {
		env = setEnv(env, "PIPER_SCRIPT_PATH="+scriptPath)
	}
	for k, v := range cfg.Params {
		env = setEnv(env, fmt.Sprintf("PIPER_PARAM_%s=%v", k, v))
	}

	slog.Info("running command", "step", step.Name, "cmd", redactArgs(step.Run.Command), "workDir", workDir)

	stdout, stderr := cfg.Stdout, cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	if err := runPrepare(ctx, step, env, workDir, stdout, stderr); err != nil {
		return "", err
	}

	cmd := exec.Command(step.Run.Command[0], step.Run.Command[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env
	// Deliberately NOT Setpgid: true. When this executor runs inside "piper
	// agent exec" (the baremetal driver's subprocess wrapper, see
	// cmd/piper/commands/agent.go), giving this command its own process
	// group means provisr's Stop() — which sends SIGTERM/SIGKILL to the
	// *wrapper's* process group — never reaches it: the wrapper can be
	// killed by the raw signal before its own Go-level ctx.Done() handler
	// gets scheduled, orphaning this command. Leaving it in the wrapper's
	// group means the OS itself delivers that same group signal here too,
	// with no dependency on any Go code running in between. The ctx.Done()
	// branch below still kills it directly as a second line of defense for
	// cancellation that isn't accompanied by a process-group signal (e.g.
	// local "piper run").
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return workDir, err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done
		return workDir, ctx.Err()
	}
}

func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = redact.String(arg)
	}
	return out
}

// writeParamsFile writes the step's parameters as YAML (types preserved) to
// a file the step reads through $PIPER_PARAMS_FILE — e.g. papermill -f.
func writeParamsFile(cfg ExecConfig) (string, error) {
	params := cfg.Params
	if params == nil {
		params = map[string]any{}
	}
	data, err := yaml.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("marshal params: %w", err)
	}
	dir, err := filepath.Abs(cfg.OutputDir)
	if err != nil {
		return "", fmt.Errorf("resolve params dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create params dir: %w", err)
	}
	path := filepath.Join(dir, ".piper-params.yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write params file: %w", err)
	}
	return path, nil
}
