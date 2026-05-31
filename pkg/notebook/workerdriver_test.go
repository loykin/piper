package notebook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newNBRegistryWithWorker(addr string) *NotebookWorkerRegistry {
	r := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	r.Register(&NotebookWorkerInfo{
		ID:       "nb-test-worker",
		Addr:     addr,
		LastSeen: time.Now(),
	})
	return r
}

func TestNBWorkerDriver_StartNoWorker(t *testing.T) {
	emptyRegistry := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	driver := NewWorkerDriver(emptyRegistry, "http://master")

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"

	_, err := driver.Start(context.Background(), spec, "yaml: content")
	if err == nil {
		t.Fatal("Start() expected error when no worker available")
	}
}

func TestNBWorkerDriver_StartWorkerReturns200(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/start" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fakeWorker.Close()

	registry := newNBRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, fakeWorker.URL)

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "nb-ok"
	spec.Spec.Runtime.WorkDir = "/notebooks"

	nb, err := driver.Start(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	if nb == nil {
		t.Fatal("Start() returned nil NotebookServer")
	}
	if nb.Name != "nb-ok" {
		t.Errorf("nb.Name = %q, want %q", nb.Name, "nb-ok")
	}
	if nb.Status != StatusRunning {
		t.Errorf("nb.Status = %q, want %q", nb.Status, StatusRunning)
	}
	if nb.WorkDir != "/notebooks" {
		t.Errorf("nb.WorkDir = %q, want %q", nb.WorkDir, "/notebooks")
	}
}

func TestNBWorkerDriver_StartWorkerReturns500(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeWorker.Close()

	registry := newNBRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, fakeWorker.URL)

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "nb-fail"

	_, err := driver.Start(context.Background(), spec, "")
	if err == nil {
		t.Fatal("Start() expected error when worker returns 500")
	}
}

func TestNBWorkerDriver_StopNoWorker(t *testing.T) {
	emptyRegistry := &NotebookWorkerRegistry{
		workers: make(map[string]*NotebookWorkerInfo),
	}
	driver := NewWorkerDriver(emptyRegistry, "http://master")

	nb := &NotebookServer{Name: "nb-to-stop"}
	// Stop treats no-worker as already stopped (returns nil).
	err := driver.Stop(context.Background(), nb)
	if err != nil {
		t.Errorf("Stop() with no worker expected nil, got %v", err)
	}
}

func TestNBWorkerDriver_StopWorkerReturns204(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fakeWorker.Close()

	registry := newNBRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, fakeWorker.URL)

	nb := &NotebookServer{Name: "nb-stop"}
	if err := driver.Stop(context.Background(), nb); err != nil {
		t.Errorf("Stop() unexpected error: %v", err)
	}
}

func TestNBWorkerDriver_StopWorkerReturns404(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fakeWorker.Close()

	registry := newNBRegistryWithWorker(fakeWorker.URL)
	driver := NewWorkerDriver(registry, fakeWorker.URL)

	// 404 is treated as "already gone" → nil error.
	nb := &NotebookServer{Name: "nb-gone"}
	if err := driver.Stop(context.Background(), nb); err != nil {
		t.Errorf("Stop() 404 expected nil error, got %v", err)
	}
}
