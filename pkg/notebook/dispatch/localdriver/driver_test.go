package localdriver

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/notebook"
	notebookdriver "github.com/loykin/piper/pkg/notebook/notebookdriver"
)

// fakeRuntime is a fake notebookdriver.Driver, mirroring
// pkg/notebook/worker/worker_test.go's conformanceRuntime pattern so the
// two-phase async start/report contract can be exercised without a real
// docker/process backend.
type fakeRuntime struct {
	mu       sync.Mutex
	started  []notebookdriver.StartRequest
	startErr error
	block    chan struct{} // if set, Start waits on this before returning

	recoverFn func(ctx context.Context, onRecovered func(notebookdriver.RecoveredHandle) func(string), onTerminal func(notebookdriver.RecoveredHandle, string)) error
}

func (r *fakeRuntime) Start(_ context.Context, req notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	r.started = append(r.started, req)
	r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	return &notebookdriver.StartedHandle{Endpoint: "http://127.0.0.1:0", PID: 4242}, nil
}

func (r *fakeRuntime) Stop(context.Context, string) error    { return nil }
func (r *fakeRuntime) KillAll(context.Context) error         { return nil }
func (r *fakeRuntime) Status(context.Context, string) string { return notebook.StatusStopped }

func (r *fakeRuntime) Recover(
	ctx context.Context,
	onRecovered func(notebookdriver.RecoveredHandle) func(string),
	onTerminal func(notebookdriver.RecoveredHandle, string),
) error {
	if r.recoverFn == nil {
		return nil
	}
	return r.recoverFn(ctx, onRecovered, onTerminal)
}

func (r *fakeRuntime) lastRequest() notebookdriver.StartRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started[len(r.started)-1]
}

type statusReport struct {
	projectID, name, status, endpoint, workDir, token string
	pid                                               int
	env                                               string
}

func newTestDriver(t *testing.T, rt notebookdriver.Driver) (*Driver, chan statusReport) {
	t.Helper()
	reports := make(chan statusReport, 16)
	d := &Driver{
		cfg: Config{
			RuntimeID:        "test-worker",
			NotebooksRoot:    t.TempDir(),
			PortRange:        "18888-18899",
			PlacementRuntime: "baremetal",
			ReportStatus: func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error {
				reports <- statusReport{projectID, name, status, endpoint, workDir, token, pid, env}
				return nil
			},
		},
		driver:        rt,
		notebooks:     make(map[string]*activeNotebook),
		reservedPorts: make(map[int]struct{}),
		terminal:      make(map[string]string),
	}
	return d, reports
}

func testSpec(projectID, name string) notebook.Notebook {
	return notebook.Notebook{
		Metadata: manifest.ObjectMeta{ProjectID: projectID, Name: name},
	}
}

func awaitReport(t *testing.T, reports chan statusReport) statusReport {
	t.Helper()
	select {
	case r := <-reports:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for status report")
		return statusReport{}
	}
}

func TestProvisionVolumeCreatesWorkDir(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{})
	vol := &notebook.NotebookVolume{ID: "vol-1"}
	if err := d.ProvisionVolume(context.Background(), vol, testSpec("proj", "nb")); err != nil {
		t.Fatalf("ProvisionVolume: %v", err)
	}
	if _, err := os.Stat(vol.WorkDir); err != nil {
		t.Fatalf("work dir not created: %v", err)
	}
	// RuntimeID is deliberately left empty so notebook/template handler code
	// takes the local-filesystem path instead of RPCing a worker ID with no
	// real connection registered for it (see ProvisionVolume's doc comment).
	if vol.RuntimeID != "" {
		t.Fatalf("RuntimeID = %q, want empty", vol.RuntimeID)
	}
}

func TestProvisionVolumeRequiresID(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{})
	if err := d.ProvisionVolume(context.Background(), &notebook.NotebookVolume{}, testSpec("proj", "nb")); err == nil {
		t.Fatal("expected error for empty volume id")
	}
}

func TestDeprovisionVolumeRemovesWorkDir(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{})
	vol := &notebook.NotebookVolume{ID: "vol-1"}
	if err := d.ProvisionVolume(context.Background(), vol, testSpec("proj", "nb")); err != nil {
		t.Fatalf("ProvisionVolume: %v", err)
	}
	if err := d.DeprovisionVolume(context.Background(), vol); err != nil {
		t.Fatalf("DeprovisionVolume: %v", err)
	}
	if _, err := os.Stat(vol.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("work dir still exists after deprovision: %v", err)
	}
}

func TestDeprovisionVolumeRejectsPathTraversal(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{})
	// DeprovisionVolume recomputes the target dir from vol.ID (not
	// vol.WorkDir), so the traversal payload belongs in ID.
	vol := &notebook.NotebookVolume{ID: "../outside"}
	if err := d.DeprovisionVolume(context.Background(), vol); err == nil {
		t.Fatal("expected path-traversal rejection")
	}
}

func TestStartReturnsFastThenReportsRunningAsync(t *testing.T) {
	rt := &fakeRuntime{}
	d, reports := newTestDriver(t, rt)
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: t.TempDir()}

	srv, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.RuntimeID != "test-worker" || srv.Token == "" || srv.Endpoint == "" {
		t.Fatalf("unexpected placeholder server: %+v", srv)
	}

	r := awaitReport(t, reports)
	if r.status != notebook.StatusRunning {
		t.Fatalf("status = %q, want running", r.status)
	}
	if r.projectID != "proj" || r.name != "nb" {
		t.Fatalf("unexpected report target: %+v", r)
	}
	if r.pid != 4242 {
		t.Fatalf("pid = %d, want 4242", r.pid)
	}
}

func TestStartRejectsDuplicateActiveNotebook(t *testing.T) {
	rt := &fakeRuntime{block: make(chan struct{})}
	d, reports := newTestDriver(t, rt)
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: t.TempDir()}

	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err == nil {
		t.Fatal("expected duplicate-start rejection")
	}
	close(rt.block)
	// The first Start's startAsync goroutine is still writing into vol.WorkDir
	// (t.TempDir()) until it reports running — without waiting for that
	// report, the goroutine can race t.TempDir()'s cleanup and intermittently
	// fail with "directory not empty".
	awaitReport(t, reports)
}

func TestStartRejectsMismatchedPlacementRuntime(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{})
	spec := testSpec("proj", "nb")
	spec.Spec.Driver.Placement.Runtime = "k8s"
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: t.TempDir()}
	if _, err := d.Start(context.Background(), spec, vol, ""); err == nil {
		t.Fatal("expected placement.runtime mismatch rejection")
	}
}

func TestStartFailureReportsFailed(t *testing.T) {
	rt := &fakeRuntime{startErr: os.ErrPermission}
	d, reports := newTestDriver(t, rt)
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: t.TempDir()}

	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r := awaitReport(t, reports)
	if r.status != notebook.StatusFailed {
		t.Fatalf("status = %q, want failed", r.status)
	}
}

func TestStopReleasesPortForReuse(t *testing.T) {
	rt := &fakeRuntime{}
	d, reports := newTestDriver(t, rt)
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: t.TempDir()}

	srv, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	awaitReport(t, reports)
	srv.ProjectID, srv.Name = "proj", "nb"

	d.mu.Lock()
	port := d.notebooks[notebookKey("proj", "nb")].port
	d.mu.Unlock()

	if err := d.Stop(context.Background(), srv); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	d.mu.Lock()
	_, stillReserved := d.reservedPorts[port]
	d.mu.Unlock()
	if stillReserved {
		t.Fatal("port still reserved after Stop")
	}
}

func TestStaleOnExitCallbackIgnoredAfterRestart(t *testing.T) {
	rt := &fakeRuntime{}
	d, reports := newTestDriver(t, rt)
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: t.TempDir()}

	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	awaitReport(t, reports) // running report for gen 1

	req := rt.lastRequest()

	// Simulate the first instance exiting late (its OnExit fires after a
	// restart already replaced it in d.notebooks).
	if err := d.Stop(context.Background(), &notebook.NotebookServer{ProjectID: "proj", Name: "nb"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	awaitReport(t, reports) // running report for gen 2

	// The stale gen-1 OnExit must not clobber gen-2's active registration.
	req.OnExit("exited")

	select {
	case r := <-reports:
		t.Fatalf("unexpected extra report from stale OnExit: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}

	d.mu.Lock()
	_, stillActive := d.notebooks[notebookKey("proj", "nb")]
	d.mu.Unlock()
	if !stillActive {
		t.Fatal("gen-2 notebook was removed by a stale gen-1 OnExit callback")
	}
}

func TestParsePortRangeInvalid(t *testing.T) {
	cases := []string{"", "abc", "100", "200-100"}
	for _, c := range cases {
		if _, _, err := parsePortRange(c); err == nil {
			t.Fatalf("parsePortRange(%q): expected error", c)
		}
	}
}

func TestRecoverReattachesRunningNotebook(t *testing.T) {
	rt := &fakeRuntime{}
	rt.recoverFn = func(_ context.Context, onRecovered func(notebookdriver.RecoveredHandle) func(string), _ func(notebookdriver.RecoveredHandle, string)) error {
		onRecovered(notebookdriver.RecoveredHandle{ProjectID: "proj", Name: "nb", RuntimeName: "proj__nb", Port: 18890})
		return nil
	}
	d, _ := newTestDriver(t, rt)

	if err := d.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	d.mu.Lock()
	_, active := d.notebooks[notebookKey("proj", "nb")]
	_, portReserved := d.reservedPorts[18890]
	d.mu.Unlock()
	if !active {
		t.Fatal("recovered notebook not registered as active")
	}
	if !portReserved {
		t.Fatal("recovered notebook's port not reserved")
	}
}

func TestRecoverNoopWhenDriverNotRecoverable(t *testing.T) {
	d, _ := newTestDriver(t, &nonRecoverableRuntime{})
	if err := d.Recover(context.Background()); err != nil {
		t.Fatalf("Recover on non-recoverable driver: %v", err)
	}
}

type nonRecoverableRuntime struct{}

func (nonRecoverableRuntime) Start(context.Context, notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	return &notebookdriver.StartedHandle{}, nil
}
func (nonRecoverableRuntime) Stop(context.Context, string) error    { return nil }
func (nonRecoverableRuntime) KillAll(context.Context) error         { return nil }
func (nonRecoverableRuntime) Status(context.Context, string) string { return notebook.StatusStopped }
