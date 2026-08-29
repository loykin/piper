package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
	"github.com/loykin/piper/pkg/security"
)

// Project-level notebook_execution.mcp_policy values (design doc §9.3).
// Despite the "mcp_" prefix inherited from the design doc's field name,
// this phase enforces the gate for REST-originated execution requests too
// (no MCP caller exists yet) — see the CreateExecution doc comment.
const (
	PolicyDisabled         = "disabled"
	PolicyApprovalRequired = "approval_required"
	PolicyAllowed          = "allowed"
)

// ErrForbidden is returned when actor lacks ownership/role to act on a
// resource it otherwise has read access to (e.g. a member trying to cancel
// another member's execution — design doc §9.1: "타인의 실행/Kernel 취소" is
// admin-only). The REST handler maps this to 403.
var ErrForbidden = errors.New("execution: forbidden")

// Actor identifies who is calling into Service — the identity/role Home's
// own middleware already resolved for this request (mirrors
// pkg/pipeline/run/handler.go's authFrom). Service is HTTP-framework-free
// (design doc §4.1), so this is a plain struct rather than reading Gin
// context directly.
type Actor struct {
	ID   string
	Role security.ProjectRole
	// ClientID identifies the calling REST/UI source (or, in a later MCP
	// phase, the MCP client) for audit purposes (design doc §13.2).
	ClientID string
}

// CreateKernelSessionRequest is the input to Service.CreateKernelSession.
type CreateKernelSessionRequest struct {
	NotebookPath string
	KernelName   string // defaults to "python3" when empty
}

// CellEdit describes a single-cell execution request (design doc §6.2).
type CellEdit struct {
	Mode   string // CellEditAppend | CellEditReplace
	Code   string
	CellID string // required for CellEditReplace
}

// CreateExecutionRequest is the input to Service.CreateExecution.
type CreateExecutionRequest struct {
	Kind            string // KindNotebook | KindCell
	Path            string
	KernelSessionID string
	Edit            *CellEdit // required for KindCell
	CreateIfMissing bool
	TimeoutSeconds  int
}

// Deps are Service's constructor dependencies.
type Deps struct {
	Repo      Repository
	Notebooks notebook.Repository
	Gateway   NotebookGateway
	Events    event.Publisher
	Limits    Limits
	// PolicyDefault is the system-wide notebook_execution.mcp_policy used
	// when a project has no override row (Repo.GetExecutionPolicy returns
	// ""). Defaults to PolicyApprovalRequired if empty, matching the
	// design doc's documented default.
	PolicyDefault string
	// Now and NewID are overridable for tests; default to time.Now().UTC
	// and uuid.NewString.
	Now   func() time.Time
	NewID func() string
}

// Service implements the domain logic and state transitions for Kernel
// sessions and Notebook executions (design doc §4.1's execution.Service).
// It holds no Gin or MCP type.
type Service struct {
	deps      Deps
	scheduler *Scheduler
	bgCtx     context.Context
	inflight  sync.Map // execution ID -> context.CancelFunc, running executions only
	wg        sync.WaitGroup
}

// NewService constructs a Service. bgCtx is a long-lived context (Piper's
// own p.ctx, cancelled on Close) that outlives any single HTTP request —
// execution runs asynchronously in a goroutine derived from bgCtx, not from
// the context of the HTTP call that created it, since that call's context
// is cancelled the moment the 201 response is written (design doc §4's
// principle 4: "실행 리소스를 만들고 즉시 execution_id를 반환").
func NewService(bgCtx context.Context, deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.PolicyDefault == "" {
		deps.PolicyDefault = PolicyApprovalRequired
	}
	return &Service{deps: deps, scheduler: NewScheduler(deps.Limits), bgCtx: bgCtx}
}

// Shutdown waits (up to ctx's deadline) for in-flight execution goroutines
// to observe cancellation of bgCtx and return. Callers should cancel bgCtx
// before calling Shutdown.
func (s *Service) Shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *Service) publish(projectID, eventType string, fields map[string]any) {
	if s.deps.Events == nil {
		return
	}
	s.deps.Events.Publish(event.New(projectID, eventType, fields))
}

func execFields(e *NotebookExecution) map[string]any {
	return map[string]any{
		"execution_id": e.ID,
		"notebook":     e.NotebookName,
		"kind":         e.Kind,
		"status":       e.Status,
		"current_cell": e.CurrentCell,
		"total_cells":  e.TotalCells,
		"error_code":   e.ErrorCode,
	}
}

func (s *Service) checkOwnership(actor Actor, ownerID string) error {
	if actor.Role >= security.ProjectRoleAdmin {
		return nil
	}
	if ownerID != "" && actor.ID == ownerID {
		return nil
	}
	return ErrForbidden
}

func (s *Service) resolvePolicy(ctx context.Context, projectID string) (string, error) {
	p, err := s.deps.Repo.GetExecutionPolicy(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("execution: resolve policy: %w", err)
	}
	if p == "" {
		return s.deps.PolicyDefault, nil
	}
	return p, nil
}

// GetPolicy returns the effective notebook_execution.mcp_policy for projectID.
func (s *Service) GetPolicy(ctx context.Context, projectID string) (string, error) {
	return s.resolvePolicy(ctx, projectID)
}

// Limits exposes the configured design doc §11.1 concurrency/size limits —
// notably InlineOutputBytes and FileReadBytes, which a later MCP phase
// layer (pkg/notebook/execution/mcp) needs to decide whether a resource
// read should inline its content or return a piper:// resource link instead
// (design doc §8.3), without duplicating the limit as a second hardcoded
// constant that could drift from what this Service was actually configured
// with.
func (s *Service) Limits() Limits { return s.scheduler.Limits() }

// SetPolicy sets a project-level policy override. Callers must already have
// enforced admin role (design doc §9.1: "MCP 정책과 무인 실행 권한 관리" is
// admin-only) via the REST route's middleware — Service does not re-check
// role here since Actor isn't threaded through (there's no ownership
// concept for a project-wide setting).
func (s *Service) SetPolicy(ctx context.Context, actor Actor, projectID, policy string) error {
	switch policy {
	case PolicyDisabled, PolicyApprovalRequired, PolicyAllowed:
	default:
		return newErr(ErrCodePathInvalid, false, "mcp_policy must be disabled, approval_required, or allowed")
	}
	return s.deps.Repo.SetExecutionPolicy(ctx, projectID, policy, actor.ID)
}

func validateNotebookPath(p string) (string, error) {
	clean, err := notebook.CleanWorkspacePath(p)
	if err != nil {
		return "", newErr(ErrCodePathInvalid, false, "invalid path: %v", err)
	}
	if clean == "" || !strings.HasSuffix(clean, ".ipynb") {
		return "", newErr(ErrCodePathInvalid, false, "path must be a non-empty .ipynb path")
	}
	return clean, nil
}

func (s *Service) getRunningServer(ctx context.Context, projectID, notebookName string) (*notebook.NotebookServer, error) {
	server, err := s.deps.Notebooks.Get(ctx, projectID, notebookName)
	if err != nil {
		return nil, fmt.Errorf("execution: get notebook server: %w", err)
	}
	if server == nil {
		return nil, ErrNotFound
	}
	if server.Status != notebook.StatusRunning {
		return nil, newErr(ErrCodeNotebookNotRunning, false, "notebook server %q is not running", notebookName)
	}
	return server, nil
}

func isCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// --- Kernel sessions ---------------------------------------------------

func (s *Service) createKernelSessionRecord(ctx context.Context, actor Actor, server *notebook.NotebookServer, notebookName, notebookPath, kernelName string) (*KernelSession, error) {
	if kernelName == "" {
		kernelName = "python3"
	}
	if err := s.scheduler.checkKernelAdmission(ctx, s.deps.Repo, server.ProjectID, notebookName); err != nil {
		return nil, err
	}
	id := s.deps.NewID()
	info, err := s.deps.Gateway.CreateKernelSession(ctx, server, notebookPath, kernelName)
	if err != nil {
		return nil, err
	}
	now := s.deps.Now()
	ks := &KernelSession{
		ID:               id,
		ProjectID:        server.ProjectID,
		NotebookName:     notebookName,
		NotebookPath:     notebookPath,
		JupyterSessionID: info.JupyterSessionID,
		KernelID:         info.KernelID,
		KernelName:       info.KernelName,
		Status:           KernelStatusIdle,
		CreatedBy:        actor.ID,
		ClientID:         actor.ClientID,
		LastActivityAt:   now,
		CreatedAt:        now,
	}
	if err := s.deps.Repo.CreateKernelSession(ctx, ks); err != nil {
		return nil, err
	}
	s.publish(server.ProjectID, "notebook.kernel.created", map[string]any{"kernel_session_id": id, "notebook": notebookName})
	return ks, nil
}

// CreateKernelSession starts a new Piper-owned Jupyter kernel session for
// notebookName (design doc §7.2, member role).
func (s *Service) CreateKernelSession(ctx context.Context, actor Actor, projectID, notebookName string, req CreateKernelSessionRequest) (*KernelSession, error) {
	cleanPath, err := validateNotebookPath(req.NotebookPath)
	if err != nil {
		return nil, err
	}
	server, err := s.getRunningServer(ctx, projectID, notebookName)
	if err != nil {
		return nil, err
	}
	return s.createKernelSessionRecord(ctx, actor, server, notebookName, cleanPath, req.KernelName)
}

// GetKernelSession returns one kernel session by ID.
func (s *Service) GetKernelSession(ctx context.Context, projectID, id string) (*KernelSession, error) {
	ks, err := s.deps.Repo.GetKernelSession(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	if ks == nil {
		return nil, ErrNotFound
	}
	return ks, nil
}

// ListKernelSessions lists kernel sessions for a notebook. Non-admin actors
// only see their own sessions (design doc §7.2: "호출자가 소유한 세션 목록;
// admin은 전체").
func (s *Service) ListKernelSessions(ctx context.Context, actor Actor, projectID, notebookName string, limit, offset int) ([]*KernelSession, error) {
	createdBy := actor.ID
	if actor.Role >= security.ProjectRoleAdmin {
		createdBy = ""
	}
	return s.deps.Repo.ListKernelSessions(ctx, projectID, notebookName, createdBy, limit, offset)
}

// InterruptKernelSession sends SIGINT to the session's kernel (design doc §6.3).
func (s *Service) InterruptKernelSession(ctx context.Context, actor Actor, projectID, id string) error {
	ks, err := s.GetKernelSession(ctx, projectID, id)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(actor, ks.CreatedBy); err != nil {
		return err
	}
	server, err := s.getRunningServer(ctx, projectID, ks.NotebookName)
	if err != nil {
		return err
	}
	if err := s.deps.Gateway.InterruptKernel(ctx, server, ks.KernelID); err != nil {
		return err
	}
	ks.LastActivityAt = s.deps.Now()
	return s.deps.Repo.UpdateKernelSession(ctx, ks)
}

// RestartKernelSession restarts the session's kernel in place.
func (s *Service) RestartKernelSession(ctx context.Context, actor Actor, projectID, id string) error {
	ks, err := s.GetKernelSession(ctx, projectID, id)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(actor, ks.CreatedBy); err != nil {
		return err
	}
	server, err := s.getRunningServer(ctx, projectID, ks.NotebookName)
	if err != nil {
		return err
	}
	if err := s.deps.Gateway.RestartKernel(ctx, server, ks.KernelID); err != nil {
		return err
	}
	ks.Status = KernelStatusIdle
	ks.LastActivityAt = s.deps.Now()
	return s.deps.Repo.UpdateKernelSession(ctx, ks)
}

// CloseKernelSession terminates a Piper-owned kernel session. Only sessions
// Piper itself created are managed this way (design doc §5.1: "사용자가
// Jupyter UI에서 만든 세션은 v1 관리 대상이 아니다").
func (s *Service) CloseKernelSession(ctx context.Context, actor Actor, projectID, id string) error {
	ks, err := s.GetKernelSession(ctx, projectID, id)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(actor, ks.CreatedBy); err != nil {
		return err
	}
	if server, serr := s.getRunningServer(ctx, projectID, ks.NotebookName); serr == nil {
		if err := s.deps.Gateway.DeleteKernelSession(ctx, server, ks.JupyterSessionID); err != nil {
			slog.Warn("execution: delete jupyter session failed during close", "kernel_session_id", id, "err", err)
		}
	}
	now := s.deps.Now()
	ks.Status = KernelStatusClosed
	ks.ClosedAt = &now
	ks.LastActivityAt = now
	if err := s.deps.Repo.UpdateKernelSession(ctx, ks); err != nil {
		return err
	}
	s.scheduler.ReleaseKernel(id)
	s.publish(projectID, "notebook.kernel.closed", map[string]any{"kernel_session_id": id, "notebook": ks.NotebookName})
	return nil
}

// --- Executions ----------------------------------------------------------

func hashCreateRequest(req CreateExecutionRequest) string {
	type canon struct {
		Kind            string `json:"kind"`
		Path            string `json:"path"`
		KernelSessionID string `json:"kernel_session_id"`
		EditMode        string `json:"edit_mode,omitempty"`
		EditCode        string `json:"edit_code,omitempty"`
		EditCellID      string `json:"edit_cell_id,omitempty"`
		CreateIfMissing bool   `json:"create_if_missing"`
		TimeoutSeconds  int    `json:"timeout_seconds"`
	}
	c := canon{Kind: req.Kind, Path: req.Path, KernelSessionID: req.KernelSessionID, CreateIfMissing: req.CreateIfMissing, TimeoutSeconds: req.TimeoutSeconds}
	if req.Edit != nil {
		c.EditMode, c.EditCode, c.EditCellID = req.Edit.Mode, req.Edit.Code, req.Edit.CellID
	}
	raw, _ := json.Marshal(c)
	return SHA256Hex(raw)
}

func resultPathFor(id string) string {
	return ".piper/executions/" + id + "/result.ipynb"
}

// CreateExecution creates a new NotebookExecution (design doc §6.1/§6.2/§7.3).
//
// Approval gate: this phase has no MCP caller yet, but per the task's
// instruction the design's approval-required gate is not MCP-specific — it
// is enforced here for REST-originated requests too, using
// notebook_execution.mcp_policy (project override, falling back to the
// system default). "disabled" rejects the request outright;
// "approval_required" creates an awaiting_approval row for any actor,
// including an admin caller (admin still has to call Approve, which keeps
// a real audit trail of who approved what and when — see §13.2); "allowed"
// queues immediately.
func (s *Service) CreateExecution(ctx context.Context, actor Actor, projectID, notebookName string, req CreateExecutionRequest, idempotencyKey string) (exec *NotebookExecution, replayed bool, err error) {
	if len(idempotencyKey) > MaxIdempotencyKeyLen {
		return nil, false, newErr(ErrCodePathInvalid, false, "idempotency key exceeds %d characters", MaxIdempotencyKeyLen)
	}
	cleanPath, err := validateNotebookPath(req.Path)
	if err != nil {
		return nil, false, err
	}
	if req.Kind != KindNotebook && req.Kind != KindCell {
		return nil, false, newErr(ErrCodePathInvalid, false, "kind must be %q or %q", KindNotebook, KindCell)
	}
	if req.Kind == KindCell {
		if req.Edit == nil || (req.Edit.Mode != CellEditAppend && req.Edit.Mode != CellEditReplace) {
			return nil, false, newErr(ErrCodePathInvalid, false, "cell execution requires edit.mode append or replace")
		}
		if req.Edit.Mode == CellEditReplace && strings.TrimSpace(req.Edit.CellID) == "" {
			return nil, false, newErr(ErrCodePathInvalid, false, "replace mode requires edit.cell_id")
		}
		if strings.TrimSpace(req.Edit.Code) == "" {
			return nil, false, newErr(ErrCodePathInvalid, false, "edit.code is required")
		}
	}

	requestHash := hashCreateRequest(req)
	if idempotencyKey != "" {
		existing, ferr := s.deps.Repo.FindExecutionByIdempotencyKey(ctx, projectID, notebookName, actor.ID, idempotencyKey)
		if ferr != nil {
			return nil, false, ferr
		}
		if existing != nil {
			if existing.RequestHash != requestHash {
				return nil, false, ErrConflict
			}
			return existing, true, nil
		}
	}

	server, err := s.getRunningServer(ctx, projectID, notebookName)
	if err != nil {
		return nil, false, err
	}

	policy, err := s.resolvePolicy(ctx, projectID)
	if err != nil {
		return nil, false, err
	}
	if policy == PolicyDisabled {
		return nil, false, newErr(ErrCodeApprovalRequired, false, "notebook execution is disabled for this project")
	}

	id := s.deps.NewID()
	now := s.deps.Now()
	exec = &NotebookExecution{
		ID:              id,
		ProjectID:       projectID,
		NotebookName:    notebookName,
		NotebookPath:    cleanPath,
		ResultPath:      resultPathFor(id),
		KernelSessionID: req.KernelSessionID,
		Kind:            req.Kind,
		RequestedBy:     actor.ID,
		ClientID:        actor.ClientID,
		IdempotencyKey:  idempotencyKey,
		RequestHash:     requestHash,
		QueuedAt:        now,
		UpdatedAt:       now,
	}

	if req.Kind == KindNotebook {
		_, hash, rerr := s.deps.Gateway.ReadNotebook(ctx, server, cleanPath)
		if rerr != nil {
			return nil, false, rerr
		}
		exec.BaseContentHash = hash
	} else {
		if err := s.stageCellExecution(ctx, server, exec, req); err != nil {
			return nil, false, err
		}
	}

	if policy == PolicyApprovalRequired {
		exec.Status = StatusAwaitingApproval
	} else {
		if err := s.scheduler.checkQueueAdmission(ctx, s.deps.Repo, projectID); err != nil {
			return nil, false, err
		}
		exec.Status = StatusQueued
	}

	if err := s.deps.Repo.CreateExecution(ctx, exec); err != nil {
		return nil, false, err
	}
	if exec.Status == StatusQueued {
		s.publish(projectID, "notebook.execution.queued", execFields(exec))
		// dispatch mutates the record it's given in place as the run
		// progresses (status, current_cell, ...). exec itself is about to
		// be returned to the caller, who may keep reading it after this
		// call returns — dispatch must run against its own copy so the
		// background goroutine and the caller never touch the same struct
		// concurrently.
		running := *exec
		s.dispatch(&running)
	}
	return exec, false, nil
}

// stageCellExecution implements the "cell" kind of §6.2: it reads (or, with
// create_if_missing, synthesizes) the target notebook, applies the
// append/replace edit, and immediately persists the edited-but-not-yet-executed
// document to exec.ResultPath via the Gateway.
//
// Judgment call: design doc §5.3 says code text must never be duplicated
// into the DB ("코드 원문과 rich output 전체를 DB에 복제하지 않는다"), but the
// code has to live somewhere durable enough to survive an awaiting_approval
// row sitting through a Piper restart before it's ever approved. Rather
// than inventing a second storage mechanism, this reuses the same
// Contents API the notebook itself is already stored through: the edited
// document is written to exec.ResultPath (a location already earmarked as
// this execution's private, execution-scoped file) as soon as the request
// is created, before either approval or scheduling. The DB only ever
// stores its SHA-256 (SourceSHA256). runExecution (service_run.go) then
// treats ResultPath as its working copy for a "cell" kind execution instead
// of re-reading NotebookPath.
func (s *Service) stageCellExecution(ctx context.Context, server *notebook.NotebookServer, exec *NotebookExecution, req CreateExecutionRequest) error {
	doc, origHash, err := s.deps.Gateway.ReadNotebook(ctx, server, exec.NotebookPath)
	if err != nil {
		if req.CreateIfMissing && isCode(err, ErrCodePathInvalid) {
			doc = jupyter.EmptyNotebook()
			origHash = ""
		} else {
			return err
		}
	}
	exec.BaseContentHash = origHash
	exec.SourceSHA256 = SHA256Hex([]byte(req.Edit.Code))

	switch req.Edit.Mode {
	case CellEditAppend:
		newID := "piper-" + uuid.NewString()
		doc.AppendCodeCell(newID, req.Edit.Code)
	case CellEditReplace:
		if _, err := doc.ReplaceCellSource(req.Edit.CellID, req.Edit.Code); err != nil {
			return newErr(ErrCodePathInvalid, false, "cell %q not found", req.Edit.CellID)
		}
	}

	if err := s.deps.Gateway.SaveNotebook(ctx, server, exec.ResultPath, doc); err != nil {
		return err
	}
	exec.TotalCells = 1
	return nil
}

// GetExecution returns one execution by ID.
func (s *Service) GetExecution(ctx context.Context, projectID, id string) (*NotebookExecution, error) {
	e, err := s.deps.Repo.GetExecution(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotFound
	}
	return e, nil
}

// ListExecutions returns a page of execution history plus the total count
// (for the X-Total-Count header, matching the rest of this codebase's list
// endpoints).
func (s *Service) ListExecutions(ctx context.Context, projectID, notebookName string, limit, offset int) ([]*NotebookExecution, int, error) {
	list, err := s.deps.Repo.ListExecutions(ctx, projectID, notebookName, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.deps.Repo.CountExecutions(ctx, projectID, notebookName)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CancelExecution cancels a not-yet-terminal execution (design doc §6.3, §7.3).
func (s *Service) CancelExecution(ctx context.Context, actor Actor, projectID, id string) error {
	exec, err := s.GetExecution(ctx, projectID, id)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(actor, exec.RequestedBy); err != nil {
		return err
	}
	switch exec.Status {
	case StatusAwaitingApproval, StatusQueued:
		now := s.deps.Now()
		exec.Status = StatusCancelled
		exec.ErrorCode = ErrCodeExecutionCancelled
		exec.FinishedAt = &now
		exec.UpdatedAt = now
		if err := s.deps.Repo.UpdateExecution(ctx, exec); err != nil {
			return err
		}
		s.publish(projectID, "notebook.execution.cancelled", execFields(exec))
		return nil
	case StatusRunning:
		if cancel, ok := s.inflight.Load(id); ok {
			cancel.(context.CancelFunc)()
		}
		if CanTransitionExecution(exec.Status, StatusCancelling) {
			exec.Status = StatusCancelling
			exec.UpdatedAt = s.deps.Now()
			_ = s.deps.Repo.UpdateExecution(ctx, exec)
		}
		return nil
	case StatusCancelling:
		return nil
	default:
		return ErrConflict
	}
}

// ApproveExecution moves an awaiting_approval execution to queued and
// schedules it (design doc §7.3, admin role).
func (s *Service) ApproveExecution(ctx context.Context, actor Actor, projectID, id string) error {
	exec, err := s.GetExecution(ctx, projectID, id)
	if err != nil {
		return err
	}
	if exec.Status != StatusAwaitingApproval {
		return ErrConflict
	}
	if err := s.scheduler.checkQueueAdmission(ctx, s.deps.Repo, projectID); err != nil {
		return err
	}
	now := s.deps.Now()
	exec.Status = StatusQueued
	exec.ApprovedBy = actor.ID
	exec.ApprovedAt = &now
	exec.UpdatedAt = now
	if err := s.deps.Repo.UpdateExecution(ctx, exec); err != nil {
		return err
	}
	s.publish(projectID, "notebook.execution.queued", execFields(exec))
	s.dispatch(exec)
	return nil
}

// DenyExecution moves an awaiting_approval execution to cancelled (design
// doc §7.3, admin role).
func (s *Service) DenyExecution(ctx context.Context, actor Actor, projectID, id string) error {
	exec, err := s.GetExecution(ctx, projectID, id)
	if err != nil {
		return err
	}
	if exec.Status != StatusAwaitingApproval {
		return ErrConflict
	}
	now := s.deps.Now()
	exec.Status = StatusCancelled
	exec.DeniedBy = actor.ID
	exec.DeniedAt = &now
	exec.FinishedAt = &now
	exec.ErrorCode = ErrCodeApprovalDenied
	exec.UpdatedAt = now
	if err := s.deps.Repo.UpdateExecution(ctx, exec); err != nil {
		return err
	}
	s.publish(projectID, "notebook.execution.cancelled", execFields(exec))
	return nil
}

// dispatch launches the async runner for a queued execution, deriving its
// context from Service's long-lived background context — never from the
// context of the HTTP call that reached CreateExecution/ApproveExecution.
func (s *Service) dispatch(exec *NotebookExecution) {
	runCtx, cancel := context.WithCancel(s.bgCtx)
	s.inflight.Store(exec.ID, cancel)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.inflight.Delete(exec.ID)
		defer cancel()
		s.runExecution(runCtx, exec)
	}()
}
