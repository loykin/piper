//go:build !windows

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"

	dockerinfra "github.com/piper/piper/internal/docker"
	"github.com/piper/piper/pkg/serving"
	servingdriver "github.com/piper/piper/pkg/serving/worker/driver"
)

// TestServingDockerE2E_CrashHelper deploys a serving container via the Docker
// driver and exits without cleanup, simulating a worker crash while the
// container keeps running.
func TestServingDockerE2E_CrashHelper(t *testing.T) {
	if os.Getenv("PIPER_SERVING_DOCKER_E2E_CRASH_HELPER") != "1" {
		t.Skip("crash helper is only run as a subprocess")
	}
	image := os.Getenv("PIPER_SERVING_DOCKER_E2E_IMAGE")
	workerID := os.Getenv("PIPER_SERVING_DOCKER_E2E_WORKER_ID")
	stateFile := os.Getenv("PIPER_SERVING_DOCKER_E2E_STATE_FILE")
	port, err := strconv.Atoi(os.Getenv("PIPER_SERVING_DOCKER_E2E_PORT"))
	if err != nil {
		t.Fatal(err)
	}

	cli, err := dockerinfra.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewWithClient(Config{WorkerID: workerID}, cli)
	if err != nil {
		t.Fatal(err)
	}

	endpoint, err := d.Deploy(context.Background(), servingdriver.DeployRequest{
		ProjectID:   "project-a",
		Name:        "crash-service",
		RuntimeName: "project-a__crash-service",
		Image:       image,
		Command:     servingDockerHTTPCommand(port),
		Port:        port,
		HealthPath:  "/",
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	data, err := json.Marshal(map[string]string{"endpoint": endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Exit without cleanup — container keeps running in the daemon.
	os.Exit(0)
}

// TestServingDockerE2E_DeployStopCrashRecover covers the full Docker serving lifecycle:
//
//	Phase 1: Deploy → health-ready → Stop → exit "stopped"
//	Phase 2: Crash (worker exits leaving container) → Recover → Stop → exit "stopped"
func TestServingDockerE2E_DeployStopCrashRecover(t *testing.T) {
	image := requireServingDockerE2E(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli, err := dockerinfra.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	// ── Phase 1: Deploy + Stop ────────────────────────────────────────────────
	workerID1 := fmt.Sprintf("serving-docker-e2e-%d", time.Now().UnixNano())
	d, err := NewWithClient(Config{WorkerID: workerID1}, cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.KillAll(context.Background()) })

	port1 := freeServingDockerE2EPort(t)
	exitCh := make(chan string, 1)

	endpoint1, err := d.Deploy(ctx, servingdriver.DeployRequest{
		ProjectID:   "project-a",
		Name:        "test-service",
		RuntimeName: "project-a__test-service",
		Image:       image,
		Command:     servingDockerHTTPCommand(port1),
		Port:        port1,
		HealthPath:  "/",
		OnExit:      func(status string) { exitCh <- status },
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	waitServingDockerReady(t, endpoint1+"/")

	if err := d.Stop(ctx, "project-a__test-service"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitServingDockerExitStatus(t, exitCh, serving.StatusStopped)

	// ── Phase 2: Crash + Recover ──────────────────────────────────────────────
	workerID2 := fmt.Sprintf("serving-docker-crash-%d", time.Now().UnixNano())
	port2 := freeServingDockerE2EPort(t)
	stateFile := t.TempDir() + "/state.json"

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	crashCmd := exec.CommandContext(ctx, executable, "-test.run=^TestServingDockerE2E_CrashHelper$")
	crashCmd.Env = append(os.Environ(),
		"PIPER_SERVING_DOCKER_E2E_CRASH_HELPER=1",
		"PIPER_SERVING_DOCKER_E2E_IMAGE="+image,
		"PIPER_SERVING_DOCKER_E2E_WORKER_ID="+workerID2,
		"PIPER_SERVING_DOCKER_E2E_PORT="+strconv.Itoa(port2),
		"PIPER_SERVING_DOCKER_E2E_STATE_FILE="+stateFile,
	)
	if out, runErr := crashCmd.CombinedOutput(); runErr != nil {
		t.Fatalf("crash helper: %v\n%s", runErr, out)
	}

	var crashState struct {
		Endpoint string `json:"endpoint"`
	}
	rawState, _ := os.ReadFile(stateFile)
	_ = json.Unmarshal(rawState, &crashState)

	waitServingDockerReady(t, crashState.Endpoint+"/")

	// New driver instance with same workerID — simulates worker restart.
	recovered, err := NewWithClient(Config{WorkerID: workerID2}, cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.KillAll(context.Background()) })

	recoverCh := make(chan servingdriver.RecoveredHandle, 1)
	recoverExitCh := make(chan string, 1)
	if err := recovered.Recover(ctx,
		func(h servingdriver.RecoveredHandle) func(string) {
			recoverCh <- h
			return func(status string) { recoverExitCh <- status }
		},
		func(_ servingdriver.RecoveredHandle, status string) {
			recoverExitCh <- status
		},
	); err != nil {
		t.Fatalf("recover: %v", err)
	}

	select {
	case h := <-recoverCh:
		if h.Name != "crash-service" || h.Port != port2 {
			t.Fatalf("recovered = %+v, want name=crash-service port=%d", h, port2)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("running container was not recovered")
	}

	// Recovered container must still be reachable.
	waitServingDockerReady(t, crashState.Endpoint+"/")

	if err := recovered.Stop(ctx, "project-a__crash-service"); err != nil {
		t.Fatalf("stop recovered: %v", err)
	}
	waitServingDockerExitStatus(t, recoverExitCh, serving.StatusStopped)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// servingDockerHTTPCommand returns a command that starts a minimal HTTP server
// on the given port using Python's built-in http.server module.
func servingDockerHTTPCommand(port int) []string {
	return []string{"python", "-m", "http.server", strconv.Itoa(port), "--bind", "0.0.0.0"}
}

func requireServingDockerE2E(t *testing.T) string {
	t.Helper()
	image := os.Getenv("PIPER_SERVING_DOCKER_E2E_IMAGE")
	if image == "" {
		t.Skip("set PIPER_SERVING_DOCKER_E2E_IMAGE to run Docker serving e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli, err := dockerinfra.NewClient()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := cli.Ping(ctx, dockerclient.PingOptions{}); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("Docker image %q not available locally; pull it before running: %v", image, err)
	}
	return image
}

func waitServingDockerReady(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < http.StatusInternalServerError {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("serving container did not become ready: %s", url)
}

func waitServingDockerExitStatus(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("exit status = %q, want %q", got, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for exit status %q", want)
	}
}

func freeServingDockerE2EPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}
