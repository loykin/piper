package notebook

import "testing"

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
	for i, want := range processArgs {
		if got := containerArgs[i+1]; got != want {
			t.Fatalf("arg[%d] mismatch: container %q process %q", i, got, want)
		}
	}
}
