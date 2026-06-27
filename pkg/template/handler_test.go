package template

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/security"
)

func TestRewriteLocalSourcesSkipsPureCommandSteps(t *testing.T) {
	input := `
metadata:
  name: original
spec:
  steps:
    - name: prepare
      run:
        type: command
        command: ["echo", "ready"]
    - name: train
      run:
        type: python
        source: local
        path: scripts/train.py
        command: ["python3", "$PIPER_SCRIPT_PATH"]
    - name: evaluate
      run:
        type: notebook
        notebook: notebooks/evaluate.ipynb
`

	rewritten := rewriteLocalSources(input, "snapshot-1", "template-name")
	pl, err := pipeline.Parse([]byte(rewritten))
	if err != nil {
		t.Fatalf("parse rewritten pipeline: %v", err)
	}

	if pl.Metadata.Name != "template-name" {
		t.Fatalf("metadata.name = %q, want template-name", pl.Metadata.Name)
	}

	prepare := pl.Spec.Steps[0].Run
	if prepare.Source != "" || prepare.SnapshotPrefix != "" {
		t.Fatalf("pure command source was rewritten: %+v", prepare)
	}

	for _, step := range pl.Spec.Steps[1:] {
		if step.Run.Source != "s3" {
			t.Errorf("step %q source = %q, want s3", step.Name, step.Run.Source)
		}
		if step.Run.SnapshotPrefix != "snapshots/snapshot-1/" {
			t.Errorf("step %q snapshot prefix = %q", step.Name, step.Run.SnapshotPrefix)
		}
	}
}

type stubTemplateRepo struct {
	templates map[string]*Template
}

func (r *stubTemplateRepo) Create(context.Context, *Template) error { return nil }

func (r *stubTemplateRepo) Get(_ context.Context, projectID, id string) (*Template, error) {
	t := r.templates[id]
	if t == nil || t.ProjectID != projectID {
		return nil, schedule.ErrInvalidCronExpr
	}
	return t, nil
}

func (r *stubTemplateRepo) List(context.Context, string, Filter) ([]*Template, error) {
	return nil, nil
}

func (r *stubTemplateRepo) Delete(context.Context, string, string) error { return nil }

type stubScheduleRepo struct {
	created *schedule.Schedule
}

func (r *stubScheduleRepo) Create(_ context.Context, sc *schedule.Schedule) error {
	cp := *sc
	r.created = &cp
	return nil
}

func (r *stubScheduleRepo) Get(context.Context, string, string) (*schedule.Schedule, error) {
	return nil, nil
}

func (r *stubScheduleRepo) List(context.Context, string) ([]*schedule.Schedule, error) {
	return nil, nil
}

func (r *stubScheduleRepo) ListDue(context.Context, time.Time) ([]*schedule.Schedule, error) {
	return nil, nil
}

func (r *stubScheduleRepo) UpdateRun(context.Context, string, string, time.Time, time.Time) error {
	return nil
}

func (r *stubScheduleRepo) SetEnabled(context.Context, string, string, bool) error { return nil }

func (r *stubScheduleRepo) Delete(context.Context, string, string) error { return nil }

func TestDeployComputesCronNextRunAt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	next := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	schedules := &stubScheduleRepo{}
	handler := NewHandler(HandlerDeps{
		Templates: &stubTemplateRepo{templates: map[string]*Template{
			"tpl-1": {
				ProjectID: "proj-1",
				ID:        "tpl-1",
				Name:      "daily",
				YAML:      "metadata:\n  name: daily\nspec:\n  steps: []\n",
			},
		}},
		Schedules: schedules,
		Parse:     pipeline.Parse,
		NextTime: func(expr string, from time.Time) (time.Time, error) {
			if expr != "0 2 * * *" {
				t.Fatalf("cron expr = %q", expr)
			}
			return next, nil
		},
		GenID: func() string { return "sch-1" },
	})

	router := gin.New()
	handler.RegisterRoutes(router.Group("/projects/:project_id", func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: "proj-1", Role: security.ProjectRoleAdmin})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}))

	body := strings.NewReader(`{"cron":"0 2 * * *","enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/projects/proj-1/pipelines/tpl-1/deploy", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if schedules.created == nil {
		t.Fatal("schedule was not created")
	}
	if schedules.created.NextRunAt != next {
		t.Fatalf("next_run_at = %s, want %s", schedules.created.NextRunAt, next)
	}
	if schedules.created.ScheduleType != "cron" || schedules.created.CronExpr != "0 2 * * *" {
		t.Fatalf("schedule cron fields = type %q expr %q", schedules.created.ScheduleType, schedules.created.CronExpr)
	}
}
