//go:build k8s_e2e

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
	"strings"
	"testing"
	"time"
)

// TestK8sE2E_SingleStepJobReportsSuccess is a real Kubernetes smoke test for
// the server -> launcher -> Job -> agent exec -> server report path.
//
// Prerequisites:
//
//	make docker IMAGE=piper/piper:e2e
//	kind load docker-image piper/piper:e2e
//	go test -tags=k8s_e2e -run TestK8sE2E_SingleStepJobReportsSuccess -v .
//
// Set PIPER_K8S_E2E_IMAGE to use a different image tag.
func TestK8sE2E_SingleStepJobReportsSuccess(t *testing.T) {
	requireKubectlCluster(t)

	image := os.Getenv("PIPER_K8S_E2E_IMAGE")
	if image == "" {
		image = "piper/piper:e2e"
	}
	ns := fmt.Sprintf("piper-e2e-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	kubectl(t, "create", "namespace", ns)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = kubectlContext(cleanupCtx, nil, "delete", "namespace", ns, "--ignore-not-found=true")
	})

	applyK8sE2EManifests(t, ns, image)
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/seaweedfs", "--timeout=60s")
	kubectl(t, "-n", ns, "wait", "job/s3-setup", "--for=condition=complete", "--timeout=90s")

	localPort := freeK8sE2EPort(t)
	pfCtx, pfCancel := context.WithCancel(ctx)
	defer pfCancel()
	pf := exec.CommandContext(pfCtx, "kubectl", "-n", ns, "port-forward", "svc/piper-server", fmt.Sprintf("%d:8080", localPort))
	var pfOutput bytes.Buffer
	pf.Stdout = &pfOutput
	pf.Stderr = &pfOutput
	if err := pf.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	t.Cleanup(func() {
		pfCancel()
		_ = pf.Wait()
	})

	serverURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	waitK8sE2EHTTP(t, serverURL+"/health", 30*time.Second)

	runID := k8sE2EPostRun(t, serverURL, fmt.Sprintf(`
metadata:
  name: k8s-e2e-smoke
spec:
  defaults:
    image: alpine:3.20
  steps:
    - name: smoke
      run:
        command: ["sh", "-c", "echo k8s-e2e-ok"]
`))

	if !waitK8sE2ERunStatus(t, serverURL, runID, "success", 2*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("run %s did not reach success", runID)
	}
}

// TestK8sE2E_ExamplePipelines runs the example YAML files against a real K8s cluster.
// MinIO is deployed per-namespace to support artifact-passing examples.
func TestK8sE2E_ExamplePipelines(t *testing.T) {
	requireKubectlCluster(t)

	image := os.Getenv("PIPER_K8S_E2E_IMAGE")
	if image == "" {
		image = "piper/piper:e2e"
	}

	cases := []struct {
		name       string
		yamlFile   string
		wantStatus string
	}{
		{
			name:       "basics/simple",
			yamlFile:   "examples/basics/simple.yaml",
			wantStatus: "success",
		},
		{
			name:       "basics/parallel",
			yamlFile:   "examples/basics/parallel.yaml",
			wantStatus: "success",
		},
		{
			name:       "basics/retry",
			yamlFile:   "examples/basics/retry.yaml",
			wantStatus: "failed", // intentionally always fails
		},
		{
			name:       "artifacts",
			yamlFile:   "examples/artifacts/pipeline.yaml",
			wantStatus: "success",
		},
		{
			name:       "kubernetes/multi-image",
			yamlFile:   "examples/kubernetes/pipeline.yaml",
			wantStatus: "success",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yamlBytes, err := os.ReadFile(tc.yamlFile)
			if err != nil {
				t.Fatalf("read %s: %v", tc.yamlFile, err)
			}

			ns := fmt.Sprintf("piper-e2e-%d", time.Now().UnixNano())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			kubectl(t, "create", "namespace", ns)
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
				defer cleanupCancel()
				_, _ = kubectlContext(cleanupCtx, nil, "delete", "namespace", ns, "--ignore-not-found=true")
			})

			applyK8sE2EManifests(t, ns, image)
			kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
			kubectl(t, "-n", ns, "rollout", "status", "deployment/seaweedfs", "--timeout=60s")
			kubectl(t, "-n", ns, "wait", "job/s3-setup", "--for=condition=complete", "--timeout=90s")

			localPort := freeK8sE2EPort(t)
			pfCtx, pfCancel := context.WithCancel(ctx)
			defer pfCancel()
			pf := exec.CommandContext(pfCtx, "kubectl", "-n", ns, "port-forward", "svc/piper-server", fmt.Sprintf("%d:8080", localPort))
			pf.Stdout = os.Stderr
			pf.Stderr = os.Stderr
			if err := pf.Start(); err != nil {
				t.Fatalf("start port-forward: %v", err)
			}
			t.Cleanup(func() { pfCancel(); _ = pf.Wait() })

			serverURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
			waitK8sE2EHTTP(t, serverURL+"/health", 30*time.Second)

			runID := k8sE2EPostRun(t, serverURL, string(yamlBytes))
			t.Logf("submitted run %s (want=%s)", runID, tc.wantStatus)

			if !waitK8sE2ERunStatus(t, serverURL, runID, tc.wantStatus, 2*time.Minute) {
				dumpK8sE2EDebug(t, ns)
				t.Fatalf("run %s did not reach %q", runID, tc.wantStatus)
			}
			t.Logf("run %s reached %q", runID, tc.wantStatus)
		})
	}
}

func requireKubectlCluster(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not found; skipping k8s_e2e")
	}
	if out, err := exec.Command("kubectl", "cluster-info").CombinedOutput(); err != nil {
		t.Skipf("kubectl cannot reach a cluster; skipping k8s_e2e: %v\n%s", err, out)
	}
}

func applyK8sE2EManifests(t *testing.T, ns, image string) {
	t.Helper()
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: piper-server
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: piper-server
  namespace: %[1]s
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "delete", "get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: piper-server
  namespace: %[1]s
subjects:
  - kind: ServiceAccount
    name: piper-server
    namespace: %[1]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: piper-server
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: piper-config
  namespace: %[1]s
data:
  piper.yaml: |
    run:
      output_dir: /tmp/piper-outputs
      retries: 0
    server:
      addr: :8080
    source:
      s3:
        endpoint: seaweedfs:9000
        access_key: anyadmin
        secret_key: anypassword
        bucket: piper-artifacts
        use_ssl: false
    k8s:
      agent_image: %[2]q
      agent_image_pull_policy: IfNotPresent
      namespace: %[1]s
      in_cluster: true
      master_url: http://piper-server.%[1]s.svc.cluster.local:8080
      default_image: alpine:3.20
      ttl_after_finished: 60
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: piper-server
  namespace: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: piper-server
  template:
    metadata:
      labels:
        app: piper-server
    spec:
      serviceAccountName: piper-server
      containers:
        - name: piper
          image: %[2]q
          imagePullPolicy: IfNotPresent
          args: ["server", "--config", "/etc/piper/piper.yaml", "--addr", ":8080"]
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/piper
      volumes:
        - name: config
          configMap:
            name: piper-config
---
apiVersion: v1
kind: Service
metadata:
  name: piper-server
  namespace: %[1]s
spec:
  selector:
    app: piper-server
  ports:
    - name: http
      port: 8080
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: seaweedfs
  namespace: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: seaweedfs
  template:
    metadata:
      labels:
        app: seaweedfs
    spec:
      containers:
        - name: seaweedfs
          image: chrislusf/seaweedfs:latest
          args: ["server", "-dir=/data", "-s3", "-s3.port=9000"]
          ports:
            - containerPort: 9000
---
apiVersion: v1
kind: Service
metadata:
  name: seaweedfs
  namespace: %[1]s
spec:
  selector:
    app: seaweedfs
  ports:
    - name: s3
      port: 9000
      targetPort: 9000
---
apiVersion: batch/v1
kind: Job
metadata:
  name: s3-setup
  namespace: %[1]s
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: awscli
          image: amazon/aws-cli:latest
          command:
            - /bin/sh
            - -c
            - |
              until aws s3api create-bucket --bucket piper-artifacts \
                --endpoint-url http://seaweedfs:9000 2>/dev/null; do
                sleep 2
              done
          env:
            - name: AWS_ACCESS_KEY_ID
              value: anyadmin
            - name: AWS_SECRET_ACCESS_KEY
              value: anypassword
            - name: AWS_DEFAULT_REGION
              value: us-east-1
`, ns, image)
	kubectlInput(t, manifest, "apply", "-f", "-")
}

func kubectl(t *testing.T, args ...string) string {
	t.Helper()
	out, err := kubectlContext(context.Background(), nil, args...)
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func kubectlInput(t *testing.T, input string, args ...string) string {
	t.Helper()
	out, err := kubectlContext(context.Background(), strings.NewReader(input), args...)
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func kubectlContext(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func freeK8sE2EPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func waitK8sE2EHTTP(t *testing.T, url string, timeout time.Duration) {
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
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s not ready within %s", url, timeout)
}

func k8sE2EPostRun(t *testing.T, serverURL, pipelineYAML string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": pipelineYAML})
	resp, err := http.Post(serverURL+"/runs", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /runs status=%d body=%s", resp.StatusCode, b)
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

func waitK8sE2ERunStatus(t *testing.T, serverURL, runID, want string, timeout time.Duration) bool {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/runs/" + runID) //nolint:noctx
		if err == nil {
			var result struct {
				Run struct {
					Status string `json:"status"`
				} `json:"run"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if result.Run.Status == want {
				return true
			}
			if result.Run.Status == "failed" || result.Run.Status == "canceled" {
				t.Logf("run reached terminal status %q", result.Run.Status)
				return false
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func dumpK8sE2EDebug(t *testing.T, ns string) {
	t.Helper()
	for _, args := range [][]string{
		{"-n", ns, "get", "pods,jobs", "-o", "wide"},
		{"-n", ns, "describe", "pods"},
		{"-n", ns, "logs", "-l", "app=piper-server", "--tail=200"},
		{"-n", ns, "logs", "-l", "app.kubernetes.io/managed-by=piper", "--all-containers=true", "--tail=200"},
	} {
		out, err := kubectlContext(context.Background(), nil, args...)
		if err != nil {
			t.Logf("kubectl %s failed: %v\n%s", strings.Join(args, " "), err, out)
			continue
		}
		t.Logf("kubectl %s\n%s", strings.Join(args, " "), out)
	}
}

// ── Notebook K8s E2E ─────────────────────────────────────────────────────────

// TestK8sE2E_NotebookLifecycle verifies the full notebook lifecycle against a
// real K8s cluster: create → running → stop → restart → delete → purge PVC.
//
// Prerequisites (in addition to TestK8sE2E_SingleStepJobReportsSuccess):
//
//	docker pull jupyter/minimal-notebook:latest
//	kind load docker-image jupyter/minimal-notebook:latest
//
// Set PIPER_K8S_E2E_NOTEBOOK_IMAGE to override the Jupyter image.
func TestK8sE2E_NotebookLifecycle(t *testing.T) {
	requireKubectlCluster(t)

	piperImage := os.Getenv("PIPER_K8S_E2E_IMAGE")
	if piperImage == "" {
		piperImage = "piper/piper:e2e"
	}
	nbImage := os.Getenv("PIPER_K8S_E2E_NOTEBOOK_IMAGE")
	if nbImage == "" {
		nbImage = "jupyter/minimal-notebook:latest"
	}

	ns := fmt.Sprintf("piper-nb-e2e-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	kubectl(t, "create", "namespace", ns)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = kubectlContext(cleanupCtx, nil, "delete", "namespace", ns, "--ignore-not-found=true")
	})

	applyK8sE2ENotebookManifests(t, ns, piperImage, nbImage)
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/seaweedfs", "--timeout=60s")
	kubectl(t, "-n", ns, "wait", "job/s3-setup", "--for=condition=complete", "--timeout=90s")

	localPort := freeK8sE2EPort(t)
	pfCtx, pfCancel := context.WithCancel(ctx)
	defer pfCancel()
	pf := exec.CommandContext(pfCtx, "kubectl", "-n", ns, "port-forward", "svc/piper-server", fmt.Sprintf("%d:8080", localPort))
	pf.Stdout = os.Stderr
	pf.Stderr = os.Stderr
	if err := pf.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	t.Cleanup(func() { pfCancel(); _ = pf.Wait() })

	serverURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	waitK8sE2EHTTP(t, serverURL+"/health", 30*time.Second)

	const nbName = "e2e-notebook"
	nbYAML := fmt.Sprintf("metadata:\n  name: %s\nspec:\n  image: %s\n  storage_size: 1Gi\n", nbName, nbImage)

	k8sE2EPostNotebook(t, serverURL, nbYAML, "")
	t.Logf("notebook %s created, waiting for running...", nbName)
	if !waitK8sE2ENotebookStatus(t, serverURL, nbName, "running", 8*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("notebook %s did not reach running", nbName)
	}
	t.Logf("notebook %s is running", nbName)

	nb := k8sE2EGetNotebook(t, serverURL, nbName)
	if ep, _ := nb["endpoint"].(string); ep == "" {
		t.Errorf("notebook endpoint is empty: %v", nb)
	}
	volID, _ := nb["volume_id"].(string)
	if volID == "" {
		t.Fatal("notebook volume_id is empty")
	}

	// Stop.
	k8sE2ENotebookAction(t, serverURL, nbName, "stop")
	if !waitK8sE2ENotebookStatus(t, serverURL, nbName, "stopped", 2*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("notebook %s did not reach stopped", nbName)
	}
	t.Logf("notebook %s stopped", nbName)

	// Restart.
	k8sE2ENotebookAction(t, serverURL, nbName, "start")
	if !waitK8sE2ENotebookStatus(t, serverURL, nbName, "running", 5*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("notebook %s did not reach running after restart", nbName)
	}
	t.Logf("notebook %s restarted", nbName)

	// Delete (volume becomes released).
	k8sE2EDeleteNotebook(t, serverURL, nbName)
	t.Logf("notebook %s deleted, purging volume %s...", nbName, volID)

	// Purge volume (deletes PVC).
	k8sE2EPurgeVolume(t, serverURL, volID)
	t.Logf("volume %s purged", volID)

	time.Sleep(3 * time.Second)
	out, _ := kubectlContext(ctx, nil, "-n", ns, "get", "pvc", "--no-headers")
	if strings.Contains(out, "piper-nb-vol") {
		t.Errorf("PVC not cleaned up:\n%s", out)
	}
}

// TestK8sE2E_NotebookVolumeReuse creates a notebook, deletes it (volume released),
// then creates a second notebook reusing the same PVC.
func TestK8sE2E_NotebookVolumeReuse(t *testing.T) {
	requireKubectlCluster(t)

	piperImage := os.Getenv("PIPER_K8S_E2E_IMAGE")
	if piperImage == "" {
		piperImage = "piper/piper:e2e"
	}
	nbImage := os.Getenv("PIPER_K8S_E2E_NOTEBOOK_IMAGE")
	if nbImage == "" {
		nbImage = "jupyter/minimal-notebook:latest"
	}

	ns := fmt.Sprintf("piper-nb-e2e-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	kubectl(t, "create", "namespace", ns)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = kubectlContext(cleanupCtx, nil, "delete", "namespace", ns, "--ignore-not-found=true")
	})

	applyK8sE2ENotebookManifests(t, ns, piperImage, nbImage)
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/seaweedfs", "--timeout=60s")
	kubectl(t, "-n", ns, "wait", "job/s3-setup", "--for=condition=complete", "--timeout=90s")

	localPort := freeK8sE2EPort(t)
	pfCtx, pfCancel := context.WithCancel(ctx)
	defer pfCancel()
	pf := exec.CommandContext(pfCtx, "kubectl", "-n", ns, "port-forward", "svc/piper-server", fmt.Sprintf("%d:8080", localPort))
	pf.Stdout = os.Stderr
	pf.Stderr = os.Stderr
	if err := pf.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	t.Cleanup(func() { pfCancel(); _ = pf.Wait() })

	serverURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	waitK8sE2EHTTP(t, serverURL+"/health", 30*time.Second)

	nb1YAML := fmt.Sprintf("metadata:\n  name: e2e-nb-1\nspec:\n  image: %s\n  storage_size: 1Gi\n", nbImage)
	k8sE2EPostNotebook(t, serverURL, nb1YAML, "")
	if !waitK8sE2ENotebookStatus(t, serverURL, "e2e-nb-1", "running", 8*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("first notebook did not reach running")
	}
	nb1 := k8sE2EGetNotebook(t, serverURL, "e2e-nb-1")
	volID, _ := nb1["volume_id"].(string)
	if volID == "" {
		t.Fatal("volume_id is empty")
	}
	t.Logf("first notebook running, volume_id=%s", volID)

	// Verify PVC exists.
	out := kubectl(t, "-n", ns, "get", "pvc", "--no-headers")
	if !strings.Contains(out, "piper-nb-vol") {
		t.Errorf("expected PVC to exist, got:\n%s", out)
	}

	// Delete first notebook — volume becomes released, PVC stays.
	k8sE2EDeleteNotebook(t, serverURL, "e2e-nb-1")
	t.Logf("first notebook deleted, volume %s released", volID)

	// Second notebook reuses the same PVC.
	nb2YAML := fmt.Sprintf("metadata:\n  name: e2e-nb-2\nspec:\n  image: %s\n", nbImage)
	k8sE2EPostNotebook(t, serverURL, nb2YAML, volID)
	if !waitK8sE2ENotebookStatus(t, serverURL, "e2e-nb-2", "running", 5*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("second notebook (volume reuse) did not reach running")
	}
	t.Logf("second notebook running with reused volume %s", volID)

	// Cleanup.
	k8sE2EDeleteNotebook(t, serverURL, "e2e-nb-2")
	k8sE2EPurgeVolume(t, serverURL, volID)
}

// ── Notebook helpers ──────────────────────────────────────────────────────────

func k8sE2EPostNotebook(t *testing.T, serverURL, notebookYAML, volumeID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": notebookYAML, "volume_id": volumeID})
	resp, err := http.Post(serverURL+"/notebooks", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /notebooks status=%d: %s", resp.StatusCode, b)
	}
}

func waitK8sE2ENotebookStatus(t *testing.T, serverURL, name, want string, timeout time.Duration) bool {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/notebooks/" + name) //nolint:noctx
		if err == nil {
			var result map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if status, _ := result["status"].(string); status == want {
				return true
			} else if status == "failed" {
				t.Logf("notebook %s reached terminal status 'failed'", name)
				return false
			}
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func k8sE2EGetNotebook(t *testing.T, serverURL, name string) map[string]any {
	t.Helper()
	resp, err := http.Get(serverURL + "/notebooks/" + name) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func k8sE2ENotebookAction(t *testing.T, serverURL, name, action string) {
	t.Helper()
	resp, err := http.Post(serverURL+"/notebooks/"+name+"/"+action, "application/json", nil) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /notebooks/%s/%s status=%d: %s", name, action, resp.StatusCode, b)
	}
}

func k8sE2EDeleteNotebook(t *testing.T, serverURL, name string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, serverURL+"/notebooks/"+name, nil) //nolint:noctx
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE /notebooks/%s status=%d: %s", name, resp.StatusCode, b)
	}
}

func k8sE2EPurgeVolume(t *testing.T, serverURL, volumeID string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, serverURL+"/notebook-volumes/"+volumeID, nil) //nolint:noctx
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE /notebook-volumes/%s status=%d: %s", volumeID, resp.StatusCode, b)
	}
}

// applyK8sE2ENotebookManifests applies a full piper-server + notebook_k8s manifest
// that includes extended RBAC (StatefulSet, PVC, Service, Pod) and notebook_k8s config.
func applyK8sE2ENotebookManifests(t *testing.T, ns, piperImage, nbImage string) {
	t.Helper()
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: piper-server
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: piper-server
  namespace: %[1]s
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "delete", "get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["statefulsets"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
  - apiGroups: [""]
    resources: ["persistentvolumeclaims"]
    verbs: ["create", "delete", "get", "list", "watch"]
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["create", "delete", "get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: piper-server
  namespace: %[1]s
subjects:
  - kind: ServiceAccount
    name: piper-server
    namespace: %[1]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: piper-server
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: piper-config
  namespace: %[1]s
data:
  piper.yaml: |
    run:
      output_dir: /tmp/piper-outputs
      retries: 0
    server:
      addr: :8080
    source:
      s3:
        endpoint: seaweedfs:9000
        access_key: anyadmin
        secret_key: anypassword
        bucket: piper-artifacts
        use_ssl: false
    k8s:
      agent_image: %[2]q
      agent_image_pull_policy: IfNotPresent
      namespace: %[1]s
      in_cluster: true
      master_url: http://piper-server.%[1]s.svc.cluster.local:8080
      default_image: alpine:3.20
      ttl_after_finished: 60
    notebook_k8s:
      namespace: %[1]s
      worker_image: %[3]q
      storage_size: 1Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: piper-server
  namespace: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: piper-server
  template:
    metadata:
      labels:
        app: piper-server
    spec:
      serviceAccountName: piper-server
      containers:
        - name: piper
          image: %[2]q
          imagePullPolicy: IfNotPresent
          args: ["server", "--config", "/etc/piper/piper.yaml", "--addr", ":8080"]
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/piper
      volumes:
        - name: config
          configMap:
            name: piper-config
---
apiVersion: v1
kind: Service
metadata:
  name: piper-server
  namespace: %[1]s
spec:
  selector:
    app: piper-server
  ports:
    - name: http
      port: 8080
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: seaweedfs
  namespace: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: seaweedfs
  template:
    metadata:
      labels:
        app: seaweedfs
    spec:
      containers:
        - name: seaweedfs
          image: chrislusf/seaweedfs:latest
          args: ["server", "-dir=/data", "-s3", "-s3.port=9000"]
          ports:
            - containerPort: 9000
---
apiVersion: v1
kind: Service
metadata:
  name: seaweedfs
  namespace: %[1]s
spec:
  selector:
    app: seaweedfs
  ports:
    - name: s3
      port: 9000
      targetPort: 9000
---
apiVersion: batch/v1
kind: Job
metadata:
  name: s3-setup
  namespace: %[1]s
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: awscli
          image: amazon/aws-cli:latest
          command:
            - /bin/sh
            - -c
            - |
              until aws s3api create-bucket --bucket piper-artifacts \
                --endpoint-url http://seaweedfs:9000 2>/dev/null; do
                sleep 2
              done
          env:
            - name: AWS_ACCESS_KEY_ID
              value: anyadmin
            - name: AWS_SECRET_ACCESS_KEY
              value: anypassword
            - name: AWS_DEFAULT_REGION
              value: us-east-1
`, ns, piperImage, nbImage)
	kubectlInput(t, manifest, "apply", "-f", "-")
}
