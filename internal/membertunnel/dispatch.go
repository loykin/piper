package membertunnel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/statsstore"
)

// callMethod decodes payload into a callEnvelope[Req], invokes fn with the
// decoded auth/ref/req, and encodes the result as MemberRPCResponse payload
// bytes. Shared by every dispatch case below.
func callMethod[Req, Resp any](ctx context.Context, payload []byte, fn func(context.Context, memberclient.AuthContext, project.ProjectRef, Req) (Resp, error)) ([]byte, error) {
	env, err := decodeCall[Req](payload)
	if err != nil {
		return nil, err
	}
	resp, err := fn(ctx, env.Auth, env.Ref, env.Req)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("membertunnel: encode response: %w", err)
	}
	return data, nil
}

// callVoidMethod is callMethod's counterpart for methods that return only
// an error (CancelRun, DeleteRun).
func callVoidMethod[Req any](ctx context.Context, payload []byte, fn func(context.Context, memberclient.AuthContext, project.ProjectRef, Req) error) ([]byte, error) {
	env, err := decodeCall[Req](payload)
	if err != nil {
		return nil, err
	}
	if err := fn(ctx, env.Auth, env.Ref, env.Req); err != nil {
		return nil, err
	}
	return []byte("{}"), nil
}

// dispatch routes one MemberRPCCommand to the corresponding memberclient.Client
// method on the Member's own local implementation (its NewLocalMemberClient).
func dispatch(ctx context.Context, member memberclient.Client, method string, payload []byte, projectClients ...projectclient.Client) ([]byte, error) {
	switch method {
	case MethodSubmitRun:
		return callMethod(ctx, payload, member.SubmitRun)
	case MethodSubmitSweep:
		return callMethod(ctx, payload, member.SubmitSweep)
	case MethodListRuns:
		return callMethod(ctx, payload, member.ListRuns)
	case MethodGetRun:
		return callMethod(ctx, payload, member.GetRun)
	case MethodCancelRun:
		return callVoidMethod(ctx, payload, member.CancelRun)
	case MethodRerunRun:
		return callMethod(ctx, payload, adaptRerunRun(member))
	case MethodDeleteRun:
		return callVoidMethod(ctx, payload, member.DeleteRun)
	case MethodListSteps:
		return callMethod(ctx, payload, member.ListSteps)
	case MethodRetryStep:
		return callMethod(ctx, payload, adaptRetryStep(member))
	case MethodQueryLogs:
		return callMethod(ctx, payload, adaptQueryLogs(member))
	case MethodQueryMetrics:
		return callMethod(ctx, payload, adaptQueryMetrics(member))
	case MethodStatsCapabilities:
		return callMethod(ctx, payload, adaptStatsCapabilities(member))
	case MethodPurgeProjectStats:
		return callVoidMethod(ctx, payload, adaptPurgeProjectStats(member))
	case MethodListArtifacts:
		return callMethod(ctx, payload, member.ListArtifacts)
	case MethodProjectRequest:
		if len(projectClients) == 0 || projectClients[0] == nil {
			return nil, fmt.Errorf("membertunnel: project API relay is unavailable")
		}
		return callMethod(ctx, payload, projectClients[0].DoProjectRequest)
	default:
		return nil, fmt.Errorf("membertunnel: unknown method %q", method)
	}
}

// adaptRerunRun/adaptRetryStep/adaptQueryLogs/adaptQueryMetrics bridge
// memberclient.Client methods that take more than one trailing argument
// into the single-Req shape callMethod needs.

func adaptRerunRun(member memberclient.Client) func(context.Context, memberclient.AuthContext, project.ProjectRef, RerunRunRequest) (string, error) {
	return func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req RerunRunRequest) (string, error) {
		return member.RerunRun(ctx, auth, ref, req.RunID, req.FailedOnly)
	}
}

func adaptRetryStep(member memberclient.Client) func(context.Context, memberclient.AuthContext, project.ProjectRef, RetryStepRequest) (string, error) {
	return func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req RetryStepRequest) (string, error) {
		return member.RetryStep(ctx, auth, ref, req.RunID, req.StepName)
	}
}

func adaptQueryLogs(member memberclient.Client) func(context.Context, memberclient.AuthContext, project.ProjectRef, memberclient.QueryLogsRequest) (queryLogsResponse, error) {
	return member.QueryLogs
}

func adaptQueryMetrics(member memberclient.Client) func(context.Context, memberclient.AuthContext, project.ProjectRef, memberclient.QueryMetricsRequest) (queryMetricsResponse, error) {
	return member.QueryMetrics
}

func adaptStatsCapabilities(member memberclient.Client) func(context.Context, memberclient.AuthContext, project.ProjectRef, struct{}) (statsstore.Capabilities, error) {
	return func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, _ struct{}) (statsstore.Capabilities, error) {
		return member.StatsCapabilities(ctx, auth, ref)
	}
}

func adaptPurgeProjectStats(member memberclient.Client) func(context.Context, memberclient.AuthContext, project.ProjectRef, struct{}) error {
	return func(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, _ struct{}) error {
		return member.PurgeProjectStats(ctx, auth, ref)
	}
}
