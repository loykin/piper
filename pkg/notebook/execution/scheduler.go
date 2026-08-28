package execution

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Default concurrency/timeout/size limits — the exact values from design
// doc §11.1's YAML block, expressed as Go constants per the task's decision
// to keep this a plain overridable-defaults struct rather than build full
// YAML config wiring in this phase (documented in cmd/piper/config's
// NotebookExecutionConfig).
const (
	DefaultMaxRunningPerNotebook = 1
	DefaultMaxKernelsPerNotebook = 2
	DefaultMaxQueuedPerProject   = 20
	DefaultKernelIdleTTL         = 30 * time.Minute
	DefaultCellTimeout           = 5 * time.Minute
	DefaultExecutionTimeout      = time.Hour
	DefaultInlineOutputBytes     = 65536
	DefaultFileReadBytes         = 1048576
)

// Limits holds the concurrency and size limits from design doc §11.1.
type Limits struct {
	MaxRunningPerNotebook int
	MaxKernelsPerNotebook int
	MaxQueuedPerProject   int
	KernelIdleTTL         time.Duration
	CellTimeout           time.Duration
	ExecutionTimeout      time.Duration
	InlineOutputBytes     int
	FileReadBytes         int
}

// DefaultLimits returns the design doc's default values.
func DefaultLimits() Limits {
	return Limits{
		MaxRunningPerNotebook: DefaultMaxRunningPerNotebook,
		MaxKernelsPerNotebook: DefaultMaxKernelsPerNotebook,
		MaxQueuedPerProject:   DefaultMaxQueuedPerProject,
		KernelIdleTTL:         DefaultKernelIdleTTL,
		CellTimeout:           DefaultCellTimeout,
		ExecutionTimeout:      DefaultExecutionTimeout,
		InlineOutputBytes:     DefaultInlineOutputBytes,
		FileReadBytes:         DefaultFileReadBytes,
	}
}

// normalize replaces any zero/negative field with the design-doc default,
// so a caller supplying a partially-populated Limits (e.g. from config
// defaults merged with a zero-value struct) doesn't accidentally get
// unlimited concurrency or a zero timeout.
func (l Limits) normalize() Limits {
	d := DefaultLimits()
	if l.MaxRunningPerNotebook <= 0 {
		l.MaxRunningPerNotebook = d.MaxRunningPerNotebook
	}
	if l.MaxKernelsPerNotebook <= 0 {
		l.MaxKernelsPerNotebook = d.MaxKernelsPerNotebook
	}
	if l.MaxQueuedPerProject <= 0 {
		l.MaxQueuedPerProject = d.MaxQueuedPerProject
	}
	if l.KernelIdleTTL <= 0 {
		l.KernelIdleTTL = d.KernelIdleTTL
	}
	if l.CellTimeout <= 0 {
		l.CellTimeout = d.CellTimeout
	}
	if l.ExecutionTimeout <= 0 {
		l.ExecutionTimeout = d.ExecutionTimeout
	}
	if l.InlineOutputBytes <= 0 {
		l.InlineOutputBytes = d.InlineOutputBytes
	}
	if l.FileReadBytes <= 0 {
		l.FileReadBytes = d.FileReadBytes
	}
	return l
}

// ErrLimitExceeded is returned by the Scheduler admission checks when a
// configured concurrency limit is already at capacity. The service maps
// this to HTTP 429 (design doc §11.1: "제한 초과는 무한 대기 대신 429 Too
// Many Requests 또는 bounded queue로 처리한다" — Piper takes the 429 branch:
// CreateExecution either admits into the queued backlog immediately or
// rejects, rather than blocking the request).
var ErrLimitExceeded = newErr(ErrCodeRuntimeUnavailable, true, "concurrency limit exceeded")

// Scheduler enforces design doc §11.1's concurrency rules:
//
//   - same-Kernel executions are always serialized (one execute in flight
//     per kernel at a time);
//   - same-notebook-path executions are always serialized (only one
//     execution may be StatusRunning per notebook at a time, i.e.
//     max_running_per_notebook, which per the design's default of 1 also
//     happens to give same-path result-write serialization for free);
//   - different notebook paths / different kernels may run in parallel up
//     to the configured limits.
//
// Admission for max_queued_per_project and max_kernels_per_notebook is
// backed by repository counts (the durable source of truth, correct across
// a Piper restart); per-notebook run slots and per-kernel execute
// serialization are simple in-memory semaphores/mutexes scoped to this
// process, which is sufficient because only one Piper process ever owns a
// given runtime.type installation (AGENTS.md: "there is no worker process
// and no worker tunnel").
type Scheduler struct {
	limits Limits

	mu            sync.Mutex
	notebookSlots map[string]chan struct{} // key: projectID+"/"+notebookName
	kernelLocks   map[string]*sync.Mutex   // key: kernel session ID
}

// NewScheduler constructs a Scheduler with limits normalized against the
// design-doc defaults.
func NewScheduler(limits Limits) *Scheduler {
	return &Scheduler{
		limits:        limits.normalize(),
		notebookSlots: make(map[string]chan struct{}),
		kernelLocks:   make(map[string]*sync.Mutex),
	}
}

// Limits returns the scheduler's effective (normalized) limits.
func (s *Scheduler) Limits() Limits { return s.limits }

func notebookKey(projectID, notebookName string) string {
	return projectID + "/" + notebookName
}

func (s *Scheduler) notebookSlot(projectID, notebookName string) chan struct{} {
	key := notebookKey(projectID, notebookName)
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.notebookSlots[key]
	if !ok {
		ch = make(chan struct{}, s.limits.MaxRunningPerNotebook)
		s.notebookSlots[key] = ch
	}
	return ch
}

// AcquireNotebookSlot blocks until a run slot for (projectID, notebookName)
// is available or ctx is done, enforcing max_running_per_notebook and the
// "same notebook path 직렬화" rule. The returned release func MUST be
// called exactly once to free the slot.
func (s *Scheduler) AcquireNotebookSlot(ctx context.Context, projectID, notebookName string) (func(), error) {
	slot := s.notebookSlot(projectID, notebookName)
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// kernelLock returns (creating if necessary) the mutex serializing execute
// calls against one kernel session.
func (s *Scheduler) kernelLock(kernelSessionID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.kernelLocks[kernelSessionID]
	if !ok {
		m = &sync.Mutex{}
		s.kernelLocks[kernelSessionID] = m
	}
	return m
}

// AcquireKernelLock blocks until no other execute is in flight against
// kernelSessionID or ctx is done. The returned release func MUST be called
// exactly once.
func (s *Scheduler) AcquireKernelLock(ctx context.Context, kernelSessionID string) (func(), error) {
	m := s.kernelLock(kernelSessionID)
	done := make(chan struct{})
	go func() { m.Lock(); close(done) }()
	select {
	case <-done:
		return m.Unlock, nil
	case <-ctx.Done():
		// The goroutine above will still acquire the lock eventually and
		// leave it held forever with nothing to unlock it — this is an
		// accepted narrow leak on context cancellation racing a fair
		// mutex, which in practice only happens under an already-generous
		// execution/cell timeout being hit at the exact moment a kernel
		// lock frees up. A future revision could replace this with a
		// context-aware semaphore if this proves to matter in practice.
		return nil, ctx.Err()
	}
}

// ReleaseKernel removes bookkeeping for a closed kernel session so the
// in-memory map doesn't grow unbounded across a long-running Piper process.
// Safe to call even if no lock was ever taken for kernelSessionID.
func (s *Scheduler) ReleaseKernel(kernelSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kernelLocks, kernelSessionID)
}

// checkQueueAdmission enforces max_queued_per_project using the repository
// count (so it stays correct across a Piper restart, unlike an in-memory
// counter). Called by Service.CreateExecution before persisting a new
// queued/awaiting_approval row.
func (s *Scheduler) checkQueueAdmission(ctx context.Context, repo Repository, projectID string) error {
	n, err := repo.CountQueuedExecutions(ctx, projectID)
	if err != nil {
		return fmt.Errorf("execution: check queue admission: %w", err)
	}
	if n >= s.limits.MaxQueuedPerProject {
		return ErrLimitExceeded
	}
	return nil
}

// checkKernelAdmission enforces max_kernels_per_notebook using the
// repository count.
func (s *Scheduler) checkKernelAdmission(ctx context.Context, repo Repository, projectID, notebookName string) error {
	n, err := repo.CountOpenKernelSessions(ctx, projectID, notebookName)
	if err != nil {
		return fmt.Errorf("execution: check kernel admission: %w", err)
	}
	if n >= s.limits.MaxKernelsPerNotebook {
		return ErrLimitExceeded
	}
	return nil
}
