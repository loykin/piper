// piper-agent는 K8s Pod 내부에서 실행되는 step 실행 에이전트다.
//
// 역할:
//  1. S3에서 입력 아티팩트 다운로드
//  2. step 커맨드 실행 (stdout/stderr 캡처)
//  3. 로그를 piper server로 배치 전송
//  4. S3에 출력 아티팩트 업로드
//  5. piper server에 done/failed 보고
//
// K8s Job에서 entrypoint로 사용:
//
//	/piper-tools/piper-agent exec \
//	  --master=http://piper:8080 \
//	  --task-id=run-123:prep \
//	  --run-id=run-123 \
//	  --step-name=prep \
//	  --step=<base64 JSON> \
//	  -- python train.py
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "piper-agent",
		Short: "piper agent — K8s Pod 내부 step 실행기",
	}
	root.AddCommand(newExecCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

type execFlags struct {
	master    string
	token     string
	taskID    string
	runID     string
	stepName  string
	stepB64   string
	outputDir string
	inputDir  string
	// S3
	s3Endpoint  string
	s3AccessKey string
	s3SecretKey string
	s3Bucket    string
	s3UseSSL    bool
}

func newExecCmd() *cobra.Command {
	var f execFlags

	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command...>",
		Short: "step을 실행하고 결과를 master에 보고한다",
		// '--' 뒤의 args를 허용
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(), f, args)
		},
	}

	cmd.Flags().StringVar(&f.master, "master", "", "piper server URL (필수)")
	cmd.Flags().StringVar(&f.token, "token", "", "인증 토큰")
	cmd.Flags().StringVar(&f.taskID, "task-id", "", "task ID")
	cmd.Flags().StringVar(&f.runID, "run-id", "", "run ID")
	cmd.Flags().StringVar(&f.stepName, "step-name", "", "step 이름")
	cmd.Flags().StringVar(&f.stepB64, "step", "", "base64 인코딩된 pipeline.Step JSON")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", "/piper-outputs", "로컬 출력 디렉토리")
	cmd.Flags().StringVar(&f.inputDir, "input-dir", "/piper-inputs", "로컬 입력 디렉토리")
	cmd.Flags().StringVar(&f.s3Endpoint, "s3-endpoint", "", "S3 엔드포인트")
	cmd.Flags().StringVar(&f.s3AccessKey, "s3-access-key", "", "S3 액세스 키")
	cmd.Flags().StringVar(&f.s3SecretKey, "s3-secret-key", "", "S3 시크릿 키")
	cmd.Flags().StringVar(&f.s3Bucket, "s3-bucket", "", "S3 버킷")
	cmd.Flags().BoolVar(&f.s3UseSSL, "s3-use-ssl", false, "S3 SSL 사용")

	return cmd
}

func runExec(ctx context.Context, f execFlags, cmdArgs []string) error {
	startedAt := time.Now()

	// step JSON 디코딩
	var step pipeline.Step
	if f.stepB64 != "" {
		b, err := base64.StdEncoding.DecodeString(f.stepB64)
		if err != nil {
			return fmt.Errorf("decode step: %w", err)
		}
		if err := json.Unmarshal(b, &step); err != nil {
			return fmt.Errorf("unmarshal step: %w", err)
		}
	}

	// S3 클라이언트 초기화 (S3 설정이 있을 때만)
	var s3Client *minio.Client
	if f.s3Endpoint != "" && f.s3Bucket != "" {
		var err error
		s3Client, err = newS3Client(f)
		if err != nil {
			return reportFailed(f, f.taskID, err.Error(), startedAt)
		}
	}

	// 입력 아티팩트 S3 다운로드
	if s3Client != nil && len(step.Inputs) > 0 {
		if err := downloadInputs(ctx, s3Client, f, step.Inputs); err != nil {
			slog.Error("download inputs failed", "err", err)
			return reportFailed(f, f.taskID, err.Error(), startedAt)
		}
	}

	// 출력 디렉토리 생성
	if err := os.MkdirAll(f.outputDir, 0755); err != nil {
		return reportFailed(f, f.taskID, err.Error(), startedAt)
	}

	// 실행할 커맨드 결정: flags '--' 뒤 args 또는 step.Run.Command
	command := cmdArgs
	if len(command) == 0 {
		command = step.Run.Command
	}
	if len(command) == 0 {
		err := fmt.Errorf("no command specified")
		return reportFailed(f, f.taskID, err.Error(), startedAt)
	}

	// 커맨드 실행 (로그 캡처)
	logger := newBatchLogger(f)
	execErr := runCommand(ctx, command, f, logger)

	// 수집된 로그 전송
	logger.flush(ctx)

	// 출력 아티팩트 S3 업로드 (커맨드 성공 시)
	if execErr == nil && s3Client != nil && len(step.Outputs) > 0 {
		if err := uploadOutputs(ctx, s3Client, f, step.Outputs); err != nil {
			slog.Error("upload outputs failed", "err", err)
			execErr = err
		}
	}

	if execErr != nil {
		return reportFailed(f, f.taskID, execErr.Error(), startedAt)
	}
	return reportDone(f, f.taskID, startedAt)
}

// ─── 커맨드 실행 ──────────────────────────────────────────────────────────────

func runCommand(ctx context.Context, command []string, f execFlags, logger *batchLogger) error {
	c := exec.CommandContext(ctx, command[0], command[1:]...)
	c.Env = append(os.Environ(),
		"PIPER_OUTPUT_DIR="+f.outputDir,
		"PIPER_INPUT_DIR="+f.inputDir,
		"PIPER_RUN_ID="+f.runID,
		"PIPER_STEP_NAME="+f.stepName,
	)

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
		scanLines(stdoutPipe, "stdout", os.Stdout, logger)
	}()
	go func() {
		defer wg.Done()
		scanLines(stderrPipe, "stderr", os.Stderr, logger)
	}()
	wg.Wait()

	return c.Wait()
}

func scanLines(r io.Reader, stream string, tee io.Writer, logger *batchLogger) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(tee, line)
		logger.append(stream, line)
	}
}

// ─── 배치 로그 전송 ───────────────────────────────────────────────────────────

type logEntry struct {
	Ts     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

type batchLogger struct {
	mu      sync.Mutex
	entries []logEntry
	f       execFlags
	client  *http.Client
}

func newBatchLogger(f execFlags) *batchLogger {
	return &batchLogger{
		f:      f,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (l *batchLogger) append(stream, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{Ts: time.Now(), Stream: stream, Line: line})
}

func (l *batchLogger) flush(ctx context.Context) {
	l.mu.Lock()
	entries := l.entries
	l.entries = nil
	l.mu.Unlock()

	if len(entries) == 0 {
		return
	}

	url := fmt.Sprintf("%s/runs/%s/steps/%s/logs", l.f.master, l.f.runID, l.f.stepName)
	data, err := json.Marshal(entries)
	if err != nil {
		slog.Warn("marshal logs failed", "err", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		slog.Warn("log flush request error", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if l.f.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.f.token)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		slog.Warn("log flush error", "err", err)
		return
	}
	_ = resp.Body.Close()
}

// ─── S3 아티팩트 ──────────────────────────────────────────────────────────────

func newS3Client(f execFlags) (*minio.Client, error) {
	endpoint := f.s3Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(f.s3AccessKey, f.s3SecretKey, ""),
		Secure: f.s3UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	return client, nil
}

// downloadInputs는 step의 입력 아티팩트를 S3에서 로컬로 다운로드한다.
//
// S3 키 형식: {runID}/{fromStep}/{artifactName}/{filename}
// 로컬 경로:  {inputDir}/{artifact.Name}/{filename}
func downloadInputs(ctx context.Context, client *minio.Client, f execFlags, inputs []pipeline.Artifact) error {
	for _, art := range inputs {
		if art.From == "" {
			continue
		}
		// "stepName/artifactName" 형식 파싱
		parts := strings.SplitN(art.From, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("input artifact %q: invalid from %q (expected stepName/artifactName)", art.Name, art.From)
		}
		fromStep, fromArtifact := parts[0], parts[1]
		prefix := fmt.Sprintf("%s/%s/%s/", f.runID, fromStep, fromArtifact)
		destDir := filepath.Join(f.inputDir, art.Name)

		if err := s3DownloadPrefix(ctx, client, f.s3Bucket, prefix, destDir); err != nil {
			return fmt.Errorf("download input %q from %q: %w", art.Name, prefix, err)
		}
		slog.Info("input downloaded", "artifact", art.Name, "from", prefix, "dest", destDir)
	}
	return nil
}

// uploadOutputs는 step의 출력 아티팩트를 S3에 업로드한다.
//
// 로컬 경로:  {outputDir}/{artifact.Path}
// S3 키 형식: {runID}/{stepName}/{artifactName}/{filename}
func uploadOutputs(ctx context.Context, client *minio.Client, f execFlags, outputs []pipeline.Artifact) error {
	for _, art := range outputs {
		if art.Path == "" {
			continue
		}
		localPath := filepath.Join(f.outputDir, art.Path)
		prefix := fmt.Sprintf("%s/%s/%s/", f.runID, f.stepName, art.Name)

		if err := s3UploadPath(ctx, client, f.s3Bucket, localPath, prefix); err != nil {
			return fmt.Errorf("upload output %q: %w", art.Name, err)
		}
		slog.Info("output uploaded", "artifact", art.Name, "path", localPath, "s3_prefix", prefix)
	}
	return nil
}

// s3DownloadPrefix는 S3 prefix 하위 모든 객체를 destDir로 다운로드한다.
func s3DownloadPrefix(ctx context.Context, client *minio.Client, bucket, prefix, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		// prefix 제거 후 로컬 경로 계산
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

// s3UploadPath는 로컬 경로(파일 또는 디렉토리)를 S3 prefix에 업로드한다.
func s3UploadPath(ctx context.Context, client *minio.Client, bucket, localPath, s3Prefix string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		key := s3Prefix + info.Name()
		_, err := client.FPutObject(ctx, bucket, key, localPath, minio.PutObjectOptions{})
		return err
	}

	// 디렉토리: 재귀 업로드
	return filepath.Walk(localPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(localPath, path)
		key := s3Prefix + rel
		_, err = client.FPutObject(ctx, bucket, key, path, minio.PutObjectOptions{})
		return err
	})
}

// ─── master 보고 ──────────────────────────────────────────────────────────────

type taskResult struct {
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Attempts  int       `json:"attempts"`
}

func reportDone(f execFlags, id string, startedAt time.Time) error {
	return report(f, id, "done", taskResult{StartedAt: startedAt, EndedAt: time.Now(), Attempts: 1})
}

func reportFailed(f execFlags, id, errMsg string, startedAt time.Time) error {
	_ = report(f, id, "failed", taskResult{Error: errMsg, StartedAt: startedAt, EndedAt: time.Now(), Attempts: 1})
	return fmt.Errorf("%s", errMsg)
}

func report(f execFlags, id, status string, result taskResult) error {
	if f.master == "" || id == "" {
		return nil // standalone 테스트 모드
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/tasks/%s/%s", f.master, id, status)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if f.token != "" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("report error", "status", status, "err", err)
		return err
	}
	_ = resp.Body.Close()
	slog.Info("reported", "task_id", id, "status", status)
	return nil
}
