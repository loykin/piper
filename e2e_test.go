//go:build e2e

package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/loykin/piper/internal/testutil"
)

const e2eBucket = "piper-e2e"

// e2eProjectID is the default project used in all e2e tests.
// newE2EServer creates this project automatically.
const e2eProjectID = "e2e"

// e2eBaremetalRuntime returns a Runtime config for direct, in-process
// baremetal execution — every e2e server owns pipeline/notebook/serving
// execution itself now, there is no separate worker to start or wait for.
func e2eBaremetalRuntime(t *testing.T) RuntimeConfig {
	t.Helper()
	return RuntimeConfig{
		Type: RuntimeBaremetal,
		Baremetal: BaremetalRuntimeConfig{
			MetaDir:     filepath.Join(t.TempDir(), "pipeline-meta"),
			Concurrency: 4,
		},
	}
}

// newE2EServer starts an in-process piper server and returns the Piper instance
// and the test HTTP server. Both are closed/stopped via t.Cleanup.
func newE2EServer(t *testing.T) (*Piper, *testutil.Server) {
	t.Helper()

	p, err := New(Config{
		OutputDir: t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "piper.db"),
		Auth:      AuthConfig{Trusted: true},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime:   e2eBaremetalRuntime(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewIPv4Server(t, p.Handler(nil))
	t.Cleanup(func() { _ = p.Close() })

	// Ensure the default e2e project exists before any test runs.
	createE2EProject(t, srv.URL)

	return p, srv
}

// createE2EProject creates the default e2e project via the API.
func createE2EProject(t *testing.T, serverURL string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"id": e2eProjectID, "name": "E2E"})
	resp, err := http.Post(serverURL+"/api/projects", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create e2e project: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create e2e project status = %d: %s", resp.StatusCode, b)
	}
}

// e2eBase returns the project-scoped API base for the default e2e project.
func e2eBase() string { return "/api/projects/" + e2eProjectID }

// postRun submits a pipeline YAML to the server and returns the run ID.
func postRun(t *testing.T, serverURL, yaml string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": yaml})
	url := serverURL + e2eBase() + "/runs"
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
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

func postE2EGenericCredential(t *testing.T, serverURL, name string, data map[string]string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name": name,
		"kind": "generic",
		"data": data,
	})
	resp, err := http.Post(serverURL+e2eBase()+"/credentials", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /credentials: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /credentials status = %d: %s", resp.StatusCode, payload)
	}
}

// waitRunStatus polls GET /projects/:project/runs/{id} until run.status matches wantStatus or timeout.
func waitRunStatus(t *testing.T, serverURL, runID, wantStatus string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + e2eBase() + "/runs/" + runID)
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
	dumpE2ERunLogs(t, serverURL, runID)
	t.Fatalf("run %s did not reach status %q within %s", runID, wantStatus, timeout)
}

func dumpE2ERunLogs(t *testing.T, serverURL, runID string) {
	t.Helper()
	resp, err := http.Get(serverURL + e2eBase() + "/runs/" + runID)
	if err != nil {
		t.Logf("dump run %s: %v", runID, err)
		return
	}
	var result struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
		Steps []struct {
			StepName string `json:"step_name"`
			Status   string `json:"status"`
			Error    string `json:"error"`
		} `json:"steps"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	t.Logf("run %s status=%s steps=%+v", runID, result.Run.Status, result.Steps)
	for _, step := range result.Steps {
		logsResp, err := http.Get(serverURL + e2eBase() + "/runs/" + runID + "/steps/" + step.StepName + "/logs")
		if err != nil {
			t.Logf("step %s logs: %v", step.StepName, err)
			continue
		}
		body, _ := io.ReadAll(logsResp.Body)
		logsResp.Body.Close()
		if len(body) > 0 {
			t.Logf("step %s logs:\n%s", step.StepName, body)
		}
	}
}

func startE2EAuthenticatedGitHTTPRepo(t *testing.T, fileName, content, user, token string) string {
	t.Helper()
	repoDir := t.TempDir()
	runE2EGit(t, repoDir, "init", "-b", "main")
	runE2EGit(t, repoDir, "config", "user.email", "test@test")
	runE2EGit(t, repoDir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repoDir, fileName), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runE2EGit(t, repoDir, "add", fileName)
	runE2EGit(t, repoDir, "commit", "-m", "init")

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runE2EGit(t, "", "clone", "--bare", repoDir, bareDir)

	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotToken, ok := r.BasicAuth()
		if !ok || gotUser != user || gotToken != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		serveE2EGitHTTPBackend(t, w, r, filepath.Dir(bareDir))
	}))
	return srv.URL + "/repo.git"
}

func runE2EGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func serveE2EGitHTTPBackend(t *testing.T, w http.ResponseWriter, r *http.Request, projectRoot string) {
	t.Helper()
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "http-backend")
	cmd.Env = append(os.Environ(),
		"GIT_PROJECT_ROOT="+projectRoot,
		"GIT_HTTP_EXPORT_ALL=1",
		"REQUEST_METHOD="+r.Method,
		"PATH_INFO="+r.URL.Path,
		"QUERY_STRING="+r.URL.RawQuery,
		"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		"CONTENT_LENGTH="+r.Header.Get("Content-Length"),
	)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git http-backend: %v", err)
	}
	header, payload, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		header, payload, ok = bytes.Cut(out, []byte("\n\n"))
	}
	if !ok {
		t.Fatalf("git http-backend returned malformed response: %q", string(out))
	}
	status := http.StatusOK
	for _, line := range bytes.Split(header, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		key := string(bytes.TrimSpace(name))
		val := string(bytes.TrimSpace(value))
		if strings.EqualFold(key, "Status") {
			if strings.HasPrefix(val, "403") {
				status = http.StatusForbidden
			} else if strings.HasPrefix(val, "404") {
				status = http.StatusNotFound
			}
			continue
		}
		w.Header().Add(key, val)
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestE2E_ExecutesPipeline verifies the full direct-runtime flow: the server
// receives a pipeline, executes it in-process, and reports the result back.
func TestE2E_ExecutesPipeline(t *testing.T) {
	_, srv := newE2EServer(t)

	yaml := `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-hello
spec:
  steps:
    - name: hello
      run:
        command: ["sh", "-c", "echo hello-from-e2e; echo 'PIPER_METRIC score=0.5'"]
    - name: world
      run:
        command: ["echo", "world"]
      depends_on: [hello]
`
	runID := postRun(t, srv.URL, yaml)
	waitRunStatus(t, srv.URL, runID, "success", 15*time.Second)

	logsResp, err := http.Get(srv.URL + e2eBase() + "/runs/" + runID + "/steps/hello/logs")
	if err != nil {
		t.Fatal(err)
	}
	logsBody, _ := io.ReadAll(logsResp.Body)
	logsResp.Body.Close()
	if !bytes.Contains(logsBody, []byte("hello-from-e2e")) {
		t.Fatalf("logs missing command output: %s", logsBody)
	}

	metricsResp, err := http.Get(srv.URL + e2eBase() + "/runs/" + runID + "/metrics?step=hello")
	if err != nil {
		t.Fatal(err)
	}
	metricsBody, _ := io.ReadAll(metricsResp.Body)
	metricsResp.Body.Close()
	if !bytes.Contains(metricsBody, []byte(`"key":"score"`)) {
		t.Fatalf("metrics missing score: %s", metricsBody)
	}
}

// TestE2E_GenericCredentialEnvRef verifies options.env credentialRef resolution
// for pipeline steps.
func TestE2E_GenericCredentialEnvRef(t *testing.T) {
	_, srv := newE2EServer(t)
	postE2EGenericCredential(t, srv.URL, "app-env", map[string]string{
		"token": "generic-env-secret",
	})

	runID := postRun(t, srv.URL, `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-generic-env-ref
spec:
  steps:
    - name: check-env
      options:
        env:
          - name: APP_TOKEN
            valueFrom:
              credentialRef:
                name: app-env
                key: token
      run:
        command: ["sh", "-c", "test \"$APP_TOKEN\" = generic-env-secret && echo generic-env-ok"]
`)
	waitRunStatus(t, srv.URL, runID, "success", 15*time.Second)

	logsResp, err := http.Get(srv.URL + e2eBase() + "/runs/" + runID + "/steps/check-env/logs")
	if err != nil {
		t.Fatal(err)
	}
	logsBody, _ := io.ReadAll(logsResp.Body)
	logsResp.Body.Close()
	if !bytes.Contains(logsBody, []byte("generic-env-ok")) {
		t.Fatalf("logs missing env confirmation: %s", logsBody)
	}
}

// TestE2E_RunCancellation verifies that canceling a run via the API propagates
// through to the running step and stops it.
func TestE2E_RunCancellation(t *testing.T) {
	_, srv := newE2EServer(t)

	yaml := `
apiVersion: piper/v1
kind: Pipeline
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
	resp, err := http.Post(srv.URL+e2eBase()+"/runs/"+runID+"/cancel", "application/json", nil)
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

	yaml := `
apiVersion: piper/v1
kind: Pipeline
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

	resp, err := http.Get(fmt.Sprintf("%s%s/runs/%s/metrics?step=train", srv.URL, e2eBase(), runID))
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

// TestE2E_S3Artifacts verifies that a server configured with S3 credentials
// uploads step output artifacts to the fake S3 store after execution.
func TestE2E_S3Artifacts(t *testing.T) {
	// Start fake S3
	faker := gofakes3.New(s3mem.New())
	fakeSrv := testutil.NewIPv4Server(t, faker.Server())

	s3Client := newE2ES3Client(t, fakeSrv.URL)
	_, err := s3Client.CreateBucket(context.Background(), &s3sdk.CreateBucketInput{
		Bucket: aws.String(e2eBucket),
	})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	s3URL := fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test", e2eBucket, fakeSrv.URL)
	_, srv := newE2EServerWithDirAndStorage(t, t.TempDir(), s3URL)

	yaml := `
apiVersion: piper/v1
kind: Pipeline
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

	yaml := `
apiVersion: piper/v1
kind: Pipeline
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
func newE2EServerWithDir(t *testing.T, outputDir string) (*Piper, *testutil.Server) {
	t.Helper()

	p, err := New(Config{
		OutputDir: outputDir,
		DBPath:    filepath.Join(t.TempDir(), "piper.db"),
		Auth:      AuthConfig{Trusted: true},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime:   e2eBaremetalRuntime(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewIPv4Server(t, p.Handler(nil))
	t.Cleanup(func() { _ = p.Close() })
	createE2EProject(t, srv.URL)
	return p, srv
}

func newE2EServerWithDirAndStorage(t *testing.T, outputDir, storageURL string) (*Piper, *testutil.Server) {
	t.Helper()

	p, err := New(Config{
		OutputDir: outputDir,
		DBPath:    filepath.Join(t.TempDir(), "piper.db"),
		Auth:      AuthConfig{Trusted: true},
		Storage:   StorageConfig{URL: storageURL},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime:   e2eBaremetalRuntime(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewIPv4Server(t, p.Handler(nil))
	t.Cleanup(func() { _ = p.Close() })
	createE2EProject(t, srv.URL)
	return p, srv
}

// postService submits a ModelService YAML to POST /services.
func postService(t *testing.T, serverURL, yaml string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": yaml})
	resp, err := http.Post(serverURL+e2eBase()+"/services", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /services status = %d: %s", resp.StatusCode, b)
	}
}

// waitServiceRunID polls GET /services/{name} until the returned run_id matches
// wantRunID or the timeout expires.
func waitServiceRunID(t *testing.T, serverURL, serviceName, wantRunID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serverURL + e2eBase() + "/services/" + serviceName)
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

// TestE2E_OnSuccessDeployTriggersRedeploy verifies that the on_success.deploy hook
// updates the service record's run_id after a successful pipeline run.
//
// Flow:
//  1. A tiny httptest.Server acts as the "service process" on the resolved port
//     so that waitReady() succeeds immediately, avoiding the 10-second timeout.
//  2. The pipeline is run twice. After each successful run, handleRunSuccess
//     calls DeployService and sets service.RunID to the current run's ID.
//  3. After the second run we poll until service.RunID == secondRunID.
func TestE2E_OnSuccessDeployTriggersRedeploy(t *testing.T) {
	const (
		pipelineName = "e2e-on-success-pipeline"
		serviceName  = "e2e-on-success-predictor"
		stepName     = "train"
		artifactName = "model"
	)

	outputDir := t.TempDir()

	faker := gofakes3.New(s3mem.New())
	fakeSrv := testutil.NewIPv4Server(t, faker.Server())
	s3Client := newE2ES3Client(t, fakeSrv.URL)
	if _, err := s3Client.CreateBucket(context.Background(), &s3sdk.CreateBucketInput{Bucket: aws.String(e2eBucket)}); err != nil {
		t.Fatal(err)
	}
	storageURL := fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test", e2eBucket, fakeSrv.URL)

	piperInst, srv := newE2EServerWithDirAndStorage(t, outputDir, storageURL)
	t.Cleanup(func() {
		_ = piperInst.StopService(context.Background(), "", serviceName)
	})

	// Pipeline YAML with on_success.deploy wired to our service.
	pipelineYAML := fmt.Sprintf(`
apiVersion: piper/v1
kind: Pipeline
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
	// we bootstrap it with from_uri so the first POST /services succeeds without
	// needing a pre-existing run. Subsequent auto-deploys will use from_artifact.
	serviceYAML := fmt.Sprintf(`
apiVersion: piper/v1
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
  run:
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
	waitServiceRunID(t, srv.URL, serviceName, secondRunID, 60*time.Second)
}

// TestE2E_ProcessGPUsFlowsToCUDA verifies the full path of driver.process.gpus
// through the baremetal direct runtime to CUDA_VISIBLE_DEVICES inside the
// step process. No real GPU hardware is needed — the test only checks env
// var injection.
func TestE2E_ProcessGPUsFlowsToCUDA(t *testing.T) {
	outputDir := t.TempDir()
	_, srv := newE2EServerWithDir(t, outputDir)

	// The step writes $CUDA_VISIBLE_DEVICES to a file in $PIPER_OUTPUT_DIR.
	// driver.process.gpus should inject "mock-0,mock-1" as CUDA_VISIBLE_DEVICES.
	yaml := `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-gpu-env
spec:
  steps:
    - name: check-cuda
      driver:
        process:
          gpus: "mock-0,mock-1"
      run:
        command: ["sh", "-c", "printf '%s' \"$CUDA_VISIBLE_DEVICES\" > $PIPER_OUTPUT_DIR/cuda.txt"]
      outputs:
        - name: cuda
          path: cuda.txt
`
	runID := postRun(t, srv.URL, yaml)
	waitRunStatus(t, srv.URL, runID, "success", 20*time.Second)

	// Read through the artifact API so the test follows the same path as UI and
	// serving consumers instead of depending on worker-local output layout.
	resp, err := http.Get(srv.URL + e2eBase() + "/runs/" + runID + "/artifacts/check-cuda/cuda/cuda.txt")
	if err != nil {
		t.Fatalf("download cuda artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("download cuda artifact status = %d: %s", resp.StatusCode, body)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read cuda artifact: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "mock-0,mock-1" {
		t.Errorf("CUDA_VISIBLE_DEVICES = %q, want mock-0,mock-1", got)
	}
}

// ─── Git Source E2E Tests ─────────────────────────────────────────────────────

// postE2EGitCredential creates a git credential via the API.
// endpoint is the scheme+host prefix (e.g. "http://127.0.0.1:PORT/") that the
// credential store uses to auto-match repo URLs.
func postE2EGitCredential(t *testing.T, serverURL, name, endpoint, user, token string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":     name,
		"kind":     "git",
		"endpoint": endpoint,
		"data": map[string]string{
			"username": user,
			"token":    token,
		},
	})
	resp, err := http.Post(serverURL+e2eBase()+"/credentials", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /credentials status = %d: %s", resp.StatusCode, b)
	}
}

// repoOrigin extracts the scheme+host prefix from a repo URL, with trailing slash.
// e.g. "http://127.0.0.1:1234/repo.git" → "http://127.0.0.1:1234/"
func repoOrigin(repoURL string) string {
	if i := strings.Index(repoURL, "://"); i >= 0 {
		rest := repoURL[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return repoURL[:i+3+j] + "/"
		}
	}
	return repoURL
}

// TestE2E_GitSourceCredentialRef verifies that a pipeline step with source: git
// and an explicit credentialRef clones the repo and runs the script successfully.
func TestE2E_GitSourceCredentialRef(t *testing.T) {
	_, srv := newE2EServer(t)

	const (
		gitUser  = "git-user"
		gitToken = "git-token"
	)
	repoURL := startE2EAuthenticatedGitHTTPRepo(t, "run.sh", "echo git-connectionref-ok\n", gitUser, gitToken)
	postE2EGitCredential(t, srv.URL, "gitea-cred", repoOrigin(repoURL), gitUser, gitToken)

	runID := postRun(t, srv.URL, fmt.Sprintf(`
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-git-connectionref
spec:
  steps:
    - name: clone-and-run
      run:
        source: git
        repo: %s
        branch: main
        path: run.sh
        credentialRef: gitea-cred
        command: ["sh", "-c", "sh \"$PIPER_SCRIPT_PATH\""]
`, repoURL))
	waitRunStatus(t, srv.URL, runID, "success", 20*time.Second)

	resp, err := http.Get(srv.URL + e2eBase() + "/runs/" + runID + "/steps/clone-and-run/logs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte("git-connectionref-ok")) {
		t.Fatalf("logs missing expected output: %s", body)
	}
}

// TestE2E_GitSourceAutoMatch verifies that when no credentialRef is set, the
// credential store auto-matches by endpoint URL and injects credentials.
func TestE2E_GitSourceAutoMatch(t *testing.T) {
	_, srv := newE2EServer(t)

	const (
		gitUser  = "auto-user"
		gitToken = "auto-token"
	)
	repoURL := startE2EAuthenticatedGitHTTPRepo(t, "run.sh", "echo git-automatch-ok\n", gitUser, gitToken)
	// Register credential with the repo origin — no explicit credentialRef in pipeline.
	postE2EGitCredential(t, srv.URL, "auto-cred", repoOrigin(repoURL), gitUser, gitToken)

	runID := postRun(t, srv.URL, fmt.Sprintf(`
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: e2e-git-automatch
spec:
  steps:
    - name: clone-and-run
      run:
        source: git
        repo: %s
        branch: main
        path: run.sh
        command: ["sh", "-c", "sh \"$PIPER_SCRIPT_PATH\""]
`, repoURL))
	waitRunStatus(t, srv.URL, runID, "success", 20*time.Second)

	resp, err := http.Get(srv.URL + e2eBase() + "/runs/" + runID + "/steps/clone-and-run/logs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte("git-automatch-ok")) {
		t.Fatalf("logs missing expected output: %s", body)
	}
}

// TestE2E_GitSourcePipelineToServing verifies the full path from git source
// pipeline execution through artifact upload to serving deployment.
func TestE2E_GitSourcePipelineToServing(t *testing.T) {
	const serviceName = "e2e-git-serving"

	outputDir := t.TempDir()
	faker := gofakes3.New(s3mem.New())
	fakeSrv := testutil.NewIPv4Server(t, faker.Server())
	s3Client := newE2ES3Client(t, fakeSrv.URL)
	if _, err := s3Client.CreateBucket(context.Background(), &s3sdk.CreateBucketInput{Bucket: aws.String(e2eBucket)}); err != nil {
		t.Fatal(err)
	}
	storageURL := fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&accessKey=test&secretKey=test", e2eBucket, fakeSrv.URL)

	piperInst, srv := newE2EServerWithDirAndStorage(t, outputDir, storageURL)
	t.Cleanup(func() { _ = piperInst.StopService(context.Background(), "", serviceName) })

	const (
		gitUser  = "model-user"
		gitToken = "model-token"
	)
	// The pipeline script writes a model artifact from git source.
	repoURL := startE2EAuthenticatedGitHTTPRepo(t,
		"train.sh",
		"mkdir -p $PIPER_OUTPUT_DIR && echo model-weights-v1 > $PIPER_OUTPUT_DIR/weights.bin\n",
		gitUser, gitToken,
	)
	postE2EGitCredential(t, srv.URL, "model-repo", repoOrigin(repoURL), gitUser, gitToken)

	const pipelineName = "e2e-git-train"
	pipelineYAML := fmt.Sprintf(`
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: %s
spec:
  steps:
    - name: train
      run:
        source: git
        repo: %s
        branch: main
        path: train.sh
        credentialRef: model-repo
        command: ["sh", "-c", "sh \"$PIPER_SCRIPT_PATH\""]
      outputs:
        - name: model
          path: weights.bin
  on_success:
    deploy:
      service: %s
`, pipelineName, repoURL, serviceName)

	serviceYAML := fmt.Sprintf(`
apiVersion: piper/v1
kind: ModelService
metadata:
  name: %s
spec:
  model:
    from_artifact:
      pipeline: %s
      step: train
      artifact: model
      run: "latest"
  run:
    command: ["sleep", "60"]
    port: 18080
`, serviceName, pipelineName)

	firstRunID := postRun(t, srv.URL, pipelineYAML)
	waitRunStatus(t, srv.URL, firstRunID, "success", 30*time.Second)

	postService(t, srv.URL, serviceYAML)
	waitServiceRunID(t, srv.URL, serviceName, firstRunID, 15*time.Second)

	// Second run triggers on_success.deploy → service run_id updates.
	secondRunID := postRun(t, srv.URL, pipelineYAML)
	waitRunStatus(t, srv.URL, secondRunID, "success", 30*time.Second)
	waitServiceRunID(t, srv.URL, serviceName, secondRunID, 30*time.Second)
}
