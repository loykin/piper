package piper

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/loykin/piper/internal/queue"
	"github.com/loykin/piper/internal/runlifecycle"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

// ── fakes ────────────────────────────────────────────────────────────────

// fakeRunRepo implements run.Repository for piperCollector cache tests.
// Only List is ever exercised (via runlifecycle.Manager.ListRunsAcrossProjects,
// which piperCollector.runSnapshot calls); it counts invocations under a
// mutex so tests can assert how many real queries the cache let through,
// and can be told to fail on demand to exercise the error path. Every other
// method exists only to satisfy run.Repository.
type fakeRunRepo struct {
	mu        sync.Mutex
	listCalls int
	runs      []*run.Run
	err       error
}

func (r *fakeRunRepo) List(_ context.Context, _ string, _ run.RunFilter) ([]*run.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls++
	if r.err != nil {
		return nil, r.err
	}
	return r.runs, nil
}

func (r *fakeRunRepo) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listCalls
}

func (r *fakeRunRepo) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *fakeRunRepo) Create(context.Context, *run.Run) error { return nil }
func (r *fakeRunRepo) Get(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}
func (r *fakeRunRepo) Count(context.Context, string, run.RunFilter) (int, error) { return 0, nil }
func (r *fakeRunRepo) UpdateStatus(context.Context, string, string, string, *time.Time) error {
	return nil
}
func (r *fakeRunRepo) FinalizeStatusCAS(context.Context, string, string, string, *time.Time) (bool, error) {
	return true, nil
}
func (r *fakeRunRepo) MarkRunning(context.Context, string, string, time.Time) error { return nil }
func (r *fakeRunRepo) Delete(context.Context, string, string) error                 { return nil }
func (r *fakeRunRepo) GetLatestSuccessful(context.Context, string, string) (*run.Run, error) {
	return nil, nil
}
func (r *fakeRunRepo) ListTerminalBefore(context.Context, string, time.Time) ([]*run.Run, error) {
	return nil, nil
}
func (r *fakeRunRepo) ExistingIDs(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

// fakeProjectRepo implements project.Repository, returning a fixed project
// list so runlifecycle.Manager.ListRunsAcrossProjects has something to loop
// over. Only List is exercised.
type fakeProjectRepo struct {
	projects []*project.Project
}

func (r *fakeProjectRepo) List(context.Context) ([]*project.Project, error) { return r.projects, nil }
func (r *fakeProjectRepo) Create(context.Context, *project.Project) error   { return nil }
func (r *fakeProjectRepo) Get(context.Context, string) (*project.Project, error) {
	return nil, nil
}
func (r *fakeProjectRepo) SetOwner(context.Context, string, string) error { return nil }
func (r *fakeProjectRepo) Delete(context.Context, string) error           { return nil }

// ── helpers ──────────────────────────────────────────────────────────────

// newTestCollector builds a piperCollector backed by a minimal *Piper whose
// only populated fields are runs (wired to the given fakes) and queue (a
// real *queue.Queue with nil repositories — safe here since Collect only
// calls its in-memory Stats(), never anything that touches runRepo/stepRepo).
func newTestCollector(runRepo *fakeRunRepo, projectRepo *fakeProjectRepo) *piperCollector {
	mgr := runlifecycle.New(runlifecycle.Deps{RunRepo: runRepo, ProjectRepo: projectRepo})
	q := queue.NewQueue(context.Background(), nil, nil)
	p := &Piper{runs: mgr, queue: q}
	return &piperCollector{p: p}
}

// drainCollect runs one Collect call against a sufficiently buffered
// channel (Collect never sends more than a handful of metrics for the
// small fixtures these tests use) and returns everything it sent.
func drainCollect(t *testing.T, c *piperCollector) []prometheus.Metric {
	t.Helper()
	ch := make(chan prometheus.Metric, 32)
	c.Collect(ch)
	close(ch)
	var out []prometheus.Metric
	for m := range ch {
		out = append(out, m)
	}
	return out
}

func timePtr(t time.Time) *time.Time { return &t }

// ── tests ────────────────────────────────────────────────────────────────

// TestPiperCollectorCachesRunSnapshotWithinTTL proves that a second Collect
// call landing within metricsCacheTTL of the first reuses the cached
// snapshot instead of issuing another ListRunsAcrossProjects query — the
// core of this fix, since that query is an unfiltered fetch of every run
// across every project and would otherwise run on every single scrape.
func TestPiperCollectorCachesRunSnapshotWithinTTL(t *testing.T) {
	now := time.Now()
	runRepo := &fakeRunRepo{runs: []*run.Run{
		{ID: "r1", ProjectID: "p1", Status: run.StatusSuccess, StartedAt: now.Add(-time.Minute), EndedAt: timePtr(now)},
		{ID: "r2", ProjectID: "p1", Status: run.StatusFailed, StartedAt: now},
	}}
	projectRepo := &fakeProjectRepo{projects: []*project.Project{{ID: "p1"}}}
	c := newTestCollector(runRepo, projectRepo)

	first := drainCollect(t, c)
	second := drainCollect(t, c)

	if got := runRepo.callCount(); got != 1 {
		t.Fatalf("ListRunsAcrossProjects called %d times across two Collect calls within the TTL window, want 1", got)
	}
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("expected both Collect calls to emit metrics: first=%d second=%d", len(first), len(second))
	}
}

// TestPiperCollectorRefreshesAfterTTLExpires proves the cache is only good
// for metricsCacheTTL: once it has expired, the next Collect call issues a
// fresh query rather than serving a stale snapshot forever. The elapsed
// time is simulated by backdating the unexported cachedAt field (same
// package) instead of sleeping metricsCacheTTL in a unit test.
func TestPiperCollectorRefreshesAfterTTLExpires(t *testing.T) {
	runRepo := &fakeRunRepo{runs: []*run.Run{
		{ID: "r1", ProjectID: "p1", Status: run.StatusSuccess, StartedAt: time.Now()},
	}}
	projectRepo := &fakeProjectRepo{projects: []*project.Project{{ID: "p1"}}}
	c := newTestCollector(runRepo, projectRepo)

	drainCollect(t, c)
	if got := runRepo.callCount(); got != 1 {
		t.Fatalf("ListRunsAcrossProjects called %d times after first Collect, want 1", got)
	}

	// Simulate the TTL having elapsed.
	c.mu.Lock()
	c.cachedAt = time.Now().Add(-metricsCacheTTL - time.Second)
	c.mu.Unlock()

	drainCollect(t, c)
	if got := runRepo.callCount(); got != 2 {
		t.Fatalf("ListRunsAcrossProjects called %d times after cache expiry, want 2 (a fresh query)", got)
	}
}

// TestPiperCollectorConcurrentCollectDoesNotRace exercises Collect under
// concurrent callers (run with -race). It also asserts the mutex-guarded
// cache collapses a burst of near-simultaneous calls into a single
// underlying query, which is the scenario metricsCacheTTL's doc comment
// calls out (concurrent scrapers, plus promhttp's own Register-time
// Describe-by-Collect racing the Gather-time Collect within one request).
func TestPiperCollectorConcurrentCollectDoesNotRace(t *testing.T) {
	runRepo := &fakeRunRepo{runs: []*run.Run{
		{ID: "r1", ProjectID: "p1", Status: run.StatusSuccess, StartedAt: time.Now()},
	}}
	projectRepo := &fakeProjectRepo{projects: []*project.Project{{ID: "p1"}}}
	c := newTestCollector(runRepo, projectRepo)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			drainCollect(t, c)
		}()
	}
	wg.Wait()

	if got := runRepo.callCount(); got != 1 {
		t.Fatalf("ListRunsAcrossProjects called %d times across %d concurrent Collect calls, want 1", got, goroutines)
	}
}

// TestPiperCollectorErrorDoesNotStickCache proves a transient
// ListRunsAcrossProjects failure doesn't wedge the cache: Collect must
// still behave like today (log and return, no panic, no metrics sent), and
// the very next Collect call — even immediately after, well within
// metricsCacheTTL — must retry rather than being stuck because cachedAt was
// never advanced past a failed refresh.
func TestPiperCollectorErrorDoesNotStickCache(t *testing.T) {
	runRepo := &fakeRunRepo{err: errors.New("boom")}
	projectRepo := &fakeProjectRepo{projects: []*project.Project{{ID: "p1"}}}
	c := newTestCollector(runRepo, projectRepo)

	metrics := drainCollect(t, c)
	if len(metrics) != 0 {
		t.Fatalf("expected no metrics from a failed Collect, got %d", len(metrics))
	}
	if got := runRepo.callCount(); got != 1 {
		t.Fatalf("ListRunsAcrossProjects called %d times after first (failing) Collect, want 1", got)
	}

	// Fix the underlying repo and retry immediately (well within TTL of the
	// failed attempt) — the failure must not have poisoned the cache.
	runRepo.setErr(nil)
	metrics = drainCollect(t, c)
	if len(metrics) == 0 {
		t.Fatalf("expected metrics once the underlying query succeeds")
	}
	if got := runRepo.callCount(); got != 2 {
		t.Fatalf("ListRunsAcrossProjects called %d times after recovery, want 2 (retried, not stuck)", got)
	}
}
