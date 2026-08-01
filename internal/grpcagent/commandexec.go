package grpcagent

import (
	"context"
	"sync"
	"time"
)

// defaultCommandTimeout bounds a single RPC handler invocation when
// ClientConfig.DefaultCommandTimeout is unset. It exists only to guard
// against a genuinely hung/buggy handler — the authoritative, user-facing
// deadline for pipeline steps is step.options.timeout, carried in the
// dispatch payload itself and enforced independently by the master queue and
// the worker's task-scoped context (see pkg/pipeline/worker/worker.go).
const defaultCommandTimeout = 5 * time.Minute

// defaultCommandConcurrency bounds how many RPC handlers can run at once per
// tunnel connection. It is a generous backpressure valve, not the real
// admission control for pipeline work — that's the worker's own
// capacity/inFlight check in dispatch().
const defaultCommandConcurrency = 64

// commandExecutor runs RPC command handlers off the tunnel's single recv
// loop, so a slow handler (e.g. pipeline.dispatch starting a container)
// cannot block reading of other frames — other RPCs, cancels, proxy data —
// sharing the same tunnel. This is the fix for the head-of-line blocking
// described in finding 20; see finding 22 for why the worker's own
// capacity/registration bookkeeping had to become concurrency-safe first.
type commandExecutor struct {
	sem chan struct{}
	wg  sync.WaitGroup
}

func newCommandExecutor(maxConcurrent int) *commandExecutor {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultCommandConcurrency
	}
	return &commandExecutor{sem: make(chan struct{}, maxConcurrent)}
}

// run acquires a concurrency slot and executes fn in a new goroutine. It
// only blocks the caller (the tunnel recv loop) long enough to acquire that
// slot, never for fn's duration — so the recv loop keeps demuxing frames
// (proxy data, other RPCs) while fn runs.
func (e *commandExecutor) run(fn func()) {
	e.sem <- struct{}{}
	e.wg.Add(1)
	go func() {
		defer func() {
			<-e.sem
			e.wg.Done()
		}()
		fn()
	}()
}

// wait blocks until every fn passed to run/runBounded has returned. Callers
// that want a bounded wait should derive a context/timer around this call
// themselves; wait itself has no timeout.
func (e *commandExecutor) wait() {
	e.wg.Wait()
}

// runBounded is like run, but also derives a ctx bounded by timeout (falling
// back to defaultCommandTimeout when timeout<=0) and passes it to fn.
func (e *commandExecutor) runBounded(ctx context.Context, timeout time.Duration, fn func(ctx context.Context)) {
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	e.run(func() {
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		fn(cmdCtx)
	})
}
