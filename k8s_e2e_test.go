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
// the server -> k8s-worker -> Job -> agent exec -> server report path.
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

	applyK8sE2EAgentManifests(t, ns, image, "jupyter/minimal-notebook:latest")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-k8s-worker", "--timeout=90s")
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

// TestK8sE2E_ExamplePipelines runs the example YAML files through k8s-worker against a real K8s cluster.
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

			applyK8sE2EAgentManifests(t, ns, image, "jupyter/minimal-notebook:latest")
			kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
			kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-k8s-worker", "--timeout=90s")
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

// TestK8sE2E_WorkerModeWorkloads verifies the outbound k8s-worker path for the
// three K8s-backed workload families: pipeline, serving, and notebook.
func TestK8sE2E_WorkerModeWorkloads(t *testing.T) {
	requireKubectlCluster(t)

	piperImage := os.Getenv("PIPER_K8S_E2E_IMAGE")
	if piperImage == "" {
		piperImage = "piper/piper:e2e"
	}
	nbImage := os.Getenv("PIPER_K8S_E2E_NOTEBOOK_IMAGE")
	if nbImage == "" {
		nbImage = "jupyter/minimal-notebook:latest"
	}

	ns := fmt.Sprintf("piper-agent-e2e-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	kubectl(t, "create", "namespace", ns)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = kubectlContext(cleanupCtx, nil, "delete", "namespace", ns, "--ignore-not-found=true")
	})

	applyK8sE2EAgentManifests(t, ns, piperImage, nbImage)
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-k8s-worker", "--timeout=90s")
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
	waitK8sE2EAgentRegistered(t, serverURL, "agent-e2e", []string{"notebook", "serving", "pipeline"}, 30*time.Second)

	runID := k8sE2EPostRun(t, serverURL, fmt.Sprintf(`
metadata:
  name: k8s-worker-e2e
spec:
  placement:
    cluster: agent-e2e
    namespace: %[1]s
  defaults:
    image: alpine:3.20
  steps:
    - name: smoke
      run:
        command: ["sh", "-c", "echo k8s-worker-e2e-ok"]
`, ns))
	if !waitK8sE2ERunStatus(t, serverURL, runID, "success", 2*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("worker-mode run %s did not reach success", runID)
	}
	if out := kubectl(t, "-n", ns, "get", "jobs", "-l", "app.kubernetes.io/managed-by=piper", "--no-headers"); !strings.Contains(out, "smoke") {
		t.Fatalf("expected worker-created pipeline Job for step 'smoke', got:\n%s", out)
	}

	k8sE2EPostService(t, serverURL, `
apiVersion: piper/v1
kind: ModelService
metadata:
  name: worker-serving
spec:
  model:
    from_uri: s3://piper-artifacts/e2e-model
  runtime:
    image: alpine:3.20
    command: ["sh", "-c", "sleep 3600"]
    port: 8080
`)
	if out := kubectl(t, "-n", ns, "get", "deploy,svc", "worker-serving", "--no-headers"); !strings.Contains(out, "worker-serving") {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("expected worker-created serving Deployment/Service, got:\n%s", out)
	}

	const nbName = "worker-notebook"
	nbYAML := fmt.Sprintf("metadata:\n  name: %s\nspec:\n  k8s:\n    image: %s\n    storage_size: 1Gi\n", nbName, nbImage)
	k8sE2EPostNotebook(t, serverURL, nbYAML, "")
	if !waitK8sE2ENotebookStatus(t, serverURL, nbName, "running", 8*time.Minute) {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("worker-mode notebook %s did not reach running", nbName)
	}
	if out := kubectl(t, "-n", ns, "get", "statefulset,svc,pvc", "--no-headers"); !strings.Contains(out, "piper-nb-worker-notebook") {
		dumpK8sE2EDebug(t, ns)
		t.Fatalf("expected worker-created notebook resources, got:\n%s", out)
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

func applyK8sE2EAgentManifests(t *testing.T, ns, piperImage, nbImage string) {
	t.Helper()
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: piper-server
  namespace: %[1]s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: piper-k8s-worker
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: piper-k8s-worker
  namespace: %[1]s
rules:
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
  - apiGroups: [""]
    resources: ["persistentvolumeclaims", "services"]
    verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: piper-k8s-worker
  namespace: %[1]s
subjects:
  - kind: ServiceAccount
    name: piper-k8s-worker
    namespace: %[1]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: piper-k8s-worker
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
      worker: true
    serving:
      worker: true
    notebook_k8s:
      worker: true
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
          args: ["server", "--config", "/etc/piper/piper.yaml", "--addr", ":8080", "--agent-addr", ":9090"]
          ports:
            - containerPort: 8080
            - containerPort: 9090
          volumeMounts:
            - name: config
              mountPath: /etc/piper
      volumes:
        - name: config
          configMap:
            name: piper-config
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: piper-k8s-worker
  namespace: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: piper-k8s-worker
  template:
    metadata:
      labels:
        app: piper-k8s-worker
    spec:
      serviceAccountName: piper-k8s-worker
      containers:
        - name: agent
          image: %[2]q
          imagePullPolicy: IfNotPresent
          args:
            - k8s-worker
            - --master=http://piper-server.%[1]s.svc.cluster.local:8080
            - --agent-addr=piper-server.%[1]s.svc.cluster.local:9090
            - --cluster=agent-e2e
            - --namespaces=%[1]s
            - --notebook-namespace=%[1]s
            - --serving-namespace=%[1]s
            - --pipeline-namespace=%[1]s
            - --notebook-image=%[3]s
            - --pipeline-worker-image=%[2]s
            - --agent-image-pull-policy=IfNotPresent
            - --default-image=alpine:3.20
            - --s3-endpoint=seaweedfs:9000
            - --s3-access-key=anyadmin
            - --s3-secret-key=anypassword
            - --s3-bucket=piper-artifacts
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
    - name: grpc-agent
      port: 9090
      targetPort: 9090
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
          image: public.ecr.aws/aws-cli/aws-cli:latest
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

func waitK8sE2EAgentRegistered(t *testing.T, serverURL, cluster string, capabilities []string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/api/agents") //nolint:noctx
		if err == nil {
			var agents []struct {
				ClusterName  string   `json:"cluster_name"`
				Capabilities []string `json:"capabilities"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&agents)
			resp.Body.Close()
			for _, agent := range agents {
				if agent.ClusterName == cluster && hasK8sE2ECapabilities(agent.Capabilities, capabilities) {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("agent for cluster %q with capabilities %v not registered within %s", cluster, capabilities, timeout)
}

func hasK8sE2ECapabilities(got, want []string) bool {
	set := make(map[string]bool, len(got))
	for _, capability := range got {
		set[capability] = true
	}
	for _, capability := range want {
		if !set[capability] {
			return false
		}
	}
	return true
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

func k8sE2EPostService(t *testing.T, serverURL, serviceYAML string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"yaml": serviceYAML})
	resp, err := http.Post(serverURL+"/serving", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /serving status=%d body=%s", resp.StatusCode, b)
	}
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
		{"-n", ns, "logs", "-l", "app=piper-k8s-worker", "--tail=200"},
		{"-n", ns, "logs", "deployment/piper-k8s-worker", "--all-containers=true", "--tail=200"},
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

	applyK8sE2EAgentManifests(t, ns, piperImage, nbImage)
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-k8s-worker", "--timeout=90s")
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
	waitK8sE2EAgentRegistered(t, serverURL, "agent-e2e", []string{"notebook"}, 30*time.Second)

	const nbName = "e2e-notebook"
	nbYAML := fmt.Sprintf("metadata:\n  name: %s\nspec:\n  k8s:\n    image: %s\n    storage_size: 1Gi\n", nbName, nbImage)

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

	applyK8sE2EAgentManifests(t, ns, piperImage, nbImage)
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-server", "--timeout=90s")
	kubectl(t, "-n", ns, "rollout", "status", "deployment/piper-k8s-worker", "--timeout=90s")
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
	waitK8sE2EAgentRegistered(t, serverURL, "agent-e2e", []string{"notebook"}, 30*time.Second)

	nb1YAML := fmt.Sprintf("metadata:\n  name: e2e-nb-1\nspec:\n  k8s:\n    image: %s\n    storage_size: 1Gi\n", nbImage)
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
	nb2YAML := fmt.Sprintf("metadata:\n  name: e2e-nb-2\nspec:\n  k8s:\n    image: %s\n", nbImage)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
