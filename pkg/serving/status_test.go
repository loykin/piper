package serving

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/pkg/manifest"
)

type delayedDeployDriver struct {
	entered, release chan struct{}
	err              error
}

func (d *delayedDeployDriver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }
func (d *delayedDeployDriver) Deploy(context.Context, ModelService, artifact.Resolved, string) (*Service, error) {
	close(d.entered)
	<-d.release
	if d.err != nil {
		return nil, d.err
	}
	return &Service{Status: StatusStarting, RuntimeID: "runtime"}, nil
}
func (*delayedDeployDriver) Stop(context.Context, *Service) error { return nil }

func TestObservedServiceWaitsForDeploymentPersistence(t *testing.T) {
	repo := newStubServingRepo()
	driver := &delayedDeployDriver{entered: make(chan struct{}), release: make(chan struct{})}
	sink := NewStatusSink(repo)
	manager := NewWithStatusSink(repo, driver, sink)
	spec := ModelService{Metadata: manifest.ObjectMeta{Name: "model"}, Spec: ModelServiceSpec{Model: ModelRef{FromURI: "https://example.com/model"}, Run: ModelServiceRun{Command: []string{"serve"}, Port: 8000}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deployed := make(chan error, 1)
	go func() { deployed <- manager.Deploy(ctx, "project", spec, artifact.Resolved{}, "") }()
	<-driver.entered
	observed := make(chan error, 1)
	go func() {
		observed <- sink.Update(ctx, "project", "runtime", "model", StatusRunning, "http://model:8000")
	}()
	select {
	case err := <-observed:
		close(driver.release)
		<-deployed
		t.Fatalf("observation completed before deployment was persisted: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(driver.release)
	if err := <-deployed; err != nil {
		t.Fatal(err)
	}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Get(ctx, "project", "model")
	if err != nil || rec == nil || rec.Status != StatusRunning {
		t.Fatalf("observed running state lost: %+v, %v", rec, err)
	}
}

func TestFailedDeployReleasesWaitingStatusReports(t *testing.T) {
	repo := newStubServingRepo()
	failure := errors.New("runtime unavailable")
	driver := &delayedDeployDriver{entered: make(chan struct{}), release: make(chan struct{}), err: failure}
	close(driver.release)
	sink := NewStatusSink(repo)
	manager := NewWithStatusSink(repo, driver, sink)
	spec := ModelService{Metadata: manifest.ObjectMeta{Name: "model"}, Spec: ModelServiceSpec{Model: ModelRef{FromURI: "https://example.com/model"}, Run: ModelServiceRun{Command: []string{"serve"}, Port: 8000}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Deploy(ctx, "project", spec, artifact.Resolved{}, ""); !errors.Is(err, failure) {
		t.Fatalf("deploy error = %v", err)
	}
	if err := sink.Update(ctx, "project", "runtime", "model", StatusRunning, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed deploy left status reporting blocked: %v", err)
	}
}
