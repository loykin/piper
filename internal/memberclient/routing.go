package memberclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/statsstore"
)

// RoutingClient dispatches each Run-domain request to the Member that owns
// the supplied ProjectRef. Resolution happens for every call so disconnects
// and reconnects are reflected without rebuilding the HTTP router.
type RoutingClient struct {
	Resolve func(project.ProjectRef) (Client, error)
}

func (c *RoutingClient) resolve(ref project.ProjectRef) (Client, error) {
	if c == nil || c.Resolve == nil {
		return nil, fmt.Errorf("memberclient: member resolver is not configured")
	}
	member, err := c.Resolve(ref)
	if err != nil {
		return nil, err
	}
	if member == nil {
		return nil, fmt.Errorf("memberclient: resolver returned no client for member %q", ref.MemberID)
	}
	return member, nil
}

func (c *RoutingClient) SubmitRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, req SubmitRunRequest) (SubmitRunResponse, error) {
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.NewString()
	}
	m, err := c.resolve(ref)
	if err != nil {
		return SubmitRunResponse{}, err
	}
	resp, err := m.SubmitRun(ctx, auth, ref, req)
	if !errors.Is(err, ErrMemberUnavailable) {
		return resp, err
	}

	// The request may already have committed on Member before its response
	// was lost. Re-resolve across a short reconnect window and retry with the
	// same durable idempotency key.
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastErr := err
	for {
		select {
		case <-ctx.Done():
			return SubmitRunResponse{}, ctx.Err()
		case <-deadline.C:
			return SubmitRunResponse{}, lastErr
		case <-ticker.C:
			m, resolveErr := c.resolve(ref)
			if resolveErr != nil {
				lastErr = resolveErr
				continue
			}
			resp, callErr := m.SubmitRun(ctx, auth, ref, req)
			if callErr == nil || !errors.Is(callErr, ErrMemberUnavailable) {
				return resp, callErr
			}
			lastErr = callErr
		}
	}
}
func (c *RoutingClient) SubmitSweep(ctx context.Context, auth AuthContext, ref project.ProjectRef, req SubmitSweepRequest) (SubmitSweepResponse, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return SubmitSweepResponse{}, err
	}
	return m.SubmitSweep(ctx, auth, ref, req)
}
func (c *RoutingClient) ListRuns(ctx context.Context, auth AuthContext, ref project.ProjectRef, req ListRunsRequest) (ListRunsResponse, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return ListRunsResponse{}, err
	}
	return m.ListRuns(ctx, auth, ref, req)
}
func (c *RoutingClient) GetRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) (RunDetail, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return RunDetail{}, err
	}
	return m.GetRun(ctx, auth, ref, runID)
}
func (c *RoutingClient) CancelRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) error {
	m, err := c.resolve(ref)
	if err != nil {
		return err
	}
	return m.CancelRun(ctx, auth, ref, runID)
}
func (c *RoutingClient) RerunRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string, failedOnly bool) (string, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return "", err
	}
	return m.RerunRun(ctx, auth, ref, runID, failedOnly)
}
func (c *RoutingClient) DeleteRun(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) error {
	m, err := c.resolve(ref)
	if err != nil {
		return err
	}
	return m.DeleteRun(ctx, auth, ref, runID)
}
func (c *RoutingClient) ListSteps(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) ([]StepSummary, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return nil, err
	}
	return m.ListSteps(ctx, auth, ref, runID)
}
func (c *RoutingClient) RetryStep(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID, stepName string) (string, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return "", err
	}
	return m.RetryStep(ctx, auth, ref, runID, stepName)
}
func (c *RoutingClient) QueryLogs(ctx context.Context, auth AuthContext, ref project.ProjectRef, req QueryLogsRequest) (QueryLogsResponse, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return QueryLogsResponse{}, err
	}
	return m.QueryLogs(ctx, auth, ref, req)
}
func (c *RoutingClient) StatsCapabilities(ctx context.Context, auth AuthContext, ref project.ProjectRef) (statsstore.Capabilities, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return statsstore.Capabilities{}, err
	}
	return m.StatsCapabilities(ctx, auth, ref)
}
func (c *RoutingClient) PurgeProjectStats(ctx context.Context, auth AuthContext, ref project.ProjectRef) error {
	m, err := c.resolve(ref)
	if err != nil {
		return err
	}
	return m.PurgeProjectStats(ctx, auth, ref)
}
func (c *RoutingClient) QueryMetrics(ctx context.Context, auth AuthContext, ref project.ProjectRef, req QueryMetricsRequest) (QueryMetricsResponse, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return QueryMetricsResponse{}, err
	}
	return m.QueryMetrics(ctx, auth, ref, req)
}
func (c *RoutingClient) ListArtifacts(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID string) ([]any, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return nil, err
	}
	return m.ListArtifacts(ctx, auth, ref, runID)
}
func (c *RoutingClient) ServeArtifact(ctx context.Context, auth AuthContext, ref project.ProjectRef, w http.ResponseWriter, r *http.Request, runID, step, path string) {
	m, err := c.resolve(ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	m.ServeArtifact(ctx, auth, ref, w, r, runID, step, path)
}

var _ Client = (*RoutingClient)(nil)
