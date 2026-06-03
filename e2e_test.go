//go:build e2e

package piper

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	_ "modernc.org/sqlite"

	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
	servingworker "github.com/piper/piper/pkg/workers/baremetal/serving"
)

const e2eBucket = "piper-e2e"

// newE2EServer starts an in-process piper server and returns the Piper instance
// and the test HTTP server. Both are closed/stopped via t.Cleanup.
//
// An in-memory SQLite DB (single connection) is used to avoid SQLITE_BUSY
// contention between concurrent goroutines across sequential tests.
func newE2EServer(t *testing.T) (*Piper, *httptest.Server) {
	t.Helper()

	// in-memory SQLite with a single connection avoids WAL file locking between tests
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	p, err := New(Config{
		OutputDir: t.TempDir(),
		DB:        db,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentAddr := startE2EAgentServer(t, p)
	p.cfg.Server.AgentAddr = agentAddr
	srv := httptest.NewServer(p.Handler(nil))
	t.Cleanup(func() {
		srv.Close()
		_ = p.Close()
	})
	return p, srv
}

func startE2EAgentServer(t *testing.T, p *Piper) string {
	t.Helper()
	port := freeE2EPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	grpcSrv := p.grpcAgentServer.GRPCServer()
	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			t.Logf("grpc agent server stopped: %v", err)
		}
	}()
	t.Cleanup(grpcSrv.GracefulStop)
	return addr
}

// startE2EWorker starts a worker goroutine connected to masterURL.
// The worker is stopped when the test ends via t.Cleanup.
func startE2EWorker(t *testing.T, masterURL string, extra ...func(*worker.Config)) {
	t.Helper()
	cfg := worker.Config{
		MasterURL:    masterURL,
		OutputDir:    t.TempDir(),
		PollInterval: 50 * time.Millisecond,
		Concurrency:  2,
	}
	for _, f := range extra {
		f(&cfg)
	}
	w, err := worker.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = w.Run(ctx) }()
}

func startE2EServingWorker(t *testing.T, httpURL, agentAddr, id string) {
	t.Helper()
	w := servingworker.New(servingworker.Config{
		AgentAddr: agentAddr,
		Hostname:  id,
		ID:        id,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- w.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("serving worker failed to start: %v", err)
			}
			t.Fatalf("serving worker exited before registering")
		default:
		}
		resp, err := http.Get(httpURL + "/api/agents")
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
	t.Fatalf("agent %q did not register within %s", id, 5*time.Second)
}

func freeE2EPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func waitE2EAgent(t *testing.T, serverURL, id string, timeout time.Duration) {
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

func findE2EAgentByCapability(t *testing.T, serverURL, capability string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + "/api/agents")
		if err == nil {
			var agents []struct {
				ID           string   `json:"id"`
				Capabilities []string `json:"capabilities"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&agents)
			resp.Body.Close()
			for _, agent := range agents {
				for _, got := range agent.Capabilities {
					if got == capability {
						return agent.ID
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	resp, err := http.Get(serverURL + "/api/agents")
	if err == nil {
		defer resp.Body.Close()
		var agents []struct {
			ID           string   `json:"id"`
			Capabilities []string `json:"capabilities"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&agents)
		t.Fatalf("no agent with capability %q: %+v", capability, agents)
	}
	t.Fatalf("no agent with capability %q", capability)
	return ""
}

// postRun submits a pipeline YAML to the server and returns the run ID.
func postRun(t *testing.T, serverURL, yaml string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": yaml})
	resp, err := http.Post(serverURL+"/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /runs status = %d: %s", resp.StatusCode, body)
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

// waitRunStatus polls GET /runs/{id} until run.status matches wantStatus or timeout.
func waitRunStatus(t *testing.T, serverURL, runID, wantStatus string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + "/runs/" + runID)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Run struct {
				Status string `json:"status"`
			} `json:"run"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if result.Run.Status == wantStatus {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach status %q within %s", runID, wantStatus, timeout)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestE2E_WorkerExecutesPipeline verifies the full polling worker flow:
// server receives a pipeline, worker picks it up, executes it, and reports back.
func TestE2E_WorkerExecutesPipeline(t *testing.T) {
	_, srv := newE2EServer(t)
	startE2EWorker(t, srv.URL)

	yaml := `
metadata:
  name: e2e-hello
spec:
  steps:
    - name: hello
      run:
        command: ["echo", "hello-from-e2e"]
    - name: world
      run:
        command: ["echo", "world"]
      depends_on: [hello]
`
	runID := postRun(t, srv.URL, yaml)
	waitRunStatus(t, srv.URL, runID, "success", 15*time.Second)
}

func TestE2E_BareMetalWorkerModePlacement(t *testing.T) {
	db, err := sql.Open("sqlite", "file:e2e_baremetal_worker?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	serverPort := freeE2EPort(t)
	p, err := New(Config{
		OutputDir: t.TempDir(),
		DB:        db,
		Server: ServerConfig{
			Addr: fmt.Sprintf("127.0.0.1:%d", serverPort),
		},
		Serving: ServingConfig{
			Worker: true,
		},
		NotebookK8s: NotebookK8sConfig{
			Worker: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentAddr := startE2EAgentServer(t, p)
	p.cfg.Server.AgentAddr = agentAddr
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", serverPort))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(p.Handler(nil))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		_ = p.Close()
	})

	startE2EWorker(t, srv.URL)
	startE2EServingWorker(t, srv.URL, p.cfg.Server.AgentAddr, "serving-agent")
	pipelineAgentID := findE2EAgentByCapability(t, srv.URL, "pipeline")

	runID := postRun(t, srv.URL, fmt.Sprintf(`
metadata:
  name: e2e-baremetal-worker
spec:
  placement:
    worker: %s
  steps:
    - name: hello
      run:
        command: ["echo", "baremetal-worker-ok"]
`, pipelineAgentID))
	waitRunStatus(t, srv.URL, runID, "success", 15*time.Second)

	postService(t, srv.URL, `
apiVersion: piper/v1
kind: ModelService
metadata:
  name: baremetal-worker-service
spec:
  model:
    from_uri: file:///tmp/model
  runtime:
    worker: serving-agent
    command: ["sleep", "60"]
    port: 18080
`)
	t.Cleanup(func() { _ = p.StopService(context.Background(), "baremetal-worker-service") })
	svc := getE2EService(t, srv.URL, "baremetal-worker-service")
	if svc.WorkerID != "serving-agent" {
		t.Fatalf("service worker_id = %q, want serving-agent", svc.WorkerID)
	}
}

// TestE2E_RunCancellation verifies that canceling a run via the API propagates
// through the SSE event stream to the worker, which stops the in-flight task.
func TestE2E_RunCancellation(t *testing.T) {
	_, srv := newE2EServer(t)
	startE2EWorker(t, srv.URL)

	yaml := `
metadata:
  name: e2e-cancel
spec:
  steps:
    - name: slow
      run:
        command: ["sleep", "60"]
`
	runID := postRun(t, srv.URL, yaml)

	// Wait until the task is picked up (status = running)
	waitRunStatus(t, srv.URL, runID, "running", 10*time.Second)

	// Cancel via API
	resp, err := http.Post(srv.URL+"/runs/"+runID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cancel status = %d", resp.StatusCode)
	}

	waitRunStatus(t, srv.URL, runID, "canceled", 10*time.Second)
}

// TestE2E_StepMetrics verifies that PIPER_METRIC lines printed by a step are
// parsed and stored, and retrievable via GET /runs/{id}/metrics.
func TestE2E_StepMetrics(t *testing.T) {
	_, srv := newE2EServer(t)
	startE2EWorker(t, srv.URL)

	yaml := `
metadata:
  name: e2e-metrics
spec:
  steps:
    - name: train
      run:
        command: ["sh", "-c", "echo 'PIPER_METRIC loss=0.312' && echo 'PIPER_METRIC accuracy=0.95'"]
`
	runID := postRun(t, srv.URL, yaml)
	waitRunStatus(t, srv.URL, runID, "success", 15*time.Second)

	resp, err := http.Get(fmt.Sprintf("%s/runs/%s/metrics?step=train", srv.URL, runID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d", resp.StatusCode)
	}

	var metrics []struct {
		Key   string  `json:"key"`
		Value float64 `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		t.Fatal(err)
	}
	found := map[string]float64{}
	for _, m := range metrics {
		found[m.Key] = m.Value
	}
	if found["loss"] != 0.312 {
		t.Errorf("loss = %v, want 0.312", found["loss"])
	}
	if found["accuracy"] != 0.95 {
		t.Errorf("accuracy = %v, want 0.95", found["accuracy"])
	}
}

// TestE2E_WorkerS3Artifacts verifies that a worker configured with S3 credentials
// uploads step output artifacts to the fake S3 store after execution.
func TestE2E_WorkerS3Artifacts(t *testing.T) {
	// Start fake S3
	faker := gofakes3.New(s3mem.New())
	fakeSrv := httptest.NewServer(faker.Server())
	t.Cleanup(fakeSrv.Close)

	s3Client := newE2ES3Client(t, fakeSrv.URL)
	_, err := s3Client.CreateBucket(context.Background(), &s3sdk.CreateBucketInput{
		Bucket: aws.String(e2eBucket),
	})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	_, srv := newE2EServer(t)
	startE2EWorker(t, srv.URL, func(cfg *worker.Config) {
		cfg.StorageURL = fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test", e2eBucket, fakeSrv.URL)
	})

	yaml := `
metadata:
  name: e2e-s3
spec:
  steps:
    - name: produce
      run:
        command: ["sh", "-c", "echo model-weights > $PIPER_OUTPUT_DIR/weights.bin"]
      outputs:
        - name: model
          path: weights.bin
`
	runID := postRun(t, srv.URL, yaml)
	waitRunStatus(t, srv.URL, runID, "success", 15*time.Second)

	// Verify object exists in fake S3 at key {runID}/produce/model/weights.bin
	prefix := fmt.Sprintf("%s/produce/model/", runID)
	out, err := s3Client.ListObjectsV2(context.Background(), &s3sdk.ListObjectsV2Input{
		Bucket: aws.String(e2eBucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(out.Contents) == 0 {
		t.Fatalf("no S3 objects found under prefix %q", prefix)
	}
}

// TestE2E_MultiStepParallel verifies that independent steps run in parallel
// and dependent steps wait for their prerequisites.
func TestE2E_MultiStepParallel(t *testing.T) {
	_, srv := newE2EServer(t)
	startE2EWorker(t, srv.URL)

	yaml := `
metadata:
  name: e2e-parallel
spec:
  steps:
    - name: fetch-data
      run:
        command: ["echo", "fetching"]
    - name: preprocess
      run:
        command: ["echo", "preprocessing"]
      depends_on: [fetch-data]
    - name: train-a
      run:
        command: ["echo", "train-a"]
      depends_on: [preprocess]
    - name: train-b
      run:
        command: ["echo", "train-b"]
      depends_on: [preprocess]
    - name: evaluate
      run:
        command: ["echo", "evaluating"]
      depends_on: [train-a, train-b]
`
	runID := postRun(t, srv.URL, yaml)
	waitRunStatus(t, srv.URL, runID, "success", 20*time.Second)
}

// newE2ES3Client creates an AWS SDK v2 S3 client pointing at the fake S3 server.
func newE2ES3Client(t *testing.T, endpoint string) *s3sdk.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		config.WithRegion("us-east-1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return s3sdk.NewFromConfig(cfg, func(o *s3sdk.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

// newE2EServerWithDir starts an in-process piper server using the given outputDir.
// This allows the server and worker to share the same artifact directory so that
// the local artifact resolver can find files produced by the worker.
//
// Each call uses a unique in-memory SQLite database identified by t.Name() to
// avoid collisions between parallel or sequential tests.
func newE2EServerWithDir(t *testing.T, outputDir string) (*Piper, *httptest.Server) {
	t.Helper()

	// Sanitize t.Name(): SQLite URI names cannot contain '/' characters.
	safeName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:e2e_%s?mode=memory&cache=shared", safeName)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Single connection prevents SQLITE_BUSY under concurrent goroutines.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	p, err := New(Config{
		OutputDir: outputDir,
		DB:        db,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentAddr := startE2EAgentServer(t, p)
	p.cfg.Server.AgentAddr = agentAddr
	srv := httptest.NewServer(p.Handler(nil))
	t.Cleanup(func() {
		srv.Close()
		_ = p.Close()
	})
	return p, srv
}

func newE2EServerWithDirAndStorage(t *testing.T, outputDir, storageURL string) (*Piper, *httptest.Server) {
	t.Helper()

	safeName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name() + "-storage")
	dsn := fmt.Sprintf("file:e2e_%s?mode=memory&cache=shared", safeName)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	p, err := New(Config{
		OutputDir: outputDir,
		DB:        db,
		Storage:   StorageConfig{URL: storageURL},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentAddr := startE2EAgentServer(t, p)
	p.cfg.Server.AgentAddr = agentAddr
	srv := httptest.NewServer(p.Handler(nil))
	t.Cleanup(func() {
		srv.Close()
		_ = p.Close()
	})
	return p, srv
}

// postService submits a ModelService YAML to POST /serving.
func postService(t *testing.T, serverURL, yaml string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": yaml})
	resp, err := http.Post(serverURL+"/serving", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /serving status = %d: %s", resp.StatusCode, b)
	}
}

// waitServiceRunID polls GET /serving/{name} until the returned run_id matches
// wantRunID or the timeout expires.
func waitServiceRunID(t *testing.T, serverURL, serviceName, wantRunID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + "/serving/" + serviceName)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var svc struct {
			RunID string `json:"run_id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&svc)
		resp.Body.Close()
		if svc.RunID == wantRunID {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("service %q run_id did not become %q within %s", serviceName, wantRunID, timeout)
}

func getE2EService(t *testing.T, serverURL, serviceName string) struct {
	WorkerID string `json:"worker_id"`
} {
	t.Helper()
	resp, err := http.Get(serverURL + "/serving/" + serviceName)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /serving/%s status = %d: %s", serviceName, resp.StatusCode, b)
	}
	var svc struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&svc); err != nil {
		t.Fatal(err)
	}
	return svc
}

// TestE2E_OnSuccessDeployTriggersRedeploy verifies that the on_success.deploy hook
// updates the service record's run_id after a successful pipeline run.
//
// Flow:
//  1. A shared outputDir is used by both server and worker so that the local
//     artifact resolver can find step outputs.
//  2. A tiny httptest.Server acts as the "service process" on the resolved port
//     so that waitReady() succeeds immediately, avoiding the 10-second timeout.
//  3. The pipeline is run twice. After each successful run, handleRunSuccess
//     calls DeployService and sets service.RunID to the current run's ID.
//  4. After the second run we poll until service.RunID == secondRunID.
func TestE2E_OnSuccessDeployTriggersRedeploy(t *testing.T) {
	const (
		pipelineName = "e2e-on-success-pipeline"
		serviceName  = "e2e-on-success-predictor"
		stepName     = "train"
		artifactName = "model"
	)

	// Shared output directory for server and worker.
	outputDir := t.TempDir()

	faker := gofakes3.New(s3mem.New())
	fakeSrv := httptest.NewServer(faker.Server())
	t.Cleanup(fakeSrv.Close)
	s3Client := newE2ES3Client(t, fakeSrv.URL)
	if _, err := s3Client.CreateBucket(context.Background(), &s3sdk.CreateBucketInput{Bucket: aws.String(e2eBucket)}); err != nil {
		t.Fatal(err)
	}
	storageURL := fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test", e2eBucket, fakeSrv.URL)

	piperInst, srv := newE2EServerWithDirAndStorage(t, outputDir, storageURL)
	t.Cleanup(func() {
		_ = piperInst.StopService(context.Background(), serviceName)
	})

	startE2EServingWorker(t, srv.URL, piperInst.cfg.Server.AgentAddr, "fake-serving-worker")

	// Worker shares the same storage backend as the server.
	startE2EWorker(t, srv.URL, func(cfg *worker.Config) {
		cfg.OutputDir = outputDir
		cfg.StorageURL = storageURL
	})

	// Pipeline YAML with on_success.deploy wired to our service.
	pipelineYAML := fmt.Sprintf(`
metadata:
  name: %s
spec:
  steps:
    - name: %s
      run:
        command: ["sh", "-c", "mkdir -p $PIPER_OUTPUT_DIR && echo weights > $PIPER_OUTPUT_DIR/weights.bin"]
      outputs:
        - name: %s
          path: weights.bin
  on_success:
    deploy:
      service: %s
`, pipelineName, stepName, artifactName, serviceName)

	// Service YAML pointing at an artifact from our pipeline.
	// The initial deploy uses run: latest; since no run exists yet for this pipeline,
	// we bootstrap it with from_uri so the first POST /serving succeeds without
	// needing a pre-existing run. Subsequent auto-deploys will use from_artifact.
	serviceYAML := fmt.Sprintf(`
apiVersion: v1
kind: ModelService
metadata:
  name: %s
spec:
  model:
    from_artifact:
      pipeline: %s
      step: %s
      artifact: %s
      run: "latest"
  runtime:
    command: ["sleep", "60"]
    port: 18080
`, serviceName, pipelineName, stepName, artifactName)

	// ----- First run: creates the initial successful run record -----
	firstRunID := postRun(t, srv.URL, pipelineYAML)
	waitRunStatus(t, srv.URL, firstRunID, "success", 30*time.Second)

	// Now the pipeline has a successful run: deploy the service manually.
	// This is necessary so that handleRunSuccess (triggered by the second run)
	// can find an existing service record with a stored YAML.
	postService(t, srv.URL, serviceYAML)

	// Verify the service now records the first run's ID.
	waitServiceRunID(t, srv.URL, serviceName, firstRunID, 15*time.Second)

	// ----- Second run: triggers on_success.deploy → updates service.run_id -----
	secondRunID := postRun(t, srv.URL, pipelineYAML)
	waitRunStatus(t, srv.URL, secondRunID, "success", 30*time.Second)

	// handleRunSuccess fires asynchronously; poll until service.RunID reflects
	// the second run.
	waitServiceRunID(t, srv.URL, serviceName, secondRunID, 20*time.Second)
}
