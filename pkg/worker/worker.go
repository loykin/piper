package worker

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
	"sync"
	"time"

	"github.com/piper/piper/pkg/executor"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/source"
)

func fileOrNil(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}

type Config struct {
	MasterURL    string
	Label        string
	Token        string
	PollInterval time.Duration
	Concurrency  int
	OutputDir    string
	SourceCfg    source.Config
}

type Worker struct {
	cfg    Config
	client *http.Client

	mu       sync.Mutex
	inFlight int
}

func New(cfg Config) *Worker {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 3 * time.Second
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 4
	}
	return &Worker{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *Worker) Run(ctx context.Context) error {
	slog.Info("worker started",
		"master", w.cfg.MasterURL,
		"label", w.cfg.Label,
		"concurrency", w.cfg.Concurrency,
		"poll_interval", w.cfg.PollInterval,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker shutting down")
			return nil
		default:
		}

		if w.available() {
			task, err := w.poll()
			if err != nil {
				slog.Warn("poll error", "err", err)
				w.sleep(ctx)
				continue
			}
			if task == nil {
				w.sleep(ctx)
				continue
			}

			w.mu.Lock()
			w.inFlight++
			w.mu.Unlock()

			go func(t *proto.Task) {
				defer func() {
					w.mu.Lock()
					w.inFlight--
					w.mu.Unlock()
				}()
				w.execute(ctx, t)
			}(task)

			continue
		}

		w.sleep(ctx)
	}
}

func (w *Worker) available() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inFlight < w.cfg.Concurrency
}

func (w *Worker) poll() (*proto.Task, error) {
	url := fmt.Sprintf("%s/api/tasks/next?label=%s", w.cfg.MasterURL, w.cfg.Label)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	w.setAuth(req)

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var task proto.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (w *Worker) execute(ctx context.Context, task *proto.Task) {
	startedAt := time.Now()
	slog.Info("executing task", "task_id", task.ID, "step", task.StepName)

	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		w.report(task.ID, "failed", fmt.Errorf("unmarshal step: %w", err), startedAt, 1)
		return
	}

	outputDir := task.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(w.cfg.OutputDir, task.RunID)
	}
	stepOutputDir := filepath.Join(outputDir, step.Name)
	if err := os.MkdirAll(stepOutputDir, 0755); err != nil {
		w.report(task.ID, "failed", err, startedAt, 1)
		return
	}

	// 로컬 로그 파일 (fallback)
	logFile := localLogFile(stepOutputDir, step.Name)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	stdoutW := newLogWriter("stdout", io.MultiWriter(os.Stdout, fileOrNil(logFile)))
	stderrW := newLogWriter("stderr", io.MultiWriter(os.Stderr, fileOrNil(logFile)))

	cfg := executor.ExecConfig{
		WorkDir:   task.WorkDir,
		InputDir:  outputDir,
		OutputDir: stepOutputDir,
		Params:    step.Params,
		SourceCfg: w.cfg.SourceCfg,
		Stdout:    stdoutW,
		Stderr:    stderrW,
	}

	exec := executor.New(&step)
	err := exec.Execute(ctx, &step, cfg)

	// 실행 후 수집된 로그 전송
	allLogs := append(stdoutW.lines, stderrW.lines...)
	w.sendLogs(task.RunID, task.StepName, allLogs)

	if err != nil {
		slog.Error("task failed", "task_id", task.ID, "err", err)
		w.report(task.ID, "failed", err, startedAt, 1)
		return
	}

	slog.Info("task done", "task_id", task.ID)
	w.report(task.ID, "done", nil, startedAt, 1)
}

func (w *Worker) report(taskID, status string, execErr error, startedAt time.Time, attempts int) {
	result := proto.TaskResult{
		TaskID:    taskID,
		Status:    status,
		StartedAt: startedAt,
		EndedAt:   time.Now(),
		Attempts:  attempts,
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}

	data, _ := json.Marshal(result)
	url := fmt.Sprintf("%s/api/tasks/%s/%s", w.cfg.MasterURL, taskID, status)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		slog.Error("report error", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	w.setAuth(req)

	resp, err := w.client.Do(req)
	if err != nil {
		slog.Error("report error", "err", err)
		return
	}
	_ = resp.Body.Close()
}

func (w *Worker) setAuth(req *http.Request) {
	if w.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	}
}

func (w *Worker) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(w.cfg.PollInterval):
	}
}
