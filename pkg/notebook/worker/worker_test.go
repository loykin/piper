package notebookworker

import (
	"context"
	"testing"
	"time"

	"github.com/piper/piper/pkg/notebook"
	notebookdriver "github.com/piper/piper/pkg/notebook/worker/driver"
)

type conformanceRuntime struct {
	req     notebookdriver.StartRequest
	started chan struct{}
}

func (r *conformanceRuntime) Start(_ context.Context, req notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	r.req = req
	if r.started != nil {
		close(r.started)
	}
	return &notebookdriver.StartedHandle{Endpoint: "http://localhost:18888", PID: 123}, nil
}

func (r *conformanceRuntime) Stop(context.Context, string) error { return nil }
func (r *conformanceRuntime) KillAll(context.Context) error      { return nil }
func (r *conformanceRuntime) Status(_ context.Context, name string) string {
	if name == "running-nb" {
		return notebook.StatusRunning
	}
	return notebook.StatusStopped
}

type recoveryRuntime struct {
	onExit func(status string)
}

func (r *recoveryRuntime) Start(context.Context, notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	return nil, nil
}
func (r *recoveryRuntime) Stop(context.Context, string) error    { return nil }
func (r *recoveryRuntime) KillAll(context.Context) error         { return nil }
func (r *recoveryRuntime) Status(context.Context, string) string { return notebook.StatusStopped }
func (r *recoveryRuntime) Recover(
	_ context.Context,
	onRecovered func(notebookdriver.RecoveredHandle) func(status string),
	_ func(notebookdriver.RecoveredHandle, string),
) error {
	r.onExit = onRecovered(notebookdriver.RecoveredHandle{ProjectID: "project-a", Name: "demo", RuntimeName: "project-a__demo", Port: 18888})
	return nil
}

type targetRecoveryRuntime struct {
	projectID    string
	notebookName string
	runtimeName  string
	port         int
	running      bool
	onExit       func(status string)
}

func (r *targetRecoveryRuntime) Start(context.Context, notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	return nil, nil
}
func (r *targetRecoveryRuntime) Stop(_ context.Context, name string) error {
	if name == r.runtimeName {
		r.running = false
	}
	return nil
}
func (r *targetRecoveryRuntime) KillAll(context.Context) error { return nil }
func (r *targetRecoveryRuntime) Status(_ context.Context, name string) string {
	if r.running && name == r.runtimeName {
		return notebook.StatusRunning
	}
	return notebook.StatusStopped
}
func (r *targetRecoveryRuntime) Recover(
	_ context.Context,
	onRecovered func(notebookdriver.RecoveredHandle) func(status string),
	onTerminal func(notebookdriver.RecoveredHandle, string),
) error {
	if r.runtimeName == "" {
		return nil
	}
	rec := notebookdriver.RecoveredHandle{
		ProjectID:   r.projectID,
		Name:        r.notebookName,
		RuntimeName: r.runtimeName,
		Port:        r.port,
	}
	if r.running {
		r.onExit = onRecovered(rec)
	} else {
		onTerminal(rec, notebook.StatusStopped)
	}
	return nil
}

func TestNotebookWorker_StartInvalidYAML(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		ProjectID: "project-a",
		YAML:      ":::not yaml:::",
	})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestNotebookWorker_StartMissingName(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	yamlPayload := `apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: ""
spec:
  driver:
    port: 8888
`
	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		ProjectID: "project-a",
		YAML:      yamlPayload,
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestNotebookWorker_StartMissingVolumeID(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	yamlPayload := `apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: test-nb
spec:
  driver:
    port: 8888
`
	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		ProjectID: "project-a",
		YAML:      yamlPayload,
		VolumeID:  "",
		WorkDir:   "",
	})
	if err == nil {
		t.Fatal("expected error for missing volume_id and work_dir")
	}
}

func TestNotebookWorkerStartResponseConformance(t *testing.T) {
	rt := &conformanceRuntime{started: make(chan struct{})}
	workDir := t.TempDir()
	w := New(Config{ID: "agent-1", PortRange: "18888-18888"})
	w.driver = rt
	w.portAllocator = func() (int, error) { return 18888, nil }

	resp, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		ProjectID: "project-a",
		YAML:      "metadata:\n  name: demo\nspec: {}\n",
		VolumeID:  "vol-demo",
		WorkDir:   workDir,
	})
	if err != nil {
		t.Fatalf("startNotebook returned error: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("token is empty")
	}
	if resp.WorkDir != workDir {
		t.Fatalf("work dir = %q", resp.WorkDir)
	}
	if resp.Endpoint != "tunnel://agent-1?target=127.0.0.1:18888" {
		t.Fatalf("endpoint = %q", resp.Endpoint)
	}
	select {
	case <-rt.started:
	case <-time.After(time.Second):
		t.Fatal("runtime was not started")
	}
	if rt.req.BaseURL != "/projects/project-a/notebooks/demo/proxy/" {
		t.Fatalf("base url = %q", rt.req.BaseURL)
	}
	if rt.req.Token == "" {
		t.Fatal("runtime token is empty; JupyterLab requires a real token")
	}
}

func TestNotebookWorker_StopNonExistent(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	// stopNotebook is idempotent for non-existent notebooks.
	if err := w.stopNotebook(context.Background(), notebook.WorkerStopRequest{ProjectID: "project-a", Name: "nonexistent"}); err != nil {
		t.Fatalf("stopNotebook nonexistent: %v", err)
	}
}

func TestNotebookWorker_RejectsDuplicateActiveNotebook(t *testing.T) {
	rt := &conformanceRuntime{started: make(chan struct{})}
	w := New(Config{ID: "nb-test-id", PortRange: "18888-18889"})
	w.driver = rt
	ports := []int{18888, 18889}
	w.portAllocator = func() (int, error) {
		port := ports[0]
		ports = ports[1:]
		w.mu.Lock()
		w.reservedPorts[port] = struct{}{}
		w.mu.Unlock()
		return port, nil
	}
	req := notebook.WorkerStartRequest{
		ProjectID: "project-a",
		YAML:      "metadata:\n  name: demo\nspec: {}\n",
		WorkDir:   t.TempDir(),
	}
	if _, err := w.startNotebook(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rt.started:
	case <-time.After(time.Second):
		t.Fatal("first runtime start did not run")
	}

	if _, err := w.startNotebook(context.Background(), req); err == nil {
		t.Fatal("expected duplicate active notebook to be rejected")
	}
	w.mu.Lock()
	_, secondPortReserved := w.reservedPorts[18889]
	w.mu.Unlock()
	if secondPortReserved {
		t.Fatal("duplicate start leaked its allocated port")
	}
}

func TestNotebookWorker_ProvisionVolumeEmptyID(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	_, err := w.provisionVolume(context.Background(), notebook.WorkerProvisionVolumeRequest{VolumeID: ""})
	if err == nil {
		t.Fatal("expected error for empty volume_id")
	}
}

func TestNotebookWorker_SyncStatus(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	w.driver = &conformanceRuntime{}

	resp, err := w.syncStatus(context.Background(), notebook.WorkerSyncStatusRequest{
		Targets: []notebook.WorkerSyncStatusTarget{
			{Name: "running-nb"},
			{Name: "stopped-nb"},
		},
	})
	if err != nil {
		t.Fatalf("syncStatus: %v", err)
	}
	// Composite key: "projectID:name" — empty projectID produces ":name".
	if resp.Statuses[":running-nb"] != notebook.StatusRunning {
		t.Errorf("running-nb status = %q, want %q", resp.Statuses[":running-nb"], notebook.StatusRunning)
	}
	if resp.Statuses[":stopped-nb"] != notebook.StatusStopped {
		t.Errorf("stopped-nb status = %q, want %q", resp.Statuses[":stopped-nb"], notebook.StatusStopped)
	}
}

func TestNotebookWorker_SyncStatusUsesRecoveredTerminalState(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	w.driver = &conformanceRuntime{}
	w.terminal[":failed-nb"] = notebook.StatusFailed // composite key: ""+":" +"failed-nb"

	resp, err := w.syncStatus(context.Background(), notebook.WorkerSyncStatusRequest{
		Targets: []notebook.WorkerSyncStatusTarget{{Name: "failed-nb"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Statuses[":failed-nb"] != notebook.StatusFailed {
		t.Fatalf("status = %q", resp.Statuses[":failed-nb"])
	}
}

func TestNotebookWorker_SyncStatusRecoversProcessTarget(t *testing.T) {
	rt := &targetRecoveryRuntime{
		notebookName: "recover-me",
		runtimeName:  "recover-me",
		port:         18888,
		running:      true,
	}
	w := New(Config{ID: "nb-test-id"})
	w.driver = rt
	w.recoverContainers(context.Background())

	resp, err := w.syncStatus(context.Background(), notebook.WorkerSyncStatusRequest{
		Targets: []notebook.WorkerSyncStatusTarget{{Name: "recover-me", Port: 18888}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Statuses[":recover-me"] != notebook.StatusRunning {
		t.Fatalf("status = %q, want running", resp.Statuses[":recover-me"])
	}

	w.mu.Lock()
	nb := w.notebooks[":recover-me"]
	_, reserved := w.reservedPorts[18888]
	w.mu.Unlock()
	if nb == nil || nb.port != 18888 || !reserved {
		t.Fatalf("recovered notebook was not registered: nb=%+v reserved=%v", nb, reserved)
	}
}

func TestNotebookWorker_RecoveryUsesProjectRuntimeName(t *testing.T) {
	rt := &targetRecoveryRuntime{
		projectID:    "project-a",
		notebookName: "recover-me",
		runtimeName:  "project-a__recover-me",
		port:         18888,
		running:      true,
	}
	w := New(Config{ID: "nb-test-id"})
	w.driver = rt
	w.recoverContainers(context.Background())

	w.mu.Lock()
	nb := w.notebooks["project-a:recover-me"]
	w.mu.Unlock()
	if nb == nil {
		t.Fatal("recovered notebook not registered under project-scoped key")
	}
	// Verify stop uses the project-qualified runtime name.
	if err := w.stopNotebook(context.Background(), notebook.WorkerStopRequest{ProjectID: "project-a", Name: "recover-me"}); err != nil {
		t.Fatal(err)
	}
	if rt.running {
		t.Fatal("runtime was not stopped via project-qualified runtime name")
	}
}

func TestNotebookWorker_RecoveryReleasesPortLearnedAfterAttach(t *testing.T) {
	rt := &targetRecoveryRuntime{
		notebookName: "recover-me",
		runtimeName:  "recover-me",
		port:         18888,
		running:      true,
	}
	w := New(Config{ID: "nb-test-id"})
	w.driver = rt
	w.recoverContainers(context.Background())

	w.mu.Lock()
	_, reserved := w.reservedPorts[18888]
	w.mu.Unlock()
	if !reserved {
		t.Fatal("recovered port was not reserved at startup")
	}

	rt.onExit(notebook.StatusStopped)

	w.mu.Lock()
	_, active := w.notebooks[":recover-me"]
	_, portReserved := w.reservedPorts[18888]
	w.mu.Unlock()
	if active {
		t.Fatal("exited recovered notebook is still active")
	}
	if portReserved {
		t.Fatal("port was not released on exit")
	}
}

func TestNotebookWorker_StopRecoversProcessBeforeSync(t *testing.T) {
	rt := &targetRecoveryRuntime{
		runtimeName: "project-a__recover-me",
		running:     true,
	}
	w := New(Config{ID: "nb-test-id"})
	w.driver = rt

	if err := w.stopNotebook(context.Background(), notebook.WorkerStopRequest{ProjectID: "project-a", Name: "recover-me"}); err != nil {
		t.Fatal(err)
	}
	if rt.running {
		t.Fatal("runtime stop was not called for untracked notebook")
	}

	w.mu.Lock()
	_, active := w.notebooks["project-a:recover-me"]
	terminal := w.terminal["project-a:recover-me"]
	w.mu.Unlock()
	// Untracked notebooks are not registered in terminal; master queries status on demand.
	if active || terminal != "" {
		t.Fatalf("active=%v terminal=%q, want active=false terminal=\"\"", active, terminal)
	}
}

func TestNotebookWorker_StartRejectsRecoveredProcessBeforeSync(t *testing.T) {
	rt := &targetRecoveryRuntime{
		projectID:    "project-a",
		notebookName: "recover-me",
		runtimeName:  "project-a__recover-me",
		port:         18888,
		running:      true,
	}
	w := New(Config{ID: "nb-test-id"})
	w.driver = rt
	w.recoverContainers(context.Background())

	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		ProjectID: "project-a",
		YAML:      "metadata:\n  name: recover-me\n",
		WorkDir:   t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected duplicate start to reject the already-recovered process")
	}
}

func TestNotebookWorker_RecoveredExitCannotRemoveNewGeneration(t *testing.T) {
	rt := &recoveryRuntime{}
	w := New(Config{ID: "nb-test-id"})
	w.driver = rt
	w.recoverContainers(context.Background())

	w.mu.Lock()
	w.nextGen++
	newGen := w.nextGen
	w.notebooks["project-a:demo"] = &localNotebook{projectID: "project-a", name: "demo", port: 18889, gen: newGen}
	w.reservedPorts[18889] = struct{}{}
	w.mu.Unlock()

	rt.onExit(notebook.StatusStopped)

	w.mu.Lock()
	current := w.notebooks["project-a:demo"]
	terminal := w.terminal["project-a:demo"]
	_, oldPortReserved := w.reservedPorts[18888]
	_, newPortReserved := w.reservedPorts[18889]
	w.mu.Unlock()

	if current == nil || current.gen != newGen {
		t.Fatalf("new generation was removed: %#v", current)
	}
	if terminal != "" {
		t.Fatalf("stale exit stored terminal status %q", terminal)
	}
	if oldPortReserved {
		t.Fatal("recovered generation port was not released")
	}
	if !newPortReserved {
		t.Fatal("new generation port was released")
	}
}

func TestParsePortRange(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
		start   int
		end     int
	}{
		{"8888-9900", false, 8888, 9900},
		{"1000-1000", false, 1000, 1000},
		{"bad", true, 0, 0},
		{"9000-8000", true, 0, 0},
	}
	for _, tc := range cases {
		start, end, err := parsePortRange(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parsePortRange(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePortRange(%q): %v", tc.input, err)
			continue
		}
		if start != tc.start || end != tc.end {
			t.Errorf("parsePortRange(%q) = (%d,%d), want (%d,%d)", tc.input, start, end, tc.start, tc.end)
		}
	}
}
