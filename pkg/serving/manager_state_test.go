package serving

import (
	"context"
	"errors"
	"testing"

	"github.com/piper/piper/pkg/artifact"
)

type stateTestRepo struct {
	service *Service
}

func (r *stateTestRepo) Create(_ context.Context, svc *Service) error {
	r.service = cloneService(svc)
	return nil
}
func (r *stateTestRepo) Get(_ context.Context, name string) (*Service, error) {
	if r.service == nil || r.service.Name != name {
		return nil, nil
	}
	return cloneService(r.service), nil
}
func (r *stateTestRepo) Update(_ context.Context, svc *Service) error {
	r.service = cloneService(svc)
	return nil
}
func (r *stateTestRepo) Upsert(_ context.Context, svc *Service) error {
	r.service = cloneService(svc)
	return nil
}
func (r *stateTestRepo) SetStatus(_ context.Context, name, status string) error {
	if r.service != nil && r.service.Name == name {
		r.service.Status = status
	}
	return nil
}
func (r *stateTestRepo) SetStatusEndpoint(_ context.Context, name, status, endpoint string) error {
	if r.service != nil && r.service.Name == name {
		r.service.Status = status
		if status == StatusStopped || status == StatusFailed {
			r.service.Endpoint = ""
			r.service.PID = 0
		} else if endpoint != "" {
			r.service.Endpoint = endpoint
		}
	}
	return nil
}
func (r *stateTestRepo) List(context.Context) ([]*Service, error) {
	if r.service == nil {
		return nil, nil
	}
	return []*Service{cloneService(r.service)}, nil
}
func (r *stateTestRepo) Delete(context.Context, string) error                   { r.service = nil; return nil }
func (r *stateTestRepo) ListHistory(context.Context) ([]*ServiceHistory, error) { return nil, nil }

type stateTestDriver struct {
	stopErr   error
	deployRec *Service
}

func (d *stateTestDriver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }
func (d *stateTestDriver) Deploy(context.Context, ModelService, artifact.Resolved, string) (*Service, error) {
	if d.deployRec != nil {
		return cloneService(d.deployRec), nil
	}
	return nil, errors.New("not implemented")
}
func (d *stateTestDriver) Stop(context.Context, *Service) error { return d.stopErr }
func (d *stateTestDriver) Restart(_ context.Context, _ ModelService, _ artifact.Resolved, _ string) error {
	return errors.New("not implemented")
}

func TestManagerStopRestoresObservedStateOnDriverFailure(t *testing.T) {
	repo := &stateTestRepo{service: &Service{Name: "demo", Status: StatusRunning, WorkerID: "worker-a"}}
	stopErr := errors.New("worker unavailable")
	m := New(repo, &stateTestDriver{stopErr: stopErr})

	if err := m.Stop(context.Background(), "demo"); !errors.Is(err, stopErr) {
		t.Fatalf("Stop() error = %v, want %v", err, stopErr)
	}
	if repo.service.Status != StatusRunning {
		t.Fatalf("status = %q, want %q", repo.service.Status, StatusRunning)
	}
}

func TestManagerUpdateStatusRejectsDifferentWorker(t *testing.T) {
	repo := &stateTestRepo{service: &Service{Name: "demo", Status: StatusRunning, WorkerID: "worker-a"}}
	m := New(repo, &stateTestDriver{})

	if err := m.UpdateStatus(context.Background(), "worker-b", "demo", StatusStopped, ""); err == nil {
		t.Fatal("UpdateStatus accepted non-owner")
	}
	if repo.service.Status != StatusRunning {
		t.Fatalf("status = %q, want unchanged", repo.service.Status)
	}
}

func TestManagerDeployPersistsResolvedRunMetadata(t *testing.T) {
	repo := &stateTestRepo{}
	driver := &stateTestDriver{deployRec: &Service{
		Name:     "demo",
		Status:   StatusStarting,
		WorkerID: "worker-a",
	}}
	m := New(repo, driver)
	spec := ModelService{}
	spec.Metadata.Name = "demo"

	if err := m.Deploy(context.Background(), spec, artifact.Resolved{RunID: "run-1"}, "service-yaml"); err != nil {
		t.Fatalf("Deploy() error: %v", err)
	}
	if repo.service.RunID != "run-1" {
		t.Fatalf("run ID = %q, want run-1", repo.service.RunID)
	}
	if repo.service.YAML != "service-yaml" {
		t.Fatalf("YAML = %q, want service-yaml", repo.service.YAML)
	}
}

func TestManagerUpdateStatusPreservesDeploymentMetadata(t *testing.T) {
	repo := &stateTestRepo{service: &Service{
		Name:     "demo",
		RunID:    "run-1",
		YAML:     "service-yaml",
		Status:   StatusStarting,
		Endpoint: "http://old",
		PID:      42,
		WorkerID: "worker-a",
	}}
	m := New(repo, &stateTestDriver{})

	if err := m.UpdateStatus(context.Background(), "worker-a", "demo", StatusStopped, ""); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}
	if repo.service.RunID != "run-1" || repo.service.YAML != "service-yaml" {
		t.Fatalf("deployment metadata changed: run=%q yaml=%q", repo.service.RunID, repo.service.YAML)
	}
	if repo.service.Endpoint != "" || repo.service.PID != 0 {
		t.Fatalf("terminal runtime state not cleared: endpoint=%q pid=%d", repo.service.Endpoint, repo.service.PID)
	}
}

func TestManagerStatusOnlySyncPreservesEndpoint(t *testing.T) {
	repo := &stateTestRepo{service: &Service{
		Name:     "demo",
		Status:   StatusStarting,
		Endpoint: "http://worker:8080",
		WorkerID: "worker-a",
	}}
	m := New(repo, &stateTestDriver{})

	if err := m.UpdateStatus(context.Background(), "worker-a", "demo", StatusRunning, ""); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}
	if repo.service.Endpoint != "http://worker:8080" {
		t.Fatalf("endpoint = %q, want preserved endpoint", repo.service.Endpoint)
	}
}

func cloneService(svc *Service) *Service {
	if svc == nil {
		return nil
	}
	cp := *svc
	return &cp
}
