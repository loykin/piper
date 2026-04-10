// Package runner는 piper task 실행의 공통 로직을 제공한다.
//
// worker(폴링)와 agent(K8s one-shot) 모두 이 패키지를 사용한다.
// 차이는 task를 어떻게 받느냐뿐 — 실행 로직은 동일하다.
//
//	task 받기 → S3 input 다운 → 커맨드 실행 → S3 output 업 → 보고
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

// Config는 Runner 설정.
type Config struct {
	MasterURL string
	Token     string
	OutputDir string // 로컬 출력 루트 디렉토리
	InputDir  string // 로컬 입력 루트 디렉토리

	// S3 아티팩트 스토어 (비어 있으면 로컬 파일시스템만 사용)
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
}

// Runner는 단일 task를 실행한다.
type Runner struct {
	cfg    Config
	client *http.Client
	s3     *minio.Client // nil이면 S3 비사용
}

// New는 Runner를 생성한다.
func New(cfg Config) (*Runner, error) {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./piper-outputs"
	}
	if cfg.InputDir == "" {
		cfg.InputDir = cfg.OutputDir // 기본: 같은 디렉토리 (단일 머신)
	}

	r := &Runner{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}

	if cfg.S3Endpoint != "" && cfg.S3Bucket != "" {
		s3, err := newS3Client(cfg)
		if err != nil {
			return nil, fmt.Errorf("s3 client: %w", err)
		}
		r.s3 = s3
	}

	return r, nil
}

// Run은 task를 실행하고 master에 결과를 보고한다.
func (r *Runner) Run(ctx context.Context, task *proto.Task) {
	startedAt := time.Now()

	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		r.reportFailed(task, fmt.Errorf("unmarshal step: %w", err), startedAt)
		return
	}

	stepOutputDir := filepath.Join(r.cfg.OutputDir, task.RunID, step.Name)
	if err := os.MkdirAll(stepOutputDir, 0755); err != nil {
		r.reportFailed(task, err, startedAt)
		return
	}

	// S3에서 입력 아티팩트 다운로드
	if r.s3 != nil && len(step.Inputs) > 0 {
		if err := r.downloadInputs(ctx, task.RunID, step.Inputs); err != nil {
			slog.Error("download inputs failed", "task_id", task.ID, "err", err)
			r.reportFailed(task, err, startedAt)
			return
		}
	}

	// 로컬 로그 파일 (fallback)
	logFile := openLogFile(stepOutputDir, step.Name)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	// 로그 수집기
	logger := newBatchLogger(r, task.RunID, task.StepName, logFile)

	// 커맨드 실행
	execErr := r.execute(ctx, &step, task, stepOutputDir, logger)

	// 수집된 로그 전송
	logger.flush(ctx)

	// S3에 출력 아티팩트 업로드 (성공 시)
	if execErr == nil && r.s3 != nil && len(step.Outputs) > 0 {
		if err := r.uploadOutputs(ctx, task.RunID, step.Name, stepOutputDir, step.Outputs); err != nil {
			slog.Error("upload outputs failed", "task_id", task.ID, "err", err)
			execErr = err
		}
	}

	if execErr != nil {
		r.reportFailed(task, execErr, startedAt)
		return
	}
	r.reportDone(task, startedAt)
}

// ─── 커맨드 실행 ──────────────────────────────────────────────────────────────

func (r *Runner) execute(
	ctx context.Context,
	step *pipeline.Step,
	task *proto.Task,
	outputDir string,
	logger *batchLogger,
) error {
	if len(step.Run.Command) == 0 {
		return fmt.Errorf("step %q: no command specified", step.Name)
	}

	c := exec.CommandContext(ctx, step.Run.Command[0], step.Run.Command[1:]...)
	c.Env = append(os.Environ(),
		"PIPER_OUTPUT_DIR="+outputDir,
		"PIPER_INPUT_DIR="+filepath.Join(r.cfg.InputDir, task.RunID),
		"PIPER_RUN_ID="+task.RunID,
		"PIPER_STEP_NAME="+step.Name,
	)
	c.Dir = task.WorkDir

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := c.StderrPipe()
	if err != nil {
		return err
	}

	if err := c.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pipeLines(stdoutPipe, "stdout", os.Stdout, logger)
	}()
	go func() {
		defer wg.Done()
		pipeLines(stderrPipe, "stderr", os.Stderr, logger)
	}()
	wg.Wait()

	return c.Wait()
}

func pipeLines(r io.Reader, stream string, tee io.Writer, logger *batchLogger) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(tee, line)
		logger.append(stream, line)
	}
}

// ─── 배치 로그 ────────────────────────────────────────────────────────────────

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

func newBatchLogger(r *Runner, runID, stepName string, file *os.File) *batchLogger {
	return &batchLogger{r: r, runID: runID, stepName: stepName, file: file}
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

// ─── 보고 ─────────────────────────────────────────────────────────────────────

type taskResult struct {
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Attempts  int       `json:"attempts"`
}

func (r *Runner) reportDone(task *proto.Task, startedAt time.Time) {
	r.report(task.ID, "done", taskResult{StartedAt: startedAt, EndedAt: time.Now(), Attempts: 1})
}

func (r *Runner) reportFailed(task *proto.Task, err error, startedAt time.Time) {
	slog.Error("task failed", "task_id", task.ID, "err", err)
	r.report(task.ID, "failed", taskResult{
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

// ─── S3 아티팩트 ──────────────────────────────────────────────────────────────

func newS3Client(cfg Config) (*minio.Client, error) {
	endpoint := cfg.S3Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	return minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		Secure: cfg.S3UseSSL,
	})
}

// downloadInputs는 step 입력 아티팩트를 S3에서 로컬로 다운로드한다.
// S3 키: {runID}/{fromStep}/{artifactName}/
// 로컬:  {inputDir}/{runID}/{artifactName}/
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
		prefix := fmt.Sprintf("%s/%s/%s/", runID, fromStep, fromArtifact)
		destDir := filepath.Join(r.cfg.InputDir, runID, art.Name)

		if err := s3DownloadPrefix(ctx, r.s3, r.cfg.S3Bucket, prefix, destDir); err != nil {
			return fmt.Errorf("download %q: %w", art.Name, err)
		}
		slog.Info("artifact downloaded", "name", art.Name, "prefix", prefix)
	}
	return nil
}

// uploadOutputs는 step 출력 아티팩트를 S3에 업로드한다.
// 로컬:  {outputDir}/{artifact.Path}
// S3 키: {runID}/{stepName}/{artifactName}/
func (r *Runner) uploadOutputs(ctx context.Context, runID, stepName, outputDir string, outputs []pipeline.Artifact) error {
	for _, art := range outputs {
		if art.Path == "" {
			continue
		}
		localPath := filepath.Join(outputDir, art.Path)
		prefix := fmt.Sprintf("%s/%s/%s/", runID, stepName, art.Name)

		if err := s3UploadPath(ctx, r.s3, r.cfg.S3Bucket, localPath, prefix); err != nil {
			return fmt.Errorf("upload %q: %w", art.Name, err)
		}
		slog.Info("artifact uploaded", "name", art.Name, "prefix", prefix)
	}
	return nil
}

func s3DownloadPrefix(ctx context.Context, client *minio.Client, bucket, prefix, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		rel := strings.TrimPrefix(obj.Key, prefix)
		dest := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := client.FGetObject(ctx, bucket, obj.Key, dest, minio.GetObjectOptions{}); err != nil {
			return fmt.Errorf("get %q: %w", obj.Key, err)
		}
	}
	return nil
}

func s3UploadPath(ctx context.Context, client *minio.Client, bucket, localPath, s3Prefix string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		_, err := client.FPutObject(ctx, bucket, s3Prefix+info.Name(), localPath, minio.PutObjectOptions{})
		return err
	}
	return filepath.Walk(localPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(localPath, path)
		_, err = client.FPutObject(ctx, bucket, s3Prefix+rel, path, minio.PutObjectOptions{})
		return err
	})
}

// ─── 유틸 ─────────────────────────────────────────────────────────────────────

func openLogFile(outputDir, stepName string) *os.File {
	path := filepath.Join(outputDir, stepName+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}
