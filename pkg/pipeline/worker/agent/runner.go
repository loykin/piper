// Package runner provides the common logic for executing piper tasks.
//
// Both long-running workers and K8s one-shot agents use this package.
// The only difference is how they receive a task — the execution logic is identical.
//
//	receive task → download inputs → run command → upload outputs → report
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/srcfetch"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/executor"
	"github.com/piper/piper/pkg/storage"
)

// Config holds Runner configuration.
type Config struct {
	StorageToken string
	OutputDir    string // local output root directory
	InputDir     string // local input root directory

	// StorageURL selects the artifact store backend.
	// Supported schemes: s3://, file://, http://, https://.
	// Empty means local filesystem only (no artifact transfer between steps).
	StorageURL string

	// Source fetch configuration (notebook/python source: git|s3|http)
	GitToken string
	GitUser  string

	// IsolatedPython creates a per-task venv and prepends it to PATH before
	// prepare commands and the task entrypoint run.
	IsolatedPython bool
}

// Runner executes a single task.
type Runner struct {
	cfg   Config
	store storage.Store // nil means local filesystem only (no artifact transfer)
	// cleanWorkdir is true when artifacts are stored remotely and local dirs are transient.
	cleanWorkdir bool
}

// New creates a Runner.
func New(cfg Config) (*Runner, error) {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./piper-outputs"
	}
	if cfg.InputDir == "" {
		cfg.InputDir = cfg.OutputDir // default: same directory (single machine)
	}

	r := &Runner{
		cfg: cfg,
	}

	if cfg.StorageURL != "" {
		st, err := storage.Open(cfg.StorageURL, cfg.StorageToken)
		if err != nil {
			return nil, fmt.Errorf("artifact store: %w", err)
		}
		r.store = st
		// Local work dirs are transient when the store is remote (S3, HTTP).
		// If it's a LocalStore, the local dir IS the durable copy.
		_, isLocal := st.(*storage.LocalStore)
		r.cleanWorkdir = !isLocal
	}

	return r, nil
}

// Run executes a task and returns the TaskResult.
// The caller is responsible for reporting the result (HTTP or result file).
func (r *Runner) Run(ctx context.Context, task *proto.Task) proto.TaskResult {
	startedAt := time.Now()

	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		return r.failedResult(task, fmt.Errorf("unmarshal step: %w", err), startedAt)
	}

	stepOutputDir := filepath.Join(r.cfg.OutputDir, task.RunID, step.Name)
	if err := os.MkdirAll(stepOutputDir, 0755); err != nil {
		return r.failedResult(task, err, startedAt)
	}

	// When using a remote store, local dirs are transient staging areas only.
	// Registered before the logFile defer so it executes after logFile.Close() (LIFO).
	if r.cleanWorkdir {
		defer r.cleanLocalWorkdir(task.RunID, step.Name)
	}

	// Download input artifacts from the store
	if r.store != nil && len(step.Inputs) > 0 {
		if err := r.downloadInputs(ctx, task.RunID, step.Name, step.Inputs); err != nil {
			slog.Error("download inputs failed", "task_id", task.ID, "err", err)
			return r.failedResult(task, err, startedAt)
		}
	}

	// Local log file (fallback)
	logFile := openLogFile(stepOutputDir, step.Name)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	logger := newLineLogger(logFile)

	// Execute the command
	execErr := r.execute(ctx, &step, task, stepOutputDir, logger)

	// Upload output artifacts to the store (on success)
	if execErr == nil && r.store != nil && len(step.Outputs) > 0 {
		if err := r.uploadOutputs(ctx, task.RunID, step.Name, stepOutputDir, step.Outputs); err != nil {
			slog.Error("upload outputs failed", "task_id", task.ID, "err", err)
			execErr = err
		}
	}

	if execErr != nil {
		return r.failedResult(task, execErr, startedAt)
	}
	result := r.doneResult(task, startedAt)
	result.Metrics = readFinalMetrics(task.RunID, step.Name, stepOutputDir)
	return result
}

func readFinalMetrics(runID, stepName, outputDir string) map[string]float64 {
	data, err := os.ReadFile(filepath.Join(outputDir, ".metrics.json"))
	if err != nil {
		return nil
	}
	var vals map[string]float64
	if err := json.Unmarshal(data, &vals); err != nil || len(vals) == 0 {
		slog.Warn("metrics.json parse failed or empty", "run_id", runID, "step", stepName, "err", err)
		return nil
	}
	return vals
}

// ─── Execution ────────────────────────────────────────────────────────────────

func (r *Runner) execute(
	ctx context.Context,
	step *pipeline.Step,
	task *proto.Task,
	outputDir string,
	logger *lineLogger,
) error {
	// io.Writer that intercepts stdout/stderr line by line
	stdoutW := &lineWriter{stream: "stdout", logger: logger, tee: os.Stdout}
	stderrW := &lineWriter{stream: "stderr", logger: logger, tee: os.Stderr}

	cfg := executor.ExecConfig{
		WorkDir:   task.WorkDir,
		SourceDir: filepath.Join(outputDir, "_source"),
		InputDir:  filepath.Join(r.cfg.InputDir, task.RunID, step.Name),
		OutputDir: outputDir,
		RunID:     task.RunID,
		StepName:  step.Name,
		Params:    proto.MergeParams(step.Params, task.RunParams),
		Stdout:    stdoutW,
		Stderr:    stderrW,
		Vars:      task.Vars,
		GPUs:      stepGPUs(step),
		SourceCfg: srcfetch.Config{
			GitToken:   r.cfg.GitToken,
			GitUser:    r.cfg.GitUser,
			StorageURL: r.cfg.StorageURL,
		},
	}

	if r.cfg.IsolatedPython && needsIsolatedPython(step) {
		pyEnv, cleanup, err := prepareIsolatedPython(ctx, step, outputDir, stdoutW, stderrW)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			stdoutW.Close()
			stderrW.Close()
			return err
		}
		cfg.PythonBin = pyEnv.python
		cfg.PapermillBin = pyEnv.papermill
		cfg.JupyterDataDir = pyEnv.jupyterDataDir
		cfg.EnvPathPrepend = []string{pyEnv.binDir}
	}

	err := executor.New(step).Execute(ctx, step, cfg)
	stdoutW.Close()
	stderrW.Close()
	return err
}

type isolatedPythonEnv struct {
	dir            string
	binDir         string
	python         string
	pip            string
	papermill      string
	jupyterDataDir string
}

func prepareIsolatedPython(ctx context.Context, step *pipeline.Step, outputDir string, stdout, stderr io.Writer) (*isolatedPythonEnv, func(), error) {
	env := &isolatedPythonEnv{
		dir: filepath.Join(outputDir, ".task-venv"),
	}
	cleanup := func() {
		if err := os.RemoveAll(env.dir); err != nil {
			slog.Warn("remove task python env failed", "path", env.dir, "err", err)
		}
	}
	if err := os.RemoveAll(env.dir); err != nil {
		return nil, cleanup, fmt.Errorf("reset task python env: %w", err)
	}

	python := os.Getenv("PIPER_PYTHON_BIN")
	if python == "" {
		python = "python3"
	}
	if err := runSetupCommand(ctx, outputDir, stdout, stderr, python, "-m", "venv", env.dir); err != nil {
		return nil, cleanup, fmt.Errorf("create task python env: %w", err)
	}

	env.binDir = filepath.Join(env.dir, "bin")
	env.python = filepath.Join(env.binDir, "python")
	env.pip = filepath.Join(env.binDir, "pip")
	env.papermill = filepath.Join(env.binDir, "papermill")
	env.jupyterDataDir = filepath.Join(env.dir, "share", "jupyter")

	if needsPapermill(step) {
		if !fileExists(env.papermill) || !hasPythonModule(ctx, env.python, "ipykernel") {
			if err := runSetupCommand(ctx, outputDir, stdout, stderr, env.pip, "install", "papermill", "ipykernel"); err != nil {
				return nil, cleanup, fmt.Errorf("install notebook dependencies in task python env: %w", err)
			}
		}
		if err := writeVenvKernelSpec(env.dir, env.python); err != nil {
			return nil, cleanup, fmt.Errorf("write notebook kernel spec in task python env: %w", err)
		}
	}

	return env, cleanup, nil
}

func needsPapermill(step *pipeline.Step) bool {
	return step.Run.Type == "notebook" || step.Run.Notebook != ""
}

func needsIsolatedPython(step *pipeline.Step) bool {
	return needsPapermill(step) ||
		step.Run.Type == "python" ||
		len(step.Run.Prepare) > 0
}

func runSetupCommand(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasPythonModule(ctx context.Context, python, module string) bool {
	cmd := exec.CommandContext(ctx, python, "-c", "import "+module)
	return cmd.Run() == nil
}

func writeVenvKernelSpec(venvDir, python string) error {
	kernelDir := filepath.Join(venvDir, "share", "jupyter", "kernels", "python3")
	if err := os.MkdirAll(kernelDir, 0755); err != nil {
		return err
	}
	spec := map[string]any{
		"argv": []string{
			python,
			"-m",
			"ipykernel_launcher",
			"-f",
			"{connection_file}",
		},
		"display_name": "Python 3",
		"language":     "python",
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(kernelDir, "kernel.json"), data, 0644)
}

// lineWriter writes complete lines locally while teeing stdout/stderr to the
// parent worker process, which owns remote log delivery.
type lineWriter struct {
	stream string
	logger *lineLogger
	tee    io.Writer
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		_, _ = fmt.Fprintln(w.tee, line)
		w.logger.append(w.stream, line)
	}
	return len(p), nil
}

func (w *lineWriter) Close() {
	if len(w.buf) == 0 {
		return
	}
	line := string(w.buf)
	w.buf = nil
	_, _ = fmt.Fprintln(w.tee, line)
	w.logger.append(w.stream, line)
}

type lineLogger struct {
	mu   sync.Mutex
	file *os.File
}

func newLineLogger(file *os.File) *lineLogger { return &lineLogger{file: file} }

func (l *lineLogger) append(stream, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_, _ = fmt.Fprintf(l.file, "[%s] %s\n", stream, line)
	}
}

// ─── Reporting ────────────────────────────────────────────────────────────────

func (r *Runner) doneResult(task *proto.Task, startedAt time.Time) proto.TaskResult {
	return proto.TaskResult{
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		WorkerID:  task.WorkerID,
		Status:    proto.TaskStatusDone,
		StartedAt: startedAt,
		EndedAt:   time.Now(),
		Attempt:   task.Attempt,
	}
}

func (r *Runner) failedResult(task *proto.Task, err error, startedAt time.Time) proto.TaskResult {
	slog.Error("task failed", "task_id", task.ID, "err", err)
	return proto.TaskResult{
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		WorkerID:  task.WorkerID,
		Status:    proto.TaskStatusFailed,
		Error:     err.Error(),
		StartedAt: startedAt,
		EndedAt:   time.Now(),
		Attempt:   task.Attempt,
	}
}

// ─── Artifact transfer ────────────────────────────────────────────────────────

// cleanLocalWorkdir removes the step's local output and input staging dirs.
// Called only when using a remote store; local dirs are transient staging areas.
func (r *Runner) cleanLocalWorkdir(runID, stepName string) {
	_ = os.RemoveAll(filepath.Join(r.cfg.OutputDir, runID, stepName))
	_ = os.RemoveAll(filepath.Join(r.cfg.InputDir, runID, stepName))
}

// downloadInputs downloads step input artifacts from the store to the local filesystem.
// Store key: {runID}/{fromStep}/{artifactName}/…
// Local:     {inputDir}/{runID}/{stepName}/{artifactName}/…
func (r *Runner) downloadInputs(ctx context.Context, runID, stepName string, inputs []pipeline.Artifact) error {
	for _, art := range inputs {
		if art.From == "" {
			continue
		}
		parts := strings.SplitN(art.From, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("artifact %q: invalid from %q (expected stepName/artifactName)", art.Name, art.From)
		}
		fromStep, fromArtifact := parts[0], parts[1]
		prefix := fmt.Sprintf("%s/%s/%s", runID, fromStep, fromArtifact)
		destDir := filepath.Join(r.cfg.InputDir, runID, stepName, art.Name)

		if err := storage.DownloadDir(ctx, r.store, prefix+"/", destDir); err != nil {
			return fmt.Errorf("download %q: %w", art.Name, err)
		}
		slog.Info("artifact downloaded", "name", art.Name, "prefix", prefix)
	}
	return nil
}

// uploadOutputs uploads step output artifacts to the store.
// Local:      {outputDir}/{artifact.Path}
// Store key:  {runID}/{stepName}/{artifactName}/…
func (r *Runner) uploadOutputs(ctx context.Context, runID, stepName, outputDir string, outputs []pipeline.Artifact) error {
	for _, art := range outputs {
		if art.Path == "" {
			continue
		}
		localPath := filepath.Join(outputDir, art.Path)
		prefix := fmt.Sprintf("%s/%s/%s", runID, stepName, art.Name)

		if err := storage.UploadPath(ctx, r.store, localPath, prefix); err != nil {
			return fmt.Errorf("upload %q: %w", art.Name, err)
		}
		slog.Info("artifact uploaded", "name", art.Name, "prefix", prefix)
	}
	return nil
}

// ─── Utilities ────────────────────────────────────────────────────────────────

// stepGPUs returns the CUDA_VISIBLE_DEVICES value for a step.
// For baremetal/docker steps, driver.process.gpus holds explicit device IDs (e.g. "0,1").
// driver.resources.gpu is a quantity hint for schedulers (K8s resource requests), not a device selector.
func stepGPUs(step *pipeline.Step) string {
	if step.Driver.Process != nil && step.Driver.Process.GPUs != "" {
		return step.Driver.Process.GPUs
	}
	return ""
}

func openLogFile(outputDir, stepName string) *os.File {
	path := filepath.Join(outputDir, stepName+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}
