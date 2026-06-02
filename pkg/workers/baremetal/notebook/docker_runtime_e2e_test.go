package notebookworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
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

	cli, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := cli.Ping(ctx, dockerclient.PingOptions{}); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("Docker image %q is not available locally; pull it before running this e2e: %v", image, err)
	}

	rt, err := newDockerRuntimeWithClient(DockerConfig{Image: image, Network: "bridge"}, cli)
	if err != nil {
		t.Fatalf("docker runtime: %v", err)
	}
	name := "docker-e2e"
	token := "docker-e2e-token"
	port := freeDockerE2EPort(t)
	workDir := t.TempDir()

	var spec notebook.NotebookServerSpec
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
