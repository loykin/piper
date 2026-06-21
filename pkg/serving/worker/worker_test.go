package servingworker

import (
	"context"
	"testing"

	"github.com/piper/piper/pkg/serving"
	servingdriver "github.com/piper/piper/pkg/serving/worker/driver"
)

type captureDriver struct {
	req       servingdriver.DeployRequest
	endpoint  string
	recovered *servingdriver.RecoveredHandle
}

func (d *captureDriver) Deploy(_ context.Context, req servingdriver.DeployRequest) (string, error) {
	d.req = req
	if req.LogSink != nil {
		req.LogSink.Stop()
	}
	if req.OnExit != nil {
		req.OnExit(serving.StatusStopped)
	}
	return d.endpoint, nil
}
func (*captureDriver) Stop(context.Context, string) error    { return nil }
func (*captureDriver) Status(context.Context, string) string { return serving.StatusStopped }
func (*captureDriver) KillAll(context.Context) error         { return nil }
func (d *captureDriver) Recover(_ context.Context, onRecovered func(servingdriver.RecoveredHandle) func(string), _ func(servingdriver.RecoveredHandle, string)) error {
	if d.recovered != nil {
		_ = onRecovered(*d.recovered)
	}
	return nil
}

func TestServingWorkerBuildsDriverRequest(t *testing.T) {
	drv := &captureDriver{endpoint: "http://127.0.0.1:18080"}
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	w.driver = drv
	payload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: demo
spec:
  run:
    command: ["serve", "--port", "18080"]
    port: 18080
    health_path: /ready
  driver:
    docker:
      image: model:test
`
	if _, err := w.deploy(context.Background(), deployRequest{ProjectID: "project-a", YAML: payload, LocalPath: "/models/demo"}); err != nil {
		t.Fatal(err)
	}
	if drv.req.RuntimeName != "project-a__demo" || drv.req.Name != "demo" {
		t.Fatalf("driver names = runtime %q, name %q", drv.req.RuntimeName, drv.req.Name)
	}
	if drv.req.Image != "model:test" || drv.req.Env["PIPER_MODEL_DIR"] != "/models/demo" {
		t.Fatalf("driver request = %#v", drv.req)
	}
}

func TestServingWorkerRecoversThroughDriver(t *testing.T) {
	handle := servingdriver.RecoveredHandle{ProjectID: "project-a", Name: "demo", RuntimeName: "project-a__demo", Port: 18080}
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	w.driver = &captureDriver{recovered: &handle}
	w.recoverServices(context.Background())
	w.mu.Lock()
	rec := w.services[serviceKey(handle.ProjectID, handle.Name)]
	w.mu.Unlock()
	if rec == nil || rec.status != serving.StatusRunning || rec.endpoint != "http://localhost:18080" {
		t.Fatalf("recovered service = %#v", rec)
	}
}

func TestServingWorker_DeployEmptyCommand(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: no-cmd-svc
spec:
  runtime:
    port: 8080
`
	_, err := w.deploy(context.Background(), deployRequest{ProjectID: "project-a", YAML: yamlPayload})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestNewDriverRejectsUnsupportedMode(t *testing.T) {
	if _, err := newDriver(Config{ID: "test-id", Infrastructure: "unknown"}); err == nil {
		t.Fatal("expected unsupported mode error")
	}
}

func TestNewDockerDriverRequiresStableWorkerID(t *testing.T) {
	if _, err := newDriver(Config{Infrastructure: InfrastructureDocker}); err == nil {
		t.Fatal("expected stable worker ID error")
	}
}

func TestServingWorker_DeployNoPort(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: no-port-svc
spec:
  runtime:
    command: ["echo"]
`
	_, err := w.deploy(context.Background(), deployRequest{ProjectID: "project-a", YAML: yamlPayload})
	if err == nil {
		t.Fatal("expected error for missing port")
	}
}

func TestServingWorker_DeployInvalidYAML(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	_, err := w.deploy(context.Background(), deployRequest{ProjectID: "project-a", YAML: ":::not yaml:::"})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestServingWorker_StopNonExistent(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	// stop is idempotent: stopping a non-existent service is not an error.
	if err := w.stop(context.Background(), ":nonexistent", "nonexistent"); err != nil {
		t.Fatalf("stop nonexistent: %v", err)
	}
}

func TestServingWorker_StaleGenerationCannotMutateCurrentService(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})

	oldGen, err := w.reserveService("demo", "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	w.removeService("demo", oldGen)
	newGen, err := w.reserveService("demo", "http://localhost:8081")
	if err != nil {
		t.Fatal(err)
	}

	if w.updateService("demo", oldGen, serving.StatusFailed, "") {
		t.Fatal("stale generation updated current service")
	}
	if w.removeService("demo", oldGen) {
		t.Fatal("stale generation removed current service")
	}
	if !w.updateService("demo", newGen, serving.StatusRunning, "http://localhost:8081") {
		t.Fatal("current generation was not updated")
	}

	w.mu.Lock()
	rec := w.services["demo"]
	w.mu.Unlock()
	if rec == nil || rec.status != serving.StatusRunning || rec.endpoint != "http://localhost:8081" {
		t.Fatalf("current service = %#v", rec)
	}
}

func TestServingWorker_HealthFailureOverridesStopStatus(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	gen, err := w.reserveService("demo", "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}

	if !w.failService("demo", gen) {
		t.Fatal("failed to mark service failed")
	}
	removed, status := w.completeService("demo", gen, serving.StatusStopped)
	if !removed {
		t.Fatal("service was not completed")
	}
	if status != serving.StatusFailed {
		t.Fatalf("status = %q, want failed", status)
	}
}

func TestServingWorker_RejectsDuplicateActiveService(t *testing.T) {
	w := New(Config{ID: "test-id", Infrastructure: InfrastructureBaremetal})
	if _, err := w.reserveService("demo", "http://localhost:8080"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.reserveService("demo", "http://localhost:8081"); err == nil {
		t.Fatal("expected duplicate active service to be rejected")
	}
}
