// Package memberclient defines the versioned request/response contract a
// Home uses to reach a Member's Run execution surface, per fed.md §13.3.
// This package must not import pkg/pipeline/run (or any Member-owned
// domain package) — run.Handler depends on Client, so a reverse dependency
// would cycle. DTOs here are therefore standalone types, not aliases of
// run.Run/run.Step (which also carry db tags and storage-layer concerns
// that don't belong in a Member-facing wire contract).
package memberclient

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// ErrRunNotFound is returned by any Client method whose runID does not
// resolve to an existing run.
var ErrRunNotFound = errors.New("run not found")

// ErrMemberUnavailable marks transport/routing failures where the owning
// Member cannot currently serve a project. HTTP callers should surface this
// as 503 rather than disguising it as a missing run or a generic 500.
var ErrMemberUnavailable = errors.New("member unavailable")

// AuthContext is the authorization context Home resolves once (via its
// project-membership middleware) and hands to Member on every call, so
// Member never looks up membership itself. Remote calls bind the context to
// a short lifetime, operation, payload, and ProjectRef with an HMAC signature;
// the in-process Local Member does not need a network-bound signature.
type AuthContext struct {
	ActorID     string
	Role        security.ProjectRole
	IssuedAt    time.Time
	ExpiresAt   time.Time
	Operation   string
	PayloadHash string
	Signature   string
}

// Client is the Home-to-Member contract for the Run execution domain — the
// first vertical slice migrated off direct repository access (fed.md
// §11.3: "Home은 Member의 execution repository에 직접 접근하지 않는다").
// The single-install Local Member implements this in-process
// (root package's NewLocalMemberClient); remote Members implement it over
// internal/membertunnel using the same request/response shapes.
type Client interface {
	SubmitRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, req SubmitRunRequest) (SubmitRunResponse, error)
	SubmitSweep(ctx context.Context, auth AuthContext, ref project.ProjectRef, req SubmitSweepRequest) (SubmitSweepResponse, error)
	ListRuns(ctx context.Context, auth AuthContext, ref project.ProjectRef, req ListRunsRequest) (ListRunsResponse, error)
	GetRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) (RunDetail, error)
	CancelRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) error
	RerunRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string, failedOnly bool) (newRunID string, err error)
	DeleteRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) error
	ListSteps(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) ([]StepSummary, error)
	RetryStep(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID, stepName string) (newRunID string, err error)
	QueryLogs(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID, stepName string, afterID int64) ([]*logstore.Line, error)
	QueryMetrics(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID, stepName string) ([]*logstore.Metric, error)
	ListArtifacts(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) ([]any, error)
	// ServeArtifact streams bytes directly to w — deliberately not a
	// request/response DTO. Forcing a large artifact download through a
	// buffered struct would be wasteful; this mirrors the existing
	// run.ArtifactProvider.ServeDownload shape.
	ServeArtifact(ctx context.Context, auth AuthContext, ref project.ProjectRef, w http.ResponseWriter, r *http.Request, runID, step, path string)
}
