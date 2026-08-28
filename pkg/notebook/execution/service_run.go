package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
)

// ensureKernel resolves the KernelSession an execution runs against: the
// caller-requested one (exec.KernelSessionID, set at CreateExecution time)
// if present and usable, otherwise a freshly created one that is then
// remembered on exec for the rest of this run and for any future recovery
// pass.
func (s *Service) ensureKernel(ctx context.Context, server *notebook.NotebookServer, exec *NotebookExecution) (*KernelSession, error) {
	if exec.KernelSessionID != "" {
		ks, err := s.deps.Repo.GetKernelSession(ctx, exec.ProjectID, exec.KernelSessionID)
		if err != nil {
			return nil, fmt.Errorf("execution: get kernel session: %w", err)
		}
		if ks == nil || ks.Status == KernelStatusClosed || ks.Status == KernelStatusFailed {
			return nil, newErr(ErrCodeKernelUnavailable, false, "requested kernel session is not available")
		}
		return ks, nil
	}
	ks, err := s.createKernelSessionRecord(ctx, Actor{ID: exec.RequestedBy, ClientID: exec.ClientID}, server, exec.NotebookName, exec.NotebookPath, "")
	if err != nil {
		return nil, newErr(ErrCodeKernelUnavailable, true, "could not start a kernel: %v", err)
	}
	exec.KernelSessionID = ks.ID
	if err := s.deps.Repo.UpdateExecution(ctx, exec); err != nil {
		slog.Warn("execution: failed to persist auto-created kernel session id", "execution_id", exec.ID, "err", err)
	}
	return ks, nil
}

// runExecution is the async body of one NotebookExecution run (design doc
// §6.1 steps 5-12 for "notebook" kind; §6.2 for "cell" kind, whose edited
// document was already staged at exec.ResultPath by
// service.go's stageCellExecution). ctx is derived from Service's
// long-lived background context via dispatch, and is cancelled either by
// CancelExecution or by Service shutdown — never by the HTTP request that
// triggered this run.
func (s *Service) runExecution(ctx context.Context, exec *NotebookExecution) {
	release, err := s.scheduler.AcquireNotebookSlot(ctx, exec.ProjectID, exec.NotebookName)
	if err != nil {
		s.finishExecution(exec, StatusCancelled, ErrCodeExecutionCancelled, "cancelled while waiting for a run slot")
		return
	}
	defer release()

	if ctx.Err() != nil {
		s.finishExecution(exec, StatusCancelled, ErrCodeExecutionCancelled, "cancelled before starting")
		return
	}

	limits := s.scheduler.Limits()
	runCtx, cancelTimeout := context.WithTimeout(ctx, limits.ExecutionTimeout)
	defer cancelTimeout()

	now := s.deps.Now()
	exec.Status = StatusRunning
	exec.StartedAt = &now
	exec.UpdatedAt = now
	if err := s.deps.Repo.UpdateExecution(runCtx, exec); err != nil {
		slog.Error("execution: failed to mark running", "execution_id", exec.ID, "err", err)
		return
	}
	s.publish(exec.ProjectID, "notebook.execution.started", execFields(exec))

	server, err := s.deps.Notebooks.Get(runCtx, exec.ProjectID, exec.NotebookName)
	if err != nil || server == nil || server.Status != notebook.StatusRunning {
		s.finishExecution(exec, StatusFailed, ErrCodeNotebookNotRunning, "notebook server is not running")
		return
	}

	ks, err := s.ensureKernel(runCtx, server, exec)
	if err != nil {
		s.finishExecution(exec, StatusFailed, ErrCodeKernelUnavailable, err.Error())
		return
	}

	releaseKernel, err := s.scheduler.AcquireKernelLock(runCtx, ks.ID)
	if err != nil {
		s.finishExecution(exec, StatusCancelled, ErrCodeExecutionCancelled, "cancelled waiting for kernel")
		return
	}
	defer releaseKernel()

	channel, err := s.deps.Gateway.OpenChannel(runCtx, server, ks.KernelID, ks.ID)
	if err != nil {
		s.finishExecution(exec, StatusFailed, ErrCodeKernelUnavailable, "could not open kernel channel")
		return
	}
	defer func() { _ = channel.Close() }()

	ks.Status = KernelStatusBusy
	ks.LastActivityAt = s.deps.Now()
	_ = s.deps.Repo.UpdateKernelSession(context.Background(), ks)
	defer func() {
		ks.Status = KernelStatusIdle
		ks.LastActivityAt = s.deps.Now()
		_ = s.deps.Repo.UpdateKernelSession(context.Background(), ks)
	}()

	readSrc := exec.NotebookPath
	if exec.Kind == KindCell {
		readSrc = exec.ResultPath
	}
	doc, _, err := s.deps.Gateway.ReadNotebook(runCtx, server, readSrc)
	if err != nil {
		s.finishExecution(exec, StatusFailed, ErrCodeRuntimeUnavailable, "could not load working document")
		return
	}

	cellIndexes := doc.CodeCellIndexes()
	if exec.Kind == KindCell {
		if len(cellIndexes) == 0 {
			s.finishExecution(exec, StatusFailed, ErrCodePathInvalid, "staged document has no code cell to execute")
			return
		}
		cellIndexes = cellIndexes[len(cellIndexes)-1:]
	}
	exec.TotalCells = len(cellIndexes)
	exec.CurrentCell = 0
	_ = s.deps.Repo.UpdateExecution(context.Background(), exec)

	cellFailed := false
	lastProgress := time.Time{}
	for i, idx := range cellIndexes {
		if runCtx.Err() != nil {
			break
		}
		cell := &doc.Cells[idx]
		sink := &collectingSink{limit: limits.InlineOutputBytes}
		cellCtx, cancelCell := context.WithTimeout(runCtx, limits.CellTimeout)
		result, execErr := channel.ExecuteCell(cellCtx, cell.Source.String(), sink)
		timedOut := cellCtx.Err() == context.DeadlineExceeded
		cancelCell()
		cell.Outputs = sink.outputs

		if execErr != nil {
			if runCtx.Err() != nil {
				break
			}
			_ = s.saveResult(context.Background(), server, exec, doc)
			if timedOut {
				s.finishExecution(exec, StatusTimedOut, ErrCodeExecutionTimeout, fmt.Sprintf("cell %d timed out", i))
			} else {
				s.finishExecution(exec, StatusFailed, ErrCodeKernelDied, "lost communication with the kernel")
			}
			return
		}

		ec := result.ExecutionCount
		cell.ExecutionCount = &ec
		exec.CurrentCell = i + 1
		if time.Since(lastProgress) > time.Second || i == len(cellIndexes)-1 {
			exec.UpdatedAt = s.deps.Now()
			_ = s.deps.Repo.UpdateExecution(context.Background(), exec)
			s.publish(exec.ProjectID, "notebook.execution.progress", execFields(exec))
			lastProgress = time.Now()
		}
		if result.Status == "error" {
			cellFailed = true
			exec.ErrorMessage = truncateStr(result.ErrorName+": "+result.ErrorValue, 500)
			break
		}
	}

	if runCtx.Err() != nil {
		interruptCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = s.deps.Gateway.InterruptKernel(interruptCtx, server, ks.KernelID)
		cancel()
		_ = s.saveResult(context.Background(), server, exec, doc)
		if ctx.Err() != nil {
			s.finishExecution(exec, StatusCancelled, ErrCodeExecutionCancelled, "execution cancelled")
		} else {
			s.finishExecution(exec, StatusTimedOut, ErrCodeExecutionTimeout, "execution timed out")
		}
		return
	}

	if err := s.saveResult(context.Background(), server, exec, doc); err != nil {
		s.finishExecution(exec, StatusFailed, ErrCodeRuntimeUnavailable, "failed to save result notebook")
		return
	}

	if cellFailed {
		s.finishExecution(exec, StatusFailed, "", exec.ErrorMessage)
		return
	}

	conflict, err := s.checkConflictAndSave(context.Background(), server, exec, doc)
	if err != nil {
		s.finishExecution(exec, StatusFailed, ErrCodeRuntimeUnavailable, "failed to finalize notebook")
		return
	}
	if conflict {
		s.finishExecution(exec, StatusConflicted, ErrCodeContentConflict, "original notebook changed since this execution started; result preserved at result_path")
		return
	}
	s.finishExecution(exec, StatusSucceeded, "", "")
}

// saveResult writes doc to exec.ResultPath — the recovery copy design doc
// §6.1 step 10 requires exist before any conditional write-back to the
// original.
func (s *Service) saveResult(ctx context.Context, server *notebook.NotebookServer, exec *NotebookExecution, doc *jupyter.Notebook) error {
	return s.deps.Gateway.SaveNotebook(ctx, server, exec.ResultPath, doc)
}

// checkConflictAndSave implements design doc §6.1 step 11: only write the
// executed document back to exec.NotebookPath if the original's content
// hash is unchanged from when this execution started (or, for a
// create_if_missing cell execution, the original still doesn't exist).
// Returns conflict=true without writing when that check fails; the caller
// is responsible for the .ipynb at ResultPath already being saved by then.
func (s *Service) checkConflictAndSave(ctx context.Context, server *notebook.NotebookServer, exec *NotebookExecution, doc *jupyter.Notebook) (bool, error) {
	_, currentHash, err := s.deps.Gateway.ReadNotebook(ctx, server, exec.NotebookPath)
	originalMissing := false
	if err != nil {
		if isCode(err, ErrCodePathInvalid) {
			originalMissing = true
		} else {
			return false, err
		}
	}

	var conflict bool
	if exec.BaseContentHash == "" {
		// The original did not exist when this execution was created
		// (create_if_missing). If it exists now, something else created it
		// first — do not clobber it.
		conflict = !originalMissing
	} else {
		conflict = originalMissing || currentHash != exec.BaseContentHash
	}
	if conflict {
		return true, nil
	}
	if err := s.deps.Gateway.SaveNotebook(ctx, server, exec.NotebookPath, doc); err != nil {
		return false, err
	}
	return false, nil
}

// finishExecution transitions exec to a terminal status and persists it
// using a fresh, non-cancelled context — runCtx/ctx may already be Done by
// the time this is called (execution_timeout or an explicit cancel), and a
// cancelled context must not be used for the final write that has to
// succeed regardless.
func (s *Service) finishExecution(exec *NotebookExecution, status, code, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	now := s.deps.Now()
	exec.Status = status
	if code != "" {
		exec.ErrorCode = code
	}
	if message != "" {
		exec.ErrorMessage = message
	}
	exec.FinishedAt = &now
	exec.UpdatedAt = now
	if err := s.deps.Repo.UpdateExecution(ctx, exec); err != nil {
		slog.Error("execution: failed to persist final status", "execution_id", exec.ID, "status", status, "err", err)
	}

	eventType := "notebook.execution.failed"
	switch status {
	case StatusSucceeded:
		eventType = "notebook.execution.succeeded"
	case StatusCancelled:
		eventType = "notebook.execution.cancelled"
	case StatusConflicted:
		eventType = "notebook.execution.conflicted"
	}
	s.publish(exec.ProjectID, eventType, execFields(exec))
}

// collectingSink implements jupyter.OutputSink, buffering outputs for one
// cell up to limit bytes (notebook_execution.inline_output_bytes, design
// doc §5.3/§11.1) before appending a truncation marker and dropping the
// rest.
type collectingSink struct {
	outputs   []jupyter.Output
	bytes     int
	limit     int
	truncated bool
}

func (c *collectingSink) OnOutput(o jupyter.Output) {
	if c.truncated {
		return
	}
	raw, _ := json.Marshal(o)
	if c.bytes+len(raw) > c.limit {
		c.truncated = true
		c.outputs = append(c.outputs, jupyter.Output{
			OutputType: "stream",
			Name:       "stderr",
			Text:       jupyter.NewSource("[piper] output truncated: exceeded inline_output_bytes limit\n"),
		})
		return
	}
	c.bytes += len(raw)
	c.outputs = append(c.outputs, o)
}

func (c *collectingSink) OnClearOutput() {
	c.outputs = nil
	c.bytes = 0
	c.truncated = false
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
