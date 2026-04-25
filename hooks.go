package piper

import (
	"context"
	"net/http"

	"github.com/piper/piper/pkg/pipeline"
)

// Hooks holds all extension points for piper.
// Every field is nilable — nil means no-op.
// Included in Config so it applies uniformly in both HTTP server mode and library mode.
//
//	piper.New(piper.Config{
//	    Hooks: piper.Hooks{
//	        Auth:        myAuthFunc,
//	        OnRunEnd:    notifySlack,
//	    },
//	})
type Hooks struct {
	// ── HTTP layer ───────────────────────────────────────────────
	// Middleware is a chain of middleware applied to all requests.
	// Handle authentication, CORS, rate limiting, etc. here.
	// Compatible with any middleware library such as chi or alice.
	Middleware []func(http.Handler) http.Handler

	// Auth is the authentication hook called on every API request.
	// Returns 401 on error. nil means no authentication.
	Auth func(r *http.Request) error

	// ── API hooks ────────────────────────────────────────────────
	// Returning an error blocks the request with a 403.
	// Useful for permission checks, input validation, audit logging, etc.

	// BeforeCreateRun is called when a pipeline run is requested.
	// Can inspect the yaml or check execution permissions.
	BeforeCreateRun func(ctx context.Context, r *http.Request, yaml string) error

	// BeforeListRuns is called when the run list is queried.
	// Returns a RunFilter to filter results per user.
	BeforeListRuns func(ctx context.Context, r *http.Request) (RunFilter, error)

	// BeforeGetRun is called when a run's details are fetched.
	// Useful for ownership checks.
	BeforeGetRun func(ctx context.Context, r *http.Request, runID string) error

	// BeforeGetLogs is called when logs are requested.
	BeforeGetLogs func(ctx context.Context, r *http.Request, runID, stepName string) error

	// ── Run lifecycle ─────────────────────────────────────────────
	// Useful for notifications, monitoring, and external system integration.

	OnRunStart func(ctx context.Context, runID string, pl *pipeline.Pipeline)
	OnRunEnd   func(ctx context.Context, runID string, result *pipeline.RunResult)

	OnStepStart func(ctx context.Context, runID, stepName string)
	OnStepEnd   func(ctx context.Context, runID, stepName string, result *pipeline.StepResult)
}

// RunFilter is the list filter returned by BeforeListRuns.
type RunFilter struct {
	// OwnerID, when set, returns only runs belonging to that owner.
	// An empty string means no filter.
	OwnerID string

	// PipelineName filters by pipeline name.
	PipelineName string
}

// ── Hook call helpers (internal) ─────────────────────────────────

func (h *Hooks) callAuth(r *http.Request) error {
	if h.Auth == nil {
		return nil
	}
	return h.Auth(r)
}

func (h *Hooks) callBeforeCreateRun(ctx context.Context, r *http.Request, yaml string) error {
	if h.BeforeCreateRun == nil {
		return nil
	}
	return h.BeforeCreateRun(ctx, r, yaml)
}

func (h *Hooks) callBeforeListRuns(ctx context.Context, r *http.Request) (RunFilter, error) {
	if h.BeforeListRuns == nil {
		return RunFilter{}, nil
	}
	return h.BeforeListRuns(ctx, r)
}

func (h *Hooks) callBeforeGetRun(ctx context.Context, r *http.Request, runID string) error {
	if h.BeforeGetRun == nil {
		return nil
	}
	return h.BeforeGetRun(ctx, r, runID)
}

func (h *Hooks) callBeforeGetLogs(ctx context.Context, r *http.Request, runID, stepName string) error {
	if h.BeforeGetLogs == nil {
		return nil
	}
	return h.BeforeGetLogs(ctx, r, runID, stepName)
}

func (h *Hooks) callOnRunStart(ctx context.Context, runID string, pl *pipeline.Pipeline) {
	if h.OnRunStart != nil {
		h.OnRunStart(ctx, runID, pl)
	}
}

func (h *Hooks) callOnRunEnd(ctx context.Context, runID string, result *pipeline.RunResult) {
	if h.OnRunEnd != nil {
		h.OnRunEnd(ctx, runID, result)
	}
}

func (h *Hooks) callOnStepStart(ctx context.Context, runID, stepName string) {
	if h.OnStepStart != nil {
		h.OnStepStart(ctx, runID, stepName)
	}
}

func (h *Hooks) callOnStepEnd(ctx context.Context, runID, stepName string, result *pipeline.StepResult) {
	if h.OnStepEnd != nil {
		h.OnStepEnd(ctx, runID, stepName, result)
	}
}
