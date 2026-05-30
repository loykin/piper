package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/run"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- stubs ---

type stubScheduleRepo struct {
	schedules map[string]*Schedule
}

func newStubScheduleRepo() *stubScheduleRepo {
	return &stubScheduleRepo{schedules: make(map[string]*Schedule)}
}

func (r *stubScheduleRepo) Create(_ context.Context, sc *Schedule) error {
	r.schedules[sc.ID] = sc
	return nil
}
func (r *stubScheduleRepo) Get(_ context.Context, id string) (*Schedule, error) {
	sc, ok := r.schedules[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return sc, nil
}
func (r *stubScheduleRepo) List(_ context.Context) ([]*Schedule, error) {
	out := make([]*Schedule, 0, len(r.schedules))
	for _, sc := range r.schedules {
		out = append(out, sc)
	}
	return out, nil
}
func (r *stubScheduleRepo) ListDue(_ context.Context, _ time.Time) ([]*Schedule, error) {
	return nil, nil
}
func (r *stubScheduleRepo) UpdateRun(_ context.Context, _ string, _, _ time.Time) error {
	return nil
}
func (r *stubScheduleRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	if sc, ok := r.schedules[id]; ok {
		sc.Enabled = enabled
	}
	return nil
}
func (r *stubScheduleRepo) Delete(_ context.Context, id string) error {
	delete(r.schedules, id)
	return nil
}

type stubRunRepo struct{}

func (r stubRunRepo) Create(context.Context, *run.Run) error        { return nil }
func (r stubRunRepo) Get(context.Context, string) (*run.Run, error) { return nil, nil }
func (r stubRunRepo) List(_ context.Context, f run.RunFilter) ([]*run.Run, error) {
	return []*run.Run{}, nil
}
func (r stubRunRepo) UpdateStatus(context.Context, string, string, *time.Time) error { return nil }
func (r stubRunRepo) MarkRunning(context.Context, string, time.Time) error           { return nil }
func (r stubRunRepo) Delete(context.Context, string) error                           { return nil }
func (r stubRunRepo) GetLatestSuccessful(context.Context, string) (*run.Run, error)  { return nil, nil }

// --- helpers ---

func newTestRouter(repo *stubScheduleRepo, extraDeps ...func(*HandlerDeps)) *gin.Engine {
	deps := HandlerDeps{
		Schedules: repo,
		Runs:      stubRunRepo{},
		GenID:     func() string { return "sch-test-id" },
		NextTime: func(expr string, from time.Time) (time.Time, error) {
			return from.Add(time.Minute), nil
		},
	}
	for _, fn := range extraDeps {
		fn(&deps)
	}
	router := gin.New()
	NewHandler(deps).RegisterRoutes(router.Group(""))
	return router
}

func doJSON(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// --- tests ---

func TestCreateSchedule_Immediate(t *testing.T) {
	repo := newStubScheduleRepo()
	triggered := false
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Trigger = func(_ context.Context, _ *Schedule) { triggered = true }
	})

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"name": "my-pipeline",
		"yaml": "metadata:\n  name: my-pipeline\nspec:\n  steps: []",
		"type": "immediate",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["schedule_id"] == "" {
		t.Error("expected non-empty schedule_id")
	}
	if len(repo.schedules) != 1 {
		t.Errorf("expected 1 schedule in repo, got %d", len(repo.schedules))
	}
	// Trigger runs async; give it a moment.
	time.Sleep(10 * time.Millisecond)
	if !triggered {
		t.Error("expected Trigger to be called for immediate schedule")
	}
}

func TestCreateSchedule_Cron_MissingExpr(t *testing.T) {
	router := newTestRouter(newStubScheduleRepo())

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"type": "cron",
		"yaml": "metadata:\n  name: x\nspec:\n  steps: []",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreateSchedule_Once_MissingRunAt(t *testing.T) {
	router := newTestRouter(newStubScheduleRepo())

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"type": "once",
		"yaml": "metadata:\n  name: x\nspec:\n  steps: []",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreateSchedule_InvalidType(t *testing.T) {
	router := newTestRouter(newStubScheduleRepo())

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"type": "weekly",
		"yaml": "metadata:\n  name: x\nspec:\n  steps: []",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
	}
}

func TestGetSchedule(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1", Name: "test"}
	router := newTestRouter(repo)

	rec := doJSON(router, http.MethodGet, "/schedules/sch-1", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestGetSchedule_NotFound(t *testing.T) {
	router := newTestRouter(newStubScheduleRepo())
	rec := doJSON(router, http.MethodGet, "/schedules/missing", nil)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("expected 404 or 403, got %d", rec.Code)
	}
}

func TestPatchSchedule_Disable(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1", Enabled: true}
	router := newTestRouter(repo)

	enabled := false
	rec := doJSON(router, http.MethodPatch, "/schedules/sch-1", map[string]any{"enabled": enabled})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if repo.schedules["sch-1"].Enabled != false {
		t.Error("expected schedule to be disabled")
	}
}

func TestPatchSchedule_MissingEnabled(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1"}
	router := newTestRouter(repo)

	rec := doJSON(router, http.MethodPatch, "/schedules/sch-1", map[string]any{})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestDeleteSchedule(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1"}
	router := newTestRouter(repo)

	rec := doJSON(router, http.MethodDelete, "/schedules/sch-1", nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if _, ok := repo.schedules["sch-1"]; ok {
		t.Error("expected schedule to be deleted from repo")
	}
}

func TestListSchedules_OwnerFilter(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-a"] = &Schedule{ID: "sch-a", OwnerID: "user-1"}
	repo.schedules["sch-b"] = &Schedule{ID: "sch-b", OwnerID: "user-2"}

	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.OwnerID = func(_ *http.Request) string { return "user-1" }
	})

	rec := doJSON(router, http.MethodGet, "/schedules", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []*Schedule
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 1 {
		t.Fatalf("expected 1 schedule for user-1, got %d", len(out))
	}
	if out[0].ID != "sch-a" {
		t.Errorf("expected sch-a, got %s", out[0].ID)
	}
}

func TestBackfillSchedule_InvalidRange(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1"}
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Backfill = func(_ context.Context, _ string, _, _ time.Time) ([]string, error) {
			return nil, nil
		}
	})

	now := time.Now()
	rec := doJSON(router, http.MethodPost, "/schedules/sch-1/backfill", map[string]any{
		"from": now.Add(time.Hour).Format(time.RFC3339),
		"to":   now.Format(time.RFC3339), // to < from → invalid
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
	}
}

func TestListScheduleRuns(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1"}
	router := newTestRouter(repo)

	rec := doJSON(router, http.MethodGet, "/schedules/sch-1/runs", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "[") {
		t.Errorf("expected JSON array in response, got: %s", body)
	}
}
