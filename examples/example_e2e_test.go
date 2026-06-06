//go:build example_e2e

// Package examples contains end-to-end tests that compile and run the example
// programs to verify they work correctly.
//
// Run with:
//
//	go test -tags=example_e2e -v -timeout=5m ./examples/
package examples

import (
	"bytes"
	"context"
	"database/sql"
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

	_ "modernc.org/sqlite"

	piper "github.com/piper/piper"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/pipeline"
	notebookworker "github.com/piper/piper/pkg/workers/baremetal/notebook"
	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
	"gopkg.in/yaml.v3"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

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

func buildBinary(t *testing.T, pkgPath string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkgPath))
	cmd := exec.Command("go", "build", "-o", bin, pkgPath)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", pkgPath, err, out)
	}
	return bin
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

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

func waitAgentRegistered(t *testing.T, serverURL, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + "/api/agents")
		if err == nil {
			var agents []struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&agents)
			resp.Body.Close()
			for _, agent := range agents {
				if agent.ID == id {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("agent %q did not register within %s", id, timeout)
}

// submitPipeline posts a pipeline YAML to baseURL/runs and returns the run ID.
func submitPipeline(t *testing.T, baseURL, yamlStr string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"yaml": yamlStr})
	resp, err := http.Post(baseURL+"/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /runs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /runs returned %d", resp.StatusCode)
	}
	var result struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode /runs response: %v", err)
	}
	if result.RunID == "" {
		t.Fatal("empty run_id in response")
	}
	return result.RunID
}

// pollRunStatus polls GET baseURL/runs/{id} until the run reaches a terminal
// status ("success", "failed", "canceled"), then returns it.
func pollRunStatus(t *testing.T, baseURL, runID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := http.Get(fmt.Sprintf("%s/runs/%s", baseURL, runID))
		if err != nil {
			time.Sleep(300 * time.Millisecond)
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
		case "success", "failed", "canceled":
			return body.Run.Status
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach terminal status within %s", runID, timeout)
	return ""
}

// startBareMetalFixture builds the server and worker binaries, starts them,
// waits for readiness, and registers t.Cleanup to kill both.
// Returns the server base URL.
func startBareMetalFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	serverBin := buildBinary(t, "./examples/bare-metal/server")
	workerBin := buildBinary(t, "./examples/bare-metal/worker")

	port := freePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	tmpDir := t.TempDir()

	serverCmd := exec.CommandContext(ctx, serverBin, fmt.Sprintf("--addr=:%d", port))
	serverCmd.Dir = tmpDir
	serverCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	serverCmd.Stdout = os.Stdout
	serverCmd.Stderr = os.Stderr
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = serverCmd.Process.Kill() })

	waitReady(t, baseURL+"/runs", 15*time.Second)

	workerCmd := exec.CommandContext(ctx, workerBin,
		"--master="+baseURL,
		"--concurrency=4",
	)
	workerCmd.Dir = tmpDir
	workerCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	workerCmd.Stdout = os.Stdout
	workerCmd.Stderr = os.Stderr
	if err := workerCmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() { _ = workerCmd.Process.Kill() })

	// Allow worker registration to complete.
	time.Sleep(500 * time.Millisecond)
	return baseURL
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

// ─── Bare-metal server + worker: smoke test ───────────────────────────────────

func TestExampleBareMetalServerWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	baseURL := startBareMetalFixture(t, ctx)

	const yaml = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-smoke
spec:
  steps:
  - name: hello
    run:
      type: command
      command: ["echo", "bare-metal e2e ok"]
`
	runID := submitPipeline(t, baseURL, yaml)
	if status := pollRunStatus(t, baseURL, runID, 30*time.Second); status != "success" {
		t.Fatalf("run %s: status=%q, want success", runID, status)
	}
}

// ─── Example pipeline execution via bare-metal server+worker ─────────────────

// TestExamplePipelines_Run submits the example pipelines from basics/ against a
// real bare-metal server+worker and verifies the expected terminal status.
//
// basics/retry.yaml deliberately exits 1 on every attempt, so its expected
// status is "failed" — this verifies that the retry logic exhausts correctly.
func TestExamplePipelines_Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	baseURL := startBareMetalFixture(t, ctx)
	root := moduleRoot(t)

	cases := []struct {
		file          string
		wantStatus    string
		timeoutPerRun time.Duration
	}{
		// simple sequential pipeline
		{"examples/basics/simple.yaml", "success", 30 * time.Second},
		// fan-out / fan-in with parallel steps
		{"examples/basics/parallel.yaml", "success", 60 * time.Second},
		// step always exits 1 — verifies retry exhaustion → failed
		{"examples/basics/retry.yaml", "failed", 90 * time.Second},
	}

	for _, tc := range cases {
		tc := tc
		name := strings.TrimSuffix(filepath.Base(tc.file), ".yaml")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			runID := submitPipeline(t, baseURL, string(data))
			t.Logf("submitted run %s for %s", runID, tc.file)
			status := pollRunStatus(t, baseURL, runID, tc.timeoutPerRun)
			if status != tc.wantStatus {
				t.Errorf("status=%q, want %q", status, tc.wantStatus)
			}
		})
	}
}

// ─── Artifact passing via in-process server + worker ─────────────────────────

// TestExampleArtifacts runs artifacts/pipeline.yaml through a real in-process
// piper server + worker so that the full artifact upload/download path is
// exercised without requiring an external S3 or storage service.
//
// The server's built-in HTTP store (served at /store/) acts as the artifact
// backend.  The worker uploads step outputs there and downloads inputs from it.
func TestExampleArtifacts(t *testing.T) {
	root := moduleRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "examples", "artifacts", "pipeline.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	tmpDir := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(tmpDir, "example-notebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })

	storeDir := filepath.Join(tmpDir, "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}

	p, err := piper.New(piper.Config{
		DB:        db,
		OutputDir: filepath.Join(tmpDir, "server-outputs"),
		Server:    piper.ServerConfig{Addr: fmt.Sprintf(":%d", port)},
		Storage:   piper.StorageConfig{URL: "file://" + storeDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	go func() { _ = p.Serve(ctx, piper.ServeOption{}) }()
	waitReady(t, serverURL+"/runs", 10*time.Second)

	w, err := worker.New(worker.Config{
		MasterURL:   serverURL,
		OutputDir:   filepath.Join(tmpDir, "worker-outputs"),
		StorageURL:  serverURL + "/store",
		Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = w.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)

	runID := submitPipeline(t, serverURL, string(data))
	t.Logf("submitted run %s", runID)

	status := pollRunStatus(t, serverURL, runID, 60*time.Second)
	if status != "success" {
		t.Errorf("run %s: status=%q, want success", runID, status)
	}
}

// ─── Example YAML parsing (static) ───────────────────────────────────────────

// TestExamplePipelineYAMLs parses every Pipeline YAML in the examples tree.
func TestExamplePipelineYAMLs(t *testing.T) {
	root := moduleRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "examples", "*", "pipeline.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no example pipeline.yaml files found")
	}
	for _, f := range files {
		f := f
		t.Run(strings.TrimPrefix(f, root+"/"), func(t *testing.T) {
			if _, err := pipeline.ParseFile(f); err != nil {
				t.Fatalf("ParseFile: %v", err)
			}
		})
	}
}

// TestExampleNotebookServerYAMLs parses every NotebookServer YAML in the examples tree.
func TestExampleNotebookServerYAMLs(t *testing.T) {
	root := moduleRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "examples", "notebook", "server-*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no example server-*.yaml files found")
	}
	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var spec notebook.NotebookServerSpec
			if err := yaml.Unmarshal(data, &spec); err != nil {
				t.Fatalf("yaml.Unmarshal: %v", err)
			}
			if spec.Metadata.Name == "" {
				t.Fatal("metadata.name is empty")
			}
		})
	}
}

// ─── Baremetal notebook lifecycle e2e ────────────────────────────────────────

// TestExampleNotebookBaremetal tests the full notebook lifecycle (create →
// running → stop → restart → running → stop) through the AgentDriver baremetal
// path using an in-process piper server and a real notebook worker.
func TestExampleNotebookBaremetal(t *testing.T) {
	port := freePort(t)
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	tmpDir := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(tmpDir, "example-notebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	p, err := piper.New(piper.Config{
		DB: db,
		Server: piper.ServerConfig{
			Addr:      fmt.Sprintf(":%d", port),
			AgentAddr: fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(func() {
		cancel()
		time.Sleep(500 * time.Millisecond)
		_ = p.Close()
	})

	go func() { _ = p.Serve(ctx, piper.ServeOption{}) }()
	waitReady(t, serverURL+"/runs", 10*time.Second)

	const nbName = "e2e-nb-baremetal"

	nw := notebookworker.New(notebookworker.Config{
		AgentAddr:     p.Config().Server.AgentAddr,
		NotebooksRoot: tmpDir,
		Hostname:      "fake-host",
		ID:            "fake-bm-1",
	})
	go func() { _ = nw.Run(ctx) }()
	waitAgentRegistered(t, serverURL, "fake-bm-1", 10*time.Second)

	// Create notebook with baremetal spec.
	const nbYAML = "metadata:\n  name: " + nbName + "\nspec:\n  process: {}\n"
	nbBody, _ := json.Marshal(map[string]string{"yaml": nbYAML})
	createResp, err := http.Post(serverURL+"/notebooks", "application/json", bytes.NewReader(nbBody))
	if err != nil {
		t.Fatalf("create notebook: %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		t.Fatalf("create notebook: status %d", createResp.StatusCode)
	}
	t.Logf("notebook %s created, waiting for running...", nbName)

	// Poll until running.
	if !pollNotebookStatus(t, serverURL, nbName, "running", 30*time.Second) {
		t.Fatalf("notebook %s did not reach running", nbName)
	}
	t.Logf("notebook %s is running", nbName)

	// Stop the notebook.
	stopResp, err := http.Post(serverURL+"/notebooks/"+nbName+"/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("stop notebook: %v", err)
	}
	stopResp.Body.Close()
	if stopResp.StatusCode >= 300 {
		t.Fatalf("stop notebook: status %d", stopResp.StatusCode)
	}
	t.Logf("notebook %s stop requested, waiting for stopped...", nbName)

	// Poll until stopped.
	if !pollNotebookStatus(t, serverURL, nbName, "stopped", 30*time.Second) {
		t.Fatalf("notebook %s did not reach stopped", nbName)
	}
	t.Logf("notebook %s stopped, restarting...", nbName)

	restartResp, err := http.Post(serverURL+"/notebooks/"+nbName+"/start", "application/json", nil)
	if err != nil {
		t.Fatalf("restart notebook: %v", err)
	}
	restartResp.Body.Close()
	if restartResp.StatusCode >= 300 {
		t.Fatalf("restart notebook: status %d", restartResp.StatusCode)
	}
	if !pollNotebookStatus(t, serverURL, nbName, "running", 30*time.Second) {
		t.Fatalf("notebook %s did not reach running after restart", nbName)
	}

	finalStopResp, err := http.Post(serverURL+"/notebooks/"+nbName+"/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("final stop notebook: %v", err)
	}
	finalStopResp.Body.Close()
	if finalStopResp.StatusCode >= 300 {
		t.Fatalf("final stop notebook: status %d", finalStopResp.StatusCode)
	}
	if !pollNotebookStatus(t, serverURL, nbName, "stopped", 30*time.Second) {
		t.Fatalf("notebook %s did not reach stopped after restart", nbName)
	}
	t.Logf("notebook %s restart lifecycle completed", nbName)
}

// pollNotebookStatus polls GET /notebooks/:name until the notebook reaches the
// desired status or the timeout elapses. Returns true if the target is reached.
func pollNotebookStatus(t *testing.T, baseURL, name, wantStatus string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := http.Get(fmt.Sprintf("%s/notebooks/%s", baseURL, name))
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var nb struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(r.Body).Decode(&nb)
		r.Body.Close()
		if nb.Status == wantStatus {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
