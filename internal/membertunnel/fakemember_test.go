package membertunnel

import (
	"context"
	"net/http"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/statsstore"
)

// fakeMember is a configurable memberclient.Client double for this
// package's tests — mirrors the fakeMemberClient pattern used in
// pkg/pipeline/run/handler_test.go.
type fakeMember struct {
	submitRunFn         func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error)
	rerunRunFn          func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string, failedOnly bool) (string, error)
	cancelRunFn         func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) error
	getRunFn            func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) (memberclient.RunDetail, error)
	queryLogsFn         func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.QueryLogsRequest) (memberclient.QueryLogsResponse, error)
	statsCapabilitiesFn func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef) (statsstore.Capabilities, error)
	purgeProjectStatsFn func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef) error
}

func (f *fakeMember) SubmitRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
	if f.submitRunFn != nil {
		return f.submitRunFn(ctx, auth, ref, req)
	}
	return memberclient.SubmitRunResponse{}, nil
}

func (f *fakeMember) SubmitSweep(context.Context, memberclient.AuthContext, project.ProjectRef, memberclient.SubmitSweepRequest) (memberclient.SubmitSweepResponse, error) {
	return memberclient.SubmitSweepResponse{}, nil
}

func (f *fakeMember) ListRuns(context.Context, memberclient.AuthContext, project.ProjectRef, memberclient.ListRunsRequest) (memberclient.ListRunsResponse, error) {
	return memberclient.ListRunsResponse{}, nil
}

func (f *fakeMember) GetRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) (memberclient.RunDetail, error) {
	if f.getRunFn != nil {
		return f.getRunFn(ctx, auth, ref, runID)
	}
	return memberclient.RunDetail{}, nil
}

func (f *fakeMember) CancelRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) error {
	if f.cancelRunFn != nil {
		return f.cancelRunFn(ctx, auth, ref, runID)
	}
	return nil
}

func (f *fakeMember) RerunRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string, failedOnly bool) (string, error) {
	if f.rerunRunFn != nil {
		return f.rerunRunFn(ctx, auth, ref, runID, failedOnly)
	}
	return "", nil
}

func (f *fakeMember) DeleteRun(context.Context, memberclient.AuthContext, project.ProjectRef, string) error {
	return nil
}

func (f *fakeMember) ListSteps(context.Context, memberclient.AuthContext, project.ProjectRef, string) ([]memberclient.StepSummary, error) {
	return nil, nil
}

func (f *fakeMember) RetryStep(context.Context, memberclient.AuthContext, project.ProjectRef, string, string) (string, error) {
	return "", nil
}

func (f *fakeMember) QueryLogs(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.QueryLogsRequest) (memberclient.QueryLogsResponse, error) {
	if f.queryLogsFn != nil {
		return f.queryLogsFn(ctx, auth, ref, req)
	}
	return memberclient.QueryLogsResponse{}, nil
}

func (f *fakeMember) StatsCapabilities(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef) (statsstore.Capabilities, error) {
	if f.statsCapabilitiesFn != nil {
		return f.statsCapabilitiesFn(ctx, auth, ref)
	}
	return statsstore.Capabilities{TimeRange: true}, nil
}

func (f *fakeMember) PurgeProjectStats(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef) error {
	if f.purgeProjectStatsFn != nil {
		return f.purgeProjectStatsFn(ctx, auth, ref)
	}
	return nil
}

func (f *fakeMember) QueryMetrics(context.Context, memberclient.AuthContext, project.ProjectRef, memberclient.QueryMetricsRequest) (memberclient.QueryMetricsResponse, error) {
	return memberclient.QueryMetricsResponse{}, nil
}

func (f *fakeMember) ListArtifacts(context.Context, memberclient.AuthContext, project.ProjectRef, string) ([]any, error) {
	return nil, nil
}

func (f *fakeMember) ServeArtifact(context.Context, memberclient.AuthContext, project.ProjectRef, http.ResponseWriter, *http.Request, string, string, string) {
}

var _ memberclient.Client = (*fakeMember)(nil)
