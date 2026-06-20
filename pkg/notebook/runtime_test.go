package notebook

import (
	"testing"

	"github.com/piper/piper/pkg/manifest"
)

func TestJupyterArgsConformance(t *testing.T) {
	baseURL := "/notebooks/demo/proxy/"
	token := "tok"
	rootDir := ContainerWorkDir
	port := 8888

	containerArgs := JupyterStartArgs(baseURL, token, rootDir, port)
	processArgs := JupyterLabArgs(baseURL, token, rootDir, port)

	if len(containerArgs) != len(processArgs)+1 {
		t.Fatalf("container args len = %d, process args len = %d", len(containerArgs), len(processArgs))
	}
	if containerArgs[0] != "start-notebook.py" {
		t.Fatalf("container entrypoint = %q", containerArgs[0])
	}
	joined := append([]string{containerArgs[0]}, processArgs...)
	foundTrust := false
	for _, arg := range joined {
		if arg == "--ServerApp.trust_xheaders=True" {
			foundTrust = true
			break
		}
	}
	if !foundTrust {
		t.Fatal("missing --ServerApp.trust_xheaders=True")
	}
	for i, want := range processArgs {
		if got := containerArgs[i+1]; got != want {
			t.Fatalf("arg[%d] mismatch: container %q process %q", i, got, want)
		}
	}
}

func TestNotebookValidateRequiresManifestOwnedK8sValues(t *testing.T) {
	n := Notebook{Spec: NotebookSpec{Driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Runtime: "k8s"}, K8s: &manifest.DriverK8sSpec{Image: "jupyter:test", Namespace: "notebooks"}}}}
	if err := n.Validate(); err == nil {
		t.Fatal("expected missing volume size error")
	}
	n.Spec.Volume = &VolumeSpec{Size: "10Gi"}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookPrepareSpec_StepsForBackend(t *testing.T) {
	spec := NotebookPrepareSpec{
		Steps: []NotebookPrepareStep{
			{Type: PrepareStepCommand, Backend: PrepareBackendProcess, Command: []string{"uv", "pip", "install", "jupyterlab"}},
			{Type: PrepareStepCommand, Backend: PrepareBackendDocker, Command: []string{"bash", "-lc", "echo docker"}},
			{Type: PrepareStepCommand, Command: []string{"echo", "all"}},
		},
	}

	steps, err := spec.StepsForBackend(PrepareBackendProcess)
	if err != nil {
		t.Fatalf("StepsForBackend(process) error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("StepsForBackend(process) len = %d, want 2", len(steps))
	}
	if got := steps[0].Command[0]; got != "uv" {
		t.Fatalf("first process step = %q, want uv", got)
	}
	if got := steps[1].Command[0]; got != "echo" {
		t.Fatalf("second process step = %q, want echo", got)
	}
}

func TestBuildLaunchScript(t *testing.T) {
	script, err := BuildLaunchScript([]string{"export FOO=bar"}, []NotebookPrepareStep{
		{Type: PrepareStepCommand, Command: []string{"echo", "hello world"}},
	}, []string{"jupyter", "lab", "--ServerApp.base_url=/notebooks/demo/proxy/"}, "/tmp/work dir")
	if err != nil {
		t.Fatalf("BuildLaunchScript error: %v", err)
	}
	wantSubstrings := []string{
		"set -e",
		"export FOO=bar",
		"cd '/tmp/work dir'",
		"echo 'hello world'",
		"exec jupyter lab --ServerApp.base_url=/notebooks/demo/proxy/",
	}
	for _, want := range wantSubstrings {
		if !containsLine(script, want) {
			t.Fatalf("script %q missing %q", script, want)
		}
	}
}

func containsLine(script, want string) bool {
	start := 0
	for i := 0; i <= len(script); i++ {
		if i == len(script) || script[i] == '\n' {
			if script[start:i] == want {
				return true
			}
			start = i + 1
		}
	}
	return false
}
