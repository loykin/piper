package notebookworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"

	"github.com/piper/piper/pkg/notebook"
)

func TestDockerRuntimeE2E_StartStopNotebook(t *testing.T) {
	image := os.Getenv("PIPER_NOTEBOOK_DOCKER_E2E_IMAGE")
	if image == "" {
		t.Skip("set PIPER_NOTEBOOK_DOCKER_E2E_IMAGE to run Docker notebook e2e")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cli, err := newDockerClient()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := cli.Ping(ctx, dockerclient.PingOptions{}); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("Docker image %q is not available locally; pull it before running this e2e: %v", image, err)
	}

	rt, err := newDockerRuntimeWithClient(DockerConfig{Image: image, Network: "bridge"}, "docker-e2e-agent", cli)
	if err != nil {
		t.Fatalf("docker runtime: %v", err)
	}
	name := "docker-e2e"
	token := "docker-e2e-token"
	port := freeDockerE2EPort(t)
	workDir := t.TempDir()

	var spec notebook.Notebook
	spec.Metadata.Name = name

	started, err := rt.Start(ctx, RuntimeStartRequest{
		Name:    name,
		Spec:    spec,
		WorkDir: workDir,
		Port:    port,
		Token:   token,
		BaseURL: "/notebooks/" + name + "/proxy/",
	})
	if err != nil {
		t.Fatalf("start docker notebook: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = rt.Stop(stopCtx, name)
	})
	if started.Endpoint != fmt.Sprintf("http://localhost:%d", port) {
		t.Fatalf("endpoint = %q", started.Endpoint)
	}
	waitDockerNotebookStatus(t, started.Endpoint+"/notebooks/"+name+"/proxy/api/status?token="+token)

	if err := rt.Stop(ctx, name); err != nil {
		t.Fatalf("stop docker notebook: %v", err)
	}
	if _, err := os.Stat(workDir); err != nil {
		t.Fatalf("work dir should remain after stop: %v", err)
	}
}

func TestDockerRuntimeE2E_WorkerCrashRecoverRestart(t *testing.T) {
	image := requireDockerNotebookE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	workerID := "docker-crash-e2e-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	name := "docker-crash-recover"
	port := freeDockerE2EPort(t)
	workDir := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "started.json")

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestDockerRuntimeE2E_CrashHelper$")
	cmd.Env = append(os.Environ(),
		"PIPER_DOCKER_E2E_CRASH_HELPER=1",
		"PIPER_DOCKER_E2E_WORKER_ID="+workerID,
		"PIPER_DOCKER_E2E_NOTEBOOK_NAME="+name,
		"PIPER_DOCKER_E2E_PORT="+strconv.Itoa(port),
		"PIPER_DOCKER_E2E_WORK_DIR="+workDir,
		"PIPER_DOCKER_E2E_STATE_FILE="+stateFile,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crash helper: %v\n%s", err, output)
	}

	var childState struct {
		Endpoint string `json:"endpoint"`
	}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read crash helper state: %v", err)
	}
	if err := json.Unmarshal(data, &childState); err != nil {
		t.Fatalf("decode crash helper state: %v", err)
	}
	waitDockerNotebookStatus(t, childState.Endpoint+"/notebooks/"+name+"/proxy/api/status?token=crash-token")

	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := newDockerRuntimeWithClient(DockerConfig{Image: image, Network: "bridge"}, workerID, cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = recovered.KillAll(cleanupCtx)
	})

	recoveredCh := make(chan recoveredRuntime, 1)
	exitCh := make(chan string, 1)
	if err := recovered.Recover(ctx, func(rec recoveredRuntime) func(string) {
		recoveredCh <- rec
		return func(status string) { exitCh <- status }
	}, func(_ string, status string) {
		exitCh <- status
	}); err != nil {
		t.Fatalf("recover containers: %v", err)
	}
	select {
	case rec := <-recoveredCh:
		if rec.Name != name || rec.Port != port {
			t.Fatalf("recovered = %#v, want name=%q port=%d", rec, name, port)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("running container was not recovered")
	}

	if err := recovered.Stop(ctx, name); err != nil {
		t.Fatalf("stop recovered notebook: %v", err)
	}
	waitDockerExitStatus(t, exitCh, notebook.StatusStopped)

	restartPort := freeDockerE2EPort(t)
	restartExit := make(chan string, 1)
	started, err := recovered.Start(ctx, RuntimeStartRequest{
		Name:    name,
		Spec:    notebook.Notebook{},
		WorkDir: workDir,
		Port:    restartPort,
		Token:   "restart-token",
		BaseURL: "/notebooks/" + name + "/proxy/",
		OnExit:  func(status string) { restartExit <- status },
	})
	if err != nil {
		t.Fatalf("restart notebook: %v", err)
	}
	waitDockerNotebookStatus(t, started.Endpoint+"/notebooks/"+name+"/proxy/api/status?token=restart-token")
	if _, err := cli.ContainerKill(ctx, started.ContainerID, dockerclient.ContainerKillOptions{}); err != nil {
		t.Fatalf("crash restarted notebook: %v", err)
	}
	waitDockerExitStatus(t, restartExit, notebook.StatusFailed)

	finalPort := freeDockerE2EPort(t)
	finalExit := make(chan string, 1)
	final, err := recovered.Start(ctx, RuntimeStartRequest{
		Name:    name,
		Spec:    notebook.Notebook{},
		WorkDir: workDir,
		Port:    finalPort,
		Token:   "final-token",
		BaseURL: "/notebooks/" + name + "/proxy/",
		OnExit:  func(status string) { finalExit <- status },
	})
	if err != nil {
		t.Fatalf("start after notebook crash: %v", err)
	}
	waitDockerNotebookStatus(t, final.Endpoint+"/notebooks/"+name+"/proxy/api/status?token=final-token")
	if err := recovered.Stop(ctx, name); err != nil {
		t.Fatalf("stop final notebook: %v", err)
	}
	waitDockerExitStatus(t, finalExit, notebook.StatusStopped)
}

func TestDockerRuntimeE2E_CrashHelper(t *testing.T) {
	if os.Getenv("PIPER_DOCKER_E2E_CRASH_HELPER") != "1" {
		t.Skip("crash helper is only run as a subprocess")
	}
	image := os.Getenv("PIPER_NOTEBOOK_DOCKER_E2E_IMAGE")
	workerID := os.Getenv("PIPER_DOCKER_E2E_WORKER_ID")
	name := os.Getenv("PIPER_DOCKER_E2E_NOTEBOOK_NAME")
	workDir := os.Getenv("PIPER_DOCKER_E2E_WORK_DIR")
	stateFile := os.Getenv("PIPER_DOCKER_E2E_STATE_FILE")
	port, err := strconv.Atoi(os.Getenv("PIPER_DOCKER_E2E_PORT"))
	if err != nil {
		t.Fatal(err)
	}

	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	rt, err := newDockerRuntimeWithClient(DockerConfig{Image: image, Network: "bridge"}, workerID, cli)
	if err != nil {
		t.Fatal(err)
	}
	started, err := rt.Start(context.Background(), RuntimeStartRequest{
		Name:    name,
		Spec:    notebook.Notebook{},
		WorkDir: workDir,
		Port:    port,
		Token:   "crash-token",
		BaseURL: "/notebooks/" + name + "/proxy/",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"endpoint": started.Endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Exit without runtime cleanup. This simulates a worker process crash while
	// the Docker-managed notebook continues running in the daemon.
	os.Exit(0)
}

func requireDockerNotebookE2E(t *testing.T) string {
	t.Helper()
	image := os.Getenv("PIPER_NOTEBOOK_DOCKER_E2E_IMAGE")
	if image == "" {
		t.Skip("set PIPER_NOTEBOOK_DOCKER_E2E_IMAGE to run Docker notebook e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli, err := newDockerClient()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := cli.Ping(ctx, dockerclient.PingOptions{}); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("Docker image %q is not available locally: %v", image, err)
	}
	return image
}

func waitDockerExitStatus(t *testing.T, statuses <-chan string, want string) {
	t.Helper()
	select {
	case got := <-statuses:
		if got != want {
			t.Fatalf("exit status = %q, want %q", got, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for exit status %q", want)
	}
}

func waitDockerNotebookStatus(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil {
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("notebook did not become reachable at %s", url)
}

func freeDockerE2EPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}
