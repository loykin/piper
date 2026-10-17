package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/srcfetch"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
)

type ExecConfig struct {
	WorkDir     string
	SourceDir   string // source fetch root (defaults to WorkDir/_source)
	InputDir    string
	OutputDir   string
	RunID       string
	StepName    string
	GPUs        string // CUDA_VISIBLE_DEVICES value, e.g. "0,1" (bare-metal only)
	Params      map[string]any
	SourceCfg   srcfetch.Config
	Stdout      io.Writer         // if nil, defaults to os.Stdout
	Stderr      io.Writer         // if nil, defaults to os.Stderr
	Vars        proto.BuiltinVars // system-injected builtin variables
	ResolvedEnv []string          // master-resolved env entries, may contain secrets
}

// fetchDir returns the directory into which this step's source will be fetched.
// The subdirectory name is determined by Run.Dir first, then StepName.
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

// Env returns the slice of environment variables to inject across all executors.
// Fixed PIPER_* vars come first, followed by BuiltinVars fields.
// To add a new builtin variable: add a field to proto.BuiltinVars and a case here.
func (c ExecConfig) Env() []string {
	env := []string{
		"PIPER_INPUT_DIR=" + c.InputDir,
		"PIPER_OUTPUT_DIR=" + c.OutputDir,
		"PIPER_RUN_ID=" + c.RunID,
		"PIPER_STEP_NAME=" + c.StepName,
	}
	if v := c.Vars.ScheduledAt; v != nil {
		env = append(env, "PIPER_SCHEDULED_AT="+v.UTC().Format(time.RFC3339))
	}
	if v := c.Vars.RunStartedAt; v != nil {
		env = append(env, "PIPER_RUN_STARTED_AT="+v.UTC().Format(time.RFC3339))
	}
	if v := c.Vars.DataIntervalEnd; v != nil {
		env = append(env, "PIPER_DATA_INTERVAL_END="+v.UTC().Format(time.RFC3339))
	}
	if c.GPUs != "" {
		env = append(env, "CUDA_VISIBLE_DEVICES="+c.GPUs)
	}
	return env
}

func (c ExecConfig) Environ(stepVars []manifest.EnvVar) []string {
	env := os.Environ()
	entries := append(stepEnv(stepVars), c.ResolvedEnv...)
	entries = append(entries, c.Env()...)
	for _, entry := range entries {
		env = setEnv(env, entry)
	}
	return env
}

func setEnv(env []string, entry string) []string {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return env
	}
	prefix := name + "="
	for i, existing := range env {
		if strings.HasPrefix(existing, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func stepEnv(env []manifest.EnvVar) []string {
	if len(env) == 0 {
		return nil
	}
	vars := make([]manifest.EnvVar, len(env))
	copy(vars, env)
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
	out := make([]string, 0, len(vars))
	for _, e := range vars {
		out = append(out, e.Name+"="+e.Value)
	}
	return out
}

type Executor interface {
	// Execute runs step.Run and returns the absolute directory the command
	// actually ran in — the same directory any relative-path output the
	// command writes (.metrics.json, outputs: artifacts) lands in. For a
	// step with no source fetch (or source: local) this is cfg.OutputDir
	// unchanged; for a git/s3/http-sourced step it is the fetch/checkout
	// directory the command's cwd was reassigned to. Callers must read
	// metrics/outputs from this directory, not from cfg.OutputDir, so the
	// two never silently disagree.
	Execute(ctx context.Context, step *pipeline.Step, cfg ExecConfig) (string, error)
}

// New returns the step executor. Every runtime and every kind of step runs
// the same way — prepare, then command — so there is only one; notebook and
// script conveniences are editor templates that expand into that command.
func New(step *pipeline.Step) Executor {
	return &CommandExecutor{}
}
