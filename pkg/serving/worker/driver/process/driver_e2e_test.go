//go:build !windows

package process

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/piper/piper/pkg/serving"
	servingdriver "github.com/piper/piper/pkg/serving/worker/driver"
)

// TestServingProcessE2EHttpHelper runs as a subprocess acting as the model
// serving process. It listens on PIPER_SERVING_E2E_PORT and blocks until killed.
func TestServingProcessE2EHttpHelper(t *testing.T) {
	if os.Getenv("PIPER_SERVING_E2E_HTTP_HELPER") != "1" {
		t.Skip("only runs as a subprocess")
	}
	port, _ := strconv.Atoi(os.Getenv("PIPER_SERVING_E2E_PORT"))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("request: %s\n", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on %d: %v", port, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	fmt.Println("serving e2e helper ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
}

// TestServingProcessE2E_CrashHelper deploys a serving process via the driver
// and then exits without cleanup, simulating a worker crash.
func TestServingProcessE2E_CrashHelper(t *testing.T) {
	if os.Getenv("PIPER_SERVING_E2E_CRASH_HELPER") != "1" {
		t.Skip("only runs as a subprocess")
	}
	pidDir := os.Getenv("PIPER_SERVING_E2E_PID_DIR")
	portStr := os.Getenv("PIPER_SERVING_E2E_PORT")
	stateFile := os.Getenv("PIPER_SERVING_E2E_STATE_FILE")
	port, _ := strconv.Atoi(portStr)

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	d := New(Config{WorkerID: "e2e-crash-worker"})
	d.pidDir = pidDir

	endpoint, err := d.Deploy(context.Background(), servingdriver.DeployRequest{
		ProjectID:   "project-a",
		Name:        "crash-service",
		RuntimeName: "project-a__crash-service",
		Command:     []string{executable, "-test.run=^TestServingProcessE2EHttpHelper$"},
		Env: map[string]string{
			"PIPER_SERVING_E2E_HTTP_HELPER": "1",
			"PIPER_SERVING_E2E_PORT":        portStr,
		},
		Port:       port,
		HealthPath: "/",
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	waitServingReady(t, endpoint+"/")

	data, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Exit without cleanup — orphaned serving process keeps running.
	os.Exit(0)
}

// TestServingProcessE2E_DeployStopCrashRecover covers the full lifecycle:
//
//	Phase 1: Deploy → health-ready → log collection → Stop → exit "stopped"
//	Phase 2: Crash (worker exits leaving orphan) → Recover → Stop → exit "stopped"
func TestServingProcessE2E_DeployStopCrashRecover(t *testing.T) {
	if os.Getenv("PIPER_SERVING_PROCESS_E2E") != "1" {
		t.Skip("set PIPER_SERVING_PROCESS_E2E=1 to run serving process e2e")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidDir := t.TempDir()

	helperCmd := func() []string {
		return []string{executable, "-test.run=^TestServingProcessE2EHttpHelper$"}
	}
	helperEnv := func(port int) map[string]string {
		return map[string]string{
			"PIPER_SERVING_E2E_HTTP_HELPER": "1",
			"PIPER_SERVING_E2E_PORT":        strconv.Itoa(port),
		}
	}

	// ── Phase 1: Deploy + log collection + Stop ───────────────────────────────
	d := New(Config{WorkerID: "e2e-worker"})
	d.pidDir = pidDir
	t.Cleanup(func() { _ = d.KillAll(context.Background()) })

	port1 := freeServingE2EPort(t)
	exitCh := make(chan string, 1)
	sink := newE2ECaptureSink()

	endpoint1, err := d.Deploy(ctx, servingdriver.DeployRequest{
		ProjectID:   "project-a",
		Name:        "test-service",
		RuntimeName: "project-a__test-service",
		Command:     helperCmd(),
		Env:         helperEnv(port1),
		Port:        port1,
		HealthPath:  "/",
		LogSink:     sink,
		OnExit:      func(status string) { exitCh <- status },
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	waitServingReady(t, endpoint1+"/")

	// Generate a log line and let the 200 ms collector pick it up.
	if _, getErr := http.Get(endpoint1 + "/probe"); getErr != nil { //nolint:noctx
		t.Logf("probe: %v (ignored)", getErr)
	}
	time.Sleep(500 * time.Millisecond)

	if err := d.Stop(ctx, "project-a__test-service"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitServingExitStatus(t, exitCh, serving.StatusStopped)

	if lines := sink.Lines(); len(lines) == 0 {
		t.Error("expected log lines from serving process, got none")
	}

	// ── Phase 2: Crash + Recover ──────────────────────────────────────────────
	port2 := freeServingE2EPort(t)
	stateFile := t.TempDir() + "/state.json"

	crashCmd := exec.CommandContext(ctx, executable, "-test.run=^TestServingProcessE2E_CrashHelper$")
	crashCmd.Env = append(os.Environ(),
		"PIPER_SERVING_E2E_CRASH_HELPER=1",
		"PIPER_SERVING_E2E_PID_DIR="+pidDir,
		"PIPER_SERVING_E2E_PORT="+strconv.Itoa(port2),
		"PIPER_SERVING_E2E_STATE_FILE="+stateFile,
	)
	if out, runErr := crashCmd.CombinedOutput(); runErr != nil {
		t.Fatalf("crash helper: %v\n%s", runErr, out)
	}

	var crashState struct {
		Endpoint string `json:"endpoint"`
	}
	rawState, _ := os.ReadFile(stateFile)
	_ = json.Unmarshal(rawState, &crashState)
	waitServingReady(t, crashState.Endpoint+"/")

	// New driver instance, same pidDir — simulates worker restart.
	recovered := New(Config{WorkerID: "e2e-worker"})
	recovered.pidDir = pidDir
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
		t.Fatal("service was not recovered as running")
	}

	// Recovered service must still be reachable.
	waitServingReady(t, crashState.Endpoint+"/")

	if err := recovered.Stop(ctx, "project-a__crash-service"); err != nil {
		t.Fatalf("stop recovered: %v", err)
	}
	waitServingExitStatus(t, recoverExitCh, serving.StatusStopped)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func waitServingReady(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < http.StatusInternalServerError {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("serving endpoint did not become ready: %s", url)
}

func waitServingExitStatus(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("exit status = %q, want %q", got, want)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for exit status %q", want)
	}
}

func freeServingE2EPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

type e2eCaptureSink struct {
	mu    sync.Mutex
	lines []string
}

func newE2ECaptureSink() *e2eCaptureSink { return &e2eCaptureSink{} }

func (s *e2eCaptureSink) Append(_, _, _, line string, _ time.Time) {
	s.mu.Lock()
	s.lines = append(s.lines, line)
	s.mu.Unlock()
}

func (s *e2eCaptureSink) Stop() {}

func (s *e2eCaptureSink) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.lines...)
}
