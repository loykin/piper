//go:build !windows

package process

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/notebook/worker/driver"
)

func TestProcessRuntimeE2E_WorkerCrashRecoverRestart(t *testing.T) {
	envRoot := requireProcessNotebookE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "process-crash-recover"
	port := freeProcessE2EPort(t)
	stateFile := filepath.Join(root, "started.json")

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProcessRuntimeE2E_CrashHelper$")
	cmd.Env = append(os.Environ(),
		"PIPER_PROCESS_E2E_CRASH_HELPER=1",
		"PIPER_PROCESS_E2E_ENV_ROOT="+envRoot,
		"PIPER_PROCESS_E2E_NOTEBOOK_ROOT="+root,
		"PIPER_PROCESS_E2E_NOTEBOOK_NAME="+name,
		"PIPER_PROCESS_E2E_PORT="+strconv.Itoa(port),
		"PIPER_PROCESS_E2E_WORK_DIR="+workDir,
		"PIPER_PROCESS_E2E_STATE_FILE="+stateFile,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crash helper: %v\n%s", err, output)
	}

	var childState struct {
		Endpoint string `json:"endpoint"`
		PID      int    `json:"pid"`
	}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read crash helper state: %v", err)
	}
	if err := json.Unmarshal(data, &childState); err != nil {
		t.Fatalf("decode crash helper state: %v", err)
	}
	waitProcessNotebookStatus(t, childState.Endpoint+"/notebooks/"+name+"/proxy/api/status")

	recovered := New(root)
	t.Cleanup(func() { _ = recovered.KillAll(context.Background()) })
	recoveredExit := make(chan string, 1)
	foundRunning := false
	if err := recovered.Recover(ctx, func(rec driver.RecoveredHandle) func(string) {
		if rec.Name != name {
			return func(string) {}
		}
		foundRunning = true
		return func(status string) { recoveredExit <- status }
	}, func(driver.RecoveredHandle, string) {}); err != nil {
		t.Fatalf("recover process notebook: %v", err)
	}
	if !foundRunning {
		t.Fatal("process notebook was not recovered")
	}
	if err := recovered.Stop(ctx, name); err != nil {
		t.Fatalf("stop recovered process notebook: %v", err)
	}
	waitProcessExitStatus(t, recoveredExit, notebook.StatusStopped)

	restartPort := freeProcessE2EPort(t)
	restartExit := make(chan string, 1)
	started, err := recovered.Start(ctx, processE2EStartRequest(name, envRoot, workDir, restartPort, func(status string) {
		restartExit <- status
	}))
	if err != nil {
		t.Fatalf("restart process notebook: %v", err)
	}
	waitProcessNotebookStatus(t, started.Endpoint+"/notebooks/"+name+"/proxy/api/status")

	if err := syscall.Kill(-started.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("crash process notebook: %v", err)
	}
	waitProcessExitStatus(t, restartExit, notebook.StatusFailed)

	finalPort := freeProcessE2EPort(t)
	finalExit := make(chan string, 1)
	final, err := recovered.Start(ctx, processE2EStartRequest(name, envRoot, workDir, finalPort, func(status string) {
		finalExit <- status
	}))
	if err != nil {
		t.Fatalf("start after process notebook crash: %v", err)
	}
	waitProcessNotebookStatus(t, final.Endpoint+"/notebooks/"+name+"/proxy/api/status")
	if err := recovered.Stop(ctx, name); err != nil {
		t.Fatalf("stop final process notebook: %v", err)
	}
	waitProcessExitStatus(t, finalExit, notebook.StatusStopped)
}

func TestProcessRuntimeE2E_CrashHelper(t *testing.T) {
	if os.Getenv("PIPER_PROCESS_E2E_CRASH_HELPER") != "1" {
		t.Skip("crash helper is only run as a subprocess")
	}
	envRoot := os.Getenv("PIPER_PROCESS_E2E_ENV_ROOT")
	root := os.Getenv("PIPER_PROCESS_E2E_NOTEBOOK_ROOT")
	name := os.Getenv("PIPER_PROCESS_E2E_NOTEBOOK_NAME")
	workDir := os.Getenv("PIPER_PROCESS_E2E_WORK_DIR")
	stateFile := os.Getenv("PIPER_PROCESS_E2E_STATE_FILE")
	port, err := strconv.Atoi(os.Getenv("PIPER_PROCESS_E2E_PORT"))
	if err != nil {
		t.Fatal(err)
	}

	rt := New(root)
	started, err := rt.Start(context.Background(), processE2EStartRequest(name, envRoot, workDir, port, nil))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"endpoint": started.Endpoint, "pid": started.PID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Simulate a worker crash: exit without calling runtime cleanup.
	os.Exit(0)
}

func processE2EStartRequest(name, envRoot, workDir string, port int, onExit func(string)) driver.StartRequest {
	var spec notebook.Notebook
	spec.Metadata.Name = name
	spec.Spec.Driver.Process = &manifest.DriverProcessSpec{Env: envRoot}
	return driver.StartRequest{
		RuntimeName: name,
		Name:        name,
		Spec:        spec,
		WorkDir:     workDir,
		Port:        port,
		Token:       "",
		BaseURL:     "/notebooks/" + name + "/proxy/",
		OnExit:      onExit,
	}
}

func freeProcessE2EPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func requireProcessNotebookE2E(t *testing.T) string {
	t.Helper()
	if os.Getenv("PIPER_NOTEBOOK_PROCESS_E2E") != "1" {
		t.Skip("set PIPER_NOTEBOOK_PROCESS_E2E=1 to run process notebook e2e")
	}
	envRoot := os.Getenv("PIPER_NOTEBOOK_PROCESS_E2E_ENV")
	if envRoot == "" {
		jupyterPath, err := exec.LookPath("jupyter-lab")
		if err != nil {
			t.Skipf("jupyter-lab is not installed: %v", err)
		}
		envRoot = filepath.Dir(filepath.Dir(jupyterPath))
	}

	jupyterPath := filepath.Join(envRoot, "bin", "jupyter-lab")
	pythonPath := filepath.Join(envRoot, "bin", "python")
	if _, err := os.Stat(jupyterPath); err != nil {
		t.Skipf("jupyter-lab is not installed in %q: %v", envRoot, err)
	}
	if _, err := os.Stat(pythonPath); err != nil {
		t.Skipf("python is not installed in %q: %v", envRoot, err)
	}
	if err := exec.Command(pythonPath, "-c", "import ipykernel").Run(); err != nil {
		t.Skipf("ipykernel is not installed in %q", envRoot)
	}
	if err := exec.Command(jupyterPath, "--version").Run(); err != nil {
		t.Skipf("jupyter-lab in %q is not executable: %v", envRoot, err)
	}
	return envRoot
}

func waitProcessNotebookStatus(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < http.StatusInternalServerError {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("notebook endpoint did not become ready: %s", url)
}

func waitProcessExitStatus(t *testing.T, statuses <-chan string, want string) {
	t.Helper()
	select {
	case got := <-statuses:
		if got != want {
			t.Fatalf("exit status = %q, want %q", got, want)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for exit status %q", want)
	}
}
