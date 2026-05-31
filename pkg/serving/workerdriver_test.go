package serving

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piper/piper/pkg/artifact"
)

// stubRepo is a no-op Repository for tests.
type stubRepo struct{}

func (s *stubRepo) Create(_ context.Context, _ *Service) error        { return nil }
func (s *stubRepo) Get(_ context.Context, _ string) (*Service, error) { return nil, nil }
func (s *stubRepo) Update(_ context.Context, _ *Service) error        { return nil }
func (s *stubRepo) Upsert(_ context.Context, _ *Service) error        { return nil }
func (s *stubRepo) SetStatus(_ context.Context, _, _ string) error    { return nil }
func (s *stubRepo) List(_ context.Context) ([]*Service, error)        { return nil, nil }
func (s *stubRepo) Delete(_ context.Context, _ string) error          { return nil }
func (s *stubRepo) ListHistory(_ context.Context) ([]*ServiceHistory, error) {
	return nil, nil
}

func newRegistryWithWorker(addr string) *ServingWorkerRegistry {
	r := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	r.Register(&ServingWorkerInfo{
		ID:       "test-worker",
		Addr:     addr,
		LastSeen: time.Now(),
	})
	return r
}

func TestWorkerDriver_DeployNoWorker(t *testing.T) {
	emptyRegistry := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	driver := NewWorkerDriver(emptyRegistry, &stubRepo{}, "http://master")

	spec := ModelService{
		Metadata: Metadata{Name: "my-svc"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromURI: "s3://bucket/model"},
			Runtime: RuntimeSpec{Command: []string{"serve"}, Port: 8080},
		},
	}
	art := artifact.Resolved{LocalPath: "/tmp/model"}

	_, err := driver.Deploy(context.Background(), spec, art, "yaml: content")
	if err == nil {
		t.Fatal("Deploy() expected error when no worker available")
	}
}

func TestWorkerDriver_DeployWorkerReturns200(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/deploy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fakeWorker.Close()

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, &stubRepo{}, fakeWorker.URL)

	spec := ModelService{
		Metadata: Metadata{Name: "svc-ok"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromURI: "s3://bucket/model"},
			Runtime: RuntimeSpec{Command: []string{"serve"}, Port: 8080},
		},
	}
	art := artifact.Resolved{LocalPath: "/tmp/model"}
	yamlStr := "apiVersion: piper/v1\nkind: ModelService\nmetadata:\n  name: svc-ok\nspec:\n  runtime:\n    command: [serve]\n    port: 8080\n"

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
	if svc.Status != StatusRunning {
		t.Errorf("svc.Status = %q, want %q", svc.Status, StatusRunning)
	}
}

func TestWorkerDriver_DeployWorkerReturns500(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeWorker.Close()

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, &stubRepo{}, fakeWorker.URL)

	spec := ModelService{
		Metadata: Metadata{Name: "svc-fail"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromURI: "s3://bucket/model"},
			Runtime: RuntimeSpec{Command: []string{"serve"}, Port: 8080},
		},
	}
	art := artifact.Resolved{}

	_, err := driver.Deploy(context.Background(), spec, art, "")
	if err == nil {
		t.Fatal("Deploy() expected error when worker returns 500")
	}
}

func TestWorkerDriver_StopNoWorker(t *testing.T) {
	emptyRegistry := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	driver := NewWorkerDriver(emptyRegistry, &stubRepo{}, "http://master")

	svc := &Service{Name: "svc-to-stop"}
	// Stop treats no-worker as already stopped (returns nil).
	err := driver.Stop(context.Background(), svc)
	if err != nil {
		t.Errorf("Stop() with no worker expected nil, got %v", err)
	}
}

func TestWorkerDriver_StopWorkerReturns200(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fakeWorker.Close()

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, &stubRepo{}, fakeWorker.URL)

	svc := &Service{Name: "svc-stop"}
	if err := driver.Stop(context.Background(), svc); err != nil {
		t.Errorf("Stop() unexpected error: %v", err)
	}
}

func TestWorkerDriver_RestartNoWorker(t *testing.T) {
	emptyRegistry := &ServingWorkerRegistry{
		workers: make(map[string]*ServingWorkerInfo),
	}
	driver := NewWorkerDriver(emptyRegistry, &stubRepo{}, "http://master")

	spec := ModelService{Metadata: Metadata{Name: "svc-restart"}}
	err := driver.Restart(context.Background(), spec, artifact.Resolved{}, "")
	if err == nil {
		t.Fatal("Restart() expected error when no worker available")
	}
}

func TestWorkerDriver_RestartWorkerReturns200(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fakeWorker.Close()

	registry := newRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, &stubRepo{}, fakeWorker.URL)

	spec := ModelService{Metadata: Metadata{Name: "svc-restart"}}
	if err := driver.Restart(context.Background(), spec, artifact.Resolved{}, ""); err != nil {
		t.Errorf("Restart() unexpected error: %v", err)
	}
}
