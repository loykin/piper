//go:build binary_e2e

// Package piper_test contains binary-level e2e tests that launch the compiled
// piper binary as a subprocess and verify behaviour through the HTTP API.
//
// These tests catch CLI-layer bugs that in-process tests cannot reach — most
// notably any regression where cobra's initConfig() has not yet loaded the
// config file when a command reads config (e.g. S3 credentials).
//
// Pre-requisite: build the binary before running.
//
//	go build -o bin/piper ./cmd/piper
//	go test -tags=binary_e2e -v -timeout=120s .
package piper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/piper/piper/internal/testutil"
)

// requireBinary returns the absolute path of the piper binary and skips the
// test if the binary has not been built yet.
func requireBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p, err := filepath.Abs(filepath.Join(wd, "bin", "piper"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("binary %q not found — build first: go build -o bin/piper ./cmd/piper", p)
	}
	return p
}

// TestBinaryE2E_WorkerS3ConfigFromFile is the primary regression guard for the
// bug where buildConfig() in main.go executed before cobra's initConfig() loaded
// the config file. As a result the piper instance had empty S3 credentials and
// the worker silently skipped all artifact uploads.
//
// The test verifies the full CLI path:
//  1. Write a real piper.yaml with S3 credentials.
//  2. Start `piper server --config piper.yaml` as a subprocess.
//  3. Start `piper worker --config piper.yaml --master …` as a subprocess.
//  4. Submit a pipeline that produces an output artifact.
//  5. Assert that the artifact appears in fake-S3.
//
// If the bug regresses (worker reads S3 from a pre-built piper instance instead
// of from viper in RunE), r.s3 will be nil and step 5 will fail.
func TestBinaryE2E_WorkerS3ConfigFromFile(t *testing.T) {
	binary := requireBinary(t)

	const bucket = "piper-binary-e2e"

	// gofakes3 listens on a real TCP port so the worker subprocess can reach it.
	s3Srv := testutil.NewIPv4Server(t, gofakes3.New(s3mem.New()).Server())

	s3Endpoint := strings.TrimPrefix(s3Srv.URL, "http://")
	s3Client := newBinaryE2ES3Client(t, s3Srv.URL)
	if _, err := s3Client.CreateBucket(context.Background(), &s3sdk.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("create S3 bucket: %v", err)
	}

	// workDir becomes the CWD for both subprocesses.
	// The server's SQLite DB lands at workDir/piper-outputs/piper.db (default).
	workDir := t.TempDir()
	serverPort := freeLocalPort(t)
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", serverPort)

	configPath := filepath.Join(workDir, "piper.yaml")
	writeBinaryE2EFile(t, configPath, fmt.Sprintf(`
source:
  s3:
    endpoint: %q
    access_key: "test"
    secret_key: "test"
    bucket: %q
    use_ssl: false
`, s3Endpoint, bucket))

	// ── Server subprocess ─────────────────────────────────────────────────────
	// Pass --addr explicitly: server.addr in the config file is ignored because
	// buildConfig() runs before initConfig() loads the YAML.
	listenAddr := fmt.Sprintf("127.0.0.1:%d", serverPort)
	srvCmd := exec.Command(binary, "server", "--config", configPath, "--addr", listenAddr)
	srvCmd.Dir = workDir
	srvCmd.Stdout = os.Stdout
	srvCmd.Stderr = os.Stderr
	if err := srvCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = srvCmd.Process.Kill() })

	waitHTTPReady(t, serverURL+"/health", 15*time.Second)

	// ── Worker subprocess ─────────────────────────────────────────────────────
	// --master is required by cobra; S3 credentials come from the config file.
	wrkCmd := exec.Command(binary, "worker", "--config", configPath, "--master", serverURL)
	wrkCmd.Dir = workDir
	wrkCmd.Stdout = os.Stdout
	wrkCmd.Stderr = os.Stderr
	if err := wrkCmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() { _ = wrkCmd.Process.Kill() })

	// ── Submit pipeline ───────────────────────────────────────────────────────
	const pipelineYAML = `
metadata:
  name: binary-e2e
spec:
  steps:
    - name: train
      run:
        command: ["sh", "-c", "printf 'weights' > $PIPER_OUTPUT_DIR/model.bin"]
      outputs:
        - name: model
          path: model.bin
`
	runID := binaryE2EPostRun(t, serverURL, pipelineYAML)
	t.Logf("submitted run: %s", runID)

	binaryE2EWaitStatus(t, serverURL, runID, "success", 30*time.Second)

	// ── Key regression check ──────────────────────────────────────────────────
	// If the S3 config bug regresses, runner.s3 will be nil and no upload occurs.
	prefix := fmt.Sprintf("%s/train/model/", runID)
	out, err := s3Client.ListObjectsV2(context.Background(), &s3sdk.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		t.Fatalf("list S3 objects: %v", err)
	}
	if len(out.Contents) == 0 {
		t.Errorf("no S3 objects found under %q\n"+
			"this indicates the worker did not read S3 config from the config file\n"+
			"(regression: buildConfig() ran before initConfig() loaded the YAML)", prefix)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func writeBinaryE2EFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func waitHTTPReady(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server at %s not ready within %s", url, timeout)
}

func binaryE2EPostRun(t *testing.T, serverURL, pipelineYAML string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": pipelineYAML})
	resp, err := http.Post(serverURL+"/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /runs: status=%d body=%s", resp.StatusCode, b)
	}
	var result struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.RunID == "" {
		t.Fatal("run_id is empty")
	}
	return result.RunID
}

func binaryE2EWaitStatus(t *testing.T, serverURL, runID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + "/runs/" + runID) //nolint:noctx
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var result struct {
			Run struct {
				Status string `json:"status"`
			} `json:"run"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		_ = resp.Body.Close()
		switch result.Run.Status {
		case want:
			return
		case "failed", "canceled":
			t.Fatalf("run %s reached terminal status %q (wanted %q)", runID, result.Run.Status, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach %q within %s", runID, want, timeout)
}

func newBinaryE2ES3Client(t *testing.T, endpoint string) *s3sdk.Client {
	t.Helper()
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		awscfg.WithRegion("us-east-1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return s3sdk.NewFromConfig(cfg, func(o *s3sdk.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}
