package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type StepStatus string

const (
	StatusPending StepStatus = "pending"
	StatusRunning StepStatus = "running"
	StatusDone    StepStatus = "done"
	StatusFailed  StepStatus = "failed"
	StatusSkipped StepStatus = "skipped"
)

type StepResult struct {
	StepName  string     `json:"step_name"`
	Status    StepStatus `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   time.Time  `json:"ended_at"`
	Err       error      `json:"-"`
	ErrMsg    string     `json:"error,omitempty"`
	Attempts  int        `json:"attempts"`
}

type RunResult struct {
	PipelineName string
	StartedAt    time.Time
	EndedAt      time.Time
	Steps        map[string]*StepResult
}

func (r *RunResult) Failed() bool {
	for _, s := range r.Steps {
		if s.Status == StatusFailed {
			return true
		}
	}
	return false
}

type StepExecutorFunc func(ctx context.Context, step *Step) error

type RunnerConfig struct {
	MaxRetries  int
	RetryDelay  time.Duration
	Concurrency int
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		MaxRetries:  2,
		RetryDelay:  5 * time.Second,
		Concurrency: 0,
	}
}

type Runner struct {
	pipeline *Pipeline
	dag      *DAG
	cfg      RunnerConfig
	execFn   StepExecutorFunc

	mu      sync.Mutex
	results map[string]*StepResult
}

func NewRunner(p *Pipeline, dag *DAG, cfg RunnerConfig, execFn StepExecutorFunc) *Runner {
	return &Runner{
		pipeline: p,
		dag:      dag,
		cfg:      cfg,
		execFn:   execFn,
		results:  make(map[string]*StepResult),
	}
}

func (r *Runner) Run(ctx context.Context) *RunResult {
	runResult := &RunResult{
		PipelineName: r.pipeline.Metadata.Name,
		StartedAt:    time.Now(),
		Steps:        make(map[string]*StepResult),
	}

	for _, s := range r.pipeline.Spec.Steps {
		r.results[s.Name] = &StepResult{StepName: s.Name, Status: StatusPending}
	}

	sem := make(chan struct{}, r.concurrency())
	var wg sync.WaitGroup
	inFlight := make(map[string]bool)

	for {
		r.mu.Lock()
		done := r.doneMap()
		terminal := r.terminalMap()
		runnable := r.dag.Runnable(done)

		var toStart []*Step
		for _, step := range runnable {
			if !inFlight[step.Name] && !terminal[step.Name] {
				toStart = append(toStart, step)
			}
		}

		for i := range r.pipeline.Spec.Steps {
			s := &r.pipeline.Spec.Steps[i]
			res := r.results[s.Name]
			if res.Status == StatusPending && !inFlight[s.Name] && r.hasFailedDep_(s) {
				res.Status = StatusSkipped
				slog.Info("step skipped", "step", s.Name, "reason", "dependency failed")
			}
		}

		allDone := len(terminal) == len(r.pipeline.Spec.Steps) && len(inFlight) == 0
		if allDone && len(toStart) == 0 {
			r.mu.Unlock()
			break
		}

		for _, step := range toStart {
			inFlight[step.Name] = true
			r.results[step.Name].Status = StatusRunning
			wg.Add(1)

			go func(s *Step) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				result := r.runWithRetry(ctx, s)

				r.mu.Lock()
				r.results[s.Name] = result
				delete(inFlight, s.Name)
				r.mu.Unlock()
			}(step)
		}
		r.mu.Unlock()

		time.Sleep(200 * time.Millisecond)
	}

	wg.Wait()

	runResult.EndedAt = time.Now()
	for k, v := range r.results {
		runResult.Steps[k] = v
	}
	return runResult
}

func (r *Runner) runWithRetry(ctx context.Context, step *Step) *StepResult {
	result := &StepResult{StepName: step.Name, StartedAt: time.Now()}
	maxAttempts := r.cfg.MaxRetries + 1

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result.Attempts = attempt

		if attempt > 1 {
			slog.Info("step retry", "step", step.Name, "attempt", attempt, "max", maxAttempts)
			select {
			case <-ctx.Done():
				result.Status = StatusFailed
				result.Err = ctx.Err()
				result.EndedAt = time.Now()
				return result
			case <-time.After(r.cfg.RetryDelay):
			}
		}

		err := r.execFn(ctx, step)
		if err == nil {
			result.Status = StatusDone
			result.EndedAt = time.Now()
			slog.Info("step done", "step", step.Name, "attempt", attempt)
			return result
		}

		slog.Warn("step failed", "step", step.Name, "attempt", attempt, "max", maxAttempts, "err", err)
		result.Err = err
		result.ErrMsg = err.Error()
	}

	result.Status = StatusFailed
	result.EndedAt = time.Now()
	return result
}

func (r *Runner) doneMap() map[string]bool {
	done := make(map[string]bool)
	for name, res := range r.results {
		if res.Status == StatusDone || res.Status == StatusSkipped {
			done[name] = true
		}
	}
	return done
}

func (r *Runner) terminalMap() map[string]bool {
	terminal := make(map[string]bool)
	for name, res := range r.results {
		if res.Status == StatusDone || res.Status == StatusSkipped || res.Status == StatusFailed {
			terminal[name] = true
		}
	}
	return terminal
}

func (r *Runner) hasFailedDep_(step *Step) bool {
	for _, dep := range step.DependsOn {
		res, ok := r.results[dep]
		if ok && (res.Status == StatusFailed || res.Status == StatusSkipped) {
			return true
		}
	}
	return false
}

func (r *Runner) concurrency() int {
	if r.cfg.Concurrency <= 0 {
		return 999
	}
	return r.cfg.Concurrency
}

func PrintRunResult(res *RunResult) {
	elapsed := res.EndedAt.Sub(res.StartedAt).Round(time.Millisecond)
	fmt.Printf("\n=== pipeline %q — %s (%s) ===\n", res.PipelineName, statusLabel(res), elapsed)
	for _, step := range res.Steps {
		dur := step.EndedAt.Sub(step.StartedAt).Round(time.Millisecond)
		if step.Err != nil {
			fmt.Printf("  %-20s %s (%s, %d attempts) — %v\n", step.StepName, step.Status, dur, step.Attempts, step.Err)
		} else {
			fmt.Printf("  %-20s %s (%s)\n", step.StepName, step.Status, dur)
		}
	}
}

func statusLabel(res *RunResult) string {
	if res.Failed() {
		return "FAILED"
	}
	return "SUCCESS"
}
