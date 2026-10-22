package serving

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/pkg/manifest"
)

// reportingDeployDriver reports a status through the sink from inside
// Deploy, as a runtime does when it becomes observable before Deploy returns.
type reportingDeployDriver struct {
	sink   *StatusSink
	report string
	err    error
}

func (d *reportingDeployDriver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }
func (d *reportingDeployDriver) Deploy(ctx context.Context, spec ModelService, _ artifact.Resolved, _ string) (*Service, error) {
	if d.report != "" {
		if err := d.sink.Update(ctx, spec.Metadata.ProjectID, "runtime", spec.Metadata.Name, d.report, "http://model:8000"); err != nil {
			return nil, err
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	return &Service{Status: StatusStarting, RuntimeID: "runtime", Endpoint: "http://model:8000", PID: 42}, nil
}
func (*reportingDeployDriver) Stop(context.Context, *Service) error { return nil }

func statusTestSpec() ModelService {
	return ModelService{Metadata: manifest.ObjectMeta{Name: "model"}, Spec: ModelServiceSpec{Model: ModelRef{FromURI: "https://example.com/model"}, Run: ModelServiceRun{Command: []string{"serve"}, Port: 8000}}}
}

func TestStatusReportedDuringDeployIsKept(t *testing.T) {
	repo := newStubServingRepo()
	sink := NewStatusSink(repo)
	manager := NewWithStatusSink(repo, &reportingDeployDriver{sink: sink, report: StatusRunning}, sink)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Deploy(ctx, "project", statusTestSpec(), artifact.Resolved{}, ""); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Get(ctx, "project", "model")
	if err != nil || rec == nil || rec.Status != StatusRunning || rec.RuntimeID != "runtime" || rec.PID != 42 {
		t.Fatalf("observed running state or runtime fields lost: %+v, %v", rec, err)
	}
}

func TestFailureReportedDuringDeployStaysFailed(t *testing.T) {
	repo := newStubServingRepo()
	sink := NewStatusSink(repo)
	manager := NewWithStatusSink(repo, &reportingDeployDriver{sink: sink, report: StatusFailed}, sink)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Deploy(ctx, "project", statusTestSpec(), artifact.Resolved{}, ""); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Get(ctx, "project", "model")
	if err != nil || rec == nil || rec.Status != StatusFailed || rec.PID != 0 {
		t.Fatalf("fast exit not kept as failed: %+v, %v", rec, err)
	}
}

func TestFailedDeployMarksServiceFailed(t *testing.T) {
	repo := newStubServingRepo()
	failure := errors.New("runtime unavailable")
	sink := NewStatusSink(repo)
	manager := NewWithStatusSink(repo, &reportingDeployDriver{sink: sink, err: failure}, sink)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Deploy(ctx, "project", statusTestSpec(), artifact.Resolved{}, ""); !errors.Is(err, failure) {
		t.Fatalf("deploy error = %v", err)
	}
	rec, err := repo.Get(ctx, "project", "model")
	if err != nil || rec == nil || rec.Status != StatusFailed {
		t.Fatalf("failed deploy left %+v, %v", rec, err)
	}
}
