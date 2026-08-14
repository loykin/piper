package memberclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/pkg/project"
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
	m, err := c.resolve(ref)
	if err != nil {
		return SubmitRunResponse{}, err
	}
	return m.SubmitRun(ctx, auth, ref, req)
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
func (c *RoutingClient) QueryLogs(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID, stepName string, afterID int64) ([]*logstore.Line, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return nil, err
	}
	return m.QueryLogs(ctx, auth, ref, runID, stepName, afterID)
}
func (c *RoutingClient) QueryMetrics(ctx context.Context, auth AuthContext, ref project.ProjectRef, runID, stepName string) ([]*logstore.Metric, error) {
	m, err := c.resolve(ref)
	if err != nil {
		return nil, err
	}
	return m.QueryMetrics(ctx, auth, ref, runID, stepName)
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
