// Package retention provides a shared scheduling/logging scaffold for
// background data-retention jobs (age-based purges, TTL sweeps). It does not
// dictate what a job actually deletes or how — resources with more than a
// plain "delete rows older than cutoff" lifecycle (e.g. pipeline runs, which
// also clean up workspace directories and artifacts) keep their own
// implementation and are simply registered as a Job like everything else, so
// they still get consistent scheduling and don't each hand-roll their own
// slice of sequential calls.
package retention

import (
	"context"
	"log/slog"
	"time"
)

// Job is a single named unit of retention work, run once per tick.
type Job interface {
	Run(ctx context.Context)
}

// JobFunc adapts a plain function to a Job.
type JobFunc func(ctx context.Context)

func (f JobFunc) Run(ctx context.Context) { f(ctx) }

// TTLPurge builds a Job that deletes rows older than ttl by calling purge
// with the computed cutoff. It is a no-op when ttl<=0 (disabled — the same
// convention RunTTL/ArtifactTTL already use), so callers can register a
// TTLPurge job unconditionally and let the configured TTL decide whether it
// ever does anything. Errors and non-zero removals are logged; the caller
// (Runner) does not need to inspect the result.
func TTLPurge(name string, ttl time.Duration, purge func(ctx context.Context, cutoff time.Time) (int64, error)) Job {
	return JobFunc(func(ctx context.Context) {
		if ttl <= 0 {
			return
		}
		cutoff := time.Now().UTC().Add(-ttl)
		removed, err := purge(ctx, cutoff)
		if err != nil {
			slog.Warn("retention purge failed", "job", name, "err", err)
			return
		}
		if removed > 0 {
			slog.Info("retention purge", "job", name, "removed", removed)
		}
	})
}

type namedJob struct {
	name string
	job  Job
}

// Runner holds an ordered list of named retention Jobs and runs them all
// each tick. Registration order is execution order.
type Runner struct {
	jobs []namedJob
}

// NewRunner returns an empty Runner ready for Register calls.
func NewRunner() *Runner {
	return &Runner{}
}

// Register adds a named Job to the Runner. Not safe to call concurrently
// with Run — all registration is expected to happen once during startup.
func (r *Runner) Register(name string, job Job) {
	r.jobs = append(r.jobs, namedJob{name: name, job: job})
}

// Run executes every registered Job in registration order. A Job that
// panics or blocks is the caller's problem — Runner does not add timeouts or
// recovery beyond what each Job already does internally, matching the
// existing CleanupRetention/cleanupStats behavior this replaces.
func (r *Runner) Run(ctx context.Context) {
	for _, nj := range r.jobs {
		nj.job.Run(ctx)
	}
}
