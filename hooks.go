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
	// Handle CORS, rate limiting, request logging, etc. here.
	// Compatible with any middleware library such as chi or alice.
	Middleware []func(http.Handler) http.Handler

	// Auth is the authentication hook called on every API request.
	// Returns an enriched context (e.g. with user identity) on success,
	// or an error to reject with 401.
	// nil means no authentication check.
	//
	// The returned context replaces the request context for the remainder of
	// the request, so downstream hooks (BeforeCreateRun, etc.) can call
	// ctx.Value(myKey) to retrieve the verified identity.
	Auth func(r *http.Request) (context.Context, error)

	// ExtractOwnerID extracts the caller's owner ID from a request.
	// Used by BeforeListRuns, BeforeGetRun, and all ownership checks.
	// When nil, falls back to the X-Piper-Owner-ID header or owner_id query param.
	//
	// Override this when user identity comes from JWT claims, sessions, or
	// any source other than a trusted header.
	ExtractOwnerID func(r *http.Request) string

	// ── Run API hooks ───────────────────────────────────────────
	// Returning an error blocks the request with a 403.
	// Useful for permission checks, input validation, audit logging, etc.

	// BeforeCreateRun is called when a pipeline run is requested.
	// Can inspect the yaml or check execution permissions.
	// ctx carries the identity injected by Auth.
	BeforeCreateRun func(ctx context.Context, r *http.Request, yaml string) error

	// BeforeListRuns is called when the run list is queried.
	// Returns a RunFilter to restrict results per caller.
	// ctx carries the identity injected by Auth.
	BeforeListRuns func(ctx context.Context, r *http.Request) (RunFilter, error)

	// BeforeGetRun is called before fetching, cancelling, rerunning, retrying,
	// or deleting a run. Useful for ownership checks.
	// ctx carries the identity injected by Auth.
	BeforeGetRun func(ctx context.Context, r *http.Request, runID string) error

	// BeforeGetLogs is called when logs are requested.
	// ctx carries the identity injected by Auth.
	BeforeGetLogs func(ctx context.Context, r *http.Request, runID, stepName string) error

	// ── Schedule API hooks ──────────────────────────────────────

	// BeforeCreateSchedule is called before a schedule is created.
	BeforeCreateSchedule func(ctx context.Context, r *http.Request, yaml string) error

	// BeforeListSchedules is called before the schedule list is queried.
	// Returns a ScheduleFilter to restrict results per caller.
	BeforeListSchedules func(ctx context.Context, r *http.Request) (ScheduleFilter, error)

	// BeforeGetSchedule is called before fetching, patching, deleting, or
	// backfilling a schedule. Useful for ownership checks.
	BeforeGetSchedule func(ctx context.Context, r *http.Request, id string) error

	// ── Serving API hooks ───────────────────────────────────────

	// BeforeCreateService is called before a model service is deployed.
	BeforeCreateService func(ctx context.Context, r *http.Request, yaml string) error

	// BeforeListServices is called before the service list is queried.
	// Returns a ServingFilter to restrict results per caller.
	BeforeListServices func(ctx context.Context, r *http.Request) (ServingFilter, error)

	// BeforeGetService is called before fetching, deleting, or restarting a
	// service. Useful for ownership checks.
	BeforeGetService func(ctx context.Context, r *http.Request, name string) error

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

// ScheduleFilter is the list filter returned by BeforeListSchedules.
type ScheduleFilter struct {
	// OwnerID, when set, returns only schedules belonging to that owner.
	OwnerID string
}

// ServingFilter is the list filter returned by BeforeListServices.
type ServingFilter struct {
	// OwnerID, when set, returns only services belonging to that owner.
	OwnerID string
}

// ── Hook call helpers (internal) ─────────────────────────────────

func (h *Hooks) callAuth(r *http.Request) (context.Context, error) {
	if h.Auth == nil {
		return r.Context(), nil
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

func (h *Hooks) callBeforeCreateSchedule(ctx context.Context, r *http.Request, yaml string) error {
	if h.BeforeCreateSchedule == nil {
		return nil
	}
	return h.BeforeCreateSchedule(ctx, r, yaml)
}

func (h *Hooks) callBeforeListSchedules(ctx context.Context, r *http.Request) (ScheduleFilter, error) {
	if h.BeforeListSchedules == nil {
		return ScheduleFilter{}, nil
	}
	return h.BeforeListSchedules(ctx, r)
}

func (h *Hooks) callBeforeGetSchedule(ctx context.Context, r *http.Request, id string) error {
	if h.BeforeGetSchedule == nil {
		return nil
	}
	return h.BeforeGetSchedule(ctx, r, id)
}

func (h *Hooks) callBeforeCreateService(ctx context.Context, r *http.Request, yaml string) error {
	if h.BeforeCreateService == nil {
		return nil
	}
	return h.BeforeCreateService(ctx, r, yaml)
}

func (h *Hooks) callBeforeListServices(ctx context.Context, r *http.Request) (ServingFilter, error) {
	if h.BeforeListServices == nil {
		return ServingFilter{}, nil
	}
	return h.BeforeListServices(ctx, r)
}

func (h *Hooks) callBeforeGetService(ctx context.Context, r *http.Request, name string) error {
	if h.BeforeGetService == nil {
		return nil
	}
	return h.BeforeGetService(ctx, r, name)
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
