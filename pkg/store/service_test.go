package store

import (
	"testing"
	"time"
)

// ─── Service CRUD ─────────────────────────────────────────────────────────────

func TestCreateAndGetService(t *testing.T) {
	st := openTestStore(t)
	svc := &Service{
		Name:     "fraud-detector",
		RunID:    "run-1",
		Artifact: "train/model",
		Status:   ServiceStatusRunning,
		Endpoint: "http://localhost:8000",
		PID:      12345,
		YAML:     "yaml: true",
	}
	if err := st.CreateService(svc); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetService("fraud-detector")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "fraud-detector" {
		t.Errorf("name: want fraud-detector, got %q", got.Name)
	}
	if got.RunID != "run-1" {
		t.Errorf("run_id: want run-1, got %q", got.RunID)
	}
	if got.Status != ServiceStatusRunning {
		t.Errorf("status: want running, got %q", got.Status)
	}
	if got.PID != 12345 {
		t.Errorf("pid: want 12345, got %d", got.PID)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}
}

func TestGetService_notFound(t *testing.T) {
	st := openTestStore(t)
	got, err := st.GetService("no-such-service")
	if err == nil && got != nil {
		t.Error("expected nil for missing service")
	}
}

func TestUpdateService(t *testing.T) {
	st := openTestStore(t)
	svc := &Service{
		Name:     "svc-1",
		Status:   ServiceStatusStopped,
		Endpoint: "http://localhost:9000",
	}
	_ = st.CreateService(svc)

	svc.Status = ServiceStatusRunning
	svc.PID = 999
	svc.Endpoint = "http://localhost:9001"
	if err := st.UpdateService(svc); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetService("svc-1")
	if got.Status != ServiceStatusRunning {
		t.Errorf("want running, got %q", got.Status)
	}
	if got.PID != 999 {
		t.Errorf("want pid 999, got %d", got.PID)
	}
	if got.Endpoint != "http://localhost:9001" {
		t.Errorf("want updated endpoint, got %q", got.Endpoint)
	}
}

func TestUpsertService_insertAndUpdate(t *testing.T) {
	st := openTestStore(t)

	svc := &Service{
		Name:   "upsert-svc",
		Status: ServiceStatusStopped,
	}
	// Insert
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetService("upsert-svc")
	if got.Status != ServiceStatusStopped {
		t.Errorf("want stopped, got %q", got.Status)
	}

	// Update via upsert
	svc.Status = ServiceStatusRunning
	svc.PID = 42
	if err := st.UpsertService(svc); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetService("upsert-svc")
	if got.Status != ServiceStatusRunning {
		t.Errorf("want running, got %q", got.Status)
	}
	if got.PID != 42 {
		t.Errorf("want pid 42, got %d", got.PID)
	}
}

func TestSetServiceStatus(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateService(&Service{Name: "s1", Status: ServiceStatusRunning})

	if err := st.SetServiceStatus("s1", ServiceStatusStopped); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetService("s1")
	if got.Status != ServiceStatusStopped {
		t.Errorf("want stopped, got %q", got.Status)
	}
}

func TestListServices(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateService(&Service{Name: "svc-a", Status: ServiceStatusRunning})
	_ = st.CreateService(&Service{Name: "svc-b", Status: ServiceStatusStopped})
	_ = st.CreateService(&Service{Name: "svc-c", Status: ServiceStatusFailed})

	svcs, err := st.ListServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 3 {
		t.Errorf("want 3 services, got %d", len(svcs))
	}
}

func TestListServices_empty(t *testing.T) {
	st := openTestStore(t)
	svcs, err := st.ListServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 0 {
		t.Errorf("want 0 services, got %d", len(svcs))
	}
}

func TestDeleteService(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateService(&Service{Name: "del-svc", Status: ServiceStatusStopped})

	if err := st.DeleteService("del-svc"); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetService("del-svc")
	if err == nil && got != nil {
		t.Error("service should be deleted")
	}
}

// ─── GetLatestSuccessfulRun ───────────────────────────────────────────────────

func TestGetLatestSuccessfulRun(t *testing.T) {
	st := openTestStore(t)
	base := time.Now()

	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "train", Status: "failed", StartedAt: base.Add(-3 * time.Minute)})
	_ = st.CreateRun(&Run{ID: "r2", PipelineName: "train", Status: "success", StartedAt: base.Add(-2 * time.Minute)})
	_ = st.CreateRun(&Run{ID: "r3", PipelineName: "train", Status: "success", StartedAt: base.Add(-1 * time.Minute)})

	run, err := st.GetLatestSuccessfulRun("train")
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatal("expected a run, got nil")
	}
	if run.ID != "r3" {
		t.Errorf("want latest successful run r3, got %q", run.ID)
	}
}

func TestGetLatestSuccessfulRun_noneFound(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateRun(&Run{ID: "r1", PipelineName: "train", Status: "failed", StartedAt: time.Now()})

	run, err := st.GetLatestSuccessfulRun("train")
	if err != nil {
		t.Fatal(err)
	}
	if run != nil {
		t.Errorf("expected nil when no successful runs, got %v", run)
	}
}

func TestGetLatestSuccessfulRun_unknownPipeline(t *testing.T) {
	st := openTestStore(t)

	run, err := st.GetLatestSuccessfulRun("no-such-pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if run != nil {
		t.Errorf("expected nil for unknown pipeline, got %v", run)
	}
}
