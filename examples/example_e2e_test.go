//go:build example_e2e

// Package examples contains end-to-end tests that compile and run the example
// programs to verify they work correctly.
//
// Run with:
//
//	go test -tags=example_e2e -v -timeout=120s ./examples/
package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freePort returns a random available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// buildBinary compiles the Go package at pkgPath into a temp binary and returns its path.
func buildBinary(t *testing.T, pkgPath string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkgPath))
	cmd := exec.Command("go", "build", "-o", bin, pkgPath)
	cmd.Dir = filepath.Join(moduleRoot(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", pkgPath, err, out)
	}
	return bin
}

// moduleRoot returns the module root directory (where go.mod lives).
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// waitReady polls url until it responds 200 (or 404) within timeout.
func waitReady(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready within %s", url, timeout)
}

// ─── Library example ─────────────────────────────────────────────────────────

func TestExampleLibrary(t *testing.T) {
	bin := buildBinary(t, "./examples/library")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("library example failed:\n%s", out)
	}
	if !strings.Contains(string(out), "pipeline finished:") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if strings.Contains(string(out), "failed=true") {
		t.Fatalf("pipeline reported failure:\n%s", out)
	}
	t.Logf("output:\n%s", out)
}

// ─── Bare-metal server + worker example ──────────────────────────────────────

const bareMetalPipeline = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-bare-metal
spec:
  steps:
  - name: hello
    run:
      type: command
      command: ["echo", "bare-metal e2e ok"]
`

func TestExampleBareMetalServerWorker(t *testing.T) {
	// Build binaries once.
	serverBin := buildBinary(t, "./examples/bare-metal/server")
	workerBin := buildBinary(t, "./examples/bare-metal/worker")

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	baseURL := "http://" + addr

	tmpDir := t.TempDir()

	// Start server.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	serverCmd := exec.CommandContext(ctx, serverBin,
		"--addr="+":"+fmt.Sprint(port),
	)
	serverCmd.Dir = tmpDir
	serverCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	serverCmd.Stdout = os.Stdout
	serverCmd.Stderr = os.Stderr
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = serverCmd.Process.Kill() }()

	waitReady(t, baseURL+"/runs", 15*time.Second)

	// Start worker.
	workerOutDir := filepath.Join(tmpDir, "worker-outputs")
	workerCmd := exec.CommandContext(ctx, workerBin,
		"--master="+baseURL,
		"--concurrency=2",
	)
	workerCmd.Dir = tmpDir
	workerCmd.Env = append(os.Environ(),
		"HOME="+tmpDir,
		// Worker writes output to a temp dir inside HOME by default.
	)
	workerCmd.Stdout = os.Stdout
	workerCmd.Stderr = os.Stderr
	if err := workerCmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	defer func() { _ = workerCmd.Process.Kill() }()
	_ = workerOutDir // referenced for clarity

	// Wait for worker to register.
	time.Sleep(500 * time.Millisecond)

	// Submit pipeline.
	body, _ := json.Marshal(map[string]string{"yaml": bareMetalPipeline})
	resp, err := http.Post(baseURL+"/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /runs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /runs returned %d", resp.StatusCode)
	}

	var runResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	runID := runResp.RunID
	if runID == "" {
		t.Fatal("run ID is empty")
	}
	t.Logf("submitted run %s", runID)

	// Poll until success or timeout.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(fmt.Sprintf("%s/runs/%s", baseURL, runID))
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var body struct {
			Run struct {
				Status string `json:"status"`
			} `json:"run"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		r.Body.Close()

		switch body.Run.Status {
		case "success":
			t.Logf("run %s succeeded", runID)
			return
		case "failed", "canceled":
			t.Fatalf("run %s ended with status=%s", runID, body.Run.Status)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("run %s did not complete within timeout", runID)
}
