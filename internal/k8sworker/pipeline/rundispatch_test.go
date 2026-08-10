package pipelineworker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	"github.com/loykin/piper/pkg/pipeline/worker/scheduler"
)

const rundispatchTestPipelineYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: rundispatch-wiring-test
spec:
  defaults:
    driver:
      placement:
        runtime: k8s
      k8s:
        image: piper:test
        namespace: default
  steps:
    - name: a
      run:
        command: ["true"]
`

// fakeDriver is a minimal pdriver.Driver test double — see
// pkg/pipeline/worker/rundispatch_internal_test.go's identical rationale:
// this proves Worker.New()'s real wiring (buildExecSpec, Registry,
// RequestOutbox-backed reporter) is correct, without depending on the real
// k8sdriver actually completing a Job against a fake clientset (there's no
// real controller running against it, so a Job's status never progresses on
// its own — that real-cluster behavior belongs to the existing e2e suite,
// not a unit-level wiring test).
type fakeDriver struct {
	startFn func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error)
	waitFn  func(context.Context, pdriver.Handle) (pdriver.Exit, error)
}

func (d *fakeDriver) Start(ctx context.Context, task *proto.Task, spec pdriver.ExecSpec) (pdriver.Handle, error) {
	return d.startFn(ctx, task, spec)
}
func (d *fakeDriver) Wait(ctx context.Context, handle pdriver.Handle) (pdriver.Exit, error) {
	return d.waitFn(ctx, handle)
}
func (d *fakeDriver) Stop(context.Context, pdriver.Handle, time.Duration) error { return nil }
func (d *fakeDriver) Recover(context.Context) ([]pdriver.Handle, error)         { return nil, nil }

// swapInFakeDriver replaces a's real k8s driver with a fake one and rebuilds
// a.registry around it, keeping the real a.buildExecSpec method (which
// reads only a.cfg, never a.driver).
func swapInFakeDriver(a *Worker, driver pdriver.Driver) {
	a.driver = driver
	a.registry = scheduler.NewRegistry(scheduler.RegistryOptions{
		Driver:        driver,
		BuildExecSpec: a.buildExecSpec,
		BuildReporter: func(projectID, runID string) scheduler.StepReporter {
			return scheduler.NewOutboxReporter(a.cfg.RequestOutbox, projectID, runID)
		},
		MaxAttempts: 1,
		WorkerID:    a.cfg.WorkerID,
	})
}

func newTestRequestOutbox(t *testing.T) *pdriver.RequestOutbox {
	t.Helper()
	outbox, err := pdriver.NewRequestOutbox(t.TempDir(), func(context.Context, string, json.RawMessage) error { return nil })
	if err != nil {
		t.Fatalf("NewRequestOutbox: %v", err)
	}
	return outbox
}

func TestK8sWorkerRunDispatchWiringDrivesRunToCompletion(t *testing.T) {
	requestOutbox := newTestRequestOutbox(t)
	a := New(Config{
		WorkerID:      "wiring-test-worker",
		K8s:           K8sConfig{Client: fake.NewSimpleClientset(), Namespaces: []string{"default"}, AgentImage: "piper:test"},
		RequestOutbox: requestOutbox,
	})
	if a.registry == nil {
		t.Fatal("New() did not construct a scheduler.Registry")
	}

	swapInFakeDriver(a, &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			return pdriver.Handle{RuntimeKey: "a"}, nil
		},
		waitFn: func(context.Context, pdriver.Handle) (pdriver.Exit, error) {
			return pdriver.Exit{Result: &proto.TaskResult{Status: proto.TaskStatusDone}}, nil
		},
	})

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-wiring-1", PipelineYAML: rundispatchTestPipelineYAML}
	if err := a.registry.StartRun(dispatch); err != nil {
		t.Fatalf("registry.StartRun: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.registry.Len() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run never reached a terminal state (Registry still tracking it after 5s) — wiring likely stuck")
}

func TestK8sWorkerCancelPipelineRunForwardsToRegistry(t *testing.T) {
	requestOutbox := newTestRequestOutbox(t)
	a := New(Config{
		WorkerID:      "wiring-test-worker-2",
		K8s:           K8sConfig{Client: fake.NewSimpleClientset(), Namespaces: []string{"default"}, AgentImage: "piper:test"},
		RequestOutbox: requestOutbox,
	})

	started := make(chan struct{}, 4)
	swapInFakeDriver(a, &fakeDriver{
		startFn: func(context.Context, *proto.Task, pdriver.ExecSpec) (pdriver.Handle, error) {
			started <- struct{}{}
			return pdriver.Handle{RuntimeKey: "a"}, nil
		},
		waitFn: func(ctx context.Context, _ pdriver.Handle) (pdriver.Exit, error) {
			<-ctx.Done()
			return pdriver.Exit{}, ctx.Err()
		},
	})

	dispatch := proto.RunDispatch{ProjectID: "proj-1", RunID: "run-wiring-2", PipelineYAML: rundispatchTestPipelineYAML}
	if err := a.registry.StartRun(dispatch); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("step never started")
	}

	if err := a.cancelPipelineRun(context.Background(), pipelineCancelRunRequest{RunID: "run-wiring-2", Namespace: "default"}); err != nil {
		t.Fatalf("cancelPipelineRun: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.registry.Len() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run was not canceled through the registry within 5s")
}
