// Package execution implements docs/jupyter-mcp-execution.md Phase 1: the
// domain layer that lets Piper (via REST today, MCP in a later phase) drive
// Kernel sessions and Notebook/cell executions against a Jupyter server it
// already manages the lifecycle of (pkg/notebook.Manager). This package
// owns a separate state machine from pkg/notebook's server lifecycle
// on purpose (design doc §3: "두 상태 머신을 섞지 않는다").
//
// Package boundary (design doc §4.1): Service holds all domain logic and
// state transitions and must not import Gin or any MCP type; Handler (the
// REST layer) calls into Service, never the other way around.
package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Kernel session statuses (design doc §5.1).
const (
	KernelStatusStarting   = "starting"
	KernelStatusIdle       = "idle"
	KernelStatusBusy       = "busy"
	KernelStatusRestarting = "restarting"
	KernelStatusClosed     = "closed"
	KernelStatusFailed     = "failed"
)

// KernelSession is a Piper-owned Jupyter kernel session. MCP protocol
// sessions are a completely different concept (an MCP transport session) —
// API and code always say "kernel_session" for this type to avoid
// confusing the two (design doc §5.1).
//
// JupyterSessionID and KernelID are Piper-internal: external callers only
// ever see the Piper-generated ID. They are deliberately excluded from
// KernelSessionResponse, mirroring how NotebookServerResponse
// (pkg/notebook/model.go) excludes NotebookServer.Token.
type KernelSession struct {
	ID               string     `db:"id"`
	ProjectID        string     `db:"project_id"`
	NotebookName     string     `db:"notebook_name"`
	NotebookPath     string     `db:"notebook_path"`
	JupyterSessionID string     `db:"jupyter_session_id"`
	KernelID         string     `db:"kernel_id"`
	KernelName       string     `db:"kernel_name"`
	Status           string     `db:"status"`
	CreatedBy        string     `db:"created_by"`
	ClientID         string     `db:"client_id"`
	LastActivityAt   time.Time  `db:"last_activity_at"`
	CreatedAt        time.Time  `db:"created_at"`
	ClosedAt         *time.Time `db:"closed_at"`
}

// KernelSessionResponse is the public wire representation of a
// KernelSession — see the KernelSession doc comment for what's excluded and
// why. Every REST handler that returns a KernelSession MUST map through
// this rather than serializing *KernelSession directly.
type KernelSessionResponse struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	NotebookName   string     `json:"notebook_name"`
	NotebookPath   string     `json:"notebook_path"`
	KernelName     string     `json:"kernel_name"`
	Status         string     `json:"status"`
	CreatedBy      string     `json:"created_by,omitempty"`
	ClientID       string     `json:"client_id,omitempty"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
}

// NewKernelSessionResponse maps a KernelSession to its public wire
// representation. Returns nil for nil input, matching
// notebook.NewNotebookServerResponse's convention.
func NewKernelSessionResponse(k *KernelSession) *KernelSessionResponse {
	if k == nil {
		return nil
	}
	return &KernelSessionResponse{
		ID:             k.ID,
		ProjectID:      k.ProjectID,
		NotebookName:   k.NotebookName,
		NotebookPath:   k.NotebookPath,
		KernelName:     k.KernelName,
		Status:         k.Status,
		CreatedBy:      k.CreatedBy,
		ClientID:       k.ClientID,
		LastActivityAt: k.LastActivityAt,
		CreatedAt:      k.CreatedAt,
		ClosedAt:       k.ClosedAt,
	}
}

// NewKernelSessionResponses maps a slice, preserving order and returning a
// non-nil empty slice for empty/nil input (so list endpoints return `[]`,
// never `null`).
func NewKernelSessionResponses(ks []*KernelSession) []*KernelSessionResponse {
	out := make([]*KernelSessionResponse, 0, len(ks))
	for _, k := range ks {
		out = append(out, NewKernelSessionResponse(k))
	}
	return out
}

// Execution kinds (design doc §6).
const (
	KindNotebook = "notebook"
	KindCell     = "cell"
)

// Cell edit modes (design doc §6.2) — the only two cell-execution modes v1
// supports; both leave a traceable record in the target notebook itself.
const (
	CellEditAppend  = "append"
	CellEditReplace = "replace"
)

// NotebookExecution statuses and their allowed transitions (design doc
// §5.2's diagram):
//
//	awaiting_approval ── approve ──> queued ── (admission) ──> running
//	awaiting_approval ── deny/expire ──> cancelled
//	queued            ── cancel ──> cancelled
//	running           ── succeeded | conflicted | failed | timed_out
//	running           ── cancel ──> cancelling ──> cancelled
//
// succeeded, conflicted, failed, timed_out, cancelled are terminal.
const (
	StatusAwaitingApproval = "awaiting_approval"
	StatusQueued           = "queued"
	StatusRunning          = "running"
	StatusSucceeded        = "succeeded"
	StatusConflicted       = "conflicted"
	StatusFailed           = "failed"
	StatusTimedOut         = "timed_out"
	StatusCancelling       = "cancelling"
	StatusCancelled        = "cancelled"
)

var executionTransitions = map[string]map[string]bool{
	StatusAwaitingApproval: {StatusQueued: true, StatusCancelled: true},
	StatusQueued:           {StatusRunning: true, StatusCancelled: true},
	StatusRunning: {
		StatusSucceeded:  true,
		StatusConflicted: true,
		StatusFailed:     true,
		StatusTimedOut:   true,
		StatusCancelling: true,
		StatusCancelled:  true,
	},
	StatusCancelling: {StatusCancelled: true, StatusFailed: true},
}

// CanTransitionExecution reports whether from -> to is an allowed
// NotebookExecution state transition.
func CanTransitionExecution(from, to string) bool {
	next, ok := executionTransitions[from]
	if !ok {
		return false
	}
	return next[to]
}

// IsTerminalExecutionStatus reports whether status is a terminal
// NotebookExecution status — no further transition is possible.
func IsTerminalExecutionStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusConflicted, StatusFailed, StatusTimedOut, StatusCancelled:
		return true
	default:
		return false
	}
}

// ActiveExecutionStatuses are the non-terminal statuses recovery scans at
// startup (design doc §11.2).
var ActiveExecutionStatuses = []string{StatusQueued, StatusRunning, StatusCancelling}

// Error codes returned to REST/MCP callers (design doc §11.3). These are
// stable identifiers for programmatic handling; ErrorMessage on
// NotebookExecution carries a human-readable string that must never
// contain Jupyter's raw internal response, a token, or a host path.
const (
	ErrCodeNotebookNotRunning = "notebook_not_running"
	ErrCodeKernelUnavailable  = "kernel_unavailable"
	ErrCodeKernelDied         = "kernel_died"
	ErrCodeExecutionTimeout   = "execution_timeout"
	ErrCodeExecutionCancelled = "execution_cancelled"
	ErrCodeContentConflict    = "content_conflict"
	ErrCodePathInvalid        = "path_invalid"
	ErrCodeOutputTooLarge     = "output_too_large"
	ErrCodeApprovalRequired   = "approval_required"
	ErrCodeApprovalDenied     = "approval_denied"
	ErrCodeRuntimeUnavailable = "runtime_unavailable"
	ErrCodeRecoveryUncertain  = "recovery_uncertain"
)

// MaxIdempotencyKeyLen is the maximum accepted length of an Idempotency-Key
// header (design doc §6: "모든 execution 생성은 최대 128자의 Idempotency-Key를 지원").
const MaxIdempotencyKeyLen = 128

// NotebookExecution is one request to run an entire notebook or a single
// cell against a Jupyter kernel. See the state machine above.
//
// Storage policy (design doc §5.3): the DB never holds raw code or full
// rich output — SourceSHA256 is the sha256 of the cell/notebook source that
// was actually executed, and OutputSummary is a size-capped structured JSON
// status summary, not the notebook's real outputs (those live in the
// .ipynb files at NotebookPath / ResultPath).
type NotebookExecution struct {
	ID              string `db:"id"`
	ProjectID       string `db:"project_id"`
	NotebookName    string `db:"notebook_name"`
	NotebookPath    string `db:"notebook_path"`
	ResultPath      string `db:"result_path"`
	KernelSessionID string `db:"kernel_session_id"`
	Kind            string `db:"kind"` // KindNotebook | KindCell
	Status          string `db:"status"`
	RequestedBy     string `db:"requested_by"`
	ClientID        string `db:"client_id"`
	IdempotencyKey  string `db:"idempotency_key"`
	// RequestHash is the sha256 of the canonicalized create-execution
	// request payload, stored alongside IdempotencyKey so a replayed key
	// with a different payload can be told apart from a true retry (design
	// doc §7.3: "payload가 다르면 409 Conflict"). Not part of the design
	// doc's NotebookExecution field list — added because the payload-match
	// check it requires needs somewhere durable to compare against.
	RequestHash     string `db:"request_hash"`
	SourceSHA256    string `db:"source_sha256"`
	BaseContentHash string `db:"base_content_hash"`
	CurrentCell     int    `db:"current_cell"`
	TotalCells      int    `db:"total_cells"`
	ErrorCode       string `db:"error_code"`
	ErrorMessage    string `db:"error_message"`
	OutputSummary   []byte `db:"output_summary"`
	// ApprovedBy/ApprovedAt/DeniedBy/DeniedAt record the approval workflow
	// (§9.3) — also not in the doc's minimal field list, but needed by the
	// audit fields §13.2 explicitly requires ("승인자와 승인 시각").
	ApprovedBy string     `db:"approved_by"`
	ApprovedAt *time.Time `db:"approved_at"`
	DeniedBy   string     `db:"denied_by"`
	DeniedAt   *time.Time `db:"denied_at"`
	QueuedAt   time.Time  `db:"queued_at"`
	StartedAt  *time.Time `db:"started_at"`
	FinishedAt *time.Time `db:"finished_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
}

// NotebookExecutionResponse is the public wire representation of a
// NotebookExecution. Unlike KernelSession, every field on NotebookExecution
// is already either non-sensitive metadata or an already-capped/hashed
// value, so the response type is a straight field-for-field mirror — kept
// as a distinct type (rather than reusing NotebookExecution for JSON
// directly) so the wire contract can't silently change just because an
// internal-only field is added to NotebookExecution later.
type NotebookExecutionResponse struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	NotebookName    string     `json:"notebook_name"`
	NotebookPath    string     `json:"notebook_path"`
	ResultPath      string     `json:"result_path,omitempty"`
	KernelSessionID string     `json:"kernel_session_id,omitempty"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	RequestedBy     string     `json:"requested_by,omitempty"`
	ClientID        string     `json:"client_id,omitempty"`
	SourceSHA256    string     `json:"source_sha256,omitempty"`
	BaseContentHash string     `json:"base_content_hash,omitempty"`
	CurrentCell     int        `json:"current_cell"`
	TotalCells      int        `json:"total_cells"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	OutputSummary   []byte     `json:"output_summary,omitempty"`
	ApprovedBy      string     `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty"`
	DeniedBy        string     `json:"denied_by,omitempty"`
	DeniedAt        *time.Time `json:"denied_at,omitempty"`
	QueuedAt        time.Time  `json:"queued_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// NewNotebookExecutionResponse maps a NotebookExecution to its wire
// representation. Returns nil for nil input.
func NewNotebookExecutionResponse(e *NotebookExecution) *NotebookExecutionResponse {
	if e == nil {
		return nil
	}
	return &NotebookExecutionResponse{
		ID:              e.ID,
		ProjectID:       e.ProjectID,
		NotebookName:    e.NotebookName,
		NotebookPath:    e.NotebookPath,
		ResultPath:      e.ResultPath,
		KernelSessionID: e.KernelSessionID,
		Kind:            e.Kind,
		Status:          e.Status,
		RequestedBy:     e.RequestedBy,
		ClientID:        e.ClientID,
		SourceSHA256:    e.SourceSHA256,
		BaseContentHash: e.BaseContentHash,
		CurrentCell:     e.CurrentCell,
		TotalCells:      e.TotalCells,
		ErrorCode:       e.ErrorCode,
		ErrorMessage:    e.ErrorMessage,
		OutputSummary:   e.OutputSummary,
		ApprovedBy:      e.ApprovedBy,
		ApprovedAt:      e.ApprovedAt,
		DeniedBy:        e.DeniedBy,
		DeniedAt:        e.DeniedAt,
		QueuedAt:        e.QueuedAt,
		StartedAt:       e.StartedAt,
		FinishedAt:      e.FinishedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

// NewNotebookExecutionResponses maps a slice, preserving order.
func NewNotebookExecutionResponses(es []*NotebookExecution) []*NotebookExecutionResponse {
	out := make([]*NotebookExecutionResponse, 0, len(es))
	for _, e := range es {
		out = append(out, NewNotebookExecutionResponse(e))
	}
	return out
}

// SHA256Hex returns the sha256 hex digest of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CellOutputStatus is one cell's entry inside an OutputSummary.
type CellOutputStatus struct {
	CellIndex      int    `json:"cell_index"`
	CellID         string `json:"cell_id,omitempty"`
	Status         string `json:"status"` // ok | error | skipped
	ExecutionCount int    `json:"execution_count,omitempty"`
	Preview        string `json:"preview,omitempty"` // truncated stdout/result preview
	ErrorName      string `json:"error_name,omitempty"`
	ErrorValue     string `json:"error_value,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
}

// OutputSummary is the capped, structured status summary stored on
// NotebookExecution.OutputSummary (design doc §5.3 — "OutputSummary는 상태
// 표시용으로 제한하며 기본 최대 64 KiB다"). It is not the notebook's real
// output — that lives in the .ipynb file at NotebookPath/ResultPath.
type OutputSummary struct {
	Cells     []CellOutputStatus `json:"cells"`
	Truncated bool               `json:"truncated,omitempty"`
}
