package servingdispatch

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/piper/piper/internal/testutil"
	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/serving"
)

type stubRepo struct {
	services map[string]*serving.Service
}

func newStubServingRepo(svcs ...*serving.Service) *stubRepo {
	m := make(map[string]*serving.Service, len(svcs))
	for _, svc := range svcs {
		m[svc.Name] = svc
	}
	return &stubRepo{services: m}
}

func (s *stubRepo) Create(_ context.Context, svc *serving.Service) error {
	s.services[svc.Name] = svc
	return nil
}
func (s *stubRepo) Get(_ context.Context, name string) (*serving.Service, error) {
	return s.services[name], nil
}
func (s *stubRepo) Update(_ context.Context, svc *serving.Service) error {
	s.services[svc.Name] = svc
	return nil
}
func (s *stubRepo) Upsert(_ context.Context, svc *serving.Service) error {
	s.services[svc.Name] = svc
	return nil
}
func (s *stubRepo) SetStatus(_ context.Context, name, status string) error {
	if svc, ok := s.services[name]; ok {
		svc.Status = status
	}
	return nil
}
func (s *stubRepo) List(_ context.Context) ([]*serving.Service, error) {
	out := make([]*serving.Service, 0, len(s.services))
	for _, svc := range s.services {
		out = append(out, svc)
	}
	return out, nil
}
func (s *stubRepo) Delete(_ context.Context, name string) error {
	delete(s.services, name)
	return nil
}
func (s *stubRepo) ListHistory(_ context.Context) ([]*serving.ServiceHistory, error) {
	return nil, nil
}

func newRegistryWithWorker(addr string) *serving.ServingWorkerRegistry {
	r := serving.NewServingWorkerRegistry()
	r.Register(&serving.ServingWorkerInfo{
		ID:       "test-worker",
		Addr:     addr,
		LastSeen: time.Now(),
	})
	return r
}

func TestWorkerDriver_DeployNoWorker(t *testing.T) {
	emptyRegistry := serving.NewServingWorkerRegistry()
	driver := NewWorkerDriver(emptyRegistry, newStubServingRepo(), "http://master")

	spec := serving.ModelService{
		Metadata: serving.Metadata{Name: "my-svc"},
		Spec: serving.ModelServiceSpec{
			Model:   serving.ModelRef{FromURI: "s3://bucket/model"},
			Runtime: serving.RuntimeSpec{Command: []string{"serve"}, Port: 8080},
		},
	}
	art := artifact.Resolved{LocalPath: "/tmp/model"}

	_, err := driver.Deploy(context.Background(), spec, art, "yaml: content")
	if err == nil {
		t.Fatal("Deploy() expected error when no worker available")
	}
}

func TestWorkerDriver_DeployWorkerReturns200(t *testing.T) {
	fakeWorker := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/deploy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, newStubServingRepo(), fakeWorker.URL)

	spec := serving.ModelService{
		Metadata: serving.Metadata{Name: "svc-ok"},
		Spec: serving.ModelServiceSpec{
			Model:   serving.ModelRef{FromURI: "s3://bucket/model"},
			Runtime: serving.RuntimeSpec{Command: []string{"serve"}, Port: 8080},
		},
	}
	art := artifact.Resolved{LocalPath: "/tmp/model"}
	yamlStr := "apiVersion: piper/v1\nkind: serving.ModelService\nmetadata:\n  name: svc-ok\nspec:\n  runtime:\n    command: [serve]\n    port: 8080\n"

	svc, err := driver.Deploy(context.Background(), spec, art, yamlStr)
	if err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("Deploy() returned nil service")
	}
	if svc.Name != "svc-ok" {
		t.Errorf("svc.Name = %q, want %q", svc.Name, "svc-ok")
	}
	if svc.Status != serving.StatusRunning {
		t.Errorf("svc.Status = %q, want %q", svc.Status, serving.StatusRunning)
	}
}

func TestWorkerDriver_DeployWorkerReturns500(t *testing.T) {
	fakeWorker := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, newStubServingRepo(), fakeWorker.URL)

	spec := serving.ModelService{
		Metadata: serving.Metadata{Name: "svc-fail"},
		Spec: serving.ModelServiceSpec{
			Model:   serving.ModelRef{FromURI: "s3://bucket/model"},
			Runtime: serving.RuntimeSpec{Command: []string{"serve"}, Port: 8080},
		},
	}
	art := artifact.Resolved{}

	_, err := driver.Deploy(context.Background(), spec, art, "")
	if err == nil {
		t.Fatal("Deploy() expected error when worker returns 500")
	}
}

func TestWorkerDriver_StopNoWorker(t *testing.T) {
	emptyRegistry := serving.NewServingWorkerRegistry()
	driver := NewWorkerDriver(emptyRegistry, newStubServingRepo(), "http://master")

	svc := &serving.Service{Name: "svc-to-stop"}
	// Stop treats no-worker as already stopped (returns nil).
	err := driver.Stop(context.Background(), svc)
	if err != nil {
		t.Errorf("Stop() with no worker expected nil, got %v", err)
	}
}

func TestWorkerDriver_StopWorkerReturns200(t *testing.T) {
	fakeWorker := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, newStubServingRepo(), fakeWorker.URL)

	svc := &serving.Service{Name: "svc-stop"}
	if err := driver.Stop(context.Background(), svc); err != nil {
		t.Errorf("Stop() unexpected error: %v", err)
	}
}

func TestWorkerDriver_RestartNoWorker(t *testing.T) {
	emptyRegistry := serving.NewServingWorkerRegistry()
	driver := NewWorkerDriver(emptyRegistry, newStubServingRepo(), "http://master")

	spec := serving.ModelService{Metadata: serving.Metadata{Name: "svc-restart"}}
	err := driver.Restart(context.Background(), spec, artifact.Resolved{}, "")
	if err == nil {
		t.Fatal("Restart() expected error when no worker available")
	}
}

func TestWorkerDriver_RestartWorkerReturns200(t *testing.T) {
	fakeWorker := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, newStubServingRepo(), fakeWorker.URL)

	spec := serving.ModelService{Metadata: serving.Metadata{Name: "svc-restart"}}
	if err := driver.Restart(context.Background(), spec, artifact.Resolved{}, ""); err != nil {
		t.Errorf("Restart() unexpected error: %v", err)
	}
}
