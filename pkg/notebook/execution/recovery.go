package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/loykin/piper/pkg/notebook"
)

// RecoverOnStartup scans every execution left in a non-terminal status from
// before a Piper restart and resolves it per design doc §11.2. It never
// re-executes code that might already have run — "모르면 재실행하지 않는다"
// — a queued (never-started) execution is safely re-queued, but a running
// one that Piper can't prove finished or not is marked
// failed(recovery_uncertain) instead of guessed at.
//
// Call this once at startup with a short-lived ctx (it only reads/writes
// the execution repository); dispatch of any re-queued execution runs
// against Service's own long-lived background context, same as a live
// CreateExecution/ApproveExecution.
func (s *Service) RecoverOnStartup(ctx context.Context) error {
	list, err := s.deps.Repo.ListExecutionsByStatus(ctx, ActiveExecutionStatuses)
	if err != nil {
		return err
	}
	for _, exec := range list {
		s.recoverOne(ctx, exec)
	}
	return nil
}

func (s *Service) recoverOne(ctx context.Context, exec *NotebookExecution) {
	switch exec.Status {
	case StatusQueued:
		s.recoverQueued(ctx, exec)
	case StatusRunning:
		// No way to tell whether the process that owned this execution was
		// mid-cell, about to write the result, or already done when Piper
		// stopped — do not guess. The result recovery copy at
		// exec.ResultPath (if the run got far enough to write one) is
		// preserved on disk regardless; only the DB status changes here.
		s.finishExecution(exec, StatusFailed, ErrCodeRecoveryUncertain, "piper restarted while this execution was running; outcome could not be verified")
	case StatusCancelling:
		s.recoverCancelling(ctx, exec)
	}
}

func (s *Service) recoverQueued(ctx context.Context, exec *NotebookExecution) {
	policy, err := s.resolvePolicy(ctx, exec.ProjectID)
	if err != nil || policy == PolicyDisabled {
		s.finishExecution(exec, StatusFailed, ErrCodeApprovalRequired, "notebook execution is disabled for this project")
		return
	}
	server, err := s.deps.Notebooks.Get(ctx, exec.ProjectID, exec.NotebookName)
	if err != nil || server == nil || server.Status != notebook.StatusRunning {
		s.finishExecution(exec, StatusFailed, ErrCodeNotebookNotRunning, "notebook server was not running after piper restart")
		return
	}
	slog.Info("execution: re-queuing after piper restart", "execution_id", exec.ID, "project_id", exec.ProjectID, "notebook", exec.NotebookName)
	s.dispatch(exec)
}

func (s *Service) recoverCancelling(ctx context.Context, exec *NotebookExecution) {
	server, err := s.deps.Notebooks.Get(ctx, exec.ProjectID, exec.NotebookName)
	if err == nil && server != nil && server.Status == notebook.StatusRunning && exec.KernelSessionID != "" {
		if ks, kerr := s.deps.Repo.GetKernelSession(ctx, exec.ProjectID, exec.KernelSessionID); kerr == nil && ks != nil && ks.KernelID != "" {
			interruptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if ierr := s.deps.Gateway.InterruptKernel(interruptCtx, server, ks.KernelID); ierr != nil {
				slog.Warn("execution: recovery interrupt failed", "execution_id", exec.ID, "err", ierr)
			}
			cancel()
		}
	}
	s.finishExecution(exec, StatusCancelled, ErrCodeExecutionCancelled, "cancelled before piper restart completed")
}
