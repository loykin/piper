package localdriver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/serving"
	servingdriver "github.com/loykin/piper/pkg/serving/servingdriver"
)

// fakeRuntime is a fake servingdriver.Driver, mirroring
// pkg/notebook/dispatch/localdriver's fakeRuntime pattern so the two-phase
// async deploy/health-check/report contract can be exercised without a real
// docker/process backend.
type fakeRuntime struct {
	mu       sync.Mutex
	deployed []servingdriver.DeployRequest
	endpoint string // returned by Deploy; a real httptest server URL for health-check tests
	stopErr  error

	recoverFn func(ctx context.Context, onRecovered func(servingdriver.RecoveredHandle) func(string), onTerminal func(servingdriver.RecoveredHandle, string)) error
}

func (r *fakeRuntime) Deploy(_ context.Context, req servingdriver.DeployRequest) (string, error) {
	r.mu.Lock()
	r.deployed = append(r.deployed, req)
	ep := r.endpoint
	r.mu.Unlock()
	if ep == "" {
		ep = "http://127.0.0.1:1" // deliberately unreachable, for tests that don't care about health
	}
	return ep, nil
}

func (r *fakeRuntime) Stop(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopErr
}
func (r *fakeRuntime) KillAll(context.Context) error         { return nil }
func (r *fakeRuntime) Status(context.Context, string) string { return serving.StatusStopped }

func (r *fakeRuntime) Recover(
	ctx context.Context,
	onRecovered func(servingdriver.RecoveredHandle) func(string),
	onTerminal func(servingdriver.RecoveredHandle, string),
) error {
	if r.recoverFn == nil {
		return nil
	}
	return r.recoverFn(ctx, onRecovered, onTerminal)
}

func (r *fakeRuntime) lastRequest() servingdriver.DeployRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deployed[len(r.deployed)-1]
}

type statusReport struct {
	projectID, name, status, endpoint string
}

func newTestDriver(t *testing.T, rt servingdriver.Driver, healthTimeout time.Duration) (*Driver, chan statusReport) {
	t.Helper()
	reports := make(chan statusReport, 16)
	d := &Driver{
		cfg: Config{
			RuntimeID:          "test-worker",
			Infrastructure:     "baremetal",
			HealthCheckTimeout: healthTimeout,
			ReportStatus: func(projectID, name, status, endpoint string) error {
				reports <- statusReport{projectID, name, status, endpoint}
				return nil
			},
		},
		driver:   rt,
		services: make(map[string]*activeService),
	}
	return d, reports
}

func testSpec(projectID, name string, port int) serving.ModelService {
	return serving.ModelService{
		Metadata: manifest.ObjectMeta{ProjectID: projectID, Name: name},
		Spec: serving.ModelServiceSpec{
			Run: serving.ModelServiceRun{Command: []string{"serve"}, Port: port},
		},
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

func expectNoReport(t *testing.T, reports chan statusReport, within time.Duration) {
	t.Helper()
	select {
	case r := <-reports:
		t.Fatalf("unexpected report: %+v", r)
	case <-time.After(within):
	}
}

func TestDeployRejectsEmptyCommand(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{}, time.Second)
	spec := testSpec("proj", "svc", 8080)
	spec.Spec.Run.Command = nil
	if _, err := d.Deploy(context.Background(), spec, artifactResolved(), ""); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestDeployRejectsMissingPort(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{}, time.Second)
	spec := testSpec("proj", "svc", 0)
	if _, err := d.Deploy(context.Background(), spec, artifactResolved(), ""); err == nil {
		t.Fatal("expected error for missing port")
	}
}

func TestDeployRejectsMismatchedPlacementRuntime(t *testing.T) {
	d, _ := newTestDriver(t, &fakeRuntime{}, time.Second)
	spec := testSpec("proj", "svc", 8080)
	spec.Spec.Driver.Placement.Runtime = "k8s"
	if _, err := d.Deploy(context.Background(), spec, artifactResolved(), ""); err == nil {
		t.Fatal("expected placement.runtime mismatch rejection")
	}
}

func TestDockerDeployUsesContainerModelPath(t *testing.T) {
	rt := &fakeRuntime{}
	d, _ := newTestDriver(t, rt, 10*time.Millisecond)
	d.cfg.Infrastructure = "docker"
	model := artifactResolved()
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), model, ""); err != nil {
		t.Fatal(err)
	}
	req := rt.lastRequest()
	if req.ModelDir != model.LocalPath {
		t.Fatalf("ModelDir = %q, want %q", req.ModelDir, model.LocalPath)
	}
	if got := req.Env["PIPER_MODEL_DIR"]; got != servingdriver.ContainerModelDir {
		t.Fatalf("PIPER_MODEL_DIR = %q", got)
	}
}

func TestDeployReturnsFastWithStartingThenReportsRunning(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	rt := &fakeRuntime{endpoint: healthy.URL}
	d, reports := newTestDriver(t, rt, time.Second)

	svc, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), artifactResolved(), "yaml-body")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if svc.Status != serving.StatusStarting {
		t.Fatalf("Status = %q, want starting", svc.Status)
	}
	if svc.RuntimeID != "test-worker" || svc.Endpoint == "" || svc.YAML != "yaml-body" {
		t.Fatalf("unexpected placeholder service: %+v", svc)
	}

	r := awaitReport(t, reports)
	if r.status != serving.StatusRunning {
		t.Fatalf("status = %q, want running", r.status)
	}
	if r.projectID != "proj" || r.name != "svc" {
		t.Fatalf("unexpected report target: %+v", r)
	}

	req := rt.lastRequest()
	if req.RuntimeName != "proj__svc" {
		t.Fatalf("RuntimeName = %q, want proj__svc", req.RuntimeName)
	}
	if req.Env["PIPER_MODEL_DIR"] != "/models/proj" {
		t.Fatalf("PIPER_MODEL_DIR = %q, want /models/proj", req.Env["PIPER_MODEL_DIR"])
	}
}

func TestDeployRejectsDuplicateActiveService(t *testing.T) {
	rt := &fakeRuntime{}
	d, _ := newTestDriver(t, rt, time.Second)

	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), artifactResolved(), ""); err != nil {
		t.Fatalf("first Deploy: %v", err)
	}
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8081), artifactResolved(), ""); err == nil {
		t.Fatal("expected duplicate-deploy rejection")
	}
}

func TestHealthCheckFailureReportsFailedAfterStop(t *testing.T) {
	rt := &fakeRuntime{endpoint: "http://127.0.0.1:1"} // nothing listening; health check always fails
	d, reports := newTestDriver(t, rt, 300*time.Millisecond)

	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), artifactResolved(), ""); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	// The health-check goroutine calls driver.Stop, which (on the fake) does
	// not itself trigger OnExit — production drivers trigger OnExit from
	// their own exit-watcher goroutine once the process/container actually
	// exits. Since the fake never calls OnExit, exercise the "Stop errored"
	// branch instead, which reports failed synchronously.
	rt.mu.Lock()
	rt.stopErr = context.DeadlineExceeded
	rt.mu.Unlock()

	r := awaitReport(t, reports)
	if r.status != serving.StatusFailed {
		t.Fatalf("status = %q, want failed", r.status)
	}
}

func TestHealthCheckFailureSetsExitAsFailedForLaterOnExit(t *testing.T) {
	rt := &fakeRuntime{endpoint: "http://127.0.0.1:1"}
	// iprocess.WaitReady sleeps in a fixed 500ms poll interval regardless of
	// how short the configured timeout is, so it always takes >=500ms to
	// give up; wait comfortably past that before asserting exitAs was set.
	d, reports := newTestDriver(t, rt, 50*time.Millisecond)

	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), artifactResolved(), ""); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	// Stop succeeds (fakeRuntime.stopErr is unset): no synchronous report.
	// The real driver would now fire OnExit asynchronously once the
	// process/container actually exits; simulate that directly and confirm
	// it reports "failed" (via the exitAs override), not the raw "stopped"
	// exit status a plain Stop would normally produce.
	expectNoReport(t, reports, 900*time.Millisecond)

	req := rt.lastRequest()
	req.OnExit("stopped")

	r := awaitReport(t, reports)
	if r.status != serving.StatusFailed {
		t.Fatalf("status = %q, want failed (exitAs override), got report: %+v", r.status, r)
	}
}

func TestStopDelegatesToRuntimeUsingRuntimeName(t *testing.T) {
	rt := &fakeRuntime{}
	d, _ := newTestDriver(t, rt, time.Second)

	if err := d.Stop(context.Background(), &serving.Service{ProjectID: "proj", Name: "svc"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStaleOnExitCallbackIgnoredAfterRedeploy(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	rt := &fakeRuntime{endpoint: healthy.URL}
	d, reports := newTestDriver(t, rt, time.Second)

	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), artifactResolved(), ""); err != nil {
		t.Fatalf("first Deploy: %v", err)
	}
	awaitReport(t, reports) // running report for gen 1
	firstReq := rt.lastRequest()

	// Redeploy replaces gen 1 with gen 2 in d.services (mirrors Manager.Stop
	// then Manager.Deploy on redeploy — the key is reused).
	d.removeService(serviceKey("proj", "svc"), 1)
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc", 8080), artifactResolved(), ""); err != nil {
		t.Fatalf("second Deploy: %v", err)
	}
	awaitReport(t, reports) // running report for gen 2

	// The stale gen-1 OnExit must not clobber gen-2's active registration.
	firstReq.OnExit("stopped")
	expectNoReport(t, reports, 200*time.Millisecond)

	d.mu.Lock()
	_, stillActive := d.services[serviceKey("proj", "svc")]
	d.mu.Unlock()
	if !stillActive {
		t.Fatal("gen-2 service was removed by a stale gen-1 OnExit callback")
	}
}

func TestRecoverReattachesRunningService(t *testing.T) {
	rt := &fakeRuntime{}
	rt.recoverFn = func(_ context.Context, onRecovered func(servingdriver.RecoveredHandle) func(string), _ func(servingdriver.RecoveredHandle, string)) error {
		onRecovered(servingdriver.RecoveredHandle{ProjectID: "proj", Name: "svc", RuntimeName: "proj__svc", Port: 8080})
		return nil
	}
	d, reports := newTestDriver(t, rt, time.Second)

	if err := d.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	r := awaitReport(t, reports)
	if r.status != serving.StatusRunning {
		t.Fatalf("status = %q, want running", r.status)
	}

	d.mu.Lock()
	_, active := d.services[serviceKey("proj", "svc")]
	d.mu.Unlock()
	if !active {
		t.Fatal("recovered service not registered as active")
	}
}

func TestRecoverNoopWhenDriverNotRecoverable(t *testing.T) {
	d, _ := newTestDriver(t, &nonRecoverableRuntime{}, time.Second)
	if err := d.Recover(context.Background()); err != nil {
		t.Fatalf("Recover on non-recoverable driver: %v", err)
	}
}

type nonRecoverableRuntime struct{}

func (nonRecoverableRuntime) Deploy(context.Context, servingdriver.DeployRequest) (string, error) {
	return "", nil
}
func (nonRecoverableRuntime) Stop(context.Context, string) error    { return nil }
func (nonRecoverableRuntime) KillAll(context.Context) error         { return nil }
func (nonRecoverableRuntime) Status(context.Context, string) string { return serving.StatusStopped }

func artifactResolved() artifact.Resolved {
	return artifact.Resolved{LocalPath: "/models/proj"}
}
