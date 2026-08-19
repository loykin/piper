// Package runlifecycle owns pipeline run creation, cancellation, rerun,
// retry, deletion, recovery-after-restart, TTL/retention cleanup, and the
// cron-schedule firing path — the thin layer of DB-row bookkeeping and
// validation that sits above internal/queue.Queue (which owns the actual
// DAG-aware execution engine: retries, timeouts, crash-recovery grace,
// cancellation, event emission).
package runlifecycle

import (
	"context"
	"sync"
	"time"

	"github.com/loykin/piper/internal/queue"
	ischeduler "github.com/loykin/piper/internal/scheduler"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/schedule"
	"github.com/loykin/piper/pkg/serving"
	"github.com/loykin/piper/pkg/storage"
)

// RunDeleter captures *internal/store.Repos's transactional multi-table
// DeleteRun/DeleteRuns — not part of run.Repository, since it spans the
// run/step/submission tables together. Declared locally so this package
// stays decoupled from internal/store's concrete type.
type RunDeleter interface {
	DeleteRun(ctx context.Context, projectID, id string) error
	DeleteRuns(ctx context.Context, projectID string, ids []string) error
}

// Deps holds every collaborator the Manager needs, injected by piper.New().
type Deps struct {
	RunRepo        run.Repository
	StepRepo       run.StepRepository
	ScheduleRepo   schedule.Repository
	SubmissionRepo run.SubmissionRepository // nil-checked before use — idempotent submission is optional
	ProjectRepo    project.Repository
	ServingRepo    serving.Repository // on_success.deploy lookup only
	RunDeleter     RunDeleter

	Queue       *queue.Queue
	Credentials *credential.Store
	Store       storage.Store         // nil when no artifact store configured
	Scheduler   *ischeduler.Scheduler // wired post-construction, see SetScheduler

	OutputDir          string
	RuntimeType        string
	RunTTL             time.Duration
	ArtifactTTL        time.Duration
	MisfirePolicy      string
	MisfireGracePeriod time.Duration
	StartedAt          time.Time

	// Callback hooks — plain func values so this package never imports piper.
	OnRunStart      func(ctx context.Context, runID string, pl *pipeline.Pipeline)
	DeployService   func(ctx context.Context, projectID string, yamlBytes []byte) (*serving.Service, error)
	DeleteArtifacts func(ctx context.Context, store storage.Store, runID string) error
	DeleteWorkspace func(outputDir, runID string) error
}

// Manager owns run creation, mutation, recovery, retention, and scheduled
// firing. All state beyond Deps is the idempotent-submission lock.
type Manager struct {
	deps         Deps
	submissionMu sync.Mutex
}

// New creates a Manager. Deps.Scheduler may be left zero and wired later via
// SetScheduler, since ischeduler.New(fireFunc) needs Manager.ScheduleFired
// as its fireFunc, creating a construction-order cycle with the Scheduler
// itself.
func New(deps Deps) *Manager {
	return &Manager{deps: deps}
}

// SetScheduler wires the *ischeduler.Scheduler into the Manager after
// construction. Mirrors *queue.Queue's own post-New() setter idiom
// (SetEventPublisher/SetBackend/SetStorageConfig/SetRetryPolicy/...).
func (m *Manager) SetScheduler(s *ischeduler.Scheduler) { m.deps.Scheduler = s }
