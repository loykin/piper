package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/loykin/piper/internal/proto"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

// RegistryOptions configures a Registry. All RunSchedulers it creates share
// the same Driver/BuildExecSpec/retry policy/WorkerID; BuildReporter is
// called once per run so each RunScheduler gets its own StepReporter
// scoped to that run's (ProjectID, RunID).
type RegistryOptions struct {
	Driver        pdriver.Driver
	BuildExecSpec BuildExecSpec
	AfterStart    AfterStart
	BuildReporter func(projectID, runID string) StepReporter
	MaxAttempts   int
	RetryDelay    time.Duration
	WorkerID      string
}

// Registry is the per-worker-process owner of every active RunScheduler,
// keyed by RunID. A Worker (baremetal/docker) or the k8s worker's pipeline
// domain holds exactly one Registry.
type Registry struct {
	mu   sync.Mutex
	runs map[string]*RunScheduler
	opts RegistryOptions
}

func NewRegistry(opts RegistryOptions) *Registry {
	return &Registry{runs: make(map[string]*RunScheduler), opts: opts}
}

// StartRun creates and starts a RunScheduler for dispatch. Idempotent: a
// call for a RunID this Registry already has an active (non-finalized)
// scheduler for is a no-op — this is what lets the master's master-restart
// recovery path (see the Phase 2/3 design) resend pipeline.run_dispatch
// at-least-once without risking a duplicate execution of the same run.
func (reg *Registry) StartRun(dispatch proto.RunDispatch) error {
	reg.mu.Lock()
	if _, exists := reg.runs[dispatch.RunID]; exists {
		reg.mu.Unlock()
		return nil
	}
	reg.mu.Unlock()

	reporter := reg.opts.BuildReporter(dispatch.ProjectID, dispatch.RunID)
	rs, err := New(dispatch, Options{
		Driver:        reg.opts.Driver,
		Report:        reporter,
		BuildExecSpec: reg.opts.BuildExecSpec,
		AfterStart:    reg.opts.AfterStart,
		MaxAttempts:   reg.opts.MaxAttempts,
		RetryDelay:    reg.opts.RetryDelay,
		WorkerID:      reg.opts.WorkerID,
		OnFinalize: func(string) {
			reg.mu.Lock()
			delete(reg.runs, dispatch.RunID)
			reg.mu.Unlock()
		},
	})
	if err != nil {
		return err
	}

	reg.mu.Lock()
	if _, exists := reg.runs[dispatch.RunID]; exists {
		// Lost a race with a concurrent StartRun call for the same run —
		// the other one is already the run's scheduler; discard this one
		// unstarted rather than running two schedulers for the same DAG.
		reg.mu.Unlock()
		return nil
	}
	reg.runs[dispatch.RunID] = rs
	reg.mu.Unlock()

	rs.Start()
	return nil
}

// CancelRun cancels the RunScheduler for runID, if this Registry currently
// has one. A no-op (not an error) if the run isn't tracked — it may have
// already finalized, or never been dispatched to this worker at all.
func (reg *Registry) CancelRun(runID string) error {
	reg.mu.Lock()
	rs, ok := reg.runs[runID]
	reg.mu.Unlock()
	if !ok {
		return nil
	}
	return rs.Cancel()
}

// Close waits for every active RunScheduler's in-flight step-execution
// goroutines to finish, bounded by ctx.
func (reg *Registry) Close(ctx context.Context) error {
	reg.mu.Lock()
	all := make([]*RunScheduler, 0, len(reg.runs))
	for _, rs := range reg.runs {
		all = append(all, rs)
	}
	reg.mu.Unlock()
	for _, rs := range all {
		if err := rs.Close(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Len reports how many runs this Registry currently tracks — used by tests
// and diagnostics, not by any scheduling decision.
func (reg *Registry) Len() int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return len(reg.runs)
}

// RunIDs returns every RunID this Registry currently owns (non-terminal —
// a run is dropped the moment it finalizes, see OnFinalize). Used to report
// a run-level heartbeat to the master (see pipeline.lease_renew), the
// worker-owned-scheduling equivalent of the old step-level lease renewal:
// the master's staleness sweep uses this to tell "worker briefly slow to
// report" apart from "worker truly gone" for a run between steps (e.g. mid
// retry-delay), which the master otherwise has no visibility into at all.
func (reg *Registry) RunIDs() []string {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	ids := make([]string, 0, len(reg.runs))
	for id := range reg.runs {
		ids = append(ids, id)
	}
	return ids
}
