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
	"github.com/piper/piper/pkg/pipeline/run"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
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
func (r *stubScheduleRepo) Get(_ context.Context, _, id string) (*Schedule, error) {
	sc, ok := r.schedules[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return sc, nil
}
func (r *stubScheduleRepo) List(_ context.Context, _ string) ([]*Schedule, error) {
	out := make([]*Schedule, 0, len(r.schedules))
	for _, sc := range r.schedules {
		out = append(out, sc)
	}
	return out, nil
}
func (r *stubScheduleRepo) ListWithMaxRuns(_ context.Context) ([]*Schedule, error) {
	out := make([]*Schedule, 0, len(r.schedules))
	for _, sc := range r.schedules {
		if sc.MaxRuns > 0 {
			out = append(out, sc)
		}
	}
	return out, nil
}
func (r *stubScheduleRepo) ListEnabled(_ context.Context) ([]*Schedule, error) {
	var out []*Schedule
	for _, sc := range r.schedules {
		if sc.Enabled {
			out = append(out, sc)
		}
	}
	return out, nil
}
func (r *stubScheduleRepo) ListDue(_ context.Context, _ time.Time) ([]*Schedule, error) {
	return nil, nil
}
func (r *stubScheduleRepo) ClaimRun(_ context.Context, _, _ string, _, _, _ time.Time) (bool, error) {
	return true, nil
}
func (r *stubScheduleRepo) AdvanceNextRun(_ context.Context, _, _ string, _, _ time.Time) (bool, error) {
	return true, nil
}
func (r *stubScheduleRepo) ClaimOneShotRun(_ context.Context, _, _ string, _, _ time.Time) (bool, error) {
	return true, nil
}
func (r *stubScheduleRepo) SetEnabled(_ context.Context, _, id string, enabled bool) error {
	if sc, ok := r.schedules[id]; ok {
		sc.Enabled = enabled
	}
	return nil
}
func (r *stubScheduleRepo) Delete(_ context.Context, _, id string) error {
	delete(r.schedules, id)
	return nil
}

// stubScheduler records Add/Remove calls for assertion.
type stubScheduler struct {
	added   []string // schedule IDs passed to Add
	removed []string // schedule IDs passed to Remove
}

func (s *stubScheduler) Add(sc *Schedule) error {
	s.added = append(s.added, sc.ID)
	return nil
}
func (s *stubScheduler) Remove(id string) {
	s.removed = append(s.removed, id)
}

type stubRunRepo struct{}

func (r stubRunRepo) Create(context.Context, *run.Run) error { return nil }
func (r stubRunRepo) Get(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}
func (r stubRunRepo) List(_ context.Context, _ string, _ run.RunFilter) ([]*run.Run, error) {
	return []*run.Run{}, nil
}
func (r stubRunRepo) UpdateStatus(context.Context, string, string, string, *time.Time) error {
	return nil
}
func (r stubRunRepo) MarkRunning(context.Context, string, string, time.Time) error { return nil }
func (r stubRunRepo) Delete(context.Context, string, string) error                 { return nil }
func (r stubRunRepo) GetLatestSuccessful(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}

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
	injectProjCtx := func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: "test-proj", Role: security.ProjectRoleAdmin})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	NewHandler(deps).RegisterRoutes(router.Group("", injectProjCtx))
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
	sched := &stubScheduler{}
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Sched = sched
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
	if len(sched.added) != 1 {
		t.Errorf("expected sched.Add to be called once, got %d", len(sched.added))
	}
	got := repo.schedules[resp["schedule_id"]]
	if got.MaxRuns != 0 {
		t.Errorf("MaxRuns = %d, want 0", got.MaxRuns)
	}
}

func TestCreateSchedule_PersistsMaxRuns(t *testing.T) {
	repo := newStubScheduleRepo()
	router := newTestRouter(repo)

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"name":     "my-pipeline",
		"yaml":     "metadata:\n  name: my-pipeline\nspec:\n  steps: []",
		"type":     "cron",
		"cron":     "0 * * * *",
		"max_runs": 5,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	got := repo.schedules["sch-test-id"]
	if got == nil {
		t.Fatal("schedule was not persisted")
	}
	if got.MaxRuns != 5 {
		t.Fatalf("MaxRuns = %d, want 5", got.MaxRuns)
	}
}

func TestCreateSchedule_RejectsNegativeMaxRuns(t *testing.T) {
	router := newTestRouter(newStubScheduleRepo())

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"name":     "my-pipeline",
		"yaml":     "metadata:\n  name: my-pipeline\nspec:\n  steps: []",
		"type":     "cron",
		"cron":     "0 * * * *",
		"max_runs": -1,
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
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

	rec := doJSON(router, http.MethodPatch, "/schedules/sch-1", map[string]any{"enabled": false})

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

func TestListSchedules_All(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-a"] = &Schedule{ID: "sch-a"}
	repo.schedules["sch-b"] = &Schedule{ID: "sch-b"}

	router := newTestRouter(repo)

	rec := doJSON(router, http.MethodGet, "/schedules", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []*Schedule
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(out))
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

// --- Scheduler sync tests ---

func TestCreateSchedule_Cron_CallsSchedAdd(t *testing.T) {
	repo := newStubScheduleRepo()
	sched := &stubScheduler{}
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Sched = sched
	})

	rec := doJSON(router, http.MethodPost, "/schedules", map[string]any{
		"name": "nightly",
		"yaml": "metadata:\n  name: nightly\nspec:\n  steps: []",
		"type": "cron",
		"cron": "0 0 * * *",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(sched.added) != 1 {
		t.Errorf("expected sched.Add called once, got %d", len(sched.added))
	}
	if len(sched.removed) != 0 {
		t.Errorf("expected no sched.Remove calls, got %d", len(sched.removed))
	}
}

func TestPatchSchedule_Enable_CallsSchedAdd(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1", Enabled: false}
	sched := &stubScheduler{}
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Sched = sched
	})

	rec := doJSON(router, http.MethodPatch, "/schedules/sch-1", map[string]any{"enabled": true})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(sched.added) != 1 || sched.added[0] != "sch-1" {
		t.Errorf("expected sched.Add(sch-1), got %v", sched.added)
	}
	if len(sched.removed) != 0 {
		t.Errorf("expected no Remove calls, got %v", sched.removed)
	}
}

func TestPatchSchedule_Disable_CallsSchedRemove(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1", Enabled: true}
	sched := &stubScheduler{}
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Sched = sched
	})

	rec := doJSON(router, http.MethodPatch, "/schedules/sch-1", map[string]any{"enabled": false})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(sched.removed) != 1 || sched.removed[0] != "sch-1" {
		t.Errorf("expected sched.Remove(sch-1), got %v", sched.removed)
	}
	if len(sched.added) != 0 {
		t.Errorf("expected no Add calls, got %v", sched.added)
	}
}

func TestDeleteSchedule_CallsSchedRemove(t *testing.T) {
	repo := newStubScheduleRepo()
	repo.schedules["sch-1"] = &Schedule{ID: "sch-1"}
	sched := &stubScheduler{}
	router := newTestRouter(repo, func(d *HandlerDeps) {
		d.Sched = sched
	})

	rec := doJSON(router, http.MethodDelete, "/schedules/sch-1", nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(sched.removed) != 1 || sched.removed[0] != "sch-1" {
		t.Errorf("expected sched.Remove(sch-1), got %v", sched.removed)
	}
}
