// Package runner provides the common logic for executing piper tasks.
//
// Both the worker (polling) and agent (K8s one-shot) use this package.
// The only difference is how they receive a task — the execution logic is identical.
//
//	receive task → download inputs → run command → upload outputs → report
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/piper/piper/pkg/blobstore"
	"github.com/piper/piper/pkg/executor"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/source"
)

// Config holds Runner configuration.
type Config struct {
	MasterURL string
	Token     string
	OutputDir string // local output root directory
	InputDir  string // local input root directory

	// StorageURL selects the artifact store backend.
	// Supported schemes: s3://, file://, http://, https://.
	// Empty means local filesystem only (no artifact transfer between steps).
	StorageURL string

	// Source fetch configuration (notebook/python source: git|s3|http)
	GitToken string
	GitUser  string
}

// Runner executes a single task.
type Runner struct {
	cfg    Config
	client *http.Client
	store  blobstore.Store // nil means local filesystem only (no artifact transfer)
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
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}

	if cfg.StorageURL != "" {
		st, err := blobstore.Open(cfg.StorageURL, cfg.Token)
		if err != nil {
			return nil, fmt.Errorf("artifact store: %w", err)
		}
		r.store = st
		// Local work dirs are transient when the store is remote (S3, HTTP).
		// If it's a LocalStore, the local dir IS the durable copy.
		_, isLocal := st.(*blobstore.LocalStore)
		r.cleanWorkdir = !isLocal
	}

	return r, nil
}

// Run executes a task and reports the result to the master.
// Returns true if the step succeeded, false if it failed.
// Callers in K8s agent mode should exit non-zero on false so the K8s Job
// status reflects the actual step outcome and ReconcileJobs can detect failures.
func (r *Runner) Run(ctx context.Context, task *proto.Task) bool {
	startedAt := time.Now()

	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		r.reportFailed(task, fmt.Errorf("unmarshal step: %w", err), startedAt)
		return false
	}

	stepOutputDir := filepath.Join(r.cfg.OutputDir, task.RunID, step.Name)
	if err := os.MkdirAll(stepOutputDir, 0755); err != nil {
		r.reportFailed(task, err, startedAt)
		return false
	}

	// When using a remote store, local dirs are transient staging areas only.
	// Registered before the logFile defer so it executes after logFile.Close() (LIFO).
	if r.cleanWorkdir {
		defer r.cleanLocalWorkdir(task.RunID, step.Name, step.Inputs)
	}

	// Download input artifacts from the store
	if r.store != nil && len(step.Inputs) > 0 {
		if err := r.downloadInputs(ctx, task.RunID, step.Inputs); err != nil {
			slog.Error("download inputs failed", "task_id", task.ID, "err", err)
			r.reportFailed(task, err, startedAt)
			return false
		}
	}

	// Local log file (fallback)
	logFile := openLogFile(stepOutputDir, step.Name)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	// Log collector
	logger := newBatchLogger(r, task.RunID, task.StepName, logFile)

	// Periodically flush logs to master while the task is running
	flushCtx, stopFlush := context.WithCancel(ctx)
	go logger.flushLoop(flushCtx)

	// Execute the command
	execErr := r.execute(ctx, &step, task, stepOutputDir, logger)

	// Stop the flush loop and do a final flush
	stopFlush()
	logger.flush(ctx)

	// Upload output artifacts to the store (on success)
	if execErr == nil && r.store != nil && len(step.Outputs) > 0 {
		if err := r.uploadOutputs(ctx, task.RunID, step.Name, stepOutputDir, step.Outputs); err != nil {
			slog.Error("upload outputs failed", "task_id", task.ID, "err", err)
			execErr = err
		}
	}

	if execErr != nil {
		r.reportFailed(task, execErr, startedAt)
		return false
	}
	r.reportDone(task, startedAt)
	return true
}

// ─── Execution ────────────────────────────────────────────────────────────────

func (r *Runner) execute(
	ctx context.Context,
	step *pipeline.Step,
	task *proto.Task,
	outputDir string,
	logger *batchLogger,
) error {
	// io.Writer that intercepts stdout/stderr line by line
	stdoutW := &lineWriter{stream: "stdout", logger: logger, tee: os.Stdout}
	stderrW := &lineWriter{stream: "stderr", logger: logger, tee: os.Stderr}

	cfg := executor.ExecConfig{
		WorkDir:   task.WorkDir,
		InputDir:  filepath.Join(r.cfg.InputDir, task.RunID),
		OutputDir: outputDir,
		RunID:     task.RunID,
		StepName:  step.Name,
		Params:    proto.MergeParams(step.Params, task.RunParams),
		Stdout:    stdoutW,
		Stderr:    stderrW,
		Vars:      task.Vars,
		SourceCfg: source.Config{
			GitToken: r.cfg.GitToken,
			GitUser:  r.cfg.GitUser,
		},
	}

	err := executor.New(step).Execute(ctx, step, cfg)
	stdoutW.Close()
	stderrW.Close()
	return err
}

// lineWriter implements io.Writer and writes to batchLogger line by line.
type lineWriter struct {
	stream string
	logger *batchLogger
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

// ─── Batch logging ────────────────────────────────────────────────────────────

type logEntry struct {
	Ts     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

type batchLogger struct {
	mu       sync.Mutex
	entries  []logEntry
	r        *Runner
	runID    string
	stepName string
	file     *os.File
}

const logFlushInterval = 2 * time.Second

func newBatchLogger(r *Runner, runID, stepName string, file *os.File) *batchLogger {
	return &batchLogger{r: r, runID: runID, stepName: stepName, file: file}
}

// flushLoop sends buffered logs to the master every logFlushInterval.
// Run in a goroutine; cancel ctx to stop.
func (l *batchLogger) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.flush(ctx)
		}
	}
}

func (l *batchLogger) append(stream, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{Ts: time.Now(), Stream: stream, Line: line})
	if l.file != nil {
		_, _ = fmt.Fprintf(l.file, "[%s] %s\n", stream, line)
	}
}

func (l *batchLogger) flush(ctx context.Context) {
	l.mu.Lock()
	entries := l.entries
	l.entries = nil
	l.mu.Unlock()

	if len(entries) == 0 || l.r.cfg.MasterURL == "" {
		return
	}

	data, err := json.Marshal(entries)
	if err != nil {
		slog.Warn("log marshal error", "err", err)
		return
	}

	url := fmt.Sprintf("%s/runs/%s/steps/%s/logs", l.r.cfg.MasterURL, l.runID, l.stepName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	l.r.setAuth(req)

	resp, err := l.r.client.Do(req)
	if err != nil {
		slog.Warn("log flush failed", "err", err)
		return
	}
	_ = resp.Body.Close()
}

// ─── Reporting ────────────────────────────────────────────────────────────────

type taskResult struct {
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Attempts  int       `json:"attempts"`
}

func (r *Runner) reportDone(task *proto.Task, startedAt time.Time) {
	r.report(task.ID, proto.TaskStatusDone, taskResult{StartedAt: startedAt, EndedAt: time.Now(), Attempts: 1})
}

func (r *Runner) reportFailed(task *proto.Task, err error, startedAt time.Time) {
	slog.Error("task failed", "task_id", task.ID, "err", err)
	r.report(task.ID, proto.TaskStatusFailed, taskResult{
		Error:     err.Error(),
		StartedAt: startedAt,
		EndedAt:   time.Now(),
		Attempts:  1,
	})
}

func (r *Runner) report(taskID, status string, result taskResult) {
	if r.cfg.MasterURL == "" {
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	url := fmt.Sprintf("%s/api/tasks/%s/%s", r.cfg.MasterURL, taskID, status)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	r.setAuth(req)

	resp, err := r.client.Do(req)
	if err != nil {
		slog.Error("report error", "task_id", taskID, "status", status, "err", err)
		return
	}
	_ = resp.Body.Close()
	slog.Info("task reported", "task_id", taskID, "status", status)
}

func (r *Runner) setAuth(req *http.Request) {
	if r.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	}
}

// ─── Artifact transfer ────────────────────────────────────────────────────────

// cleanLocalWorkdir removes the step's local output dir and any downloaded input
// dirs. Called only when using a remote store; local dirs are transient staging areas.
func (r *Runner) cleanLocalWorkdir(runID, stepName string, inputs []pipeline.Artifact) {
	_ = os.RemoveAll(filepath.Join(r.cfg.OutputDir, runID, stepName))
	for _, art := range inputs {
		_ = os.RemoveAll(filepath.Join(r.cfg.InputDir, runID, art.Name))
	}
}

// downloadInputs downloads step input artifacts from the store to the local filesystem.
// Store key: {runID}/{fromStep}/{artifactName}/…
// Local:     {inputDir}/{runID}/{artifactName}/…
func (r *Runner) downloadInputs(ctx context.Context, runID string, inputs []pipeline.Artifact) error {
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
		destDir := filepath.Join(r.cfg.InputDir, runID, art.Name)

		if err := blobstore.DownloadDir(ctx, r.store, prefix+"/", destDir); err != nil {
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

		if err := blobstore.UploadPath(ctx, r.store, localPath, prefix); err != nil {
			return fmt.Errorf("upload %q: %w", art.Name, err)
		}
		slog.Info("artifact uploaded", "name", art.Name, "prefix", prefix)
	}
	return nil
}

// ─── Utilities ────────────────────────────────────────────────────────────────

func openLogFile(outputDir, stepName string) *os.File {
	path := filepath.Join(outputDir, stepName+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}
